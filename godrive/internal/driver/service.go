package driver

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

// DefaultDebtLimit là hạn mức MẶC ĐỊNH: tài xế được nợ tối đa 200.000đ tiền
// chiết khấu. Giá trị thực tế lấy từ cấu hình chỉnh được ở bảng điều khiển.
const DefaultDebtLimit = money.VND(200000)

// Biển số VN: 2 số + 1-2 chữ + dãy số. Chấp nhận cả có/không dấu chấm.
var plateRe = regexp.MustCompile(`^[0-9]{2}[A-Z]{1,2}[0-9]?-?[0-9]{3}\.?[0-9]{2}$`)

// TripPort cho biết tài xế có chuyến nào đang chạy không.
//
// Port khai báo ở đây (bên tiêu thụ) nên driver không phải import trip.
type TripPort interface {
	HasActiveTrip(ctx context.Context, driverID string) (bool, error)
}

type Service struct {
	repo        Repository
	bus         eventbus.Bus
	clk         clock.Clock
	debtLimit   money.VND
	debtLimitFn DebtLimitProvider
	balance     BalanceReader
	trips       TripPort
}

// UseTripPort bật đường tự khôi phục khi tài xế kẹt trạng thái.
func (s *Service) UseTripPort(t TripPort) { s.trips = t }

func NewService(repo Repository, bus eventbus.Bus, clk clock.Clock) *Service {
	return &Service{repo: repo, bus: bus, clk: clk, debtLimit: DefaultDebtLimit}
}

type OnboardInput struct {
	AccountID string    `json:"account_id"`
	FullName  string    `json:"full_name"`
	Phone     string    `json:"phone"`
	City      string    `json:"city"`
	Vehicle   Vehicle   `json:"vehicle"`
	Documents Documents `json:"documents"`
}

