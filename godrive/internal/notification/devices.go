package notification

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/godrive/pkg/errs"
)

// Device là một máy nhận được thông báo đẩy.
type Device struct {
	AccountID string    `json:"-"`
	DeviceID  string    `json:"device_id"`
	Token     string    `json:"-"` // token FCM là bí mật của thiết bị, không trả ra API
	Platform  string    `json:"platform"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeviceStore lưu token thiết bị.
type DeviceStore interface {
	// Upsert ghi token cho (tài khoản, máy). Token đó nếu đang thuộc máy hay
	// tài khoản khác thì chuyển sang — một máy đổi người dùng không được tiếp
	// tục nhận thông báo của người cũ.
	Upsert(ctx context.Context, d Device) error
	ListByAccount(ctx context.Context, accountID string) ([]Device, error)
	DeleteToken(ctx context.Context, token string) error
	DeleteDevice(ctx context.Context, accountID, deviceID string) error
	DeleteAccount(ctx context.Context, accountID string) error
}

func validPlatform(p string) bool { return p == "android" || p == "ios" }

// validateDevice chặn rác trước khi xuống đĩa. Token FCM dài cỡ 150–200 ký
// tự; 4.096 là trần thoáng mà vẫn chặn được ai đó nhồi dữ liệu.
func validateDevice(d Device) error {
	if strings.TrimSpace(d.Token) == "" || len(d.Token) > 4096 {
		return errs.Invalid("device_token_invalid", "Token thiết bị không hợp lệ.")
	}
	if !validPlatform(d.Platform) {
		return errs.Invalid("platform_invalid", "Nền tảng phải là android hoặc ios.")
	}
	if d.DeviceID == "" {
		return errs.Invalid("device_id_required", "Phiên đăng nhập không có mã thiết bị.")
	}
	return nil
}

// ------------------------------------------------------------------ bộ nhớ

type MemoryDeviceStore struct {
	mu   sync.Mutex
	byTk map[string]Device
}

func NewMemoryDeviceStore() *MemoryDeviceStore { return &MemoryDeviceStore{byTk: map[string]Device{}} }

func (m *MemoryDeviceStore) Upsert(_ context.Context, d Device) error {
	if err := validateDevice(d); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for tk, x := range m.byTk {
		if x.AccountID == d.AccountID && x.DeviceID == d.DeviceID {
			delete(m.byTk, tk)
		}
	}
	m.byTk[d.Token] = d
	return nil
}

func (m *MemoryDeviceStore) ListByAccount(_ context.Context, accountID string) ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Device
	for _, d := range m.byTk {
		if d.AccountID == accountID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out, nil
}

func (m *MemoryDeviceStore) DeleteToken(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byTk, token)
	return nil
}

func (m *MemoryDeviceStore) DeleteDevice(_ context.Context, accountID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tk, d := range m.byTk {
		if d.AccountID == accountID && d.DeviceID == deviceID {
			delete(m.byTk, tk)
		}
	}
	return nil
}

func (m *MemoryDeviceStore) DeleteAccount(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tk, d := range m.byTk {
		if d.AccountID == accountID {
			delete(m.byTk, tk)
		}
	}
	return nil
}

// ------------------------------------------------------------------ Postgres

type PostgresDeviceStore struct{ db *sql.DB }

func NewPostgresDeviceStore(db *sql.DB) *PostgresDeviceStore { return &PostgresDeviceStore{db: db} }

func dbErr(err error) error { return errs.Wrap(errs.KindInternal, "db_error", "db", err) }

// Upsert trong một transaction: xoá token cũ của máy này, rồi ghi token mới
// (chiếm lại nếu token đó đang gắn với máy/tài khoản khác).
func (p *PostgresDeviceStore) Upsert(ctx context.Context, d Device) error {
	if err := validateDevice(d); err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_tokens
        WHERE account_id=$1 AND device_id=$2 AND token<>$3`, d.AccountID, d.DeviceID, d.Token); err != nil {
		return dbErr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_tokens (token, account_id, device_id, platform, updated_at)
        VALUES ($1,$2,$3,$4,$5)
        ON CONFLICT (token) DO UPDATE SET account_id=EXCLUDED.account_id, device_id=EXCLUDED.device_id,
            platform=EXCLUDED.platform, updated_at=EXCLUDED.updated_at`,
		d.Token, d.AccountID, d.DeviceID, d.Platform, d.UpdatedAt); err != nil {
		return dbErr(err)
	}
	if err := tx.Commit(); err != nil {
		return dbErr(err)
	}
	return nil
}

func (p *PostgresDeviceStore) ListByAccount(ctx context.Context, accountID string) ([]Device, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT token, account_id, device_id, platform, updated_at
        FROM device_tokens WHERE account_id=$1 ORDER BY device_id`, accountID)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.Token, &d.AccountID, &d.DeviceID, &d.Platform, &d.UpdatedAt); err != nil {
			return nil, dbErr(err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *PostgresDeviceStore) DeleteToken(ctx context.Context, token string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM device_tokens WHERE token=$1`, token)
	if err != nil {
		return dbErr(err)
	}
	return nil
}

func (p *PostgresDeviceStore) DeleteDevice(ctx context.Context, accountID, deviceID string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM device_tokens WHERE account_id=$1 AND device_id=$2`, accountID, deviceID)
	if err != nil {
		return dbErr(err)
	}
	return nil
}

func (p *PostgresDeviceStore) DeleteAccount(ctx context.Context, accountID string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM device_tokens WHERE account_id=$1`, accountID)
	if err != nil {
		return dbErr(err)
	}
	return nil
}
