package driver

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// PostgresRepo dùng database/sql (stdlib). Driver thực tế đăng ký ở
// internal/platform/store — mặc định pgx stdlib:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//	sql.Open("pgx", dsn)
//
// DocumentCipher mã hoá giấy tờ nhạy cảm. Port khai báo ở đây (bên tiêu thụ)
// nên driver không phải phụ thuộc vào một thư viện mã hoá cụ thể.
type DocumentCipher interface {
	Encrypt(plain string) (string, error)
	Decrypt(enc string) (string, error)
	BlindIndex(plain string) string
}

type PostgresRepo struct {
	db     *sql.DB
	cipher DocumentCipher
}

func NewPostgresRepo(db *sql.DB) *PostgresRepo { return &PostgresRepo{db: db} }

// UseCipher bật mã hoá giấy tờ ở tầng ứng dụng.
//
// Không gọi thì giấy tờ lưu dạng thô — chấp nhận được ở môi trường phát triển,
// KHÔNG chấp nhận được ở production (Nghị định 13/2023).
func (r *PostgresRepo) UseCipher(c DocumentCipher) { r.cipher = c }

// encDoc mã hoá một trường giấy tờ nếu đã bật mã hoá.
func (r *PostgresRepo) encDoc(v string) (string, error) {
	if r.cipher == nil {
		return v, nil
	}
	return r.cipher.Encrypt(v)
}

// decDoc giải mã, và KHÔNG nuốt lỗi: giải mã thất bại nghĩa là sai khoá hoặc dữ
// liệu đã bị sửa — trả ra chuỗi rỗng sẽ biến sự cố bảo mật thành "hồ sơ thiếu
// giấy tờ", một triệu chứng dẫn người tìm lỗi đi sai hướng.
func (r *PostgresRepo) decDoc(v string) (string, error) {
	if r.cipher == nil {
		return v, nil
	}
	return r.cipher.Decrypt(v)
}

func (r *PostgresRepo) blindIndex(v string) string {
	if r.cipher == nil {
		return ""
	}
	return r.cipher.BlindIndex(v)
}

// Giấy tờ nằm ở cuối danh sách cột để thứ tự tham số của Create dễ đối chiếu.
// scan() ĐỌC LẠI cả nhóm này — trước đây nó bị bỏ qua, nên Get() luôn trả
// Documents rỗng và admin không xem được gì khi duyệt hồ sơ.
const driverCols = `id, account_id, full_name, phone, city,
    vehicle_type, vehicle_plate, vehicle_model, vehicle_color,
    kyc_state, status, wallet_balance, version, created_at, updated_at,
    offers_received, offers_accepted, completed_trips, trips_cancelled,
    rating_sum, rating_count, idle_since,
    national_id, driver_license, vehicle_reg_no, insurance_no, insurance_until`

// nullDate đổi chuỗi YYYY-MM-DD sang giá trị DATE; chuỗi rỗng thành NULL.
// Service đã validate trước khi tới đây; kiểm lại ở đây là lớp phòng thủ thứ hai
// để dữ liệu hỏng không lọt xuống đĩa.
func nullDate(s string) (any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return nil, errs.Invalid("insurance_until_invalid", "Ngày hết hạn bảo hiểm phải theo dạng YYYY-MM-DD.")
	}
	return t, nil
}

func (r *PostgresRepo) scan(row interface{ Scan(...any) error }) (*Driver, error) {
	var d Driver
	var insuranceUntil, idleSince sql.NullTime
	err := row.Scan(&d.ID, &d.AccountID, &d.FullName, &d.Phone, &d.City,
		&d.Vehicle.Type, &d.Vehicle.Plate, &d.Vehicle.Model, &d.Vehicle.Color,
		&d.KYC, &d.Status, &d.WalletBalance, &d.Version, &d.CreatedAt, &d.UpdatedAt,
		&d.Stats.OffersReceived, &d.Stats.OffersAccepted, &d.Stats.TripsCompleted,
		&d.Stats.TripsCancelled, &d.Stats.RatingSum, &d.Stats.RatingCount, &idleSince,
		&d.Documents.NationalID, &d.Documents.DriverLicense, &d.Documents.VehicleRegNo,
		&d.Documents.InsuranceNo, &insuranceUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	for _, f := range []*string{
		&d.Documents.NationalID, &d.Documents.DriverLicense,
		&d.Documents.VehicleRegNo, &d.Documents.InsuranceNo,
	} {
		plain, derr := r.decDoc(*f)
		if derr != nil {
			return nil, derr
		}
		*f = plain
	}
	if insuranceUntil.Valid {
		d.Documents.InsuranceUntil = insuranceUntil.Time.Format(DateLayout)
	}
	if idleSince.Valid {
		t := idleSince.Time
		d.IdleSince = &t
	}
	return &d, nil
}

func (r *PostgresRepo) Create(ctx context.Context, d *Driver) error {
	until, err := nullDate(d.Documents.InsuranceUntil)
	if err != nil {
		return err
	}
	enc, err := r.encDocuments(d.Documents)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
        INSERT INTO drivers (`+driverCols+`, national_id_idx)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
                $16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
		d.ID, d.AccountID, d.FullName, d.Phone, d.City,
		d.Vehicle.Type, d.Vehicle.Plate, d.Vehicle.Model, d.Vehicle.Color,
		d.KYC, d.Status, d.WalletBalance, d.Version, d.CreatedAt, d.UpdatedAt,
		d.Stats.OffersReceived, d.Stats.OffersAccepted, d.Stats.TripsCompleted,
		d.Stats.TripsCancelled, d.Stats.RatingSum, d.Stats.RatingCount, d.IdleSince,
		enc.NationalID, enc.DriverLicense, enc.VehicleRegNo,
		enc.InsuranceNo, until, r.blindIndex(d.Documents.NationalID))
	if err != nil {
		return errs.Wrap(errs.KindConflict, "driver_create_failed", "Không tạo được hồ sơ tài xế.", err)
	}
	return nil
}

