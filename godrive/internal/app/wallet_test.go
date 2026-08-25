package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// mockBase là mốc của đồng hồ giả: 10 giờ UTC HÔM NAY (17 giờ Việt Nam).
//
// Không ghim cứng ngày tháng. Một mốc như time.Date(2026, 8, 24, ...) đúng vào
// hôm viết test rồi hỏng lặng lẽ vài ngày sau, khi khoảng cách tới giờ thật vượt
// quá hạn của token — và lỗi hiện ra ở một test chẳng liên quan gì tới thời gian.
// Giờ trong ngày vẫn cố định để phụ phí đêm và các luật theo khung giờ tất định.
func mockBase() time.Time {
	y, m, d := time.Now().UTC().Date()
	return time.Date(y, m, d, 10, 0, 0, 0, time.UTC)
}

func newMockClockApp(t *testing.T) (*App, *clock.Mock) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = "test"
	clk := clock.NewMock(mockBase())
	a, err := NewWithClock(cfg, logger.New("error", false), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, clk
}

// eventually bỏ phiếu cho tới khi điều kiện đúng. Consumer sự kiện chạy bất
// đồng bộ nên không thể assert ngay sau khi publish.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hết thời gian chờ: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCashDebtBlocksDriverEndToEnd là điều kiện hoàn thành quan trọng nhất của
// Giai đoạn 1.
//
// Mô hình công nợ tiền mặt là trụ cột của sản phẩm ở thị trường VN: tài xế cầm
// tiền của khách rồi nợ lại nền tảng phần chiết khấu. Trước GĐ 1, cổng chặn nợ
// KHÔNG BAO GIỜ kích hoạt vì SettleCashTrip ghi vào ledger_entries còn
// CanAcceptTrip lại đọc cột cache drivers.wallet_balance mà không ai cập nhật.
func TestCashDebtBlocksDriverEndToEnd(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	d := seedDriver(t, a, "0912345678", "Nguyễn Văn Tài", "59X1-123.45")

	const fare, fee = money.VND(50000), money.VND(10000) // chiết khấu 20%
	if err := a.Drivers.Reserve(ctx, d.ID); err != nil {
		t.Fatalf("tài xế sạch nợ phải nhận được chuyến: %v", err)
	}
	if err := a.Drivers.SetStatus(ctx, d.ID, driver.StatusIdle); err != nil {
		t.Fatal(err)
	}

	// 21 chuyến tiền mặt × 10.000đ chiết khấu = nợ 210.000đ, vượt hạn mức 200.000đ.
	for i := 0; i < 21; i++ {
		tripID := "trp_debt_" + string(rune('a'+i))
		if err := a.Wallet.SettleTrip(ctx, tripID, d.ID, fare, fee, true); err != nil {
			t.Fatalf("SettleTrip %d: %v", i, err)
		}
	}

	bal, err := a.Wallet.DriverBalance(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bal != -210000 {
		t.Fatalf("ví phải âm 210.000đ, đang là %d", bal)
	}
	cash, err := a.Wallet.CashOnHand(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cash != 21*fare {
		t.Fatalf("tiền mặt đang cầm phải là %d, là %d", 21*fare, cash)
	}

	// Cổng chặn phải kích hoạt — đây chính là điều trước đây không xảy ra.
	err = a.Drivers.Reserve(ctx, d.ID)
	if got := errs.CodeOf(err); got != "wallet_debt_exceeded" {
		t.Fatalf("tài xế nợ vượt hạn mức phải bị chặn với wallet_debt_exceeded, được %q (err=%v)", got, err)
	}

	// Cột cache cũng phải được đồng bộ (bảng điều khiển đọc nó).
	eventually(t, "cột cache drivers.wallet_balance đồng bộ với sổ cái", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.WalletBalance == -210000
	})

	// Dispatcher cũng phải loại tài xế này khỏi danh sách ứng viên.
	riderID := login(t, a, "0901234567", authn.RoleRider)
	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := a.Matcher.DispatchRound(ctx, tr.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("tài xế nợ quá hạn mức không được lọt vào danh sách ứng viên, đã gửi %d lời mời", sent)
	}

	// Nạp tiền -> nhận chuyến lại được NGAY, không phải chờ đồng bộ cache.
	if err := a.Wallet.TopUp(ctx, d.ID, "vietqr-001", 300000); err != nil {
		t.Fatalf("TopUp: %v", err)
	}
	if bal, _ = a.Wallet.DriverBalance(ctx, d.ID); bal != 90000 {
		t.Fatalf("sau khi nạp 300k, ví phải là +90.000đ, là %d", bal)
	}
	if err := a.Drivers.Reserve(ctx, d.ID); err != nil {
		t.Fatalf("nạp xong phải nhận chuyến lại được ngay: %v", err)
	}
}

// TestSettleTripIsIdempotentAcrossRetries: worker retry không được ghi sổ hai lần.
func TestSettleTripIsIdempotentAcrossRetries(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")

	for i := 0; i < 5; i++ {
		if err := a.Wallet.SettleTrip(ctx, "trp_same", d.ID, 50000, 10000, true); err != nil {
			t.Fatal(err)
		}
	}
	bal, _ := a.Wallet.DriverBalance(ctx, d.ID)
	if bal != -10000 {
		t.Fatalf("ghi sổ 5 lần cùng tripID chỉ được tính 1 lần: ví = %d, muốn -10000", bal)
	}
}

// TestLateCancelCreditsDriver là hồi quy cho G-05.
//
// Phí huỷ trước đây được TÍNH và đưa vào nhật ký, nhưng không ai ghi sổ — tài xế
// bị huỷ chuyến trễ không nhận được đồng nào dù giao diện đã hứa.
func TestLateCancelCreditsDriver(t *testing.T) {
	ctx := context.Background()
	a, clk := newMockClockApp(t)
	a.StartWorkers(ctx)

	d := seedDriverAt(t, a, clk, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatal(err)
	}
	offers, err := waitForOffers(t, a, d.ID)
	if err != nil || len(offers) != 1 {
		t.Fatalf("cần đúng 1 lời mời, được %d (err=%v)", len(offers), err)
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}

	// Quá cửa sổ huỷ miễn phí 2 phút.
	clk.Advance(trip.FreeCancelWindow + time.Second)
	if _, err := a.Trips.Cancel(ctx, trip.CancelInput{
		TripID: tr.ID, By: trip.CancelByRider, Actor: riderID, Reason: "đổi ý",
	}); err != nil {
		t.Fatal(err)
	}

	eventually(t, "phí huỷ được ghi có cho tài xế", func() bool {
		bal, err := a.Wallet.DriverBalance(ctx, d.ID)
		return err == nil && bal == trip.CancelFeeVND
	})

	// Sổ cái kép: khách bị ghi nợ đúng bằng số tài xế được ghi có.
	from := clk.Now().Add(-time.Hour)
	entries, err := a.Wallet.Statement(ctx, riderID, from, clk.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var riderTotal money.VND
	for _, e := range entries {
		if e.RefType == wallet.RefCancelFee {
			riderTotal += e.Amount
		}
	}
	if riderTotal != -trip.CancelFeeVND {
		t.Fatalf("khách phải bị ghi nợ %d, thực tế %d", -trip.CancelFeeVND, riderTotal)
	}

	// Tài xế phải được trả về IDLE để nhận chuyến tiếp.
	eventually(t, "tài xế quay lại IDLE sau khi chuyến bị huỷ", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.Status == driver.StatusIdle
	})
}

// TestEarlyCancelIsFree: huỷ trong cửa sổ miễn phí thì không ai bị ghi sổ.
func TestEarlyCancelIsFree(t *testing.T) {
	ctx := context.Background()
	a, clk := newMockClockApp(t)
	a.StartWorkers(ctx)

	d := seedDriverAt(t, a, clk, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

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
	offers, _ := waitForOffers(t, a, d.ID)
	if len(offers) != 1 {
		t.Fatalf("cần 1 lời mời, được %d", len(offers))
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}

	clk.Advance(trip.FreeCancelWindow - time.Second) // vẫn trong cửa sổ
	if _, err := a.Trips.Cancel(ctx, trip.CancelInput{
		TripID: tr.ID, By: trip.CancelByRider, Actor: riderID,
	}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "tài xế quay lại IDLE", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.Status == driver.StatusIdle
	})
	if bal, _ := a.Wallet.DriverBalance(ctx, d.ID); bal != 0 {
		t.Fatalf("huỷ trong cửa sổ miễn phí không được ghi sổ, ví = %d", bal)
	}
}

// seedDriverAt giống seedDriver nhưng dùng đồng hồ tiêm vào cho mốc thời gian ping.
func seedDriverAt(t *testing.T, a *App, clk *clock.Mock, phone, name, plate string) *driver.Driver {
	t.Helper()
	ctx := context.Background()
	accID := login(t, a, phone, authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: accID, FullName: name, Phone: phone, City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: plate},
		Documents: driver.Documents{NationalID: "079", DriverLicense: "790"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.ReviewKYC(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Location.Ingest(ctx, location.Ping{
		DriverID: d.ID, Point: nearby, AccuracyM: 10, BatteryPc: 90, At: clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return d
}
