package admin

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

// MaxPageSize chặn giao diện kéo quá nhiều dòng một lần.
const MaxPageSize = 200

// FraudWindow là cửa sổ đếm cờ gian lận hiển thị trên bảng tài xế.
const FraudWindow = 24 * time.Hour

// Vùng quan sát mặc định của bản đồ vận hành: Chợ Bến Thành, TP.HCM —
// trung tâm mật độ chuyến cao nhất. Trần 50km chặn việc kéo cả nước một lần.
const (
	DefaultMapLat     = 10.7725
	DefaultMapLng     = 106.6980
	DefaultMapRadiusM = 5000.0
	MaxMapRadiusM     = 50000.0
)

type Service struct {
	drivers   DriverPort
	trips     TripPort
	loc       LocationPort
	wallet    WalletPort
	audit     AuditLog
	clk       clock.Clock
	debtLimit money.VND
}

func NewService(d DriverPort, t TripPort, l LocationPort, w WalletPort, a AuditLog, clk clock.Clock) *Service {
	return &Service{drivers: d, trips: t, loc: l, wallet: w, audit: a, clk: clk, debtLimit: driver.DefaultDebtLimit}
}

// allDriverStatuses / allTripStatuses: repo chỉ cho liệt kê theo từng trạng
// thái, nên "tất cả" nghĩa là hợp của các trạng thái. Giữ danh sách ở một chỗ
// để không sót trạng thái mới khi domain mở rộng.
var allDriverStatuses = []driver.Status{
	driver.StatusOffline, driver.StatusIdle, driver.StatusAssigned,
	driver.StatusOnTrip, driver.StatusSuspended,
}

var allTripStatuses = []trip.Status{
	trip.StatusCreated, trip.StatusSearching, trip.StatusAssigned, trip.StatusArrived,
	trip.StatusInProgress, trip.StatusCompleted, trip.StatusPaid,
	trip.StatusCancelled, trip.StatusExpired,
}

// ListDriversInput là bộ lọc do giao diện gửi lên. Việc lọc thực hiện ở đây,
// không phải ở trình duyệt.
type ListDriversInput struct {
	Status   string // rỗng = tất cả
	KYC      string // rỗng = tất cả
	City     string
	Query    string // khớp tên / số điện thoại / biển số
	OnlyDebt bool   // chỉ tài xế đang nợ quá hạn mức
	Limit    int
}

func (s *Service) ListDrivers(ctx context.Context, in ListDriversInput) ([]DriverRow, error) {
	limit := clampLimit(in.Limit)

	statuses := allDriverStatuses
	if in.Status != "" {
		st := driver.Status(in.Status)
		if !validDriverStatus(st) {
			return nil, errs.Invalid("status_invalid", "Trạng thái tài xế không hợp lệ.")
		}
		statuses = []driver.Status{st}
	}

	seen := map[string]bool{}
	rows := make([]DriverRow, 0, limit)
	for _, st := range statuses {
		ds, err := s.drivers.ListByStatus(ctx, st, limit)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			if !matchDriverFilter(d, in) {
				continue
			}
			row, err := s.driverRow(ctx, d)
			if err != nil {
				return nil, err
			}
			if in.OnlyDebt && !row.InDebt {
				continue
			}
			rows = append(rows, row)
		}
	}

	// Thứ tự ổn định: tài xế cần chú ý lên trước (nợ, chờ duyệt), rồi theo tên.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].InDebt != rows[j].InDebt {
			return rows[i].InDebt
		}
		pi := rows[i].KYC == driver.KYCPending
		pj := rows[j].KYC == driver.KYCPending
		if pi != pj {
			return pi
		}
		if rows[i].FullName != rows[j].FullName {
			return rows[i].FullName < rows[j].FullName
		}
		return rows[i].ID < rows[j].ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *Service) GetDriver(ctx context.Context, driverID string) (*DriverRow, error) {
	d, err := s.drivers.Get(ctx, driverID)
	if err != nil {
		return nil, err
	}
	row, err := s.driverRow(ctx, d)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Actor là quản trị viên thực hiện thao tác, lấy từ token.
type Actor struct {
	AccountID string
	Phone     string
}

// ReviewKYC duyệt hoặc từ chối hồ sơ tài xế. Đây là hành động ghi duy nhất của
// module admin — mọi thay đổi khác phải đi qua module sở hữu nghiệp vụ.
//
// Ghi nhật ký SAU khi thao tác thành công. Nếu ghi nhật ký lỗi thì trả lỗi luôn:
// một thay đổi hồ sơ không truy vết được còn tệ hơn một lần duyệt thất bại, vì
// nó âm thầm phá bất biến "mọi thao tác quản trị đều có dấu vết".
func (s *Service) ReviewKYC(ctx context.Context, actor Actor, driverID string, approved bool) (*DriverRow, error) {
	before, err := s.drivers.Get(ctx, driverID)
	if err != nil {
		return nil, err
	}
	if err := s.drivers.ReviewKYC(ctx, driverID, approved); err != nil {
		return nil, err
	}
	if s.audit != nil {
		entry := NewAuditEntry(actor.AccountID, actor.Phone, ActionReviewKYC,
			TargetDriver, driverID, map[string]any{
				"approved": approved,
				"from":     string(before.KYC),
				"to":       string(kycStateFor(approved)),
			}, s.clk.Now())
		if err := s.audit.Record(ctx, entry); err != nil {
			return nil, err
		}
	}
	return s.GetDriver(ctx, driverID)
}

func kycStateFor(approved bool) driver.KYCState {
	if approved {
		return driver.KYCApproved
	}
	return driver.KYCRejected
}

// Audit trả nhật ký thao tác quản trị. Chỉ đọc — không có đường nào sửa hay xoá.
func (s *Service) Audit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	if s.audit == nil {
		return []AuditEntry{}, nil
	}
	if f.TargetType != "" && f.TargetType != TargetDriver && f.TargetType != TargetTrip {
		return nil, errs.Invalid("target_type_invalid", "Loại đối tượng không hợp lệ.")
	}
	return s.audit.List(ctx, f)
}