// encDocuments mã hoá toàn bộ nhóm giấy tờ.
func (r *PostgresRepo) encDocuments(d Documents) (Documents, error) {
	out := d
	for _, f := range []*string{&out.NationalID, &out.DriverLicense, &out.VehicleRegNo, &out.InsuranceNo} {
		v, err := r.encDoc(*f)
		if err != nil {
			return Documents{}, err
		}
		*f = v
	}
	return out, nil
}

func (r *PostgresRepo) Get(ctx context.Context, id string) (*Driver, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT `+driverCols+` FROM drivers WHERE id=$1`, id))
}

func (r *PostgresRepo) GetByAccount(ctx context.Context, accountID string) (*Driver, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT `+driverCols+` FROM drivers WHERE account_id=$1`, accountID))
}

// UpdateStatus là compare-and-swap: WHERE status=$from AND version=$v.
// Nếu RowsAffected = 0 nghĩa là có request khác đã đổi trạng thái trước.
func (r *PostgresRepo) UpdateStatus(ctx context.Context, id string, from, to Status, version int) error {
	// idle_since đặt/xoá NGAY TRONG câu CAS: trạng thái và mốc bắt đầu rảnh
	// phải đổi cùng lúc, nếu không sẽ có khoảnh khắc tài xế IDLE mà không biết
	// đã rảnh từ bao giờ.
	res, err := r.db.ExecContext(ctx, `
        UPDATE drivers SET status=$1, version=version+1, updated_at=now(),
            idle_since = CASE WHEN $1 = 'IDLE' THEN now() ELSE NULL END
        WHERE id=$2 AND status=$3 AND version=$4`, to, id, from, version)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errs.Conflict("driver_state_changed", "Trạng thái tài xế vừa thay đổi, vui lòng thử lại.")
	}
	return nil
}

func (r *PostgresRepo) Update(ctx context.Context, d *Driver) error {
	until, err := nullDate(d.Documents.InsuranceUntil)
	if err != nil {
		return err
	}
	enc, err := r.encDocuments(d.Documents)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
        UPDATE drivers SET full_name=$2, phone=$3, city=$4, kyc_state=$5,
            wallet_balance=$6,
            national_id=$7, driver_license=$8, vehicle_reg_no=$9,
            insurance_no=$10, insurance_until=$11, national_id_idx=$12,
            version=version+1, updated_at=now()
        WHERE id=$1`,
		d.ID, d.FullName, d.Phone, d.City, d.KYC, d.WalletBalance,
		enc.NationalID, enc.DriverLicense, enc.VehicleRegNo,
		enc.InsuranceNo, until, r.blindIndex(d.Documents.NationalID))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

// ApplyStats cộng dồn số đếm bằng MỘT câu UPDATE.
//
// Cộng dồn trong SQL chứ không đọc-sửa-ghi ở Go: nhiều sự kiện của cùng một tài
// xế (offer gửi đi, offer được nhận, chuyến hoàn tất) đến song song là chuyện
// bình thường, và đọc-sửa-ghi sẽ nuốt mất một phần trong số đó.
//
// Không tăng version, cùng lý do với UpdateWalletBalance: thống kê không phải
// trạng thái được CAS bảo vệ.
func (r *PostgresRepo) ApplyStats(ctx context.Context, driverID string, d StatsDelta) error {
	_, err := r.db.ExecContext(ctx, `
        UPDATE drivers SET
            offers_received = offers_received + $2,
            offers_accepted = offers_accepted + $3,
            completed_trips = completed_trips + $4,
            trips_cancelled = trips_cancelled + $5,
            rating_sum      = rating_sum + $6,
            rating_count    = rating_count + $7,
            updated_at      = now()
        WHERE id=$1`, driverID,
		d.OffersReceived, d.OffersAccepted, d.TripsCompleted, d.TripsCancelled,
		d.RatingSum, d.RatingCount)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

// UpdateWalletBalance không tăng version — xem ghi chú ở Repository.
func (r *PostgresRepo) UpdateWalletBalance(ctx context.Context, driverID string, bal money.VND) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE drivers SET wallet_balance=$2, updated_at=now() WHERE id=$1`, driverID, int64(bal))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (r *PostgresRepo) ListByStatus(ctx context.Context, s Status, limit int) ([]*Driver, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+driverCols+` FROM drivers WHERE status=$1 ORDER BY id LIMIT $2`, s, limit)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer rows.Close()
	var out []*Driver
	for rows.Next() {
		d, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
