// Package pricing tính giá cước và hệ số tăng giá (surge).
package pricing

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

// Tariff là biểu giá theo thành phố + loại xe.
// Số liệu dưới đây chỉ là mẫu; giá thật phải khớp hồ sơ kê khai giá cước.
type Tariff struct {
	City         string
	VehicleType  driver.VehicleType
	OpeningFare  money.VND // giá mở cửa (đã bao gồm 2km đầu)
	OpeningMeter float64   // số mét đã bao gồm trong giá mở cửa
	PerKm        money.VND
	PerMinute    money.VND // phí chờ / di chuyển chậm
	MinFare      money.VND
	// NightSurchargePermille: phụ phí ban đêm (22h-05h) theo phần nghìn.
	NightSurchargePermille int64
	// PlatformFeePermille: chiết khấu nền tảng (phần nghìn), ví dụ 200 = 20%.
	PlatformFeePermille int64
}

func DefaultTariffs() map[driver.VehicleType]Tariff {
	return map[driver.VehicleType]Tariff{
		driver.VehicleBike: {
			City: "HCM", VehicleType: driver.VehicleBike,
			OpeningFare: 12000, OpeningMeter: 2000, PerKm: 4300, PerMinute: 300,
			MinFare: 12000, NightSurchargePermille: 100, PlatformFeePermille: 200,
		},
		driver.VehicleCar4: {
			City: "HCM", VehicleType: driver.VehicleCar4,
			OpeningFare: 29000, OpeningMeter: 2000, PerKm: 9500, PerMinute: 600,
			MinFare: 29000, NightSurchargePermille: 100, PlatformFeePermille: 250,
		},
		driver.VehicleCar7: {
			City: "HCM", VehicleType: driver.VehicleCar7,
			OpeningFare: 34000, OpeningMeter: 2000, PerKm: 11500, PerMinute: 700,
			MinFare: 34000, NightSurchargePermille: 100, PlatformFeePermille: 250,
		},
	}
}

// Route là kết quả tính đường đi.
type Route struct {
	DistanceM float64
	DurationS float64
	Polyline  string
}

// RouteEngine trừu tượng hoá dịch vụ chỉ đường.
// Production: OSRM self-host (chính) + Goong/VietMap (dự phòng, geocoding).
type RouteEngine interface {
	Route(ctx context.Context, from, to geo.Point) (Route, error)
}

// HaversineEngine là bản dự phòng offline: đường chim bay nhân hệ số uốn khúc.
// Hệ số 1.35 phản ánh mật độ hẻm và đường một chiều ở TP.HCM/Hà Nội.
type HaversineEngine struct {
	DetourFactor float64
	AvgSpeedKph  float64
}

func NewHaversineEngine() *HaversineEngine {
	return &HaversineEngine{DetourFactor: 1.35, AvgSpeedKph: 22}
}

func (h *HaversineEngine) Route(_ context.Context, from, to geo.Point) (Route, error) {
	d := geo.DistanceM(from, to) * h.DetourFactor
	return Route{DistanceM: d, DurationS: d / (h.AvgSpeedKph * 1000 / 3600)}, nil
}

// SurgeProvider trả hệ số tăng giá theo PHẦN NGHÌN (1000 = ×1.0, 2000 = ×2.0).
//
// Đơn vị là số nguyên chứ không phải float vì hệ số này nằm trên đường đi của
// tiền: `giá × 1.4` bằng float lệch 1 đồng ở ~24% các mức giá.
type SurgeProvider interface {
	SurgePermille(ctx context.Context, at geo.Point, t time.Time) (int64, error)
}

// Trần và sàn hệ số tăng giá, tính bằng phần nghìn.
// Trần 2.0 giảm rủi ro truyền thông và phản ứng người dùng.
const (
	MinSurgePermille int64 = 1000
	MaxSurgePermille int64 = 2000
)

type Quote struct {
	ID          string             `json:"id"`
	VehicleType driver.VehicleType `json:"vehicle_type"`
	Pickup      geo.Point          `json:"pickup"`
	Dropoff     geo.Point          `json:"dropoff"`
	DistanceM   float64            `json:"distance_m"`
	DurationS   float64            `json:"duration_s"`
	BaseFare    money.VND          `json:"base_fare"`
	NightFee    money.VND          `json:"night_fee"`
	// SurgePermille là nguồn sự thật dùng để tính tiền (1000 = ×1.0).
	SurgePermille int64 `json:"surge_permille"`
	// SurgeMult chỉ để HIỂN THỊ cho người dùng (1.4). Không bao giờ dùng nó để
	// tính tiền — mọi phép nhân đi qua SurgePermille bằng số nguyên.
	SurgeMult float64   `json:"surge_multiplier"`
	Discount  money.VND `json:"discount"`
	Total     money.VND `json:"total"`
	// PlatformFee là phần nền tảng thu của tài xế, tách sẵn để ghi sổ.
	PlatformFee money.VND `json:"platform_fee"`
	DriverEarn  money.VND `json:"driver_earn"`
	ExpiresAt   time.Time `json:"expires_at"`
}
