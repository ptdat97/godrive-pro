// Package admin cung cấp API vận hành cho bảng điều khiển nội bộ.
//
// Nguyên tắc: MỌI logic tổng hợp, lọc, phân trang, phân quyền đều nằm ở đây.
// Giao diện (Next.js) chỉ gọi endpoint và hiển thị — không tự tính toán, không
// tự lọc, không tự quyết định cái gì được xem.
//
// Module này chỉ đọc qua Port do chính nó định nghĩa (consumer-defined ports),
// không import struct nội bộ của module khác — giữ đúng quy ước phụ thuộc một
// chiều của toàn repo.
package admin

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

// ==== Port: những gì admin cần từ các module khác ====

type DriverPort interface {
	Get(ctx context.Context, id string) (*driver.Driver, error)
	ListByStatus(ctx context.Context, s driver.Status, limit int) ([]*driver.Driver, error)
	ReviewKYC(ctx context.Context, driverID string, approved bool) error
}

type TripPort interface {
	Get(ctx context.Context, tripID string) (*trip.Trip, error)
	ListByStatus(ctx context.Context, s trip.Status, limit int) ([]*trip.Trip, error)
	Events(ctx context.Context, tripID string) ([]trip.Event, error)
}

type LocationPort interface {
	Get(ctx context.Context, driverID string) (location.Snapshot, bool, error)
	Nearby(ctx context.Context, c geo.Point, radiusM float64, f location.Filter) ([]location.Snapshot, error)
	FraudCount(driverID string, within time.Duration) int
}

type WalletPort interface {
	DriverBalance(ctx context.Context, driverID string) (money.VND, error)
	CashOnHand(ctx context.Context, driverID string) (money.VND, error)
	Statement(ctx context.Context, accountID string, from, to time.Time) ([]wallet.Entry, error)
}

// ==== Kiểu dữ liệu trả về cho giao diện ====

// DriverRow là một dòng trong bảng tài xế. Đã gộp sẵn số dư ví, công nợ tiền
// mặt và vị trí — giao diện không phải gọi thêm endpoint nào.
type DriverRow struct {
	ID             string             `json:"id"`
	FullName       string             `json:"full_name"`
	Phone          string             `json:"phone"`
	City           string             `json:"city"`
	VehicleType    driver.VehicleType `json:"vehicle_type"`
	VehiclePlate   string             `json:"vehicle_plate"`
	KYC            driver.KYCState    `json:"kyc"`
	Status         driver.Status      `json:"status"`
	Rating         float64            `json:"rating"`
	AcceptanceRate float64            `json:"acceptance_rate"`
	CancelRate     float64            `json:"cancel_rate"`
	CompletedTrips int                `json:"completed_trips"`
	// RatingCount là số lượt đánh giá thật. Rating 5.00 với 0 lượt đánh giá là
	// giá trị tiền nghiệm, không phải thành tích — giao diện cần phân biệt được.
	RatingCount int        `json:"rating_count"`
	IdleSince   *time.Time `json:"idle_since,omitempty"`

	WalletBalance money.VND `json:"wallet_balance"`
	CashOnHand    money.VND `json:"cash_on_hand"`
	// InDebt = ví âm quá hạn mức -> bị chặn nhận chuyến.
	InDebt bool `json:"in_debt"`
	// CanAcceptTrip nêu lý do tài xế không nhận được chuyến (rỗng = nhận được).
	BlockedReason string `json:"blocked_reason,omitempty"`

	LastSeen  *time.Time `json:"last_seen,omitempty"`
	Position  *geo.Point `json:"position,omitempty"`
	BatteryPc int        `json:"battery_pc,omitempty"`
	// FraudFlags24h là số cờ gian lận trong 24 giờ gần nhất.
	FraudFlags24h int `json:"fraud_flags_24h"`

	CreatedAt time.Time `json:"created_at"`
}

// TripRow là một dòng trong bảng chuyến đi.
type TripRow struct {
	ID          string             `json:"id"`
	Status      trip.Status        `json:"status"`
	RiderID     string             `json:"rider_id"`
	DriverID    *string            `json:"driver_id,omitempty"`
	VehicleType driver.VehicleType `json:"vehicle_type"`

	PickupAddress  string    `json:"pickup_address"`
	DropoffAddress string    `json:"dropoff_address"`
	Pickup         geo.Point `json:"pickup"`
	Dropoff        geo.Point `json:"dropoff"`

	Fare          money.VND          `json:"fare"`
	PlatformFee   money.VND          `json:"platform_fee"`
	DriverEarn    money.VND          `json:"driver_earn"`
	PaymentMethod trip.PaymentMethod `json:"payment_method"`

	RequestedAt time.Time  `json:"requested_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	// WaitingSec là thời gian chuyến đã chờ ghép (chỉ có ý nghĩa khi SEARCHING).
	WaitingSec float64 `json:"waiting_sec,omitempty"`
}

// PendingPickup là một điểm đón đang chờ ghép tài xế — phía "cầu" trên bản đồ.
type PendingPickup struct {
	TripID      string             `json:"trip_id"`
	Point       geo.Point          `json:"point"`
	Address     string             `json:"address"`
	VehicleType driver.VehicleType `json:"vehicle_type"`
	Fare        money.VND          `json:"fare"`
	// WaitingSec càng lớn càng cần can thiệp.
	WaitingSec float64 `json:"waiting_sec"`
}

// LiveMapResult gộp cung (tài xế) và cầu (điểm đón chờ ghép) cùng một thời điểm.
type LiveMapResult struct {
	Center      geo.Point           `json:"center"`
	RadiusM     float64             `json:"radius_m"`
	Drivers     []location.Snapshot `json:"drivers"`
	Pending     []PendingPickup     `json:"pending"`
	GeneratedAt time.Time           `json:"generated_at"`
}

// Overview là số liệu tổng quan cho trang chủ bảng điều khiển.
type Overview struct {
	Drivers struct {
		Online     int `json:"online"`  // IDLE
		OnTrip     int `json:"on_trip"` // ASSIGNED + ON_TRIP
		Offline    int `json:"offline"`
		Suspended  int `json:"suspended"`
		PendingKYC int `json:"pending_kyc"`
	} `json:"drivers"`

	Trips struct {
		Searching int `json:"searching"`
		Active    int `json:"active"` // ASSIGNED + ARRIVED + IN_PROGRESS
		Completed int `json:"completed"`
		Cancelled int `json:"cancelled"`
		Expired   int `json:"expired"`
	} `json:"trips"`

	Money struct {
		// GrossToday là tổng cước của các chuyến đã hoàn tất trong danh sách đọc được.
		Gross       money.VND `json:"gross"`
		PlatformFee money.VND `json:"platform_fee"`
		DriverEarn  money.VND `json:"driver_earn"`
		CashShare   float64   `json:"cash_share"` // tỉ lệ chuyến trả tiền mặt, 0..1
	} `json:"money"`

	// Alerts là những thứ vận hành cần để mắt tới ngay.
	Alerts []Alert `json:"alerts"`

	GeneratedAt time.Time `json:"generated_at"`
}

type AlertLevel string

const (
	AlertWarn AlertLevel = "warn"
	AlertInfo AlertLevel = "info"
)

type Alert struct {
	Level   AlertLevel `json:"level"`
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Count   int        `json:"count"`
}
