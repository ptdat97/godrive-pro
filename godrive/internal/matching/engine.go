package matching

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

type LocationPort interface {
	Nearby(ctx context.Context, c geo.Point, radiusM float64, f location.Filter) ([]location.Snapshot, error)
}

type DriverPort interface {
	Get(ctx context.Context, id string) (*driver.Driver, error)
	Reserve(ctx context.Context, driverID string) error
	SetStatus(ctx context.Context, driverID string, to driver.Status) error
}

type TripPort interface {
	Get(ctx context.Context, tripID string) (*trip.Trip, error)
	Assign(ctx context.Context, tripID, driverID string) (*trip.Trip, error)
	Expire(ctx context.Context, tripID string) error
}

// WalletPort cho phép dispatcher đọc số dư THẬT từ sổ cái.
//
// Không dùng driver.WalletBalance (cột cache) vì cache có thể chậm hơn sổ cái
// vài nhịp sự kiện; dispatcher là nơi tiền của tài xế bị chặn nên phải đọc
// nguồn sự thật, không đọc bản sao.
type WalletPort interface {
	DriverBalance(ctx context.Context, driverID string) (money.VND, error)
}

// ETAEngine ước lượng thời gian tài xế tới điểm đón.
type ETAEngine interface {
	ETASeconds(ctx context.Context, from, to geo.Point) (float64, error)
}

type Engine struct {
	cfg     Config
	loc     LocationPort
	drivers DriverPort
	trips   TripPort
	store   Store
	eta     ETAEngine
	wallet  WalletPort
	bus     eventbus.Bus
	clk     clock.Clock
}

func NewEngine(cfg Config, loc LocationPort, d DriverPort, t TripPort, st Store, eta ETAEngine, w WalletPort, bus eventbus.Bus, clk clock.Clock) *Engine {
	return &Engine{cfg: cfg, loc: loc, drivers: d, trips: t, store: st, eta: eta, wallet: w, bus: bus, clk: clk}
}

// DispatchRound chạy một vòng chào mời cho chuyến. Trả về số lời mời đã gửi.
func (e *Engine) DispatchRound(ctx context.Context, tripID string, round int) (int, error) {
	t, err := e.trips.Get(ctx, tripID)
	if err != nil {
		return 0, err
	}
	if t.Status != trip.StatusSearching {
		return 0, nil
	}

	radius := e.cfg.InitialRadiusM + float64(round)*e.cfg.RadiusStepM
	if radius > e.cfg.MaxRadiusM {
		radius = e.cfg.MaxRadiusM
	}

	cands, err := e.candidates(ctx, t, radius)
	if err != nil {
		return 0, err
	}
	if len(cands) == 0 {
		return 0, nil
	}

	cands = rank(e.cfg, cands, t.Pickup.Point)
	n := e.cfg.BatchSize
	if len(cands) < n {
		n = len(cands)
	}

	now := e.clk.Now()
	offers := make([]Offer, 0, n)
	for _, c := range cands[:n] {
		offers = append(offers, Offer{
			ID:        id.New("ofr"),
			TripID:    t.ID,
			DriverID:  c.DriverID,
			Round:     round,
			Status:    OfferPending,
			Score:     c.Score,
			ETASec:    c.ETASec,
			PickupM:   c.DistanceM,
			CreatedAt: now,
			ExpiresAt: now.Add(e.cfg.OfferTTL),
		})
	}
	if err := e.store.SaveOffers(ctx, offers); err != nil {
		return 0, err
	}
	for _, o := range offers {
		_ = e.bus.Publish(ctx, eventbus.TopicOfferCreated, o)
	}
	return len(offers), nil
}

// Dispatch chạy toàn bộ chu trình cho tới khi có tài xế nhận hoặc hết vòng.
// Gọi trong goroutine của worker; mỗi chuyến một chu trình.
func (e *Engine) Dispatch(ctx context.Context, tripID string) error {
	for round := 0; round < e.cfg.MaxRounds; round++ {
		sent, err := e.DispatchRound(ctx, tripID, round)
		if err != nil {
			return err
		}
		if sent > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(e.cfg.OfferTTL):
			}
		} else {
			wait := e.cfg.EmptyRoundWait
			if wait <= 0 {
				wait = 2 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		t, err := e.trips.Get(ctx, tripID)
		if err != nil {
			return err
		}
		if t.Status != trip.StatusSearching {
			return nil // đã ghép được hoặc đã huỷ
		}
	}
	return e.trips.Expire(ctx, tripID)
}

