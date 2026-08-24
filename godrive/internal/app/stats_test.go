package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/geo"
)

// Tài xế mới phải bắt đầu ở mức trung tính, không phải 0.
//
// Nếu tính tỉ lệ thô, một tài xế nhận đúng một lời mời rồi bỏ lỡ sẽ có
// acceptance = 0 và không bao giờ được mời lần thứ hai để gỡ lại — một mẫu duy
// nhất khoá chết sự nghiệp của họ.
func TestNewDriverStartsNeutralNotZero(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài mới", "59X1-123.45")

	got, err := a.Drivers.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.AcceptanceRate(); r < 0.79 || r > 0.81 {
		t.Fatalf("tài xế mới phải ở mức trung tính ~0.8, đang %.3f", r)
	}
	if r := got.Rating(); r != 5.0 {
		t.Fatalf("tài xế mới phải là 5.00 sao, đang %.2f", r)
	}
	if got.CancelRate() != 0 {
		t.Fatalf("tài xế mới không có tỉ lệ huỷ, đang %.3f", got.CancelRate())
	}

	// Bỏ lỡ đúng MỘT lời mời không được đánh sập tỉ lệ.
	if err := a.Drivers.ApplyStats(ctx, d.ID, driver.StatsDelta{OffersReceived: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ = a.Drivers.Get(ctx, d.ID)
	if r := got.AcceptanceRate(); r < 0.7 {
		t.Fatalf("một lần bỏ lỡ không được kéo tỉ lệ xuống %.3f", r)
	}
}

// AC Giai đoạn 2: từ chối nhiều lời mời -> tỉ lệ nhận giảm -> rơi xuống cuối bảng.
//
// Trước GĐ 2, acceptance_rate không có dòng code nào ghi vào nên thành phần
// WeightAcceptance (trọng số lớn nhất, 90 điểm) là hằng số cộng đều cho mọi
// ứng viên — nghĩa là nó không phân biệt được ai với ai.
func TestPoorAcceptanceRateSinksDriverInRanking(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	// Hai tài xế: người hay bỏ chuyến ở GẦN hơn, người đáng tin ở XA hơn.
	flaky := seedDriver(t, a, "0912345671", "Hay bỏ chuyến", "59X1-111.11")
	solid := seedDriver(t, a, "0912345672", "Đáng tin", "59X1-222.22")

	// Khoảng cách chênh phải nằm trong tầm mà tỉ lệ nhận bù lại được.
	// WeightAcceptance = 90 điểm, quy đổi ~90 giây ETA ~ 330m ở tốc độ 18km/h
	// có hệ số uốn khúc 1.35. Chênh 200m (~54 giây) thì tỉ lệ nhận thắng;
	// chênh 600m thì không, và đó là hành vi ĐÚNG chứ không phải lỗi.
	near := geo.Point{Lat: pickup.Lat + 0.0018, Lng: pickup.Lng} // ~200m
	far := geo.Point{Lat: pickup.Lat + 0.0036, Lng: pickup.Lng}  // ~400m
	if err := a.Location.Ingest(ctx, locationPingAt(flaky.ID, near)); err != nil {
		t.Fatal(err)
	}
	if err := a.Location.Ingest(ctx, locationPingAt(solid.ID, far)); err != nil {
		t.Fatal(err)
	}

	// Khi cả hai còn "mới", người GẦN hơn phải được xếp trước.
	if first := rankFirst(t, a, riderID); first != flaky.ID {
		t.Fatalf("cả hai cùng chỉ số thì người gần hơn phải đứng đầu, được %s", first)
	}

	// Người gần từ chối 30 lời mời liên tiếp; người xa nhận đủ 30.
	if err := a.Drivers.ApplyStats(ctx, flaky.ID, driver.StatsDelta{OffersReceived: 30}); err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.ApplyStats(ctx, solid.ID, driver.StatsDelta{OffersReceived: 30, OffersAccepted: 30}); err != nil {
		t.Fatal(err)
	}

	fd, _ := a.Drivers.Get(ctx, flaky.ID)
	sd, _ := a.Drivers.Get(ctx, solid.ID)
	t.Logf("acceptance: hay-bỏ-chuyến=%.3f  đáng-tin=%.3f", fd.AcceptanceRate(), sd.AcceptanceRate())
	if fd.AcceptanceRate() >= sd.AcceptanceRate() {
		t.Fatalf("tỉ lệ nhận phải phân biệt được hai người: %.3f vs %.3f",
			fd.AcceptanceRate(), sd.AcceptanceRate())
	}

	// Giờ người XA mà đáng tin phải được ưu tiên hơn người GẦN mà hay bỏ chuyến.
	// Đây chính là lý lẽ của spec §3.2: tài xế gần nhưng hay bỏ chuyến làm khách
	// chờ lâu hơn tài xế xa mà luôn nhận.
	if first := rankFirst(t, a, riderID); first != solid.ID {
		t.Fatalf("tài xế đáng tin (dù xa hơn) phải đứng đầu, được %s", first)
	}
}

// Điểm đánh giá của khách phải thật sự thay đổi chỉ số tài xế.
func TestRatingFlowUpdatesDriverStats(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	tr := runTripToCompletion(t, a, riderID, d)

	// Chấm 1 sao.
	if _, err := a.Trips.Rate(ctx, tr.ID, riderID, 1); err != nil {
		t.Fatalf("Rate: %v", err)
	}
	eventually(t, "điểm đánh giá vào thống kê tài xế", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.Stats.RatingCount == 1 && cur.Stats.RatingSum == 1
	})
	cur, _ := a.Drivers.Get(ctx, d.ID)
	// Tiền nghiệm 5 lượt × 5 sao + 1 lượt × 1 sao = 26/6 ≈ 4.33.
	if r := cur.Rating(); r >= 5.0 || r < 4.0 {
		t.Fatalf("một lượt 1 sao phải kéo điểm xuống nhưng không sập, đang %.2f", r)
	}

	// Chấm lại lần hai phải bị từ chối — không cho sửa đánh giá.
	if _, err := a.Trips.Rate(ctx, tr.ID, riderID, 5); err == nil {
		t.Fatal("chuyến đã đánh giá thì không được chấm lại")
	}
}

func TestRateRejectsUnfinishedTripAndOutsiders(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	other := login(t, a, "0909999999", authn.RoleRider)

	tr := seedTrip(t, a, riderID)
	if _, err := a.Trips.Rate(ctx, tr.ID, riderID, 5); err == nil {
		t.Fatal("chuyến chưa kết thúc thì chưa đánh giá được")
	}
	if _, err := a.Trips.Rate(ctx, tr.ID, other, 5); err == nil {
		t.Fatal("người không phải khách của chuyến không được đánh giá")
	}
	for _, bad := range []int{0, 6, -1, 100} {
		if _, err := a.Trips.Rate(ctx, tr.ID, riderID, bad); err == nil {
			t.Fatalf("điểm %d phải bị từ chối", bad)
		}
	}
}

// completed_trips phải tăng theo chuyến thật, không đứng yên ở 0.
func TestCompletedTripsCounterAdvances(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	for i := 0; i < 3; i++ {
		runTripToCompletion(t, a, riderID, d)
	}
	eventually(t, "completed_trips lên 3", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.Stats.TripsCompleted == 3
	})
}

