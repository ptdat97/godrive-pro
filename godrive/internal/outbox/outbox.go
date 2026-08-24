// Package outbox cài đặt mẫu Transactional Outbox.
//
// Vấn đề: nếu ghi DB xong rồi mới publish message, tiến trình chết ở giữa sẽ
// làm mất sự kiện; nếu publish trước rồi ghi DB lỗi thì sự kiện thành ma.
// Giải pháp: ghi message vào bảng outbox TRONG CÙNG transaction nghiệp vụ,
// một relay riêng đọc bảng đó và publish (at-least-once).
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/pkg/id"
)

// MaxAttempts là số lần thử phát tối đa. Vượt ngưỡng thì bản ghi nằm lại làm
// "thư chết": relay thôi lấy nó, còn cảnh báo vận hành nhìn thấy qua DeadCount.
//
// Thử lại vô hạn một sự kiện hỏng sẽ chặn hàng đợi và giấu luôn sự cố.
const MaxAttempts = 10

type Record struct {
	ID          string          `json:"id"`
	Topic       string          `json:"topic"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	Attempts    int             `json:"attempts"`
}

type Store interface {
	Enqueue(ctx context.Context, topic string, payload any) error
	FetchUnpublished(ctx context.Context, limit int) ([]Record, error)
	MarkPublished(ctx context.Context, ids []string) error
	MarkFailed(ctx context.Context, id string) error
}

// TxEnqueuer ghi sự kiện vào outbox TRONG một transaction đang mở.
//
// Đây là điểm mấu chốt của mẫu Transactional Outbox: nếu Enqueue nằm ngoài
// transaction nghiệp vụ thì vẫn còn khe hở "ghi DB xong, chết trước khi
// enqueue" — đúng vấn đề mà outbox sinh ra để giải quyết.
type TxEnqueuer interface {
	EnqueueTx(ctx context.Context, tx *sql.Tx, topic string, payload any) error
}

type MemoryStore struct {
	mu   sync.Mutex
	recs []Record
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) Enqueue(_ context.Context, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, Record{ID: id.New("obx"), Topic: topic, Payload: raw, CreatedAt: time.Now().UTC()})
	return nil
}

func (m *MemoryStore) FetchUnpublished(_ context.Context, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.recs {
		if r.PublishedAt == nil {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *MemoryStore) MarkPublished(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	set := map[string]bool{}
	for _, i := range ids {
		set[i] = true
	}
	for i := range m.recs {
		if set[m.recs[i].ID] {
			m.recs[i].PublishedAt = &now
		}
	}
	return nil
}

func (m *MemoryStore) MarkFailed(_ context.Context, rid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.recs {
		if m.recs[i].ID == rid {
			m.recs[i].Attempts++
		}
	}
	return nil
}

// PostgresStore lưu outbox xuống bảng `outbox`.
type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Enqueue(ctx context.Context, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO outbox (id, topic, payload) VALUES ($1,$2,$3)`,
		id.New("obx"), topic, raw)
	return err
}

// EnqueueTx ghi vào outbox bằng chính transaction nghiệp vụ đang mở.
func (s *PostgresStore) EnqueueTx(ctx context.Context, tx *sql.Tx, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO outbox (id, topic, payload) VALUES ($1,$2,$3)`,
		id.New("obx"), topic, raw)
	return err
}

// FetchUnpublished lấy lô sự kiện chưa publish.
//
// FOR UPDATE SKIP LOCKED cho phép chạy nhiều relay song song mà không tranh
// nhau cùng một dòng — mỗi tiến trình nhận một lô khác nhau.
func (s *PostgresStore) FetchUnpublished(ctx context.Context, limit int) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, topic, payload, attempts, created_at
        FROM outbox WHERE published_at IS NULL AND attempts < $2
        ORDER BY created_at LIMIT $1
        FOR UPDATE SKIP LOCKED`, limit, MaxAttempts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Topic, &r.Payload, &r.Attempts, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) MarkPublished(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, pqArray(ids))
	return err
}

func (s *PostgresStore) MarkFailed(ctx context.Context, rid string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET attempts = attempts + 1 WHERE id = $1`, rid)
	return err
}

// DeadCount là số sự kiện đã thử quá MaxAttempts lần. Bất kỳ giá trị nào khác 0
// đều cần người xem — sự kiện nghiệp vụ đã mất.
func (s *PostgresStore) DeadCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE published_at IS NULL AND attempts >= $1`,
		MaxAttempts).Scan(&n)
	return n, err
}

// PendingCount là số sự kiện chưa publish. Dùng cho cảnh báo vận hành: outbox
// tồn đọng nghĩa là relay chết hoặc bus hỏng.
func (s *PostgresStore) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n)
	return n, err
}

// pqArray dựng literal mảng của Postgres. Dùng thay cho lib/pq để giữ nguyên
// nguyên tắc zero-dependency ngoài driver pgx.
func pqArray(ids []string) string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, v := range ids {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('"')
		sb.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v))
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
	return sb.String()
}

// Relay đọc outbox và publish lên bus theo chu kỳ.
type Relay struct {
	store    Store
	bus      eventbus.Bus
	log      *slog.Logger
	Interval time.Duration
	Batch    int
}

// DefaultInterval là chu kỳ quét outbox.
//
// 200ms chứ không phải 1 giây: sự kiện trip.requested đi qua outbox, nên chu kỳ
// này chính là độ trễ trước khi dispatcher bắt đầu tìm tài xế — khách hàng cảm
// nhận được. Quét rẻ nhờ index một phần outbox_unpublished_idx.
const DefaultInterval = 200 * time.Millisecond

func NewRelay(s Store, bus eventbus.Bus, log *slog.Logger) *Relay {
	return &Relay{store: s, bus: bus, log: log, Interval: DefaultInterval, Batch: 100}
}

func (r *Relay) Run(ctx context.Context) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Quét cho tới khi hết việc: một lô đầy nghĩa là còn tồn đọng, chờ
			// nhịp sau mới xử lý tiếp sẽ làm hàng đợi dài mãi khi có đợt tải cao.
			for {
				n, err := r.tick(ctx)
				if err != nil {
					r.log.Error("outbox relay lỗi", "err", err)
					break
				}
				if n < r.Batch {
					break
				}
			}
		}
	}
}

// tick phát một lô và trả về số bản ghi đã xử lý.
func (r *Relay) tick(ctx context.Context) (int, error) {
	recs, err := r.store.FetchUnpublished(ctx, r.Batch)
	if err != nil || len(recs) == 0 {
		return 0, err
	}
	var done []string
	for _, rec := range recs {
		var payload any
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			// Payload hỏng thì thử lại bao nhiêu lần cũng hỏng. Đếm số lần để
			// nó rơi vào thư chết thay vì chặn hàng đợi mãi mãi.
			r.log.Error("payload outbox không giải mã được", "id", rec.ID, "topic", rec.Topic, "err", err)
			_ = r.store.MarkFailed(ctx, rec.ID)
			continue
		}
		if err := r.bus.Publish(ctx, rec.Topic, payload); err != nil {
			_ = r.store.MarkFailed(ctx, rec.ID)
			continue
		}
		done = append(done, rec.ID)
	}
	if len(done) > 0 {
		return len(recs), r.store.MarkPublished(ctx, done)
	}
	return len(recs), nil
}