// Accept xử lý tài xế bấm "Nhận chuyến". Thứ tự kiểm tra rất quan trọng:
// giành khoá chuyến TRƯỚC, rồi mới giữ chỗ tài xế; nếu giữ chỗ thất bại
// phải nhả khoá bằng cách để offer hết hạn (chuyến quay lại SEARCHING).
func (e *Engine) Accept(ctx context.Context, offerID, driverID string) (*trip.Trip, error) {
	o, err := e.store.GetOffer(ctx, offerID)
	if err != nil {
		return nil, err
	}
	if o.DriverID != driverID {
		return nil, errs.E(errs.KindForbidden, "not_your_offer", "Lời mời này không dành cho bạn.")
	}
	if o.Status != OfferPending {
		return nil, errs.Conflict("offer_not_pending", "Lời mời không còn hiệu lực.")
	}
	if e.clk.Now().After(o.ExpiresAt) {
		_ = e.store.UpdateStatus(ctx, offerID, OfferExpired)
		return nil, errs.Conflict("offer_expired", "Lời mời đã hết hạn.")
	}

	won, err := e.store.ClaimTrip(ctx, o.TripID, driverID, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !won {
		_ = e.store.UpdateStatus(ctx, offerID, OfferLost)
		return nil, errs.Conflict("trip_taken", "Chuyến đã có tài xế khác nhận.")
	}

	if err := e.drivers.Reserve(ctx, driverID); err != nil {
		_ = e.store.UpdateStatus(ctx, offerID, OfferRejected)
		return nil, err
	}

	t, err := e.trips.Assign(ctx, o.TripID, driverID)
	if err != nil {
		_ = e.drivers.SetStatus(ctx, driverID, driver.StatusIdle)
		return nil, err
	}

	_ = e.store.UpdateStatus(ctx, offerID, OfferAccepted)
	_ = e.store.ExpireOffers(ctx, o.TripID, offerID)
	_ = e.bus.Publish(ctx, eventbus.TopicOfferAccepted, map[string]string{
		"trip_id": o.TripID, "driver_id": driverID, "offer_id": offerID,
	})
	return t, nil
}

func (e *Engine) Reject(ctx context.Context, offerID, driverID string) error {
	o, err := e.store.GetOffer(ctx, offerID)
	if err != nil {
		return err
	}
	if o.DriverID != driverID {
		return errs.E(errs.KindForbidden, "not_your_offer", "Lời mời này không dành cho bạn.")
	}
	return e.store.UpdateStatus(ctx, offerID, OfferRejected)
}

func (e *Engine) PendingOffers(ctx context.Context, driverID string) ([]Offer, error) {
	return e.store.PendingForDriver(ctx, driverID)
}

func (e *Engine) candidates(ctx context.Context, t *trip.Trip, radius float64) ([]Candidate, error) {
	snaps, err := e.loc.Nearby(ctx, t.Pickup.Point, radius, location.Filter{
		VehicleTypes: []driver.VehicleType{t.VehicleType},
		Statuses:     []driver.Status{driver.StatusIdle},
		MinBatteryPc: e.cfg.MinBatteryPc,
		FreshWithin:  location.StaleAfter,
	})
	if err != nil {
		return nil, err
	}
	now := e.clk.Now()
	out := make([]Candidate, 0, len(snaps))
	for _, s := range snaps {
		d, err := e.drivers.Get(ctx, s.DriverID)
		if err != nil {
			continue
		}
		// Đọc số dư từ SỔ CÁI, không tin cột cache trên hồ sơ tài xế.
		// Không đọc được thì bỏ qua ứng viên: thà mất một chuyến còn hơn giao
		// chuyến cho tài xế đang nợ vượt hạn mức.
		if e.wallet != nil {
			bal, err := e.wallet.DriverBalance(ctx, s.DriverID)
			if err != nil {
				continue
			}
			d.WalletBalance = bal
		}
		if err := d.CanAcceptTrip(driver.DefaultDebtLimit); err != nil {
			continue
		}
		etaSec, err := e.eta.ETASeconds(ctx, s.Point, t.Pickup.Point)
		if err != nil {
			continue
		}
		out = append(out, Candidate{
			DriverID:    s.DriverID,
			Point:       s.Point,
			BearingDeg:  s.BearingDeg,
			VehicleType: s.VehicleType,
			DistanceM:   geo.DistanceM(s.Point, t.Pickup.Point),
			ETASec:      etaSec,
			Rating:      d.Rating(),
			Acceptance:  d.AcceptanceRate(),
			// Thời gian RẢNH thật, không phải độ cũ của ping vị trí.
			// Dùng độ cũ của ping là vô tình thưởng cho tài xế mạng kém.
			IdleSeconds: d.IdleSeconds(now),
		})
	}
	return out, nil
}
