// Package driver quản lý hồ sơ tài xế, eKYC và trạng thái trực tuyến.
package driver

import (
	"context"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

type VehicleType string

const (
	VehicleBike VehicleType = "BIKE"  // xe máy 2 bánh
	VehicleCar4 VehicleType = "CAR_4" // ô tô 4 chỗ
	VehicleCar7 VehicleType = "CAR_7" // ô tô 7 chỗ
)

func (v VehicleType) Valid() bool {
	switch v {
	case VehicleBike, VehicleCar4, VehicleCar7:
		return true
	}
	return false
}

type Status string

const (
	StatusOffline   Status = "OFFLINE"
	StatusIdle      Status = "IDLE"      // online, sẵn sàng nhận chuyến
	StatusAssigned  Status = "ASSIGNED"  // đã nhận chuyến, đang tới đón
	StatusOnTrip    Status = "ON_TRIP"   // đang chở khách
	StatusSuspended Status = "SUSPENDED" // bị khoá (gian lận, nợ ví...)
)

type KYCState string

const (
	KYCPending  KYCState = "PENDING"
	KYCApproved KYCState = "APPROVED"
	KYCRejected KYCState = "REJECTED"
)

type Vehicle struct {
	Type  VehicleType `json:"type"`
	Plate string      `json:"plate"` // biển số, ví dụ 59X1-123.45
	Model string      `json:"model"`
	Color string      `json:"color"`
}

// DateLayout là định dạng của Documents.InsuranceUntil.
const DateLayout = "2006-01-02"

// Documents là giấy tờ bắt buộc theo Nghị định 10/2020.
type Documents struct {
	NationalID     string `json:"national_id"`     // số CCCD gắn chip
	DriverLicense  string `json:"driver_license"`  // số GPLX
	VehicleRegNo   string `json:"vehicle_reg_no"`  // số đăng ký xe (cà vẹt)
	InsuranceNo    string `json:"insurance_no"`    // bảo hiểm TNDS bắt buộc
	InsuranceUntil string `json:"insurance_until"` // YYYY-MM-DD
}

// Ngưỡng làm mượt Bayes cho thống kê tài xế.
//
// Vấn đề nếu tính tỉ lệ thô: tài xế mới nhận đúng một lời mời rồi bỏ lỡ (đang
// kẹt xe, điện thoại trong túi) sẽ có acceptance = 0, rơi xuống cuối bảng chấm
// điểm, và vì thế không bao giờ nhận được lời mời thứ hai để gỡ lại. Một mẫu
// duy nhất khoá chết sự nghiệp của họ.
//
// Cách chữa: cộng thêm một "tiền nghiệm" đóng vai trò N quan sát ảo ở mức mặc
// định. Tài xế mới bắt đầu đúng ở mức mặc định và cần khoảng N lời mời thật thì
// hành vi thật mới lấn át được tiền nghiệm.
const (
	PriorOffers     = 10.0 // số lời mời ảo
	PriorAcceptance = 0.8  // tỉ lệ nhận của tài xế mới
	PriorTrips      = 10.0 // số chuyến ảo cho tỉ lệ huỷ
	PriorRatings    = 5.0  // số lượt đánh giá ảo
	PriorRating     = 5.0  // điểm của tài xế mới
)

// Stats là các SỐ ĐẾM thô. Đây là nguồn sự thật duy nhất; mọi tỉ lệ đều suy ra
// từ đây, không bao giờ được lưu song song thành cột riêng.
type Stats struct {
	OffersReceived int `json:"offers_received"`
	OffersAccepted int `json:"offers_accepted"`
	TripsCompleted int `json:"completed_trips"`
	TripsCancelled int `json:"trips_cancelled"`
	RatingSum      int `json:"-"`
	RatingCount    int `json:"rating_count"`
}

// StatsDelta là phần cộng thêm vào Stats. Repository cộng dồn nguyên tử.
type StatsDelta struct {
	OffersReceived int
	OffersAccepted int
	TripsCompleted int
	TripsCancelled int
	RatingSum      int
	RatingCount    int
}

// Empty cho biết delta có thay đổi gì không — tránh gọi DB vô ích.
func (d StatsDelta) Empty() bool { return d == StatsDelta{} }

// AcceptanceRate: tỉ lệ nhận lời mời, đã làm mượt. 0..1.
func (s Stats) AcceptanceRate() float64 {
	return (float64(s.OffersAccepted) + PriorOffers*PriorAcceptance) /
		(float64(s.OffersReceived) + PriorOffers)
}

// CancelRate: tỉ lệ tài xế tự huỷ chuyến đã nhận, đã làm mượt. 0..1.
func (s Stats) CancelRate() float64 {
	return float64(s.TripsCancelled) /
		(float64(s.TripsCompleted) + float64(s.TripsCancelled) + PriorTrips)
}

// Rating: điểm trung bình, đã làm mượt. 0..5.
func (s Stats) Rating() float64 {
	return (float64(s.RatingSum) + PriorRatings*PriorRating) /
		(float64(s.RatingCount) + PriorRatings)
}

type Driver struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	FullName  string    `json:"full_name"`
	Phone     string    `json:"phone"`
	City      string    `json:"city"` // HCM, HN, DN...
	Vehicle   Vehicle   `json:"vehicle"`
	Documents Documents `json:"-"` // dữ liệu nhạy cảm, không trả về API công khai
	KYC       KYCState  `json:"kyc"`
	Status    Status    `json:"status"`

	// Stats là số đếm thô; Rating/AcceptanceRate/CancelRate suy ra từ nó.
	Stats Stats `json:"stats"`

	// IdleSince là lúc tài xế BẮT ĐẦU rảnh; nil khi không ở trạng thái IDLE.
	// Hàm chấm điểm dùng nó để ưu tiên người chờ lâu.
	IdleSince *time.Time `json:"idle_since,omitempty"`

	// WalletBalance âm nghĩa là tài xế đang nợ chiết khấu tiền mặt.
	WalletBalance money.VND `json:"wallet_balance"`

	Version   int       `json:"-"` // optimistic lock, chống nhận 2 chuyến cùng lúc
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Rating là điểm đánh giá đã làm mượt, 0..5.
func (d *Driver) Rating() float64 { return d.Stats.Rating() }

// AcceptanceRate là tỉ lệ nhận lời mời đã làm mượt, 0..1.
func (d *Driver) AcceptanceRate() float64 { return d.Stats.AcceptanceRate() }

// CancelRate là tỉ lệ tự huỷ đã làm mượt, 0..1.
func (d *Driver) CancelRate() float64 { return d.Stats.CancelRate() }

// IdleSeconds là số giây tài xế đã rảnh tính tới now. 0 nếu không rảnh.
//
// Khác hẳn độ cũ của ping vị trí: một tài xế mạng kém vẫn đang rảnh, còn một
// tài xế vừa xong chuyến thì ping mới tinh nhưng chưa chờ giây nào.
func (d *Driver) IdleSeconds(now time.Time) float64 {
	if d.IdleSince == nil || d.Status != StatusIdle {
		return 0
	}
	if s := now.Sub(*d.IdleSince).Seconds(); s > 0 {
		return s
	}
	return 0
}

// CanAcceptTrip quyết định tài xế có đủ điều kiện nhận chuyến hay không.
// Ngưỡng nợ ví là điểm mấu chốt của mô hình tiền mặt tại VN.
func (d *Driver) CanAcceptTrip(debtLimit money.VND) error {
	if d.KYC != KYCApproved {
		return errs.E(errs.KindForbidden, "kyc_not_approved", "Hồ sơ của bạn chưa được duyệt.")
	}
	if d.Status == StatusSuspended {
		return errs.E(errs.KindForbidden, "driver_suspended", "Tài khoản đang bị tạm khoá.")
	}
	if d.Status != StatusIdle {
		return errs.Conflict("driver_busy", "Bạn đang trong một chuyến khác.")
	}
	if d.WalletBalance < debtLimit.Neg() {
		return errs.E(errs.KindForbidden, "wallet_debt_exceeded",
			"Ví của bạn đang âm quá hạn mức. Vui lòng nạp tiền để tiếp tục nhận chuyến.")
	}
	return nil
}

type Repository interface {
	Create(ctx context.Context, d *Driver) error
	Get(ctx context.Context, id string) (*Driver, error)
	GetByAccount(ctx context.Context, accountID string) (*Driver, error)
	// UpdateStatus dùng CAS theo Version; trả lỗi Conflict nếu version lệch.
	UpdateStatus(ctx context.Context, id string, from, to Status, version int) error
	Update(ctx context.Context, d *Driver) error
	// ApplyStats cộng dồn thống kê NGUYÊN TỬ. Không đọc-sửa-ghi: nhiều sự kiện
	// của cùng một tài xế đến song song là chuyện bình thường.
	ApplyStats(ctx context.Context, driverID string, d StatsDelta) error
	// UpdateWalletBalance chỉ cập nhật cột cache số dư. CỐ Ý không tăng
	// version: version bảo vệ chuyển trạng thái, còn wallet_balance là giá trị
	// suy ra từ sổ cái. Nếu tăng version ở đây, việc đồng bộ cache sẽ làm hỏng
	// CAS của Reserve/SetStatus đang chạy song song.
	UpdateWalletBalance(ctx context.Context, driverID string, bal money.VND) error
	ListByStatus(ctx context.Context, s Status, limit int) ([]*Driver, error)
}
