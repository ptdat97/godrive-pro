package wallet

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// PostgresLedger là sổ cái kép trên Postgres.
//
// Hai bảng, hai vai trò khác nhau:
//   - ledger_transactions: khoá idempotency. PRIMARY KEY (tx_id) là chốt chặn ở
//     tầng CSDL — worker retry bao nhiêu lần cũng chỉ ghi được một lần, kể cả
//     khi hai tiến trình chạy song song.
//   - ledger_entries: các bút toán. CHỈ INSERT, không bao giờ UPDATE/DELETE.
//     Ghi sai thì ghi bút toán đảo (ADJUSTMENT), không sửa lịch sử.
//
// Cả hai ghi trong CÙNG một transaction: không thể có giao dịch được đánh dấu
// đã ghi mà bút toán lại thiếu, hoặc ngược lại.
type PostgresLedger struct{ db *sql.DB }

func NewPostgresLedger(db *sql.DB) *PostgresLedger { return &PostgresLedger{db: db} }

// Post ghi một giao dịch. Idempotent theo TxID.
func (l *PostgresLedger) Post(ctx context.Context, tx Transaction) error {
	// Validate TRƯỚC khi mở transaction: giao dịch lệch không bao giờ được
	// chạm tới cơ sở dữ liệu, kể cả trong một transaction sẽ bị rollback.
	if err := tx.Validate(); err != nil {
		return err
	}

	dbtx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = dbtx.Rollback() }()

	res, err := dbtx.ExecContext(ctx, `
        INSERT INTO ledger_transactions (tx_id, ref_type, ref_id, created_at)
        VALUES ($1,$2,$3,$4)
        ON CONFLICT (tx_id) DO NOTHING`,
		tx.ID, tx.RefType, tx.RefID, tx.At)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Giao dịch này đã ghi rồi. Đây là đường đi BÌNH THƯỜNG khi worker
		// retry, không phải lỗi.
		return nil
	}

	stmt, err := dbtx.PrepareContext(ctx, `
        INSERT INTO ledger_entries
            (id, tx_id, account_id, account_type, amount_vnd, ref_type, ref_id, memo, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range tx.Entries {
		if _, err := stmt.ExecContext(ctx, e.ID, e.TxID, e.AccountID, e.AccountType,
			int64(e.Amount), e.RefType, e.RefID, e.Memo, e.CreatedAt); err != nil {
			return errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
	}
	if err := dbtx.Commit(); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

// Balance tính số dư bằng SUM. Không có bảng số dư nào được cập nhật trực tiếp —
// nguồn sự thật luôn là tập bút toán.
func (l *PostgresLedger) Balance(ctx context.Context, accountID string, at AccountType) (money.VND, error) {
	var v int64
	err := l.db.QueryRowContext(ctx, `
        SELECT COALESCE(SUM(amount_vnd), 0) FROM ledger_entries
        WHERE account_id = $1 AND account_type = $2`, accountID, at).Scan(&v)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return money.VND(v), nil
}

// Statement liệt kê bút toán trong khoảng [from, to). Thứ tự ổn định theo
// (created_at, id) để phân trang và đối soát cho ra kết quả lặp lại được.
func (l *PostgresLedger) Statement(ctx context.Context, accountID string, from, to time.Time) ([]Entry, error) {
	rows, err := l.db.QueryContext(ctx, `
        SELECT id, tx_id, account_id, account_type, amount_vnd, ref_type, ref_id, memo, created_at
        FROM ledger_entries
        WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
        ORDER BY created_at, id`, accountID, from, to)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var e Entry
		var amt int64
		if err := rows.Scan(&e.ID, &e.TxID, &e.AccountID, &e.AccountType, &amt,
			&e.RefType, &e.RefID, &e.Memo, &e.CreatedAt); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		e.Amount = money.VND(amt)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return out, nil
}

func (l *PostgresLedger) Exists(ctx context.Context, txID string) (bool, error) {
	var one int
	err := l.db.QueryRowContext(ctx,
		`SELECT 1 FROM ledger_transactions WHERE tx_id = $1`, txID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return true, nil
}