// driverRow gộp hồ sơ + ví + vị trí + cờ gian lận thành một dòng.
func (s *Service) driverRow(ctx context.Context, d *driver.Driver) (DriverRow, error) {
	bal, err := s.wallet.DriverBalance(ctx, d.ID)
	if err != nil {
		return DriverRow{}, err
	}
	cash, err := s.wallet.CashOnHand(ctx, d.ID)
	if err != nil {
		return DriverRow{}, err
	}

	row := DriverRow{
		ID: d.ID, FullName: d.FullName, Phone: d.Phone, City: d.City,
		VehicleType: d.Vehicle.Type, VehiclePlate: d.Vehicle.Plate,
		KYC: d.KYC, Status: d.Status, Rating: d.Rating(),
		AcceptanceRate: d.AcceptanceRate(), CancelRate: d.CancelRate(),
		CompletedTrips: d.Stats.TripsCompleted, RatingCount: d.Stats.RatingCount,
		IdleSince:     d.IdleSince,
		WalletBalance: bal, CashOnHand: cash,
		InDebt:        bal < s.debtLimit.Neg(),
		FraudFlags24h: s.loc.FraudCount(d.ID, FraudWindow),
		CreatedAt:     d.CreatedAt,
	}

	// Lý do bị chặn nhận chuyến lấy thẳng từ domain để giao diện không phải
	// tự suy luận lại điều kiện — một nguồn sự thật duy nhất.
	probe := *d
	probe.WalletBalance = bal
	if err := probe.CanAcceptTrip(s.debtLimit); err != nil {
		row.BlockedReason = errs.CodeOf(err)
	}

	if snap, ok, err := s.loc.Get(ctx, d.ID); err == nil && ok {
		p := snap.Point
		t := snap.UpdatedAt
		row.Position = &p
		row.LastSeen = &t
		row.BatteryPc = snap.BatteryPc
	}
	return row, nil
}

type ListTripsInput struct {
	Status string // rỗng = tất cả
	Limit  int
}

