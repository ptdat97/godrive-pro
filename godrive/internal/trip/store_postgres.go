package trip

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/example/godrive/pkg/errs"
)

// TxEnqueuer ghi sự kiện vào outbox bằng chính transaction đang mở.
// Port khai báo ở đây (bên tiêu thụ) để trip không phải import internal/outbox.
type TxEnqueuer interface {
	EnqueueTx(ctx context.Context, tx *sql.Tx, topic string, payload any) error
}

type PostgresRepo struct {
	db     *sql.DB
	outbox TxEnqueuer
}

func NewPostgresRepo(db *sql.DB) *PostgresRepo { return &PostgresRepo{db: db} }

// UseOutbox bật mẫu Transactional Outbox. Không gọi thì Save trả msgs về cho
// người gọi tự phát, giống bản bộ nhớ.
func (r *PostgresRepo) UseOutbox(o TxEnqueuer) { r.outbox = o }

const tripCols = `id, rider_id, driver_id,
    pickup_lat, pickup_lng, pickup_address, pickup_note,
    dropoff_lat, dropoff_lng, dropoff_address, dropoff_note,
    vehicle_type, quote_id, fare, platform_fee, driver_earn, payment_method,
    status, cancel_by, cancel_reason,
    requested_at, assigned_at, arrived_at, started_at, ended_at, version, updated_at, rating`

func (r *PostgresRepo) scan(row interface{ Scan(...any) error }) (*Trip, error) {
	var t Trip
	err := row.Scan(&t.ID, &t.RiderID, &t.DriverID,
		&t.Pickup.Point.Lat, &t.Pickup.Point.Lng, &t.Pickup.Address, &t.Pickup.Note,
		&t.Dropoff.Point.Lat, &t.Dropoff.Point.Lng, &t.Dropoff.Address, &t.Dropoff.Note,
		&t.VehicleType, &t.QuoteID, &t.Fare, &t.PlatformFee, &t.DriverEarn, &t.PaymentMethod,
		&t.Status, &t.CancelBy, &t.Reason,
		&t.RequestedAt, &t.AssignedAt, &t.ArrivedAt, &t.StartedAt, &t.EndedAt, &t.Version, &t.UpdatedAt,
		&t.Rating)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.NotFound("trip_not_found", "Không tìm thấy chuyến đi.")
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return &t, nil
}

func (r *PostgresRepo) Create(ctx context.Context, t *Trip) error {
	_, err := r.db.ExecContext(ctx, `
        INSERT INTO trips (`+tripCols+`)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
		t.ID, t.RiderID, t.DriverID,
		t.Pickup.Point.Lat, t.Pickup.Point.Lng, t.Pickup.Address, t.Pickup.Note,
		t.Dropoff.Point.Lat, t.Dropoff.Point.Lng, t.Dropoff.Address, t.Dropoff.Note,
		t.VehicleType, t.QuoteID, t.Fare, t.PlatformFee, t.DriverEarn, t.PaymentMethod,
		t.Status, t.CancelBy, t.Reason,
		t.RequestedAt, t.AssignedAt, t.ArrivedAt, t.StartedAt, t.EndedAt, t.Version, t.UpdatedAt,
		t.Rating)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (r *PostgresRepo) Get(ctx context.Context, id string) (*Trip, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT `+tripCols+` FROM trips WHERE id=$1`, id))
}

// Save cập nhật trip và ghi trip_event trong cùng một transaction.
// Điều kiện version=$N là optimistic lock chống ghi đè đồng thời.
func (r *PostgresRepo) Save(ctx context.Context, t *Trip, e Event, msgs ...Message) ([]Message, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
        UPDATE trips SET driver_id=$2, status=$3, cancel_by=$4, cancel_reason=$5,
            assigned_at=$6, arrived_at=$7, started_at=$8, ended_at=$9,
            fare=$10, platform_fee=$11, driver_earn=$12,
            version=version+1, updated_at=$13
        WHERE id=$1 AND version=$14`,
		t.ID, t.DriverID, t.Status, t.CancelBy, t.Reason,
		t.AssignedAt, t.ArrivedAt, t.StartedAt, t.EndedAt,
		t.Fare, t.PlatformFee, t.DriverEarn, t.UpdatedAt, t.Version)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errs.Conflict("trip_version_conflict", "Chuyến vừa được cập nhật, vui lòng thử lại.")
	}

	meta, _ := json.Marshal(e.Meta)
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO trip_events (id, trip_id, from_status, to_status, actor, meta, at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.ID, e.TripID, e.From, e.To, e.Actor, meta, e.At); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}

	// Sự kiện ghi vào outbox TRONG CÙNG transaction với thay đổi nghiệp vụ:
	// hoặc cả hai cùng thành công, hoặc không có gì xảy ra. Đây chính là điều
	// mà "ghi DB xong rồi mới publish" không bảo đảm được.
	pending := msgs
	if r.outbox != nil {
		for _, m := range msgs {
			if err := r.outbox.EnqueueTx(ctx, tx, m.Topic, m.Payload); err != nil {
				return nil, errs.Wrap(errs.KindInternal, "outbox_enqueue_failed", "db", err)
			}
		}
		pending = nil // relay sẽ phát
	}

	t.Version++
	if err := tx.Commit(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return pending, nil
}

// SetRating ghi điểm MỘT lần. Điều kiện `rating IS NULL` ngay trong câu UPDATE
// là chốt chặn nguyên tử: hai request chấm điểm song song thì chỉ một cái thắng.
func (r *PostgresRepo) SetRating(ctx context.Context, tripID string, rating int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE trips SET rating=$2, updated_at=now() WHERE id=$1 AND rating IS NULL`, tripID, rating)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.Conflict("trip_already_rated", "Chuyến này đã được đánh giá.")
	}
	return nil
}

func (r *PostgresRepo) ListByRider(ctx context.Context, riderID string, limit int) ([]*Trip, error) {
	return r.list(ctx, `SELECT `+tripCols+` FROM trips WHERE rider_id=$1 ORDER BY requested_at DESC LIMIT $2`, riderID, limit)
}

func (r *PostgresRepo) ListByStatus(ctx context.Context, s Status, limit int) ([]*Trip, error) {
	return r.list(ctx, `SELECT `+tripCols+` FROM trips WHERE status=$1 ORDER BY requested_at LIMIT $2`, s, limit)
}

func (r *PostgresRepo) ActiveByDriver(ctx context.Context, driverID string) (*Trip, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT `+tripCols+` FROM trips
        WHERE driver_id=$1 AND status IN ('ASSIGNED','ARRIVED','IN_PROGRESS','COMPLETED')
        ORDER BY requested_at DESC LIMIT 1`, driverID))
}

func (r *PostgresRepo) Events(ctx context.Context, tripID string) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, trip_id, from_status, to_status, actor, meta, at FROM trip_events WHERE trip_id=$1 ORDER BY at`, tripID)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var meta []byte
		if err := rows.Scan(&e.ID, &e.TripID, &e.From, &e.To, &e.Actor, &meta, &e.At); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		_ = json.Unmarshal(meta, &e.Meta)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *PostgresRepo) list(ctx context.Context, q string, args ...any) ([]*Trip, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer rows.Close()
	var out []*Trip
	for rows.Next() {
		t, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
