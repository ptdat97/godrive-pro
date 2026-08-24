package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/outbox"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/platform/safego"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

// StartWorkers đăng ký các consumer nền.
// Ở production, mỗi consumer chạy trong tiến trình riêng và đọc từ NATS/Kafka
// với consumer group để scale ngang.
func (a *App) StartWorkers(ctx context.Context) {
	a.Bus.Subscribe(eventbus.TopicTripRequested, a.onTripRequested(ctx))
	// Đo CẦU cho surge. Trước đây DemandSurge.RecordRequest không có ai gọi,
	// nên demand vĩnh viễn bằng 0 và hệ số luôn là 1.0.
	a.Bus.Subscribe(eventbus.TopicTripRequested, a.onTripRequestedSurge)
	a.Bus.Subscribe(eventbus.TopicTripCompleted, a.onTripCompleted)
	// Đồng bộ trạng thái tài xế theo vòng đời chuyến.
	a.Bus.Subscribe(eventbus.TopicTripStarted, a.setDriverStatus(driver.StatusOnTrip))
	a.Bus.Subscribe(eventbus.TopicTripCancelled, a.onTripCancelled)
	// Đồng bộ cột cache drivers.wallet_balance từ sổ cái.
	a.Bus.Subscribe(eventbus.TopicWalletBalanceChanged, a.onWalletBalanceChanged)

	// Thống kê tài xế — đầu vào của hàm chấm điểm. Không có những consumer này
	// thì rating/acceptance/cancel đóng băng ở tiền nghiệm và ba trong năm thành
	// phần chấm điểm trở thành hằng số.
	a.Bus.Subscribe(eventbus.TopicOfferCreated, a.onOfferStat(driver.StatsDelta{OffersReceived: 1}))
	a.Bus.Subscribe(eventbus.TopicOfferAccepted, a.onOfferStat(driver.StatsDelta{OffersAccepted: 1}))
	a.Bus.Subscribe(eventbus.TopicTripRated, a.onTripRated)

	go a.sweepOTPChallenges(ctx)

	// Ở chế độ Postgres, sự kiện đi qua outbox nên PHẢI có relay chạy, nếu không
	// không consumer nào nhận được gì.
	if a.Outbox != nil {
		relay := outbox.NewRelay(a.Outbox, a.Bus, a.Log)
		go func() {
			defer safego.Recover(a.Log, "outbox.relay", nil)
			relay.Run(ctx)
		}()
	}
}

// OTPSweepInterval là chu kỳ dọn thử thách OTP quá hạn. Thử thách sống 5 phút,
// nên quét mỗi phút là đủ mà không tạo tải đáng kể.
const OTPSweepInterval = time.Minute

// sweepOTPChallenges dọn thử thách OTP không ai quay lại xác thực. Ở chế độ
// bộ nhớ đây là chống rò rỉ RAM; ở Postgres là chống phình bảng otp_challenges.
func (a *App) sweepOTPChallenges(ctx context.Context) {
	defer safego.Recover(a.Log, "identity.otp_sweep", nil)
	t := time.NewTicker(OTPSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := a.Identity.SweepExpiredChallenges(ctx)
			if err != nil {
				a.Log.Error("dọn thử thách OTP lỗi", "err", err)
				continue
			}
			if n > 0 {
				a.Log.Debug("đã dọn thử thách OTP quá hạn", "count", n)
			}
		}
	}
}

