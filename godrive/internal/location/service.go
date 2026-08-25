package location

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// Ngưỡng phát hiện bất thường.
const (
	MaxPlausibleSpeedMps = 33.0 // ~120 km/h, cao hơn mức hợp lý trong đô thị VN
	MaxAccuracyM         = 200.0
	StaleAfter           = 45 * time.Second
)

type DriverPort interface {
	Get(ctx context.Context, id string) (*driver.Driver, error)
}

type Service struct {
	idx     Index
	drivers DriverPort
	clk     clock.Clock
	fraud   *FraudDetector
}

func NewService(idx Index, drivers DriverPort, clk clock.Clock) *Service {
	return &Service{idx: idx, drivers: drivers, clk: clk, fraud: NewFraudDetector(clk)}
}

// Ingest xử lý một ping. Trả lỗi Invalid nếu ping bị loại.
func (s *Service) Ingest(ctx context.Context, p Ping) error {
	if !p.Point.Valid() || !p.Point.InVietnam() {
		return errs.Invalid("point_out_of_range", "Toạ độ không hợp lệ.")
	}
	if p.Mocked {
		s.fraud.Flag(p.DriverID, ReasonMockLocation)
		return errs.E(errs.KindForbidden, "mock_location", "Phát hiện ứng dụng giả lập vị trí.")
	}
	if p.AccuracyM > MaxAccuracyM {
		return errs.Invalid("low_accuracy", "Tín hiệu GPS quá yếu.")
	}
	// Tốc độ TỰ KHAI vượt ngưỡng: gắn cờ nhưng vẫn nhận ping.
	// Khác với TELEPORT (suy ra từ hai vị trí liên tiếp — bằng chứng chắc chắn),
	// đây chỉ là một trường do thiết bị gửi lên và cảm biến tốc độ GPS hay nhiễu.
	// Từ chối ping vì một trường phụ sẽ đá nhầm tài xế thật ra khỏi chỉ mục.
	if p.SpeedMps > MaxPlausibleSpeedMps {
		s.fraud.Flag(p.DriverID, ReasonSpeedOutlier)
	}
	if p.At.IsZero() {
		p.At = s.clk.Now()
	}

	if prev, ok, _ := s.idx.Get(ctx, p.DriverID); ok {
		if dt := p.At.Sub(prev.UpdatedAt).Seconds(); dt > 0.5 {
			if geo.DistanceM(prev.Point, p.Point)/dt > MaxPlausibleSpeedMps {
				s.fraud.Flag(p.DriverID, ReasonTeleport)
				return errs.Invalid("implausible_jump", "Vị trí thay đổi bất thường.")
			}
		}
	}

	d, err := s.drivers.Get(ctx, p.DriverID)
	if err != nil {
		return err
	}
	if d.Status == driver.StatusOffline || d.Status == driver.StatusSuspended {
		return s.idx.Remove(ctx, p.DriverID)
	}

	return s.idx.Upsert(ctx, Snapshot{
		DriverID:    p.DriverID,
		Point:       p.Point,
		BearingDeg:  p.BearingDeg,
		VehicleType: d.Vehicle.Type,
		Status:      d.Status,
		BatteryPc:   p.BatteryPc,
		UpdatedAt:   p.At,
	})
}

func (s *Service) Nearby(ctx context.Context, c geo.Point, radiusM float64, f Filter) ([]Snapshot, error) {
	if f.FreshWithin == 0 {
		f.FreshWithin = StaleAfter
	}
	return s.idx.Nearby(ctx, c, radiusM, f)
}

// Remove gỡ tài xế khỏi chỉ mục. Dùng khi thiết bị mất kết nối (Last Will của
// MQTT) hoặc tài xế tắt nhận chuyến.
func (s *Service) Remove(ctx context.Context, driverID string) error {
	return s.idx.Remove(ctx, driverID)
}

func (s *Service) Get(ctx context.Context, driverID string) (Snapshot, bool, error) {
	return s.idx.Get(ctx, driverID)
}

func (s *Service) Fraud() *FraudDetector { return s.fraud }
