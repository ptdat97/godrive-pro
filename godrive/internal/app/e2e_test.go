package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/geo"
)

var (
	pickup  = geo.Point{Lat: 10.7725, Lng: 106.6980} // Chợ Bến Thành
	dropoff = geo.Point{Lat: 10.8014, Lng: 106.7109} // Thảo Cầm Viên hướng Q.3
	nearby  = geo.Point{Lat: 10.7740, Lng: 106.6995} // tài xế cách ~250m
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = "test"
	a, err := New(cfg, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// login đăng nhập bằng OTP và trả về accountID.
func login(t *testing.T, a *App, phone string, role authn.Role) string {
	t.Helper()
	ctx := context.Background()
	cid, code, err := a.Identity.RequestOTP(ctx, phone, role)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	if code == "" {
		t.Fatal("DevMode phải trả mã OTP trong test")
	}
	tp, err := a.Identity.VerifyOTP(ctx, cid, code, "dev-device")
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if tp.AccessToken == "" {
		t.Fatal("thiếu access token")
	}
	if _, err := a.Issuer.Parse(tp.AccessToken, time.Now().UTC()); err != nil {
		t.Fatalf("token phát hành ra phải tự xác thực được: %v", err)
	}
	return tp.Account.ID
}

// waitForOffers chờ dispatcher nền gửi lời mời. Bỏ phiếu thay vì ngủ cố định
// để test không phụ thuộc tốc độ máy (chạy dưới -race chậm hơn nhiều).
func waitForOffers(t *testing.T, a *App, driverID string) ([]matching.Offer, error) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(3 * time.Second)
	for {
		offers, err := a.Matcher.PendingOffers(ctx, driverID)
		if err != nil {
			return nil, err
		}
		if len(offers) > 0 {
			return offers, nil
		}
		if time.Now().After(deadline) {
			return offers, nil // để lời gọi kiểm tra số lượng và báo lỗi rõ ràng
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestFullTripLifecycle chạy trọn vòng đời một chuyến xe ôm trả tiền mặt.
func TestFullTripLifecycle(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)

	riderID := login(t, a, "0901234567", authn.RoleRider)
	drvAccID := login(t, a, "0912345678", authn.RoleDriver)

	// 1. Tài xế đăng ký hồ sơ và được duyệt eKYC.
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: drvAccID,
		FullName:  "Nguyễn Văn Tài",
		Phone:     "+84912345678",
		City:      "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45", Model: "Air Blade", Color: "Đen"},
		Documents: driver.Documents{NationalID: "079090001234", DriverLicense: "790123456789"},
	})
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if err := a.Drivers.ReviewKYC(ctx, d.ID, true); err != nil {
		t.Fatalf("ReviewKYC: %v", err)
	}

	// 2. Tài xế bật nhận chuyến và gửi ping vị trí.
	if err := a.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatalf("GoOnline: %v", err)
	}
	if err := a.Location.Ingest(ctx, location.Ping{
		DriverID: d.ID, Point: nearby, BearingDeg: 45, SpeedMps: 5,
		AccuracyM: 10, BatteryPc: 80, At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// 3. Khách lấy báo giá.
	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if q.Total <= 0 || q.PlatformFee <= 0 || q.DriverEarn != q.Total-q.PlatformFee {
		t.Fatalf("báo giá không hợp lệ: %+v", q)
	}

	// 4. Khách đặt chuyến (idempotent theo key).
	in := trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup:         trip.Place{Point: pickup, Address: "Chợ Bến Thành"},
		Dropoff:        trip.Place{Point: dropoff, Address: "Quận 3"},
		PaymentMethod:  trip.PayCash,
		IdempotencyKey: "key-abc-123",
	}
	tr, err := a.Trips.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create trip: %v", err)
	}
	if tr.Status != trip.StatusSearching {
		t.Fatalf("chuyến mới phải ở trạng thái SEARCHING, được %s", tr.Status)
	}
	again, err := a.Trips.Create(ctx, in)
	if err != nil {
		t.Fatalf("retry idempotent thất bại: %v", err)
	}
	if again.ID != tr.ID {
		t.Fatalf("idempotency hỏng: tạo ra 2 chuyến %s và %s", tr.ID, again.ID)
	}

	// 5. Dispatcher chào mời tài xế.
	//
	// StartWorkers đã đăng ký consumer trip.requested, nên chu trình dispatch
	// chạy nền ngay khi chuyến được tạo. Gọi thêm DispatchRound ở đây sẽ tạo
	// lời mời thứ hai cho cùng chuyến và làm test bấp bênh — chờ worker thay vì
	// tự chào mời.
	offers, err := waitForOffers(t, a, d.ID)
	if err != nil {
		t.Fatalf("PendingOffers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("tài xế phải có đúng 1 lời mời chờ, được %d", len(offers))
	}

	// 6. Tài xế nhận chuyến.
	tr, err = a.Matcher.Accept(ctx, offers[0].ID, d.ID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if tr.Status != trip.StatusAssigned || tr.DriverID == nil || *tr.DriverID != d.ID {
		t.Fatalf("chuyến phải được gán cho tài xế, trạng thái=%s", tr.Status)
	}
	if got, _ := a.Drivers.Get(ctx, d.ID); got.Status != driver.StatusAssigned {
		t.Fatalf("tài xế phải ở trạng thái ASSIGNED, được %s", got.Status)
	}

	// 7. Đón khách -> bắt đầu -> hoàn tất.
	if _, err := a.Trips.MarkArrived(ctx, tr.ID, d.ID); err != nil {
		t.Fatalf("MarkArrived: %v", err)
	}
	if _, err := a.Trips.Start(ctx, tr.ID, d.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "tài xế chuyển sang ON_TRIP", func() bool {
		got, _ := a.Drivers.Get(ctx, d.ID)
		return got.Status == driver.StatusOnTrip
	})
	if _, err := a.Trips.Complete(ctx, tr.ID, d.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// 8. Worker ghi sổ tiền mặt và trả tài xế về IDLE.
	waitFor(t, "sổ cái ghi nhận chiết khấu", func() bool {
		b, _ := a.Wallet.DriverBalance(ctx, d.ID)
		return b == q.PlatformFee.Neg()
	})
	cash, _ := a.Wallet.CashOnHand(ctx, d.ID)
	if cash != q.Total {
		t.Fatalf("tiền mặt tài xế cầm phải bằng cước %s, được %s", q.Total, cash)
	}
	waitFor(t, "tài xế quay lại IDLE", func() bool {
		got, _ := a.Drivers.Get(ctx, d.ID)
		return got.Status == driver.StatusIdle
	})

	final, err := a.Trips.Get(ctx, tr.ID)
	if err != nil || final.Status != trip.StatusPaid {
		t.Fatalf("chuyến phải kết thúc ở PAID, được %v (%v)", final.Status, err)
	}

	// 9. Nhật ký chuyển trạng thái phải đầy đủ cho mục đích đối soát/khiếu nại.
	evs, err := a.Trips.Events(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []trip.Status{
		trip.StatusSearching, trip.StatusAssigned, trip.StatusArrived,
		trip.StatusInProgress, trip.StatusCompleted, trip.StatusPaid,
	}
	if len(evs) != len(want) {
		t.Fatalf("phải có %d sự kiện, được %d", len(want), len(evs))
	}
	for i, s := range want {
		if evs[i].To != s {
			t.Fatalf("sự kiện #%d phải là %s, được %s", i, s, evs[i].To)
		}
	}

	// 10. Sổ cái toàn hệ thống phải cân bằng.
	if b, _ := a.Wallet.DriverBalance(ctx, d.ID); b != q.PlatformFee.Neg() {
		t.Fatalf("công nợ tài xế sai: %s", b)
	}
}

// TestOnlyOneDriverWinsTrip kiểm tra chống ghép trùng khi hai tài xế bấm nhận
// cùng lúc — lỗi nghiêm trọng nhất mà bộ ghép chuyến có thể mắc phải.
func TestOnlyOneDriverWinsTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	riderID := login(t, a, "0901111111", authn.RoleRider)
	ds := make([]*driver.Driver, 0, 2)
	for i, phone := range []string{"0922222222", "0933333333"} {
		accID := login(t, a, phone, authn.RoleDriver)
		d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
			AccountID: accID, FullName: "Tài xế", Phone: phone, City: "HCM",
			Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-10" + string(rune('0'+i)) + ".45"},
			Documents: driver.Documents{NationalID: "079", DriverLicense: "790"},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = a.Drivers.ReviewKYC(ctx, d.ID, true)
		_ = a.Drivers.GoOnline(ctx, d.ID)
		_ = a.Location.Ingest(ctx, location.Ping{
			DriverID: d.ID, Point: nearby, AccuracyM: 10, BatteryPc: 90, At: time.Now().UTC(),
		})
		ds = append(ds, d)
	}

	q, _ := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Matcher.DispatchRound(ctx, tr.ID, 0); err != nil {
		t.Fatal(err)
	}

	type res struct{ err error }
	results := make(chan res, 2)
	for _, d := range ds {
		offers, _ := a.Matcher.PendingOffers(ctx, d.ID)
		if len(offers) != 1 {
			t.Fatalf("mỗi tài xế phải nhận 1 lời mời, được %d", len(offers))
		}
		go func(offerID, driverID string) {
			_, err := a.Matcher.Accept(ctx, offerID, driverID)
			results <- res{err}
		}(offers[0].ID, d.ID)
	}

	wins, losses := 0, 0
	for i := 0; i < 2; i++ {
		if r := <-results; r.err == nil {
			wins++
		} else {
			losses++
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("đúng 1 tài xế được thắng, kết quả: %d thắng / %d thua", wins, losses)
	}
}

// TestCashSettlementBlocksIndebtedDriver kiểm tra chốt chặn công nợ tiền mặt.
func TestCashSettlementBlocksIndebtedDriver(t *testing.T) {
	d := &driver.Driver{
		KYC: driver.KYCApproved, Status: driver.StatusIdle,
		WalletBalance: -250000, // nợ 250k, vượt hạn mức 200k
	}
	if err := d.CanAcceptTrip(driver.DefaultDebtLimit); err == nil {
		t.Fatal("tài xế nợ quá hạn mức phải bị chặn nhận chuyến")
	}
	d.WalletBalance = -150000
	if err := d.CanAcceptTrip(driver.DefaultDebtLimit); err != nil {
		t.Fatalf("nợ trong hạn mức vẫn phải nhận được chuyến: %v", err)
	}
}

// TestLedgerBalancedForCashTrip: bất biến cốt lõi của sổ cái.
func TestLedgerBalancedForCashTrip(t *testing.T) {
	tx := wallet.SettleCashTrip("tx", "drv", "trp", 50000, 10000, time.Now())
	if err := tx.Validate(); err != nil {
		t.Fatalf("giao dịch tiền mặt phải cân bằng: %v", err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hết thời gian chờ: %s", what)
}
