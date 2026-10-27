package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/refund"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

const (
	opsA = "acc_ops_a"
	opsB = "acc_ops_b"
)

// completedTrip chạy một chuyến tiền mặt tới khi đã ghi sổ (PAID).
func completedTrip(t *testing.T, a *App) (*trip.Trip, string) {
	t.Helper()
	ctx := context.Background()
	a.StartWorkers(ctx)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	tr := runTripToCompletion(t, a, riderID, d)
	eventually(t, "chuyến đã ghi sổ", func() bool {
		cur, err := a.Trips.Get(ctx, tr.ID)
		return err == nil && cur.Status == trip.StatusPaid
	})
	tr, _ = a.Trips.Get(ctx, tr.ID)
	return tr, riderID
}

func riderBal(t *testing.T, a *App, riderID string) money.VND {
	t.Helper()
	b, err := a.Wallet.RiderBalance(context.Background(), riderID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Khoản nhỏ: người xin tự duyệt, tiền vào ví khách ngay, nền tảng chịu.
func TestSmallRefundIsSelfApprovedAndCredited(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	tr, riderID := completedTrip(t, a)

	r, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: 5000, Reason: "giá hiển thị sai trên app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != refund.StatusApproved || !r.SelfApproved || !r.Posted || r.ChargeTo != wallet.ChargePlatform {
		t.Fatalf("khoản dưới ngưỡng phải tự duyệt, đã ghi sổ, nền tảng chịu: %+v", r)
	}
	if got := riderBal(t, a, riderID); got != 5000 {
		t.Fatalf("ví khách phải có 5.000đ, có %d", got)
	}
	entries, _ := a.Admin.Audit(ctx, admin.AuditFilter{TargetID: r.ID})
	if len(entries) != 1 || entries[0].Action != refund.ActionRequest {
		t.Fatalf("phải có đúng một dòng nhật ký request_refund: %+v", entries)
	}
}

// Khoản lớn: chờ người thứ hai; chính người xin không duyệt được; duyệt hai
// lần không hoàn hai lần.
func TestLargeRefundNeedsSecondApprover(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	tr, riderID := completedTrip(t, a)
	a.Refunds.UseThreshold(func(context.Context) money.VND { return 1000 })

	r, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: 8000, Reason: "tài xế đi đường vòng",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != refund.StatusPending || riderBal(t, a, riderID) != 0 {
		t.Fatalf("khoản trên ngưỡng phải chờ duyệt và chưa vào ví: %+v", r)
	}
	if _, err := a.Refunds.Approve(ctx, opsA, r.ID, ""); errs.CodeOf(err) != "second_approver_required" {
		t.Fatalf("người xin tự duyệt khoản lớn phải bị chặn, được %v", err)
	}
	for i := 0; i < 2; i++ { // lần hai: bấm lại không được hoàn thêm
		if _, err := a.Refunds.Approve(ctx, opsB, r.ID, "đã nghe ghi âm cuộc gọi"); err != nil {
			t.Fatal(err)
		}
	}
	if got := riderBal(t, a, riderID); got != 8000 {
		t.Fatalf("duyệt (kể cả bấm hai lần) phải hoàn đúng 8.000đ, ví có %d", got)
	}
	if _, err := a.Refunds.Reject(ctx, opsB, r.ID, "đổi ý sau khi duyệt"); errs.CodeOf(err) != "refund_already_decided" {
		t.Fatalf("đã duyệt thì không từ chối được nữa, được %v", err)
	}
}

// Không bao giờ hoàn quá số khách đã trả, kể cả cộng dồn nhiều lần; khoản bị
// từ chối không chiếm hạn mức.
func TestRefundCannotExceedFare(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	tr, _ := completedTrip(t, a)
	a.Refunds.UseThreshold(func(context.Context) money.VND { return 0 })
	half := tr.Fare / 2

	if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: tr.Fare + 1, Reason: "hoàn nhiều hơn cước",
	}); errs.CodeOf(err) != "refund_exceeds_paid" {
		t.Fatalf("hoàn quá cước phải bị chặn, được %v", err)
	}
	r1, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{TripID: tr.ID, Amount: half, Reason: "lần một"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: tr.Fare - half + 1, Reason: "lần hai vượt",
	}); errs.CodeOf(err) != "refund_exceeds_paid" {
		t.Fatalf("cộng dồn vượt cước phải bị chặn — kể cả khi lần một còn chờ duyệt: %v", err)
	}
	if _, err := a.Refunds.Reject(ctx, opsB, r1.ID, "không đủ căn cứ"); err != nil {
		t.Fatal(err)
	}
	full, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: tr.Fare, Reason: "lần một bị từ chối nên còn đủ",
	})
	if err != nil {
		t.Fatalf("khoản bị từ chối không được chiếm hạn mức: %v", err)
	}
	if _, err := a.Refunds.Reject(ctx, opsB, full.ID, ""); errs.CodeOf(err) != "reason_required" {
		t.Fatalf("từ chối phải có lý do: %v", err)
	}
}

