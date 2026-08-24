package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

func mustQuote(t *testing.T, a *App) pricing.Quote {
	t.Helper()
	q, err := a.Pricing.Estimate(context.Background(), pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestIdempotencyKeyReleasedOnFailure(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	const key = "retry-key-1"

	if _, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: "qte_khong_ton_tai",
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash, IdempotencyKey: key,
	}); err == nil {
		t.Fatal("quote không tồn tại thì Create phải lỗi")
	}

	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuote(t, a).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("retry sau lỗi phải tạo được chuyến, bị: %s", errs.CodeOf(err))
	}
	if tr.Status != trip.StatusSearching {
		t.Fatalf("chuyến phải ở SEARCHING, đang %s", tr.Status)
	}
}

// Retry SAU KHI thành công phải trả về đúng chuyến cũ, không tạo chuyến thứ hai.
func TestIdempotentCreateReturnsSameTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	const key = "same-key"

	first, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuote(t, a).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := a.Trips.Create(ctx, trip.CreateInput{
			RiderID: riderID, QuoteID: mustQuote(t, a).ID,
			Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
			PaymentMethod: trip.PayCash, IdempotencyKey: key,
		})
		if err != nil {
			t.Fatalf("retry lần %d: %v", i, err)
		}
		if again.ID != first.ID {
			t.Fatalf("retry phải trả chuyến cũ %s, trả %s", first.ID, again.ID)
		}
	}
	all, err := a.Trips.ListByRider(ctx, riderID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("phải chỉ có 1 chuyến, có %d", len(all))
	}
}

// Nhiều thiết bị retry song song cùng một khoá: đúng một chuyến được tạo.
func TestConcurrentCreateSameKeyCreatesOneTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	q := mustQuote(t, a)

	const n = 10
	var wg sync.WaitGroup
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr, err := a.Trips.Create(ctx, trip.CreateInput{
				RiderID: riderID, QuoteID: q.ID,
				Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
				PaymentMethod: trip.PayCash, IdempotencyKey: "burst",
			})
			if err == nil {
				ids <- tr.ID
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) > 1 {
		t.Fatalf("cùng khoá idempotency phải cho cùng một chuyến, có %d chuyến khác nhau", len(seen))
	}
	all, _ := a.Trips.ListByRider(ctx, riderID, 50)
	if len(all) != 1 {
		t.Fatalf("kho dữ liệu phải chỉ có 1 chuyến, có %d", len(all))
	}
}

// Một offer chỉ được nhận một lần, kể cả khi bấm liên tiếp.
func TestAcceptSameOfferTwiceFails(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuote(t, a).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Matcher.DispatchRound(ctx, tr.ID, 0); err != nil {
		t.Fatal(err)
	}
	offers, _ := a.Matcher.PendingOffers(ctx, d.ID)
	if len(offers) != 1 {
		t.Fatalf("cần 1 lời mời, có %d", len(offers))
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}
	_, err = a.Matcher.Accept(ctx, offers[0].ID, d.ID)
	if err == nil {
		t.Fatal("nhận lại cùng một lời mời phải bị từ chối")
	}
	if got := errs.CodeOf(err); got != "offer_not_pending" {
		t.Fatalf("mã lỗi phải là offer_not_pending, được %q", got)
	}
}

// Tài xế của người khác không được nhận lời mời không dành cho mình.
func TestAcceptOfferOfAnotherDriverFails(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d1 := seedDriver(t, a, "0912345678", "Tài A", "59X1-111.11")
	d2 := seedDriver(t, a, "0987654321", "Tài B", "59X2-222.22")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	tr, _ := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuote(t, a).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if _, err := a.Matcher.DispatchRound(ctx, tr.ID, 0); err != nil {
		t.Fatal(err)
	}
	offers, _ := a.Matcher.PendingOffers(ctx, d1.ID)
	if len(offers) == 0 {
		t.Fatal("cần lời mời cho tài xế A")
	}
	_, err := a.Matcher.Accept(ctx, offers[0].ID, d2.ID)
	if got := errs.CodeOf(err); got != "not_your_offer" {
		t.Fatalf("phải trả not_your_offer, được %q", got)
	}
}

