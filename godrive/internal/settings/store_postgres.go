package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/example/godrive/pkg/errs"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) GetAll(ctx context.Context) (map[Key]Record, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value, version, updated_by, updated_at FROM settings`)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[Key]Record{}
	for rows.Next() {
		var r Record
		var raw []byte
		if err := rows.Scan(&r.Key, &raw, &r.Version, &r.UpdatedBy, &r.UpdatedAt); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		r.Value = json.RawMessage(raw)
		out[r.Key] = r
	}
	return out, rows.Err()
}

// Put ghi cấu hình và lịch sử trong CÙNG một transaction.
//
// Tách hai việc ra là tự tạo khả năng có một thay đổi không có dấu vết — đúng
// thứ mà lịch sử sinh ra để chống.
func (s *PostgresStore) Put(ctx context.Context, k Key, value json.RawMessage,
	expectVersion int, by, reason string, now time.Time, newID func(string) string) (Record, error) {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Đọc giá trị cũ để ghi vào lịch sử. Khoá dòng để hai quản trị viên cùng
	// ghi thì người sau đọc được phiên bản đã cập nhật, không phải bản cũ.
	var oldRaw []byte
	var curVersion int
	err = tx.QueryRowContext(ctx,
		`SELECT value, version FROM settings WHERE key=$1 FOR UPDATE`, k).Scan(&oldRaw, &curVersion)
	if err != nil && err != sql.ErrNoRows {
		return Record{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if err == sql.ErrNoRows {
		curVersion = 0
		// Chưa có dòng nào không có nghĩa là "trước đó không có gì": nhóm này
		// vẫn đang chạy bằng mặc định trong mã nguồn. Ghi mặc định làm giá trị
		// cũ để lần sửa đầu tiên cũng so sánh được, thay vì hiện ra như thể mọi
		// con số đều vừa mới xuất hiện từ hư không.
		if def, derr := marshalDefault(k); derr == nil {
			oldRaw = def
		}
	}
	if curVersion != expectVersion {
		return Record{}, errs.Conflict("setting_version_conflict",
			"Cấu hình vừa được người khác thay đổi. Tải lại rồi thử lại.")
	}

	next := curVersion + 1
	if curVersion == 0 {
		_, err = tx.ExecContext(ctx, `
            INSERT INTO settings (key, value, version, updated_by, updated_at)
            VALUES ($1,$2,$3,$4,$5)`, k, []byte(value), next, by, now)
	} else {
		_, err = tx.ExecContext(ctx, `
            UPDATE settings SET value=$2, version=$3, updated_by=$4, updated_at=$5
            WHERE key=$1`, k, []byte(value), next, by, now)
	}
	if err != nil {
		return Record{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO settings_history (id, key, version, old_value, new_value, changed_by, reason, at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		newID("sth"), k, next, nullRaw(oldRaw), []byte(value), by, reason, now); err != nil {
		return Record{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}

	if err := tx.Commit(); err != nil {
		return Record{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return Record{Key: k, Value: value, Version: next, UpdatedBy: by, UpdatedAt: now}, nil
}

func nullRaw(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (s *PostgresStore) History(ctx context.Context, k Key, limit int) ([]HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, key, version, old_value, new_value, changed_by, reason, at
        FROM settings_history WHERE key=$1 ORDER BY at DESC LIMIT $2`, k, limit)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()
	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		var oldRaw, newRaw []byte
		if err := rows.Scan(&e.ID, &e.Key, &e.Version, &oldRaw, &newRaw,
			&e.ChangedBy, &e.Reason, &e.At); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		e.OldValue, e.NewValue = json.RawMessage(oldRaw), json.RawMessage(newRaw)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MemoryStore dùng cho dev/test.
type MemoryStore struct {
	recs map[Key]Record
	hist []HistoryEntry
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{recs: map[Key]Record{}} }

func (m *MemoryStore) GetAll(context.Context) (map[Key]Record, error) {
	out := make(map[Key]Record, len(m.recs))
	for k, v := range m.recs {
		out[k] = v
	}
	return out, nil
}

func (m *MemoryStore) Put(_ context.Context, k Key, value json.RawMessage, expectVersion int,
	by, reason string, now time.Time, newID func(string) string) (Record, error) {
	cur := m.recs[k]
	if cur.Version != expectVersion {
		return Record{}, errs.Conflict("setting_version_conflict",
			"Cấu hình vừa được người khác thay đổi. Tải lại rồi thử lại.")
	}
	next := cur.Version + 1
	old := cur.Value
	if len(old) == 0 {
		// Giống PostgresStore: trước lần ghi đầu tiên, nhóm chạy bằng mặc định.
		if def, err := marshalDefault(k); err == nil {
			old = def
		}
	}
	m.hist = append([]HistoryEntry{{
		ID: newID("sth"), Key: k, Version: next, OldValue: old,
		NewValue: value, ChangedBy: by, Reason: reason, At: now,
	}}, m.hist...)
	r := Record{Key: k, Value: value, Version: next, UpdatedBy: by, UpdatedAt: now}
	m.recs[k] = r
	return r, nil
}

func (m *MemoryStore) History(_ context.Context, k Key, limit int) ([]HistoryEntry, error) {
	out := []HistoryEntry{}
	for _, e := range m.hist {
		if e.Key == k {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