func (s *Service) Onboard(ctx context.Context, in OnboardInput) (*Driver, error) {
	if strings.TrimSpace(in.FullName) == "" {
		return nil, errs.Invalid("full_name_required", "Vui lòng nhập họ tên.")
	}
	if !in.Vehicle.Type.Valid() {
		return nil, errs.Invalid("vehicle_type_invalid", "Loại xe không hợp lệ.")
	}
	plate := strings.ToUpper(strings.ReplaceAll(in.Vehicle.Plate, " ", ""))
	if !plateRe.MatchString(plate) {
		return nil, errs.Invalid("plate_invalid", "Biển số xe không đúng định dạng.")
	}
	in.Vehicle.Plate = plate
	if in.Documents.DriverLicense == "" || in.Documents.NationalID == "" {
		return nil, errs.Invalid("documents_required", "Vui lòng cung cấp CCCD và GPLX.")
	}
	// Hạn bảo hiểm giờ được lưu thành DATE để job cảnh báo sắp hết hạn truy vấn
	// được. Chặn định dạng sai ngay tại cửa, đừng để dữ liệu rác xuống đĩa.
	if in.Documents.InsuranceUntil != "" {
		if _, err := time.Parse(DateLayout, in.Documents.InsuranceUntil); err != nil {
			return nil, errs.Invalid("insurance_until_invalid",
				"Ngày hết hạn bảo hiểm phải theo dạng YYYY-MM-DD.")
		}
	}
	now := s.clk.Now()
	d := &Driver{
		ID:        id.New("drv"),
		AccountID: in.AccountID,
		FullName:  in.FullName,
		Phone:     in.Phone,
		City:      in.City,
		Vehicle:   in.Vehicle,
		Documents: in.Documents,
		KYC:       KYCPending,
		Status:    StatusOffline,
		// Không đặt điểm khởi đầu ở đây: Stats bắt đầu từ 0 và tiền nghiệm
		// Bayes (xem PriorOffers/PriorRating) tự cho tài xế mới mức trung tính,
		// rồi nhường dần cho hành vi thật khi đã đủ mẫu.
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// ReviewKYC dùng cho admin sau khi đối chiếu kết quả eKYC (FPT.AI / VNPT eKYC).
func (s *Service) ReviewKYC(ctx context.Context, driverID string, approved bool) error {
	d, err := s.repo.Get(ctx, driverID)
	if err != nil {
		return err
	}
	if approved {
		d.KYC = KYCApproved
	} else {
		d.KYC = KYCRejected
	}
	d.UpdatedAt = s.clk.Now()
	return s.repo.Update(ctx, d)
}

func (s *Service) GoOnline(ctx context.Context, driverID string) error {
	d, err := s.repo.Get(ctx, driverID)
	if err != nil {
		return err
	}
	if d.KYC != KYCApproved {
		return errs.E(errs.KindForbidden, "kyc_not_approved", "Hồ sơ của bạn chưa được duyệt.")
	}
	if d.Status == StatusSuspended {
		return errs.E(errs.KindForbidden, "driver_suspended", "Tài khoản đang bị tạm khoá.")
	}
	if d.Status == StatusIdle {
		return nil // idempotent
	}
	// Đường thoát cho trạng thái kẹt: tài xế đang mang trạng thái ASSIGNED hoặc
	// ON_TRIP nhưng thực tế KHÔNG có chuyến nào đang chạy (tiến trình chết giữa
	// chừng, sự kiện bị mất). Nếu không có lối này, cách duy nhất để họ làm việc
	// lại là gọi tổng đài nhờ sửa tay trong CSDL.
	if d.Status == StatusAssigned || d.Status == StatusOnTrip {
		if s.trips == nil {
			return errs.Conflict("driver_on_trip", "Bạn đang trong một chuyến, không thể bật nhận chuyến.")
		}
		active, err := s.trips.HasActiveTrip(ctx, driverID)
		if err != nil {
			return err
		}
		if active {
			return errs.Conflict("driver_on_trip", "Bạn cần hoàn tất chuyến hiện tại trước.")
		}
	}
	if err := s.repo.UpdateStatus(ctx, driverID, d.Status, StatusIdle, d.Version); err != nil {
		return err
	}
	return s.bus.Publish(ctx, eventbus.TopicDriverOnline, map[string]string{"driver_id": driverID})
}

func (s *Service) GoOffline(ctx context.Context, driverID string) error {
	d, err := s.repo.Get(ctx, driverID)
	if err != nil {
		return err
	}
	if d.Status == StatusOnTrip || d.Status == StatusAssigned {
		return errs.Conflict("driver_on_trip", "Bạn cần hoàn tất chuyến hiện tại trước khi tắt nhận chuyến.")
	}
	if d.Status == StatusOffline {
		return nil
	}
	if err := s.repo.UpdateStatus(ctx, driverID, d.Status, StatusOffline, d.Version); err != nil {
		return err
	}
	return s.bus.Publish(ctx, eventbus.TopicDriverOffline, map[string]string{"driver_id": driverID})
}

// BalanceReader đọc số dư ví từ sổ cái. Tuỳ chọn: nil thì Reserve dùng cột cache.
//
// Port khai báo ở đây (bên tiêu thụ) để driver không phải import wallet.
type BalanceReader interface {
	DriverBalance(ctx context.Context, driverID string) (money.VND, error)
}

// UseBalanceReader nối nguồn số dư thật vào cổng chặn công nợ.
func (s *Service) UseBalanceReader(b BalanceReader) { s.balance = b }

// DebtLimitProvider trả hạn mức công nợ hiện hành.
type DebtLimitProvider func(ctx context.Context) money.VND

// UseDebtLimit nối nguồn hạn mức động.
func (s *Service) UseDebtLimit(p DebtLimitProvider) { s.debtLimitFn = p }

func (s *Service) limit(ctx context.Context) money.VND {
	if s.debtLimitFn != nil {
		return s.debtLimitFn(ctx)
	}
	return s.debtLimit
}

// Reserve chuyển tài xế IDLE -> ASSIGNED bằng CAS. Đây là chốt chặn duy nhất
// bảo đảm một tài xế không nhận hai chuyến song song.
//
// Cũng là nơi CUỐI CÙNG kiểm tra công nợ: dispatcher đã lọc ở bước chấm điểm,
// nhưng giữa lúc gửi lời mời và lúc tài xế bấm nhận, số dư có thể đã đổi.
func (s *Service) Reserve(ctx context.Context, driverID string) error {
	d, err := s.repo.Get(ctx, driverID)
	if err != nil {
		return err
	}
	if s.balance != nil {
		bal, err := s.balance.DriverBalance(ctx, driverID)
		if err != nil {
			return err
		}
		d.WalletBalance = bal
	}
	if err := d.CanAcceptTrip(s.limit(ctx)); err != nil {
		return err
	}
	return s.repo.UpdateStatus(ctx, driverID, StatusIdle, StatusAssigned, d.Version)
}

func (s *Service) SetStatus(ctx context.Context, driverID string, to Status) error {
	d, err := s.repo.Get(ctx, driverID)
	if err != nil {
		return err
	}
	return s.repo.UpdateStatus(ctx, driverID, d.Status, to, d.Version)
}

// ApplyStats cộng dồn thống kê tài xế. Consumer sự kiện gọi hàm này.
func (s *Service) ApplyStats(ctx context.Context, driverID string, d StatsDelta) error {
	if d.Empty() {
		return nil
	}
	return s.repo.ApplyStats(ctx, driverID, d)
}

// SyncWalletBalance cập nhật cột cache drivers.wallet_balance từ sổ cái.
//
// Nguồn sự thật là ledger_entries; cột này chỉ để CanAcceptTrip đọc nhanh khi
// chặn nhận chuyến. Không có bước đồng bộ này thì cột vĩnh viễn bằng 0 và cổng
// chặn công nợ không bao giờ kích hoạt.
func (s *Service) SyncWalletBalance(ctx context.Context, driverID string, bal money.VND) error {
	return s.repo.UpdateWalletBalance(ctx, driverID, bal)
}

// DebtLimit là hạn mức nợ đang áp dụng.
func (s *Service) DebtLimit(ctx context.Context) money.VND { return s.limit(ctx) }

func (s *Service) Get(ctx context.Context, driverID string) (*Driver, error) {
	return s.repo.Get(ctx, driverID)
}

func (s *Service) GetByAccount(ctx context.Context, accountID string) (*Driver, error) {
	return s.repo.GetByAccount(ctx, accountID)
}

// ListByStatus liệt kê tài xế theo trạng thái, dùng cho bảng điều khiển vận hành.
func (s *Service) ListByStatus(ctx context.Context, st Status, limit int) ([]*Driver, error) {
	return s.repo.ListByStatus(ctx, st, limit)
}