// Ghi sổ với tài khoản rỗng sẽ tạo bút toán vô chủ, không đối soát được.
func TestPostCancelFeeRejectsEmptyAccounts(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	if err := a.Wallet.PostCancelFee(ctx, "trp_1", "", "drv_1", 10000); err == nil {
		t.Fatal("riderID rỗng phải bị từ chối")
	}
	if err := a.Wallet.PostCancelFee(ctx, "trp_2", "acc_1", "", 10000); err == nil {
		t.Fatal("driverID rỗng phải bị từ chối")
	}
	// Phí bằng 0 hoặc âm thì bỏ qua, không phải lỗi.
	if err := a.Wallet.PostCancelFee(ctx, "trp_3", "acc_1", "drv_1", 0); err != nil {
		t.Fatalf("phí 0 phải là no-op: %v", err)
	}
}

// Nạp ví hai lần cùng mã tham chiếu chỉ được cộng tiền một lần.
func TestTopUpIsIdempotentByRef(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")

	for i := 0; i < 4; i++ {
		if err := a.Wallet.TopUp(ctx, d.ID, "momo-abc-123", 100000); err != nil {
			t.Fatal(err)
		}
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, d.ID); bal != 100000 {
		t.Fatalf("nạp 4 lần cùng mã phải chỉ cộng 1 lần: ví = %d", bal)
	}
	if err := a.Wallet.TopUp(ctx, d.ID, "momo-abc-124", -5000); err == nil {
		t.Fatal("nạp số tiền âm phải bị từ chối")
	}
}

// Tài xế tắt nhận chuyến rồi thì lời mời cũ không được dùng nữa.
func TestOfflineDriverCannotAcceptPendingOffer(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	tr, _ := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuote(t, a).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if _, err := a.Matcher.DispatchRound(ctx, tr.ID, 0); err != nil {
		t.Fatal(err)
	}
	offers, _ := a.Matcher.PendingOffers(ctx, d.ID)
	if len(offers) == 0 {
		t.Fatal("cần lời mời")
	}
	if err := a.Drivers.GoOffline(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err == nil {
		t.Fatal("tài xế đã tắt nhận chuyến không được nhận lời mời")
	}
	// Và chuyến phải vẫn đang chờ ghép, không bị treo ở ASSIGNED.
	cur, err := a.Trips.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != trip.StatusSearching {
		t.Fatalf("nhận thất bại thì chuyến phải còn SEARCHING, đang %s", cur.Status)
	}
}

// Báo giá hết hạn không được dùng để tạo chuyến.
func TestExpiredQuoteRejected(t *testing.T) {
	ctx := context.Background()
	a, clk := newMockClockApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	q := mustQuote(t, a)

	clk.Advance(pricing.QuoteTTL + time.Second)
	_, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if got := errs.CodeOf(err); got != "quote_expired" {
		t.Fatalf("phải trả quote_expired, được %q", got)
	}
}

// Số dư ví luôn phải bằng tổng bút toán — không có đường nào lệch.
func TestBalanceAlwaysEqualsSumOfEntries(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")

	for i := 0; i < 7; i++ {
		if err := a.Wallet.SettleTrip(ctx, "trp_"+string(rune('a'+i)), d.ID, 47000, 9400, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Wallet.TopUp(ctx, d.ID, "ref-1", 50000); err != nil {
		t.Fatal(err)
	}
	if err := a.Wallet.PostCancelFee(ctx, "trp_c", "acc_rider", d.ID, 10000); err != nil {
		t.Fatal(err)
	}

	bal, _ := a.Wallet.DriverBalance(ctx, d.ID)
	entries, err := a.Wallet.Statement(ctx, d.ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var sum money.VND
	for _, e := range entries {
		if e.AccountType == "DRIVER_WALLET" {
			sum += e.Amount
		}
	}
	if sum != bal {
		t.Fatalf("số dư %d không khớp tổng bút toán %d", bal, sum)
	}
	want := money.VND(-7*9400 + 50000 + 10000)
	if bal != want {
		t.Fatalf("số dư phải là %d, là %d", want, bal)
	}
}