// IdleSince phải đo thời gian RẢNH, không phải độ cũ của ping vị trí.
func TestIdleSecondsMeasuresIdleNotPingAge(t *testing.T) {
	ctx := context.Background()
	a, clk := newMockClockApp(t)
	d := seedDriverAt(t, a, clk, "0912345678", "Tài", "59X1-123.45")

	cur, err := a.Drivers.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.IdleSince == nil {
		t.Fatal("tài xế vừa online phải có mốc bắt đầu rảnh")
	}
	if s := cur.IdleSeconds(clk.Now()); s != 0 {
		t.Fatalf("vừa online thì chưa chờ giây nào, đang %.0f", s)
	}

	// Trôi 10 phút, KHÔNG gửi ping mới: tài xế vẫn đang rảnh 10 phút.
	clk.Advance(10 * time.Minute)
	cur, _ = a.Drivers.Get(ctx, d.ID)
	if s := cur.IdleSeconds(clk.Now()); s < 599 || s > 601 {
		t.Fatalf("phải là ~600 giây rảnh, đang %.0f", s)
	}

	// Gửi ping mới KHÔNG được reset thời gian rảnh — ping chỉ là vị trí.
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, nearby)); err != nil {
		t.Fatal(err)
	}
	cur, _ = a.Drivers.Get(ctx, d.ID)
	if s := cur.IdleSeconds(clk.Now()); s < 599 {
		t.Fatalf("ping mới không được xoá thời gian đã chờ, đang %.0f", s)
	}

	// Nhận chuyến thì hết rảnh.
	if err := a.Drivers.Reserve(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	cur, _ = a.Drivers.Get(ctx, d.ID)
	if s := cur.IdleSeconds(clk.Now()); s != 0 {
		t.Fatalf("đang nhận chuyến thì không còn rảnh, đang %.0f", s)
	}
}

