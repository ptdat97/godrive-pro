package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
)

// PostgresRepo lưu tài khoản và thử thách OTP xuống Postgres.
//
// Nếu không có repo này thì bảng accounts không bao giờ được ghi, mà cả
// drivers.account_id lẫn trips.rider_id đều là khoá ngoại trỏ tới nó — nghĩa là
// toàn bộ chế độ Postgres không dùng được.
//
// Thử thách OTP có TTL 5 phút và bị ghi/xoá liên tục nên Redis hợp hơn. Bảng
// otp_challenges là đường dự phòng cho môi trường chưa có Redis; job dọn chạy
// trong worker nền (xem Service.SweepExpiredChallenges).
type PostgresRepo struct{ db *sql.DB }

func NewPostgresRepo(db *sql.DB) *PostgresRepo { return &PostgresRepo{db: db} }

const accountCols = `id, phone, full_name, role, blocked, created_at`

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Phone, &a.FullName, &a.Role, &a.Blocked, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.NotFound("account_not_found", "Không tìm thấy tài khoản.")
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return &a, nil
}

// UpsertAccount tạo tài khoản nếu chưa có, ngược lại trả về tài khoản đang tồn tại.
//
// DO UPDATE (thay vì DO NOTHING) là cố ý: DO NOTHING không trả về dòng nào qua
// RETURNING, nên sẽ phải SELECT thêm một vòng. Gán lại chính giá trị cũ là phép
// cập nhật rỗng, đủ để RETURNING hoạt động trong cả hai nhánh.
func (r *PostgresRepo) UpsertAccount(ctx context.Context, phone string, role authn.Role, now time.Time) (*Account, error) {
	return scanAccount(r.db.QueryRowContext(ctx, `
        INSERT INTO accounts (id, phone, role, created_at)
        VALUES ($1,$2,$3,$4)
        ON CONFLICT (phone, role) DO UPDATE SET phone = EXCLUDED.phone
        RETURNING `+accountCols, id.New("acc"), phone, role, now))
}

func (r *PostgresRepo) GetAccount(ctx context.Context, aid string) (*Account, error) {
	return scanAccount(r.db.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE id=$1`, aid))
}

// SaveChallenge dùng upsert vì VerifyOTP gọi lại chính hàm này để tăng số lần
// nhập sai. Chỉ attempts được phép đổi — mã hash và hạn thì không.
func (r *PostgresRepo) SaveChallenge(ctx context.Context, c Challenge) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO otp_challenges (id, phone, role, code_hash, attempts, expires_at, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)
        ON CONFLICT (id) DO UPDATE SET attempts = EXCLUDED.attempts`,
		c.ID, c.Phone, c.Role, c.CodeHash, c.Attempts, c.ExpiresAt, c.CreatedAt)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (r *PostgresRepo) GetChallenge(ctx context.Context, cid string) (Challenge, error) {
	var c Challenge
	err := r.db.QueryRowContext(ctx, `
        SELECT id, phone, role, code_hash, attempts, expires_at, created_at
        FROM otp_challenges WHERE id=$1`, cid).
		Scan(&c.ID, &c.Phone, &c.Role, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Challenge{}, errs.NotFound("challenge_not_found", "Phiên xác thực không tồn tại.")
	}
	if err != nil {
		return Challenge{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return c, nil
}

func (r *PostgresRepo) DeleteChallenge(ctx context.Context, cid string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM otp_challenges WHERE id=$1`, cid); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

// DeleteExpiredChallenges dọn thử thách quá hạn. VerifyOTP đã tự xoá cái nó
// chạm tới, nhưng thử thách không ai xác thực (gõ nhầm số, đổi ý) thì nằm lại
// mãi — đây là phần bù cho chúng.
func (r *PostgresRepo) DeleteExpiredChallenges(ctx context.Context, now time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM otp_challenges WHERE expires_at < $1`, now)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