// Chuyến bị huỷ có thu phí: trần hoàn là phí huỷ, không phải cước.
func TestRefundCancelFee(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	// Không có cửa sổ miễn phí: huỷ ngay sau khi ghép là mất phí.
	a.Trips.UseCancelPolicy(func(context.Context) trip.CancelPolicy {
		return trip.CancelPolicy{FreeWindow: 0, FeeVND: 12000}
	})
	riderID := login(t, a, "0901234567", authn.RoleRider)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	tr := seedTrip(t, a, riderID)
	offers, _ := waitForOffers(t, a, d.ID)
	if len(offers) == 0 {
		t.Fatal("cần lời mời")
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // qua hẳn cửa sổ miễn phí 0 giây
	if _, err := a.Trips.Cancel(ctx, trip.CancelInput{TripID: tr.ID, By: trip.CancelByRider, Actor: riderID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: 12001, Reason: "hoàn quá phí huỷ",
	}); errs.CodeOf(err) != "refund_exceeds_paid" {
		t.Fatalf("trần hoàn của chuyến huỷ phải là phí huỷ, được %v", err)
	}
	eventually(t, "phí huỷ đã ghi sổ", func() bool { return riderBal(t, a, riderID) == -12000 })
	if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: 12000, Reason: "tài xế không tới đón", ChargeTo: wallet.ChargeDriver,
	}); err != nil {
		t.Fatal(err)
	}
	if got := riderBal(t, a, riderID); got != 0 {
		t.Fatalf("hoàn trọn phí huỷ thì ví khách về 0, đang %d", got)
	}
}

// Trừ lại từ tài xế: ví tài xế giảm đúng khoản hoàn, sổ cái vẫn cân.
func TestRefundChargedToDriver(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	tr, riderID := completedTrip(t, a)
	before, _ := a.Wallet.DriverBalance(ctx, *tr.DriverID)
	if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: 4000, Reason: "tài xế thu thêm tiền mặt", ChargeTo: wallet.ChargeDriver,
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := a.Wallet.DriverBalance(ctx, *tr.DriverID)
	if before-after != 4000 || riderBal(t, a, riderID) != 4000 {
		t.Fatalf("tài xế phải bị trừ 4.000đ (%d → %d), khách được 4.000đ", before, after)
	}
}

// Chuyến chưa xong thì không có gì để hoàn.
func TestRefundRejectsUnfinishedTrip(t *testing.T) {
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	tr := seedTrip(t, a, riderID)
	if _, err := a.Refunds.Request(context.Background(), opsA, refund.RequestInput{
		TripID: tr.ID, Amount: 1000, Reason: "chuyến còn đang tìm xe",
	}); errs.CodeOf(err) != "trip_not_refundable" {
		t.Fatalf("chuyến chưa xong phải bị từ chối, được %v", err)
	}
}

// Tiền hoàn phải TIÊU ĐƯỢC: trước T-36, chuyến trả bằng ví vẫn ghi sổ như trả
// qua cổng thanh toán — ví khách không bao giờ bị trừ, nên hoàn về ví là vô nghĩa.
func TestRefundedBalancePaysNextTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	tr, riderID := completedTrip(t, a)
	d, _ := a.Drivers.Get(ctx, *tr.DriverID)

	book := func() (*trip.Trip, error) {
		return a.Trips.Create(ctx, trip.CreateInput{
			RiderID: riderID, QuoteID: mustQuote(t, a).ID,
			Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
			PaymentMethod: trip.PayWallet,
		})
	}
	if _, err := book(); errs.CodeOf(err) != "wallet_insufficient" {
		t.Fatalf("ví trống phải bị từ chối khi đặt, được %v", err)
	}
	if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
		TripID: tr.ID, Amount: tr.Fare, Reason: "hoàn trọn chuyến trước",
	}); err != nil {
		t.Fatal(err)
	}
	a.Refunds.UseThreshold(nil)

	eventually(t, "tài xế rảnh", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.Status == "IDLE"
	})
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, nearby)); err != nil {
		t.Fatal(err)
	}
	next, err := book()
	if err != nil {
		t.Fatalf("ví đủ tiền phải đặt được: %v", err)
	}
	offers, _ := waitForOffers(t, a, d.ID)
	if len(offers) == 0 {
		t.Fatal("cần lời mời")
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}
	driverBefore, _ := a.Wallet.DriverBalance(ctx, d.ID)
	for _, step := range []func(context.Context, string, string) (*trip.Trip, error){
		a.Trips.MarkArrived, a.Trips.Start, a.Trips.Complete,
	} {
		if _, err := step(ctx, next.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "chuyến trả bằng ví đã ghi sổ", func() bool {
		cur, _ := a.Trips.Get(ctx, next.ID)
		return cur.Status == trip.StatusPaid
	})
	if got := riderBal(t, a, riderID); got != tr.Fare-next.Fare {
		t.Fatalf("ví khách phải còn %d, còn %d", tr.Fare-next.Fare, got)
	}
	driverAfter, _ := a.Wallet.DriverBalance(ctx, d.ID)
	if driverAfter-driverBefore != next.DriverEarn {
		t.Fatalf("tài xế phải được ghi có %d thu nhập, được %d", next.DriverEarn, driverAfter-driverBefore)
	}
}

// Hai vận hành bấm hoàn cùng một chuyến cùng lúc: tổng không được vượt cước.
// Chạy trên Postgres vì đó là nơi khoá dòng chuyến thật sự được thử.
func TestPostgresConcurrentRefundsCannotExceedFare(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)
	tr, _ := completedTrip(t, a)
	a.Refunds.UseThreshold(func(context.Context) money.VND { return 0 })

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{
				TripID: tr.ID, Amount: tr.Fare / 2, Reason: "bấm cùng lúc",
			}); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 2 {
		t.Fatalf("10 yêu cầu nửa cước cùng lúc: đúng 2 được nhận, được %d", ok)
	}

	// Chốt ở CSDL: khoản trên ngưỡng không thể bị chính người xin duyệt, kể
	// cả bằng một câu SQL chạy tay.
	var id string
	if err := db.QueryRow(`SELECT id FROM refunds WHERE trip_id=$1 LIMIT 1`, tr.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`UPDATE refunds SET status='APPROVED', decided_by=requested_by, decided_at=$2
        WHERE id=$1`, id, time.Now())
	if err == nil {
		t.Fatal("CSDL phải từ chối việc người xin tự duyệt khoản trên ngưỡng")
	}
}
