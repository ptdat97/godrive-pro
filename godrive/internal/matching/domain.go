// Package matching là bộ ghép chuyến (dispatcher).
//
// Chiến lược: broadcast theo lô có chấm điểm.
//  1. Tìm tài xế rảnh trong bán kính quanh điểm đón (chỉ mục ô lưới / H3).
//  2. Chấm điểm theo ETA, đánh giá, tỉ lệ nhận chuyến, thời gian chờ, hướng xe.
//  3. Gửi lời mời cho N tài xế điểm cao nhất, hết hạn sau OfferTTL.
//  4. Ai bấm nhận trước thì thắng (giành khoá nguyên tử ở tầng Store).
//  5. Không ai nhận -> nới bán kính, thử lại tối đa MaxRounds vòng.
package matching

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

type OfferStatus string

const (
	OfferPending  OfferStatus = "PENDING"
	OfferAccepted OfferStatus = "ACCEPTED"
	OfferRejected OfferStatus = "REJECTED"
	OfferExpired  OfferStatus = "EXPIRED"
	OfferLost     OfferStatus = "LOST" // tài xế khác nhận trước
)

type Offer struct {
	ID        string      `json:"id"`
	TripID    string      `json:"trip_id"`
	DriverID  string      `json:"driver_id"`
	Round     int         `json:"round"`
	Status    OfferStatus `json:"status"`
	Score     float64     `json:"-"`
	ETASec    float64     `json:"eta_sec"`
	PickupM   float64     `json:"pickup_distance_m"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type Candidate struct {
	DriverID    string
	Point       geo.Point
	BearingDeg  float64
	VehicleType driver.VehicleType
	DistanceM   float64
	ETASec      float64
	Rating      float64
	Acceptance  float64
	IdleSeconds float64
	Score       float64
}

// Store lưu lời mời. Điểm mấu chốt: ClaimTrip phải NGUYÊN TỬ
// (Redis SET NX hoặc UPDATE ... WHERE claimed_by IS NULL).
type Store interface {
	SaveOffers(ctx context.Context, offers []Offer) error
	GetOffer(ctx context.Context, offerID string) (Offer, error)
	// ClaimTrip trả về true nếu driverID là người giành được chuyến.
	ClaimTrip(ctx context.Context, tripID, driverID string, ttl time.Duration) (bool, error)
	UpdateStatus(ctx context.Context, offerID string, s OfferStatus) error
	PendingForDriver(ctx context.Context, driverID string) ([]Offer, error)
	ExpireOffers(ctx context.Context, tripID string, except string) error
}

// Config điều chỉnh hành vi ghép chuyến.
type Config struct {
	InitialRadiusM float64
	RadiusStepM    float64
	MaxRadiusM     float64
	MaxRounds      int
	BatchSize      int
	OfferTTL       time.Duration
	// EmptyRoundWait là thời gian chờ trước khi thử vòng sau khi vòng này không
	// tìm được ứng viên nào. Ngắn quá thì quét lại vô ích, dài quá thì khách chờ.
	EmptyRoundWait time.Duration
	MinBatteryPc   int

	// DebtLimit là hạn mức công nợ dùng để lọc ứng viên. Nằm ở đây thay vì đọc
	// hằng số của module driver, để đổi từ bảng điều khiển có hiệu lực ngay.
	DebtLimit money.VND

	// Trọng số chấm điểm (điểm càng THẤP càng tốt).
	WeightETA        float64
	WeightRating     float64
	WeightAcceptance float64
	WeightIdle       float64
	WeightHeading    float64
}

func DefaultConfig() Config {
	return Config{
		InitialRadiusM: 1500,
		RadiusStepM:    1500,
		MaxRadiusM:     5000,
		MaxRounds:      3,
		BatchSize:      5,
		OfferTTL:       15 * time.Second,
		EmptyRoundWait: 2 * time.Second,
		MinBatteryPc:   15,
		DebtLimit:      driver.DefaultDebtLimit,

		WeightETA:        1.0,  // mỗi giây ETA = 1 điểm phạt
		WeightRating:     60.0, // chênh 1 sao ~ 60 giây
		WeightAcceptance: 90.0, // chênh 100% tỉ lệ nhận ~ 90 giây
		WeightIdle:       0.25, // chờ lâu được ưu tiên (trừ điểm phạt)
		WeightHeading:    0.20, // xe đang đi ngược hướng bị phạt nhẹ
	}
}
