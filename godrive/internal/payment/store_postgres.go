package payment

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

type PostgresRepo struct{ db *sql.DB }

func NewPostgresRepo(db *sql.DB) *PostgresRepo { return &PostgresRepo{db: db} }

const payCols = `id, provider, order_id, provider_tx_id, account_id, purpose,
    amount_vnd, status, created_at, paid_at, expires_at`

func scanPayment(row interface{ Scan(...any) error }) (*Transaction, error) {
	var t Transaction
	var providerTx sql.NullString
	var paidAt sql.NullTime
	var amount int64
	err := row.Scan(&t.ID, &t.Provider, &t.OrderID, &providerTx, &t.AccountID,
		&t.Purpose, &amount, &t.Status, &t.CreatedAt, &paidAt, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.NotFound("payment_not_found", "Không tìm thấy giao dịch thanh toán.")
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	t.Amount = money.VND(amount)
	t.ProviderTxID = providerTx.String
	if paidAt.Valid {
		p := paidAt.Time
		t.PaidAt = &p
	}
	return &t, nil
}

func (r *PostgresRepo) Create(ctx context.Context, t *Transaction) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO payment_transactions
            (id, provider, order_id, account_id, purpose, amount_vnd, status, created_at, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		t.ID, t.Provider, t.OrderID, t.AccountID, t.Purpose,
		int64(t.Amount), t.Status, t.CreatedAt, t.ExpiresAt)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (r *PostgresRepo) GetByOrderID(ctx context.Context, p ProviderName, orderID string) (*Transaction, error) {
	return scanPayment(r.db.QueryRowContext(ctx,
		`SELECT `+payCols+` FROM payment_transactions WHERE provider=$1 AND order_id=$2`, p, orderID))
}

// MarkResult ghi kết quả MỘT lần.
//
// Điều kiện `status='PENDING'` ngay trong câu UPDATE là chốt chặn nguyên tử:
// cổng gửi lại webhook (chuyện thường ngày) hoặc hai pod cùng nhận một thông báo
// thì chỉ một bên ghi được, bên kia nhận Conflict và KHÔNG ghi sổ lần nữa.
func (r *PostgresRepo) MarkResult(ctx context.Context, txID string, n Notification, st Status, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `
        UPDATE payment_transactions
        SET status=$2, provider_tx_id=$3, raw_callback=$4,
            paid_at = CASE WHEN $2 = 'SUCCESS' THEN $5 ELSE paid_at END
        WHERE id=$1 AND status='PENDING'`,
		txID, st, n.ProviderTxID, []byte(n.Raw), at)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errs.Conflict("payment_already_settled", "Giao dịch này đã có kết quả.")
	}
	return nil
}

func (r *PostgresRepo) ExpireStale(ctx context.Context, now time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `
        UPDATE payment_transactions SET status='EXPIRED'
        WHERE status='PENDING' AND expires_at < $1`, now)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *PostgresRepo) ListByAccount(ctx context.Context, accountID string, limit int) ([]*Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+payCols+` FROM payment_transactions
         WHERE account_id=$1 ORDER BY created_at DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()
	out := []*Transaction{}
	for rows.Next() {
		t, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
