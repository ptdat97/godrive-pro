package refund

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

func errExceeds(max, used money.VND) error {
	return errs.Conflict("refund_exceeds_paid",
		"Tổng số tiền hoàn vượt quá số khách đã trả cho chuyến này (đã xin hoàn "+
			used.String()+" trên tối đa "+max.String()+").")
}

var errDecided = errs.Conflict("refund_already_decided", "Yêu cầu này vừa được người khác quyết định.")

// ------------------------------------------------------------------ bộ nhớ

type MemoryStore struct {
	mu   sync.Mutex
	data map[string]*Refund
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{data: map[string]*Refund{}} }

func (m *MemoryStore) Create(_ context.Context, r *Refund, max money.VND) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var used money.VND
	for _, x := range m.data {
		if x.TripID == r.TripID && x.Status != StatusRejected {
			used += x.Amount
		}
	}
	if used+r.Amount > max {
		return errExceeds(max, used)
	}
	c := *r
	m.data[r.ID] = &c
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Refund, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.data[id]
	if !ok {
		return nil, errs.NotFound("refund_not_found", "Không tìm thấy yêu cầu hoàn tiền.")
	}
	c := *r
	return &c, nil
}

func (m *MemoryStore) Decide(_ context.Context, id string, to Status, by, note string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.data[id]
	if !ok {
		return errs.NotFound("refund_not_found", "Không tìm thấy yêu cầu hoàn tiền.")
	}
	if r.Status != StatusPending {
		return errDecided
	}
	r.Status, r.DecidedBy, r.DecisionNote, r.DecidedAt = to, by, note, &at
	return nil
}

func (m *MemoryStore) List(_ context.Context, f ListFilter) ([]*Refund, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*Refund{}
	for _, r := range m.data {
		if (f.Status == "" || r.Status == f.Status) && (f.TripID == "" || r.TripID == f.TripID) {
			c := *r
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// ------------------------------------------------------------------ Postgres

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

const cols = `id, trip_id, rider_id, driver_id, amount_vnd, reason, charge_to, status,
    requested_by, self_approved, decided_by, decision_note, created_at, decided_at`

func scan(row interface{ Scan(...any) error }) (*Refund, error) {
	var r Refund
	var decided sql.NullTime
	err := row.Scan(&r.ID, &r.TripID, &r.RiderID, &r.DriverID, &r.Amount, &r.Reason, &r.ChargeTo,
		&r.Status, &r.RequestedBy, &r.SelfApproved, &r.DecidedBy, &r.DecisionNote, &r.CreatedAt, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.NotFound("refund_not_found", "Không tìm thấy yêu cầu hoàn tiền.")
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if decided.Valid {
		t := decided.Time
		r.DecidedAt = &t
	}
	return &r, nil
}

// Create khoá dòng chuyến trong transaction rồi mới cộng các khoản đã xin:
// hai vận hành xin hoàn cùng một chuyến cùng lúc sẽ xếp hàng, người sau thấy
// khoản của người trước.
func (p *PostgresStore) Create(ctx context.Context, r *Refund, max money.VND) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM trips WHERE id=$1 FOR UPDATE`, r.TripID); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(amount_vnd), 0) FROM refunds
        WHERE trip_id=$1 AND status <> 'REJECTED'`, r.TripID).Scan(&used); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if money.VND(used)+r.Amount > max {
		return errExceeds(max, money.VND(used))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO refunds (`+cols+`)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'',$12,$13)`,
		r.ID, r.TripID, r.RiderID, r.DriverID, r.Amount, r.Reason, r.ChargeTo, r.Status,
		r.RequestedBy, r.SelfApproved, r.DecidedBy, r.CreatedAt, r.DecidedAt); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if err := tx.Commit(); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Refund, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+cols+` FROM refunds WHERE id=$1`, id))
}

// Decide là compare-and-swap trên status: hai người bấm duyệt và từ chối cùng
// lúc thì đúng một người thắng.
func (p *PostgresStore) Decide(ctx context.Context, id string, to Status, by, note string, at time.Time) error {
	res, err := p.db.ExecContext(ctx, `UPDATE refunds
        SET status=$2, decided_by=$3, decision_note=$4, decided_at=$5
        WHERE id=$1 AND status='PENDING'`, id, to, by, note, at)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := p.Get(ctx, id); err != nil {
			return err
		}
		return errDecided
	}
	return nil
}

func (p *PostgresStore) List(ctx context.Context, f ListFilter) ([]*Refund, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+cols+` FROM refunds
        WHERE ($1 = '' OR status = $1) AND ($2 = '' OR trip_id = $2)
        ORDER BY created_at DESC, id DESC LIMIT $3`, string(f.Status), f.TripID, f.Limit)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer rows.Close()
	out := []*Refund{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