func (s *Service) ListTrips(ctx context.Context, in ListTripsInput) ([]TripRow, error) {
	limit := clampLimit(in.Limit)

	statuses := allTripStatuses
	if in.Status != "" {
		st := trip.Status(in.Status)
		if !validTripStatus(st) {
			return nil, errs.Invalid("status_invalid", "Trạng thái chuyến không hợp lệ.")
		}
		statuses = []trip.Status{st}
	}

	now := s.clk.Now()
	seen := map[string]bool{}
	rows := make([]TripRow, 0, limit)
	for _, st := range statuses {
		ts, err := s.trips.ListByStatus(ctx, st, limit)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			rows = append(rows, tripRow(t, now))
		}
	}

	// Mới nhất lên đầu — vận hành quan tâm cái đang xảy ra.
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].RequestedAt.Equal(rows[j].RequestedAt) {
			return rows[i].RequestedAt.After(rows[j].RequestedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *Service) GetTrip(ctx context.Context, tripID string) (*TripRow, error) {
	t, err := s.trips.Get(ctx, tripID)
	if err != nil {
		return nil, err
	}
	row := tripRow(t, s.clk.Now())
	return &row, nil
}

// TripEvents trả nhật ký chuyển trạng thái — bằng chứng đối soát khi khiếu nại.
func (s *Service) TripEvents(ctx context.Context, tripID string) ([]trip.Event, error) {
	if _, err := s.trips.Get(ctx, tripID); err != nil {
		return nil, err
	}
	return s.trips.Events(ctx, tripID)
}

func tripRow(t *trip.Trip, now time.Time) TripRow {
	row := TripRow{
		ID: t.ID, Status: t.Status, RiderID: t.RiderID, DriverID: t.DriverID,
		VehicleType:   t.VehicleType,
		PickupAddress: t.Pickup.Address, DropoffAddress: t.Dropoff.Address,
		Pickup: t.Pickup.Point, Dropoff: t.Dropoff.Point,
		Fare: t.Fare, PlatformFee: t.PlatformFee, DriverEarn: t.DriverEarn,
		PaymentMethod: t.PaymentMethod,
		RequestedAt:   t.RequestedAt, EndedAt: t.EndedAt,
	}
	if t.Status == trip.StatusSearching {
		row.WaitingSec = now.Sub(t.RequestedAt).Seconds()
	}
	return row
}

// LiveMapInput giới hạn vùng quan sát bản đồ trực tuyến.
type LiveMapInput struct {
	Center  geo.Point
	RadiusM float64
	// OnlyIdle chỉ lấy tài xế đang rảnh (đúng tập ứng viên mà dispatcher thấy).
	OnlyIdle bool
}

// LiveMap trả cung (tài xế) và cầu (chuyến đang chờ ghép) trong cùng một vùng.
//
// Trả cả hai trong một lời gọi là có chủ đích: câu hỏi vận hành thực sự không
// phải "tài xế ở đâu" mà là "chỗ nào có khách chờ mà không có tài xế". Ghép hai
// tập ở backend bảo đảm chúng cùng một thời điểm và cùng một bán kính.
func (s *Service) LiveMap(ctx context.Context, in LiveMapInput) (*LiveMapResult, error) {
	if !in.Center.Valid() {
		return nil, errs.Invalid("point_invalid", "Toạ độ trung tâm không hợp lệ.")
	}
	radius := in.RadiusM
	if radius <= 0 {
		radius = DefaultMapRadiusM
	}
	if radius > MaxMapRadiusM {
		radius = MaxMapRadiusM
	}

	f := location.Filter{FreshWithin: location.StaleAfter}
	if in.OnlyIdle {
		f.Statuses = []driver.Status{driver.StatusIdle}
	}
	snaps, err := s.loc.Nearby(ctx, in.Center, radius, f)
	if err != nil {
		return nil, err
	}
	if snaps == nil {
		snaps = []location.Snapshot{}
	}

	// Chuyến đang chờ ghép, lọc theo cùng bán kính tính từ điểm đón.
	now := s.clk.Now()
	pending := []PendingPickup{}
	if ts, err := s.trips.ListByStatus(ctx, trip.StatusSearching, MaxPageSize); err == nil {
		for _, t := range ts {
			if geo.DistanceM(t.Pickup.Point, in.Center) > radius {
				continue
			}
			pending = append(pending, PendingPickup{
				TripID:      t.ID,
				Point:       t.Pickup.Point,
				Address:     t.Pickup.Address,
				VehicleType: t.VehicleType,
				Fare:        t.Fare,
				WaitingSec:  now.Sub(t.RequestedAt).Seconds(),
			})
		}
		// Chờ lâu nhất lên đầu — đó là chuyến cần can thiệp trước.
		sort.Slice(pending, func(i, j int) bool {
			return pending[i].WaitingSec > pending[j].WaitingSec
		})
	}

	return &LiveMapResult{
		Center:      in.Center,
		RadiusM:     radius,
		Drivers:     snaps,
		Pending:     pending,
		GeneratedAt: now,
	}, nil
}

// Overview tổng hợp số liệu trang chủ. Toàn bộ phép đếm và cảnh báo tính ở
// đây — giao diện chỉ hiển thị con số nhận được.
func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	var ov Overview
	ov.GeneratedAt = s.clk.Now()

	for _, st := range allDriverStatuses {
		ds, err := s.drivers.ListByStatus(ctx, st, MaxPageSize)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if d.KYC == driver.KYCPending {
				ov.Drivers.PendingKYC++
			}
		}
		switch st {
		case driver.StatusIdle:
			ov.Drivers.Online = len(ds)
		case driver.StatusAssigned, driver.StatusOnTrip:
			ov.Drivers.OnTrip += len(ds)
		case driver.StatusOffline:
			ov.Drivers.Offline = len(ds)
		case driver.StatusSuspended:
			ov.Drivers.Suspended = len(ds)
		}
	}

	var cashTrips, paidTrips int
	for _, st := range allTripStatuses {
		ts, err := s.trips.ListByStatus(ctx, st, MaxPageSize)
		if err != nil {
			return nil, err
		}
		switch st {
		case trip.StatusSearching:
			ov.Trips.Searching = len(ts)
		case trip.StatusAssigned, trip.StatusArrived, trip.StatusInProgress:
			ov.Trips.Active += len(ts)
		case trip.StatusCancelled:
			ov.Trips.Cancelled = len(ts)
		case trip.StatusExpired:
			ov.Trips.Expired = len(ts)
		}
		// Doanh thu tính trên chuyến đã hoàn tất hoặc đã ghi sổ.
		if st == trip.StatusCompleted || st == trip.StatusPaid {
			ov.Trips.Completed += len(ts)
			for _, t := range ts {
				ov.Money.Gross += t.Fare
				ov.Money.PlatformFee += t.PlatformFee
				ov.Money.DriverEarn += t.DriverEarn
				paidTrips++
				if t.PaymentMethod == trip.PayCash {
					cashTrips++
				}
			}
		}
	}
	if paidTrips > 0 {
		ov.Money.CashShare = float64(cashTrips) / float64(paidTrips)
	}

	ov.Alerts = s.alerts(ctx, &ov)
	return &ov, nil
}

