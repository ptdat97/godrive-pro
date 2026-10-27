package driver

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
	"github.com/example/godrive/pkg/page"
)

// Matches cho biết tài xế có khớp bộ lọc không. Bản bộ nhớ dùng trực tiếp;
// bản Postgres dựng mệnh đề WHERE tương đương trong listQuery.
func (f ListFilter) Matches(d *Driver) bool {
	if len(f.Statuses) > 0 {
		ok := false
		for _, st := range f.Statuses {
			ok = ok || d.Status == st
		}
		if !ok {
			return false
		}
	}
	if f.KYC != "" && d.KYC != f.KYC {
		return false
	}
	if f.City != "" && d.City != f.City {
		return false
	}
	if f.Query != "" {
		q := strings.ToLower(f.Query)
		if !strings.Contains(strings.ToLower(d.FullName), q) &&
			!strings.Contains(strings.ToLower(d.Phone), q) &&
			!strings.Contains(strings.ToLower(d.Vehicle.Plate), q) {
			return false
		}
	}
	if f.InDebtBeyond > 0 && d.WalletBalance >= f.InDebtBeyond.Neg() {
		return false
	}
	return true
}

func (r *MemoryRepo) List(_ context.Context, f ListFilter, after page.Cursor, limit int) ([]*Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []*Driver{}
	for _, d := range r.data {
		if f.Matches(d) && after.Before(d.CreatedAt, d.ID) {
			out = append(out, clone(d))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *MemoryRepo) Counts(_ context.Context, now time.Time, debtLimit money.VND) (Counts, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := Counts{ByStatus: map[Status]int{}}
	for _, d := range r.data {
		c.ByStatus[d.Status]++
		if d.KYC == KYCPending {
			c.PendingKYC++
		}
		if (d.Status == StatusIdle || d.Status == StatusOffline) && d.WalletBalance < debtLimit.Neg() {
			c.InDebt++
		}
		if d.KYC != KYCApproved || d.Status == StatusSuspended {
			continue
		}
		if as := d.Documents.Alerts(now); len(as) > 0 {
			switch as[0].Level {
			case AlertExpired:
				c.DocsExpired++
			case AlertUrgent:
				c.DocsExpiring++
			}
		}
	}
	return c, nil
}

func (r *PostgresRepo) List(ctx context.Context, f ListFilter, after page.Cursor, limit int) ([]*Driver, error) {
	where := []string{"TRUE"}
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if len(f.Statuses) > 0 {
		ss := make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			ss[i] = string(st)
		}
		where = append(where, "status = ANY("+arg(ss)+"::text[])")
	}
	if f.KYC != "" {
		where = append(where, "kyc_state = "+arg(string(f.KYC)))
	}
	if f.City != "" {
		where = append(where, "city = "+arg(f.City))
	}
	if f.Query != "" {
		// Thoát ký tự đại diện của LIKE: người dùng gõ "%" hay "_" là tìm đúng
		// ký tự đó, không phải khớp mọi thứ.
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)
		p := arg("%" + esc + "%")
		where = append(where, "(full_name ILIKE "+p+" OR phone ILIKE "+p+" OR vehicle_plate ILIKE "+p+")")
	}
	if f.InDebtBeyond > 0 {
		where = append(where, "wallet_balance < "+arg(f.InDebtBeyond.Neg()))
	}
	if !after.IsZero() {
		where = append(where, "(created_at, id) < ("+arg(after.At)+", "+arg(after.ID)+")")
	}
	q := `SELECT ` + driverCols + ` FROM drivers WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at DESC, id DESC LIMIT ` + arg(limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer rows.Close()
	out := []*Driver{}
	for rows.Next() {
		d, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Counts đếm bằng một lượt quét có điều kiện. Quy tắc giấy tờ phải khớp
// Documents.Alerts: hết hạn = ngày sớm nhất đã QUA hôm nay (giờ VN); sắp hết
// hạn = ngày sớm nhất trong [hôm nay, hôm nay + 7]. LEAST bỏ qua NULL, nên
// giấy không có thời hạn không bị tính. TestCountsMatchAlertRules giữ hai bên
// khớp nhau.
func (r *PostgresRepo) Counts(ctx context.Context, now time.Time, debtLimit money.VND) (Counts, error) {
	c := Counts{ByStatus: map[Status]int{}}
	rows, err := r.db.QueryContext(ctx, `SELECT status, count(*) FROM drivers GROUP BY status`)
	if err != nil {
		return c, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	for rows.Next() {
		var st Status
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return c, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		c.ByStatus[st] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}

	today := TodayVN(now).Format(DateLayout)
	err = r.db.QueryRowContext(ctx, `
        SELECT
            count(*) FILTER (WHERE kyc_state = 'PENDING'),
            count(*) FILTER (WHERE status IN ('IDLE','OFFLINE') AND wallet_balance < $1),
            count(*) FILTER (WHERE kyc_state = 'APPROVED' AND status <> 'SUSPENDED'
                AND LEAST(insurance_until, license_until, inspection_until) < $2::date),
            count(*) FILTER (WHERE kyc_state = 'APPROVED' AND status <> 'SUSPENDED'
                AND LEAST(insurance_until, license_until, inspection_until)
                    BETWEEN $2::date AND $2::date + 7)
        FROM drivers`, debtLimit.Neg(), today).
		Scan(&c.PendingKYC, &c.InDebt, &c.DocsExpired, &c.DocsExpiring)
	if err != nil {
		return c, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return c, nil
}
