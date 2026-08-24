// Package location nhận ping vị trí tài xế, lọc gian lận và lập chỉ mục không gian.
//
// Luồng production: app tài xế -> MQTT (EMQX) topic `drv/{id}/loc` (QoS 1)
// -> consumer gọi Service.Ingest -> Index (Redis GEO / H3) + publish sự kiện.
// MQTT được chọn thay WebSocket vì tiết kiệm pin và băng thông trên máy Android
// giá rẻ, mạng 4G không ổn định — bối cảnh phổ biến của tài xế tại VN.
package location

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/geo"
)

// Ping là một bản tin vị trí từ thiết bị tài xế.
type Ping struct {
	DriverID   string    `json:"driver_id"`
	Point      geo.Point `json:"point"`
	BearingDeg float64   `json:"bearing_deg"`
	SpeedMps   float64   `json:"speed_mps"`
	AccuracyM  float64   `json:"accuracy_m"`
	// Mocked lấy từ Location.isFromMockProvider (Android). GPS giả là hình thức
	// gian lận phổ biến nhất — tài xế "dịch chuyển" tới điểm đón để nhận chuyến.
	Mocked    bool      `json:"mocked"`
	BatteryPc int       `json:"battery_pc"`
	At        time.Time `json:"at"`
}

// Snapshot là trạng thái mới nhất của tài xế trong chỉ mục.
type Snapshot struct {
	DriverID    string             `json:"driver_id"`
	Point       geo.Point          `json:"point"`
	BearingDeg  float64            `json:"bearing_deg"`
	VehicleType driver.VehicleType `json:"vehicle_type"`
	Status      driver.Status      `json:"status"`
	BatteryPc   int                `json:"battery_pc"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// Filter thu hẹp tập ứng viên khi truy vấn lân cận.
type Filter struct {
	VehicleTypes []driver.VehicleType
	Statuses     []driver.Status
	MinBatteryPc int
	// FreshWithin loại tài xế lâu không gửi ping (mất mạng, tắt app).
	FreshWithin time.Duration
}

func (f Filter) match(s Snapshot, now time.Time) bool {
	if len(f.VehicleTypes) > 0 && !containsVehicle(f.VehicleTypes, s.VehicleType) {
		return false
	}
	if len(f.Statuses) > 0 && !containsStatus(f.Statuses, s.Status) {
		return false
	}
	if f.MinBatteryPc > 0 && s.BatteryPc > 0 && s.BatteryPc < f.MinBatteryPc {
		return false
	}
	if f.FreshWithin > 0 && now.Sub(s.UpdatedAt) > f.FreshWithin {
		return false
	}
	return true
}

func containsVehicle(list []driver.VehicleType, v driver.VehicleType) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsStatus(list []driver.Status, v driver.Status) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Index là chỉ mục không gian của tài xế đang online.
// Bản production: Redis GEO (GEOSEARCH) hoặc H3 cell -> Redis Set.
type Index interface {
	Upsert(ctx context.Context, s Snapshot) error
	Remove(ctx context.Context, driverID string) error
	Get(ctx context.Context, driverID string) (Snapshot, bool, error)
	Nearby(ctx context.Context, center geo.Point, radiusM float64, f Filter) ([]Snapshot, error)
}