// alerts nêu những việc vận hành cần xử lý. Ngưỡng đặt ở đây, không ở giao diện.
func (s *Service) alerts(ctx context.Context, ov *Overview) []Alert {
	out := []Alert{}

	if ov.Drivers.PendingKYC > 0 {
		out = append(out, Alert{
			Level: AlertInfo, Code: "kyc_pending", Count: ov.Drivers.PendingKYC,
			Message: "hồ sơ tài xế đang chờ duyệt",
		})
	}

	// Chuyến chờ ghép quá lâu = thiếu cung tài xế ở khu vực đó.
	const stuckAfter = 60 * time.Second
	if ts, err := s.trips.ListByStatus(ctx, trip.StatusSearching, MaxPageSize); err == nil {
		now := s.clk.Now()
		stuck := 0
		for _, t := range ts {
			if now.Sub(t.RequestedAt) > stuckAfter {
				stuck++
			}
		}
		if stuck > 0 {
			out = append(out, Alert{
				Level: AlertWarn, Code: "trips_stuck", Count: stuck,
				Message: "chuyến chờ ghép quá 60 giây — có thể thiếu tài xế",
			})
		}
	}

	// Tài xế nợ quá hạn mức không nhận được chuyến -> mất cung.
	indebt := 0
	for _, st := range []driver.Status{driver.StatusIdle, driver.StatusOffline} {
		ds, err := s.drivers.ListByStatus(ctx, st, MaxPageSize)
		if err != nil {
			continue
		}
		for _, d := range ds {
			if bal, err := s.wallet.DriverBalance(ctx, d.ID); err == nil && bal < s.debtLimit.Neg() {
				indebt++
			}
		}
	}
	if indebt > 0 {
		out = append(out, Alert{
			Level: AlertWarn, Code: "drivers_in_debt", Count: indebt,
			Message: "tài xế bị chặn nhận chuyến do nợ vượt hạn mức",
		})
	}

	if ov.Trips.Expired > 0 {
		out = append(out, Alert{
			Level: AlertWarn, Code: "trips_expired", Count: ov.Trips.Expired,
			Message: "chuyến không tìm được tài xế",
		})
	}
	return out
}

func matchDriverFilter(d *driver.Driver, in ListDriversInput) bool {
	if in.KYC != "" && string(d.KYC) != in.KYC {
		return false
	}
	if in.City != "" && d.City != in.City {
		return false
	}
	if in.Query != "" && !containsFold(d.FullName, in.Query) &&
		!containsFold(d.Phone, in.Query) && !containsFold(d.Vehicle.Plate, in.Query) {
		return false
	}
	return true
}

// containsFold so khớp không phân biệt hoa thường — tìm kiếm gõ nhanh trên
// bảng tài xế (tên có dấu tiếng Việt vẫn khớp theo byte, đủ dùng ở giai đoạn này).
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func validDriverStatus(s driver.Status) bool {
	for _, x := range allDriverStatuses {
		if x == s {
			return true
		}
	}
	return false
}

func validTripStatus(s trip.Status) bool {
	for _, x := range allTripStatuses {
		if x == s {
			return true
		}
	}
	return false
}

func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > MaxPageSize {
		return MaxPageSize
	}
	return n
}