// onTripRequested khởi động chu trình ghép tài xế cho chuyến mới.
func (a *App) onTripRequested(root context.Context) eventbus.Handler {
	return func(_ context.Context, e eventbus.Event) error {
		var p struct {
			TripID string `json:"trip_id"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return err
		}
		go func() {
			// Panic ở đây từng giết cả tiến trình. Cleanup đẩy chuyến ra khỏi
			// SEARCHING để nó không kẹt vĩnh viễn chờ một dispatcher đã chết.
			defer safego.Recover(a.Log, "matcher.dispatch", func() {
				if err := a.Trips.Expire(root, p.TripID); err != nil {
					a.Log.Error("không đẩy được chuyến về EXPIRED sau panic",
						"trip_id", p.TripID, "err", err)
				}
			})
			if err := a.Matcher.Dispatch(root, p.TripID); err != nil {
				a.Log.Error("dispatch lỗi", "trip_id", p.TripID, "err", err)
			}
		}()
		return nil
	}
}

// onTripRequestedSurge ghi nhận một yêu cầu đặt xe vào bộ đếm cầu.
func (a *App) onTripRequestedSurge(_ context.Context, e eventbus.Event) error {
	var p struct {
		Pickup geo.Point `json:"pickup"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	if !p.Pickup.Valid() {
		return nil
	}
	a.Surge.RecordRequest(p.Pickup, a.Clock.Now())
	return nil
}

// onTripCompleted ghi sổ tiền. Idempotent theo tripID nên retry an toàn.
func (a *App) onTripCompleted(ctx context.Context, e eventbus.Event) error {
	var p struct {
		TripID        string `json:"trip_id"`
		DriverID      string `json:"driver_id"`
		Fare          int64  `json:"fare"`
		PlatformFee   int64  `json:"platform_fee"`
		PaymentMethod string `json:"payment_method"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	cash := trip.PaymentMethod(p.PaymentMethod) == trip.PayCash
	if err := a.Wallet.SettleTrip(ctx, p.TripID, p.DriverID,
		money.VND(p.Fare), money.VND(p.PlatformFee), cash); err != nil {
		return err
	}
	if _, err := a.Trips.MarkPaid(ctx, p.TripID); err != nil {
		return err
	}
	if err := a.Drivers.ApplyStats(ctx, p.DriverID,
		driver.StatsDelta{TripsCompleted: 1}); err != nil {
		return err
	}
	// Trả tài xế về trạng thái sẵn sàng nhận chuyến mới.
	return a.Drivers.SetStatus(ctx, p.DriverID, driver.StatusIdle)
}

// onOfferStat cộng dồn số lời mời gửi đi / được nhận cho tài xế.
//
// Mẫu số (offers_received) và tử số (offers_accepted) đến từ hai sự kiện khác
// nhau nên có thể lệch nhau vài nhịp; tiền nghiệm Bayes làm phần lệch đó không
// đáng kể, và tỉ lệ hội tụ đúng khi đủ mẫu.
func (a *App) onOfferStat(delta driver.StatsDelta) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		var p struct {
			DriverID string `json:"driver_id"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return err
		}
		if p.DriverID == "" {
			return nil
		}
		return a.Drivers.ApplyStats(ctx, p.DriverID, delta)
	}
}

// onTripRated cộng điểm đánh giá của khách vào thống kê tài xế.
func (a *App) onTripRated(ctx context.Context, e eventbus.Event) error {
	var p struct {
		DriverID string `json:"driver_id"`
		Rating   int    `json:"rating"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	if p.DriverID == "" || p.Rating < 1 || p.Rating > 5 {
		return nil
	}
	return a.Drivers.ApplyStats(ctx, p.DriverID, driver.StatsDelta{
		RatingSum: p.Rating, RatingCount: 1,
	})
}

// onTripCancelled trả tài xế về IDLE và ghi sổ phí huỷ nếu có.
//
// Phí huỷ do trip.Service tính (khách huỷ quá cửa sổ miễn phí 2 phút sau khi
// ghép). Trước đây nó chỉ được ghi vào nhật ký chứ không vào sổ cái, nên tài xế
// không bao giờ nhận được đồng đền bù nào.
func (a *App) onTripCancelled(ctx context.Context, e eventbus.Event) error {
	var p struct {
		TripID    string  `json:"trip_id"`
		RiderID   string  `json:"rider_id"`
		DriverID  *string `json:"driver_id"`
		By        string  `json:"by"`
		CancelFee int64   `json:"cancel_fee"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	// Chuyến bị huỷ khi chưa ghép tài xế: không có ai để trả về IDLE, cũng
	// không có ai để đền bù.
	if p.DriverID == nil || *p.DriverID == "" {
		return nil
	}
	if p.CancelFee > 0 {
		if err := a.Wallet.PostCancelFee(ctx, p.TripID, p.RiderID, *p.DriverID,
			money.VND(p.CancelFee)); err != nil {
			return err
		}
	}
	// Chỉ tài xế TỰ huỷ mới bị tính vào tỉ lệ huỷ. Khách huỷ không phải lỗi của
	// tài xế, tính vào đó là phạt oan và làm hỏng chính tín hiệu chấm điểm.
	if p.By == string(trip.CancelByDriver) {
		if err := a.Drivers.ApplyStats(ctx, *p.DriverID,
			driver.StatsDelta{TripsCancelled: 1}); err != nil {
			return err
		}
	}
	return a.Drivers.SetStatus(ctx, *p.DriverID, driver.StatusIdle)
}

// onWalletBalanceChanged đồng bộ cột cache drivers.wallet_balance.
//
// Đọc lại từ sổ cái thay vì tin con số trong payload: sổ cái là nguồn sự thật,
// và đọc sau khi ghi xong luôn cho giá trị mới nhất.
func (a *App) onWalletBalanceChanged(ctx context.Context, e eventbus.Event) error {
	var p struct {
		AccountID   string `json:"account_id"`
		AccountType string `json:"account_type"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	if p.AccountID == "" || p.AccountType != string(wallet.AccDriverWallet) {
		return nil
	}
	bal, err := a.Wallet.DriverBalance(ctx, p.AccountID)
	if err != nil {
		return err
	}
	return a.Drivers.SyncWalletBalance(ctx, p.AccountID, bal)
}

// setDriverStatus tạo handler đổi trạng thái tài xế theo sự kiện chuyến.
func (a *App) setDriverStatus(to driver.Status) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		var p struct {
			TripID   string  `json:"trip_id"`
			DriverID *string `json:"driver_id"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return err
		}
		if p.DriverID == nil || *p.DriverID == "" {
			// Chuyến bị huỷ khi chưa ghép tài xế: không có gì để cập nhật.
			if p.TripID != "" {
				t, err := a.Trips.Get(ctx, p.TripID)
				if err != nil || t.DriverID == nil {
					return nil
				}
				return a.Drivers.SetStatus(ctx, *t.DriverID, to)
			}
			return nil
		}
		return a.Drivers.SetStatus(ctx, *p.DriverID, to)
	}
}
