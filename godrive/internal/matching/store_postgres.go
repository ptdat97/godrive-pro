package matching

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
)

// PostgresStore lưu lời mời và khoá chuyến xuống Postgres.
//
// Đây là bước kích hoạt chốt chặn cuối cùng của việc chống ghép trùng:
// UNIQUE INDEX offers_one_accepted_per_trip. Ở chế độ bộ nhớ, hai lớp bảo vệ
// duy nhất là ClaimTrip và CAS Reserve — cả hai đều ở tầng ứng dụng.
//
// Production nên dùng Redis `SET trip:{id}:claim NX EX 30` cho ClaimTrip:
// khoá chuyến là thao tác nóng, ghi rất nhiều và sống rất ngắn. Bảng
// trip_claims ở đây là đường dùng được ngay khi chưa có Redis.
type PostgresStore struct {
	db  *sql.DB
	clk clock.Clock
}

func NewPostgresStore(db *sql.DB, clk clock.Clock) *PostgresStore {
	return &PostgresStore{db: db, clk: clk}
}

func (s *PostgresStore) SaveOffers(ctx context.Context, offers []Offer) error {
	if len(offers) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO offers (id, trip_id, driver_id, round, status, eta_sec, pickup_distance_m, created_at, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, o := range offers {
		if _, err := stmt.ExecContext(ctx, o.ID, o.TripID, o.DriverID, o.Round,
			o.Status, o.ETASec, o.PickupM, o.CreatedAt, o.ExpiresAt); err != nil {
			return errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

const offerCols = `id, trip_id, driver_id, round, status, eta_sec, pickup_distance_m, created_at, expires_at`

func scanOffer(row interface{ Scan(...any) error }) (Offer, error) {
	var o Offer
	err := row.Scan(&o.ID, &o.TripID, &o.DriverID, &o.Round, &o.Status,
		&o.ETASec, &o.PickupM, &o.CreatedAt, &o.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Offer{}, errs.NotFound("offer_not_found", "Không tìm thấy lời mời.")
	}
	if err != nil {
		return Offer{}, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return o, nil
}

func (s *PostgresStore) GetOffer(ctx context.Context, offerID string) (Offer, error) {
	return scanOffer(s.db.QueryRowContext(ctx,
		`SELECT `+offerCols+` FROM offers WHERE id=$1`, offerID))
}

// ClaimTrip là thao tác NGUYÊN TỬ quyết định ai giành được chuyến.
//
// `ON CONFLICT (trip_id) DO UPDATE ... WHERE trip_claims.expires_at < now()`
// gộp ba việc vào một câu: giành khoá nếu chưa ai giữ, giành lại nếu khoá cũ đã
// hết hạn, và giữ nguyên nếu người khác đang giữ. RETURNING cho biết ai là chủ
// sau khi câu lệnh chạy — nếu là chính mình thì thắng.
//
// Cùng một tài xế gọi lại vẫn thắng (idempotent): app mobile retry khi mạng
// chập chờn không được biến thành "chuyến đã có người khác nhận".
func (s *PostgresStore) ClaimTrip(ctx context.Context, tripID, driverID string, ttl time.Duration) (bool, error) {
	now := s.clk.Now()
	var owner string
	err := s.db.QueryRowContext(ctx, `
        INSERT INTO trip_claims (trip_id, driver_id, expires_at)
        VALUES ($1,$2,$3)
        ON CONFLICT (trip_id) DO UPDATE
            SET driver_id = EXCLUDED.driver_id, expires_at = EXCLUDED.expires_at
            WHERE trip_claims.expires_at < $4 OR trip_claims.driver_id = EXCLUDED.driver_id
        RETURNING driver_id`, tripID, driverID, now.Add(ttl), now).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		// Không dòng nào trả về = mệnh đề WHERE của DO UPDATE chặn lại,
		// nghĩa là người khác đang giữ khoá còn hạn.
		return false, nil
	}
	if err != nil {
		return false, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return owner == driverID, nil
}

func (s *PostgresStore) UpdateStatus(ctx context.Context, offerID string, st OfferStatus) error {
	res, err := s.db.ExecContext(ctx, `UPDATE offers SET status=$2 WHERE id=$1`, offerID, st)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.NotFound("offer_not_found", "Không tìm thấy lời mời.")
	}
	return nil
}

func (s *PostgresStore) PendingForDriver(ctx context.Context, driverID string) ([]Offer, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT `+offerCols+` FROM offers
        WHERE driver_id=$1 AND status='PENDING' AND expires_at > $2
        ORDER BY created_at`, driverID, s.clk.Now())
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Offer
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ExpireOffers(ctx context.Context, tripID, except string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE offers SET status='LOST'
        WHERE trip_id=$1 AND id <> $2 AND status='PENDING'`, tripID, except)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}