// AC §3.4: surge tăng theo mật độ cầu và KHÔNG BAO GIỜ vượt 2.0.
func TestSurgeRisesWithRealDemand(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	base, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if base.SurgePermille != pricing.MinSurgePermille {
		t.Fatalf("chưa có cầu thì surge phải là %d, được %d",
			pricing.MinSurgePermille, base.SurgePermille)
	}

	// Tạo nhiều chuyến ở cùng một ô lưới, không có tài xế nào -> cầu vượt cung.
	for i := 0; i < 12; i++ {
		seedTrip(t, a, riderID)
	}
	eventually(t, "bộ đếm cầu ghi nhận các chuyến", func() bool {
		return a.Surge.Cells() > 0
	})
	eventually(t, "surge tăng theo cầu", func() bool {
		q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
			VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
		})
		return err == nil && q.SurgePermille > pricing.MinSurgePermille
	})

	q, _ := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	t.Logf("sau %d yêu cầu: surge = %d‰ (×%.1f), giá %d -> %d",
		12, q.SurgePermille, q.SurgeMult, base.Total, q.Total)
	if q.SurgePermille > pricing.MaxSurgePermille {
		t.Fatalf("surge %d vượt trần %d", q.SurgePermille, pricing.MaxSurgePermille)
	}
	if q.Total <= base.Total {
		t.Fatalf("surge tăng thì giá phải tăng: %d -> %d", base.Total, q.Total)
	}
}

// rankFirst chạy một vòng dispatch với BatchSize = 1 và trả về tài xế duy nhất
// được mời — tức là người Engine chấm điểm tốt nhất.
//
// Đặt BatchSize = 1 thay vì chép lại công thức chấm điểm vào test: một bản sao
// công thức trong test sẽ vẫn xanh khi công thức thật đổi, đúng lúc cần nó đỏ.
func rankFirst(t *testing.T, a *App, riderID string) string {
	t.Helper()
	ctx := context.Background()
	tr := seedTrip(t, a, riderID)

	cfg := matching.DefaultConfig()
	cfg.BatchSize = 1
	cfg.OfferTTL = time.Minute
	m := matching.NewEngine(cfg, a.Location, a.Drivers, a.Trips,
		matching.NewMemoryStore(a.Clock), matching.NewSimpleETA(), a.Wallet, a.Bus, a.Clock)

	sent, err := m.DispatchRound(ctx, tr.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("BatchSize=1 phải gửi đúng 1 lời mời, gửi %d", sent)
	}
	ds, err := a.Drivers.ListByStatus(ctx, driver.StatusIdle, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if os, err := m.PendingOffers(ctx, d.ID); err == nil && len(os) > 0 {
			return d.ID
		}
	}
	t.Fatal("không tìm được tài xế nào có lời mời")
	return ""
}

// runTripToCompletion chạy trọn một chuyến từ tạo tới COMPLETED.
func runTripToCompletion(t *testing.T, a *App, riderID string, d *driver.Driver) *trip.Trip {
	t.Helper()
	ctx := context.Background()
	// Chờ worker của chuyến trước trả tài xế về IDLE. Không chờ thì ping tiếp
	// theo chụp lại trạng thái ON_TRIP vào chỉ mục, và dispatcher sẽ không thấy
	// ứng viên nào — triệu chứng giống hệt "không có tài xế quanh đây".
	eventually(t, "tài xế sẵn sàng cho chuyến kế tiếp", func() bool {
		cur, err := a.Drivers.Get(ctx, d.ID)
		return err == nil && cur.Status == driver.StatusIdle
	})
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, nearby)); err != nil {
		t.Fatal(err)
	}
	tr := seedTrip(t, a, riderID)
	offers, err := waitForOffers(t, a, d.ID)
	if err != nil || len(offers) == 0 {
		t.Fatalf("cần lời mời, được %d (err=%v)", len(offers), err)
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func(context.Context, string, string) (*trip.Trip, error){
		a.Trips.MarkArrived, a.Trips.Start, a.Trips.Complete,
	} {
		if _, err := step(ctx, tr.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	return tr
}
