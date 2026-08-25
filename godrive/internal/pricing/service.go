package pricing

import (
	"context"
	"math"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

// QuoteTTL là hạn báo giá MẶC ĐỊNH. Giá trị thực tế lấy từ RuntimeConfig.
const QuoteTTL = 5 * time.Minute

// RuntimeConfig là phần cấu hình có thể đổi lúc chạy.
//
// Nhận qua hàm cung cấp chứ không nhận giá trị: cấu hình đổi từ bảng điều khiển
// phải có hiệu lực ngay, không phải chờ khởi động lại.
type RuntimeConfig struct {
	Tariffs        map[driver.VehicleType]Tariff
	QuoteTTL       time.Duration
	NightStartHour int
	NightEndHour   int
}

// ConfigProvider trả cấu hình hiện hành.
type ConfigProvider func(ctx context.Context) RuntimeConfig

type QuoteStore interface {
	Save(ctx context.Context, q Quote) error
	Get(ctx context.Context, quoteID string) (Quote, error)
}

type Service struct {
	routes RouteEngine
	surge  SurgeProvider
	store  QuoteStore
	clk    clock.Clock
	cfg    ConfigProvider
}

func NewService(routes RouteEngine, surge SurgeProvider, store QuoteStore, clk clock.Clock) *Service {
	return &Service{routes: routes, surge: surge, store: store, clk: clk}
}

// UseConfig nối nguồn cấu hình động. Không gọi thì dùng biểu giá mặc định.
func (s *Service) UseConfig(p ConfigProvider) { s.cfg = p }

func (s *Service) config(ctx context.Context) RuntimeConfig {
	if s.cfg != nil {
		return s.cfg(ctx)
	}
	return RuntimeConfig{
		Tariffs: DefaultTariffs(), QuoteTTL: QuoteTTL,
		NightStartHour: 22, NightEndHour: 5,
	}
}

type EstimateInput struct {
	VehicleType driver.VehicleType `json:"vehicle_type"`
	Pickup      geo.Point          `json:"pickup"`
	Dropoff     geo.Point          `json:"dropoff"`
	PromoCode   string             `json:"promo_code,omitempty"`
}

func (s *Service) Estimate(ctx context.Context, in EstimateInput) (Quote, error) {
	cfg := s.config(ctx)
	t, ok := cfg.Tariffs[in.VehicleType]
	if !ok {
		return Quote{}, errs.Invalid("vehicle_type_invalid", "Loại xe không được hỗ trợ.")
	}
	if !in.Pickup.Valid() || !in.Dropoff.Valid() {
		return Quote{}, errs.Invalid("point_invalid", "Điểm đón/đến không hợp lệ.")
	}

	r, err := s.routes.Route(ctx, in.Pickup, in.Dropoff)
	if err != nil {
		return Quote{}, err
	}

	now := s.clk.Now()
	base := computeBase(t, r)

	var night money.VND
	if isNight(now, cfg.NightStartHour, cfg.NightEndHour) {
		night = base.MulPermille(t.NightSurchargePermille)
	}

	surge := MinSurgePermille
	if s.surge != nil {
		if p, err := s.surge.SurgePermille(ctx, in.Pickup, now); err == nil {
			surge = p
		}
	}
	// Clamp LẦN HAI. Bảng bậc thang đã clamp một lần; lặp lại ở đây để một
	// SurgeProvider khác cắm vào sau này cũng không thể vượt trần.
	if surge > MaxSurgePermille {
		surge = MaxSurgePermille
	}
	if surge < MinSurgePermille {
		surge = MinSurgePermille
	}

	subtotal := (base + night).MulPermille(surge)
	if subtotal < t.MinFare {
		subtotal = t.MinFare
	}
	total := subtotal.RoundTo(1000)

	fee := total.MulPermille(t.PlatformFeePermille)
	q := Quote{
		ID:            id.New("qte"),
		VehicleType:   in.VehicleType,
		Pickup:        in.Pickup,
		Dropoff:       in.Dropoff,
		DistanceM:     r.DistanceM,
		DurationS:     r.DurationS,
		BaseFare:      base,
		NightFee:      night,
		SurgePermille: surge,
		SurgeMult:     float64(surge) / 1000,
		Total:         total,
		PlatformFee:   fee,
		DriverEarn:    total - fee,
		ExpiresAt:     now.Add(cfg.QuoteTTL),
	}
	if s.store != nil {
		if err := s.store.Save(ctx, q); err != nil {
			return Quote{}, err
		}
	}
	return q, nil
}

// EstimateAll trả báo giá cho mọi loại xe để app hiển thị danh sách lựa chọn.
func (s *Service) EstimateAll(ctx context.Context, pickup, dropoff geo.Point) ([]Quote, error) {
	order := []driver.VehicleType{driver.VehicleBike, driver.VehicleCar4, driver.VehicleCar7}
	out := make([]Quote, 0, len(order))
	for _, vt := range order {
		q, err := s.Estimate(ctx, EstimateInput{VehicleType: vt, Pickup: pickup, Dropoff: dropoff})
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

// Tariffs trả biểu giá hiện hành — dùng cho bảng điều khiển hiển thị.
func (s *Service) Tariffs(ctx context.Context) map[driver.VehicleType]Tariff {
	return s.config(ctx).Tariffs
}

func (s *Service) GetQuote(ctx context.Context, quoteID string) (Quote, error) {
	q, err := s.store.Get(ctx, quoteID)
	if err != nil {
		return Quote{}, err
	}
	if s.clk.Now().After(q.ExpiresAt) {
		return Quote{}, errs.Invalid("quote_expired", "Báo giá đã hết hạn, vui lòng thử lại.")
	}
	return q, nil
}

// computeBase là hàm THUẦN: không I/O, không side-effect. Đây là phần phải test
// kỹ nhất vì nó được dùng để audit khi khách khiếu nại giá.
//
// Quãng đường và thời lượng vào đây là float (đến từ máy chỉ đường), nhưng được
// quy về số nguyên NGAY LẬP TỨC — từ đó trở đi mọi phép tính là số nguyên.
func computeBase(t Tariff, r Route) money.VND {
	fare := t.OpeningFare
	if extra := r.DistanceM - t.OpeningMeter; extra > 0 {
		extraM := int64(math.Round(extra))
		fare += t.PerKm.MulDiv(extraM, 1000) // đơn giá/km × số mét ÷ 1000
	}
	durS := int64(math.Round(r.DurationS))
	fare += t.PerMinute.MulDiv(durS, 60) // đơn giá/phút × số giây ÷ 60
	return fare
}

// isNight theo giờ Việt Nam (UTC+7), khung giờ lấy từ cấu hình.
//
// Khung có thể vắt qua nửa đêm (22h–5h) hoặc không (13h–15h), nên phải xử lý
// cả hai trường hợp chứ không chỉ so sánh một chiều.
func isNight(t time.Time, startHour, endHour int) bool {
	h := t.UTC().Add(7 * time.Hour).Hour()
	if startHour == endHour {
		return false
	}
	if startHour < endHour {
		return h >= startHour && h < endHour
	}
	return h >= startHour || h < endHour
}
