package matching

import (
	"context"
	"sort"
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

// ETAEngine ước lượng thời gian tới điểm đón cho MỘT LÔ ứng viên.
//
// Nhận cả lô chứ không nhận từng cặp là quyết định về CHI PHÍ, không phải về
// hiệu năng: OSRM có endpoint /table trả ma trận thời gian trong một lần gọi,
// còn gọi /route cho từng ứng viên là N lần. Với dịch vụ bản đồ tính tiền theo
// request, một vòng dispatch 50 ứng viên là 50 lần tính tiền thay vì 1.
//
// Trả về slice CÙNG ĐỘ DÀI và CÙNG THỨ TỰ với from. Ứng viên nào không tính
// được ETA thì trả giá trị âm ở đúng vị trí đó.
type ETAEngine interface {
	ETASeconds(ctx context.Context, from []geo.Point, to geo.Point) ([]float64, error)
}

// ConfigProvider trả cấu hình ghép chuyến hiện hành.
//
// Đọc mỗi vòng dispatch chứ không giữ một bản sao: đổi bán kính hay trọng số từ
// bảng điều khiển phải có hiệu lực ở vòng kế tiếp, không phải sau khi khởi động lại.
type ConfigProvider func(ctx context.Context) Config

type Engine struct {
	cfg     Config
	cfgFn   ConfigProvider
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

// UseConfig nối nguồn cấu hình động.
func (e *Engine) UseConfig(p ConfigProvider) { e.cfgFn = p }

// config trả cấu hình hiện hành cho một lượt dispatch.
func (e *Engine) config(ctx context.Context) Config {
	if e.cfgFn != nil {
		return e.cfgFn(ctx)
	}
	return e.cfg
}

// DispatchRound chạy một vòng chào mời cho chuyến. Trả về số lời mời đã gửi.
func (e *Engine) DispatchRound(ctx context.Context, tripID string, round int) (int, error) {
	cfg := e.config(ctx)
	t, err := e.trips.Get(ctx, tripID)
	if err != nil {
		return 0, err
	}
	if t.Status != trip.StatusSearching {
		return 0, nil
	}

	radius := cfg.InitialRadiusM + float64(round)*cfg.RadiusStepM
	if radius > cfg.MaxRadiusM {
		radius = cfg.MaxRadiusM
	}

	cands, err := e.candidates(ctx, t, radius, cfg)
	if err != nil {
		return 0, err
	}
	if len(cands) == 0 {
		return 0, nil
	}

	cands = rank(cfg, cands, t.Pickup.Point)
	n := cfg.BatchSize
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
			ExpiresAt: now.Add(cfg.OfferTTL),
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
	cfg := e.config(ctx)
	for round := 0; round < cfg.MaxRounds; round++ {
		sent, err := e.DispatchRound(ctx, tripID, round)
		if err != nil {
			return err
		}
		if sent > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.OfferTTL):
			}
		} else {
			wait := cfg.EmptyRoundWait
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
	e.withdraw(ctx, o.TripID, offerID, OfferLost, WithdrawTaken)
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

// PendingOfferViews như PendingOffers nhưng kèm thông tin chuyến — đây là thứ
// app tài xế hiển thị.
func (e *Engine) PendingOfferViews(ctx context.Context, driverID string) ([]OfferView, error) {
	offers, err := e.store.PendingForDriver(ctx, driverID)
	if err != nil {
		return nil, err
	}
	out := make([]OfferView, 0, len(offers))
	for _, o := range offers {
		v, err := e.View(ctx, o)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// View ghép lời mời với chuyến của nó.
//
// Ghép lúc đọc chứ không chép vào lời mời lúc tạo: lời mời nằm ở Redis hoặc
// Postgres tuỳ cấu hình, và bảng offers không có cột nào cho điểm đón hay số
// tiền. Chép vào nghĩa là thêm cột cho cả hai kho và giữ chúng khớp với chuyến.
func (e *Engine) View(ctx context.Context, o Offer) (OfferView, error) {
	t, err := e.trips.Get(ctx, o.TripID)
	if err != nil {
		return OfferView{}, err
	}
	return OfferView{
		Offer: o, Pickup: t.Pickup, Dropoff: t.Dropoff, VehicleType: t.VehicleType,
		PaymentMethod: t.PaymentMethod, DriverEarn: t.DriverEarn,
	}, nil
}

// WithdrawOffers thu hồi mọi lời mời còn chờ của một chuyến vừa bị huỷ.
func (e *Engine) WithdrawOffers(ctx context.Context, tripID string) error {
	return e.withdraw(ctx, tripID, "", OfferExpired, WithdrawTripCancelled)
}

// withdraw đóng các lời mời còn chờ và báo cho từng tài xế.
//
// Không báo thì tài xế vẫn thấy lời mời trên màn hình tới hết 15 giây, bấm
// nhận rồi mới biết là đã mất — đúng lúc họ có thể đang tấp xe vào lề để bấm.
func (e *Engine) withdraw(ctx context.Context, tripID, except string, to OfferStatus, reason string) error {
	gone, err := e.store.ExpireOffers(ctx, tripID, except, to)
	for _, o := range gone {
		_ = e.bus.Publish(ctx, eventbus.TopicOfferWithdrawn, OfferWithdrawn{
			OfferID: o.ID, TripID: o.TripID, DriverID: o.DriverID, Reason: reason,
		})
	}
	return err
}

func (e *Engine) candidates(ctx context.Context, t *trip.Trip, radius float64, cfg Config) ([]Candidate, error) {
	snaps, err := e.loc.Nearby(ctx, t.Pickup.Point, radius, location.Filter{
		VehicleTypes: []driver.VehicleType{t.VehicleType},
		Statuses:     []driver.Status{driver.StatusIdle},
		MinBatteryPc: cfg.MinBatteryPc,
		FreshWithin:  location.StaleAfter,
	})
	if err != nil {
		return nil, err
	}
	now := e.clk.Now()

	// Xét từ GẦN ra XA và dừng khi đủ MaxCandidates người hợp lệ.
	//
	// Mỗi ứng viên tốn hai truy vấn (hồ sơ + số dư sổ cái). Trước đây mọi tài
	// xế trong bán kính đều được tra, trong khi mỗi vòng chỉ mời BatchSize
	// người: test tải T-35 với 5.000 tài xế cho thấy một vòng quét hơn 1.000
	// ứng viên, CSDL nghẽn, API chậm cả giây, rồi broker MQTT quá hạn khi hỏi
	// xác thực và ngắt kết nối hàng loạt. Tài xế ở xa có ETA lớn hơn hẳn nên
	// gần như không bao giờ lọt vào BatchSize người được mời — cắt ở đây không
	// đổi kết quả chấm điểm trên thực tế.
	sort.Slice(snaps, func(i, j int) bool {
		return geo.DistanceM(snaps[i].Point, t.Pickup.Point) < geo.DistanceM(snaps[j].Point, t.Pickup.Point)
	})
	limit := cfg.MaxCandidates
	if limit <= 0 {
		limit = len(snaps)
	}
	// Xét ít người hơn số người được mời là vòng nào cũng mời thiếu. Tự nâng
	// thay vì bắt vận hành sửa hai ô cấu hình cho khớp nhau.
	limit = max(limit, cfg.BatchSize)

	// Lọc điều kiện TRƯỚC, tính ETA SAU: không hỏi dịch vụ bản đồ về những tài
	// xế chắc chắn không dùng được (chưa duyệt hồ sơ, đang nợ quá hạn mức).
	type cand struct {
		snap location.Snapshot
		drv  *driver.Driver
	}
	eligible := make([]cand, 0, min(len(snaps), limit))
	for _, s := range snaps {
		if len(eligible) >= limit {
			break
		}
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
		if err := d.CanAcceptTrip(cfg.DebtLimit, e.clk.Now()); err != nil {
			continue
		}
		eligible = append(eligible, cand{snap: s, drv: d})
	}
	if len(eligible) == 0 {
		return nil, nil
	}

	froms := make([]geo.Point, len(eligible))
	for i, c := range eligible {
		froms[i] = c.snap.Point
	}
	etas, err := e.eta.ETASeconds(ctx, froms, t.Pickup.Point)
	if err != nil {
		return nil, err
	}
	if len(etas) != len(eligible) {
		return nil, errs.E(errs.KindInternal, "eta_length_mismatch",
			"Máy ước lượng thời gian trả về số kết quả không khớp số ứng viên.")
	}

	out := make([]Candidate, 0, len(eligible))
	for i, c := range eligible {
		if etas[i] < 0 {
			continue // không tính được ETA cho ứng viên này
		}
		out = append(out, Candidate{
			DriverID:    c.snap.DriverID,
			Point:       c.snap.Point,
			BearingDeg:  c.snap.BearingDeg,
			VehicleType: c.snap.VehicleType,
			DistanceM:   geo.DistanceM(c.snap.Point, t.Pickup.Point),
			ETASec:      etas[i],
			Rating:      c.drv.Rating(),
			Acceptance:  c.drv.AcceptanceRate(),
			// Đã làm mượt Bayes như tỉ lệ nhận: tài xế mới huỷ một chuyến đầu
			// tiên không bị coi là "huỷ 100%".
			CancelRate: c.drv.CancelRate(),
			// Thời gian RẢNH thật, không phải độ cũ của ping vị trí.
			// Dùng độ cũ của ping là vô tình thưởng cho tài xế mạng kém.
			IdleSeconds: c.drv.IdleSeconds(now),
		})
	}
	return out, nil
}
