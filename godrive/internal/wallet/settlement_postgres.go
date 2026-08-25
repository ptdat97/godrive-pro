package wallet

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

type PostgresSettlementStore struct{ db *sql.DB }

func NewPostgresSettlementStore(db *sql.DB) *PostgresSettlementStore {
	return &PostgresSettlementStore{db: db}
}

const batchCols = `id, period_start, period_end, status, driver_count, total_vnd, created_at, closed_at`

func scanBatch(row interface{ Scan(...any) error }) (*Batch, error) {
	var b Batch
	var total int64
	var closed sql.NullTime
	err := row.Scan(&b.ID, &b.PeriodStart, &b.PeriodEnd, &b.Status,
		&b.DriverCount, &total, &b.CreatedAt, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.NotFound("batch_not_found", "Không tìm thấy đợt đối soát.")
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	b.Total = money.VND(total)
	if closed.Valid {
		c := closed.Time
		b.ClosedAt = &c
	}
	return &b, nil
}

// CreateBatch: UNIQUE (period_start, period_end) là chốt chặn tầng một chống
// chạy job hai lần cho cùng một kỳ.
func (s *PostgresSettlementStore) CreateBatch(ctx context.Context, b *Batch) error {
	res, err := s.db.ExecContext(ctx, `
        INSERT INTO settlement_batches (id, period_start, period_end, status, created_at)
        VALUES ($1,$2,$3,$4,$5) ON CONFLICT (period_start, period_end) DO NOTHING`,
		b.ID, b.PeriodStart, b.PeriodEnd, b.Status, b.CreatedAt)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.Conflict("batch_exists", "Kỳ này đã có đợt đối soát.")
	}
	return nil
}

func (s *PostgresSettlementStore) GetBatch(ctx context.Context, id string) (*Batch, error) {
	return scanBatch(s.db.QueryRowContext(ctx,
		`SELECT `+batchCols+` FROM settlement_batches WHERE id=$1`, id))
}

func (s *PostgresSettlementStore) GetBatchByPeriod(ctx context.Context, from, to time.Time) (*Batch, error) {
	return scanBatch(s.db.QueryRowContext(ctx,
		`SELECT `+batchCols+` FROM settlement_batches WHERE period_start=$1 AND period_end=$2`, from, to))
}

// AddItem: UNIQUE (batch_id, driver_id) là chốt chặn tầng hai.
func (s *PostgresSettlementStore) AddItem(ctx context.Context, it *Item) error {
	res, err := s.db.ExecContext(ctx, `
        INSERT INTO settlement_items (id, batch_id, driver_id, amount_vnd, status, reason, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (batch_id, driver_id) DO NOTHING`,
		it.ID, it.BatchID, it.DriverID, int64(it.Amount), it.Status, it.Reason, it.CreatedAt)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.Conflict("item_exists", "Tài xế này đã có dòng trong đợt.")
	}
	return nil
}

func (s *PostgresSettlementStore) ListItems(ctx context.Context, batchID string, st ItemStatus) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, batch_id, driver_id, amount_vnd, status, reason, created_at, paid_at
        FROM settlement_items WHERE batch_id=$1 AND ($2 = '' OR status=$2)
        ORDER BY driver_id`, batchID, string(st))
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()
	out := []*Item{}
	for rows.Next() {
		var it Item
		var amount int64
		var paid sql.NullTime
		if err := rows.Scan(&it.ID, &it.BatchID, &it.DriverID, &amount,
			&it.Status, &it.Reason, &it.CreatedAt, &paid); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		it.Amount = money.VND(amount)
		if paid.Valid {
			p := paid.Time
			it.PaidAt = &p
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

// MarkItemPaid: PENDING -> PAID nguyên tử. Đây là chỗ giành quyền chi.
func (s *PostgresSettlementStore) MarkItemPaid(ctx context.Context, itemID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE settlement_items SET status='PAID', paid_at=$2 WHERE id=$1 AND status='PENDING'`,
		itemID, at)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.Conflict("item_already_paid", "Dòng này đã được chi trả.")
	}
	return nil
}

func (s *PostgresSettlementStore) MarkItemFailed(ctx context.Context, itemID, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE settlement_items SET status='FAILED', reason=$2, paid_at=NULL WHERE id=$1`,
		itemID, reason)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (s *PostgresSettlementStore) CloseBatch(ctx context.Context, id string, st BatchStatus,
	count int, total money.VND, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE settlement_batches SET status=$2, driver_count=$3, total_vnd=$4, closed_at=$5
        WHERE id=$1`, id, st, count, int64(total), at)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

// DriversWithBalance tính số dư ví DƯƠNG của mọi tài xế bằng một câu truy vấn.
//
// Tính bằng SUM trên sổ cái chứ không đọc cột cache drivers.wallet_balance:
// tiền ra khỏi hệ thống phải dựa trên nguồn sự thật, không dựa trên bản sao.
func (s *PostgresSettlementStore) DriversWithBalance(ctx context.Context, min money.VND) (map[string]money.VND, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT account_id, SUM(amount_vnd) AS bal
        FROM ledger_entries WHERE account_type='DRIVER_WALLET'
        GROUP BY account_id HAVING SUM(amount_vnd) >= $1`, int64(min))
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]money.VND{}
	for rows.Next() {
		var id string
		var bal int64
		if err := rows.Scan(&id, &bal); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		out[id] = money.VND(bal)
	}
	return out, rows.Err()
}
