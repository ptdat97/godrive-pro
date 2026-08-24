package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/geo"
)

// fastMatcher dựng Engine với vòng chào mời rất ngắn để test chạy trong tíc tắc.
// Dispatch dùng time.After (thời gian thật) nên không rút ngắn được bằng đồng hồ giả.
func fastMatcher(a *App, ttl time.Duration, rounds int) *matching.Engine {
	cfg := matching.DefaultConfig()
	cfg.OfferTTL = ttl
	cfg.EmptyRoundWait = ttl
	cfg.MaxRounds = rounds
	return matching.NewEngine(cfg, a.Location, a.Drivers, a.Trips,
		matching.NewMemoryStore(a.Clock), matching.NewSimpleETA(), a.Wallet, a.Bus, a.Clock)
}

func seedTrip(t *testing.T, a *App, riderID string) *trip.Trip {
	t.Helper()
	ctx := context.Background()
	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuote(t, a).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// AC §3: không ai phản hồi -> nới bán kính qua đủ số vòng -> chuyến về EXPIRED.
//
// Đây là đường đi mà trước GĐ 2 chưa từng được test: một chuyến ở khu vực không
// có tài xế sẽ nằm mãi ở SEARCHING nếu vòng lặp hoặc bước Expire hỏng.
func TestOfferExpiryExpandsRadiusThenExpires(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t) // KHÔNG StartWorkers: test tự lái vòng dispatch
	riderID := login(t, a, "0901234567", authn.RoleRider)

	// Có tài xế, nhưng ở tận Hà Nội — ngoài mọi bán kính của chuyến ở TP.HCM.
	far := seedDriver(t, a, "0912345678", "Tài xa", "59X1-123.45")
	if err := a.Location.Ingest(ctx, locationPingAt(far.ID, geo.Point{Lat: 21.0278, Lng: 105.8342})); err != nil {
		t.Fatal(err)
	}

	tr := seedTrip(t, a, riderID)
	m := fastMatcher(a, 50*time.Millisecond, 3)

	// Từng vòng đều không tìm được ai.
	for round := 0; round < 3; round++ {
		sent, err := m.DispatchRound(ctx, tr.ID, round)
		if err != nil {
			t.Fatal(err)
		}
		if sent != 0 {
			t.Fatalf("vòng %d không được có ứng viên nào (tài xế ở Hà Nội), gửi %d lời mời", round, sent)
		}
	}

	if err := m.Dispatch(ctx, tr.ID); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got, err := a.Trips.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != trip.StatusExpired {
		t.Fatalf("hết vòng mà không ai nhận thì chuyến phải EXPIRED, đang %s", got.Status)
	}
}

// Bán kính phải NỚI RỘNG theo vòng, nếu không tài xế hơi xa sẽ không bao giờ
// được mời dù hệ thống còn nhiều vòng.
func TestDispatchWidensRadiusEachRound(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	// Cách điểm đón ~2.7km: ngoài bán kính vòng 0 (1500m), trong bán kính vòng 1 (3000m).
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	mid := geo.Point{Lat: pickup.Lat + 0.0245, Lng: pickup.Lng}
	if got := geo.DistanceM(pickup, mid); got < 1500 || got > 3000 {
		t.Fatalf("điểm test phải nằm giữa 1500m và 3000m, đang là %.0fm", got)
	}
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, mid)); err != nil {
		t.Fatal(err)
	}

	tr := seedTrip(t, a, riderID)
	m := fastMatcher(a, 50*time.Millisecond, 3)

	if sent, err := m.DispatchRound(ctx, tr.ID, 0); err != nil || sent != 0 {
		t.Fatalf("vòng 0 (bán kính 1500m) không được với tới tài xế: sent=%d err=%v", sent, err)
	}
	if sent, err := m.DispatchRound(ctx, tr.ID, 1); err != nil || sent != 1 {
		t.Fatalf("vòng 1 (bán kính 3000m) phải mời được tài xế: sent=%d err=%v", sent, err)
	}
}

// AC §3: chấm điểm phải TẤT ĐỊNH — cùng đầu vào luôn cho cùng thứ tự.
//
// Không tất định thì hai lần dispatch cùng một chuyến có thể chọn hai tài xế
// khác nhau, và mọi khiếu nại "vì sao chuyến này không vào tay tôi" đều không
// trả lời được.
func TestScoringDeterministic(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	// 6 tài xế rải quanh điểm đón, mỗi người một khoảng cách khác nhau.
	for i := 0; i < 6; i++ {
		d := seedDriver(t, a,
			[]string{"0912345671", "0912345672", "0912345673", "0912345674", "0912345675", "0912345676"}[i],
			"Tài", []string{"59X1-111.11", "59X1-222.22", "59X1-333.33", "59X1-444.44", "59X1-555.55", "59X1-666.66"}[i])
		p := geo.Point{Lat: pickup.Lat + float64(i)*0.001, Lng: pickup.Lng + float64(i)*0.0007}
		if err := a.Location.Ingest(ctx, locationPingAt(d.ID, p)); err != nil {
			t.Fatal(err)
		}
	}

	var first []string
	for run := 0; run < 30; run++ {
		tr := seedTrip(t, a, riderID)
		m := fastMatcher(a, time.Minute, 1)
		if _, err := m.DispatchRound(ctx, tr.ID, 0); err != nil {
			t.Fatal(err)
		}
		// Thu thứ tự tài xế được mời theo điểm.
		var order []string
		offers := allPendingOffers(t, a, m)
		for _, o := range offers {
			order = append(order, o.DriverID)
		}
		if run == 0 {
			first = order
			if len(first) != matching.DefaultConfig().BatchSize {
				t.Fatalf("phải mời đúng BatchSize=%d tài xế, mời %d",
					matching.DefaultConfig().BatchSize, len(first))
			}
			continue
		}
		if len(order) != len(first) {
			t.Fatalf("lần %d mời %d tài xế, lần đầu mời %d", run, len(order), len(first))
		}
		for i := range order {
			if order[i] != first[i] {
				t.Fatalf("lần %d thứ tự khác lần đầu tại vị trí %d: %s vs %s",
					run, i, order[i], first[i])
			}
		}
	}
}

// allPendingOffers gom lời mời của mọi tài xế, sắp theo điểm (thứ tự Engine đã chọn).
func allPendingOffers(t *testing.T, a *App, m *matching.Engine) []matching.Offer {
	t.Helper()
	ctx := context.Background()
	ds, err := a.Drivers.ListByStatus(ctx, driver.StatusIdle, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []matching.Offer
	for _, d := range ds {
		os, err := m.PendingOffers(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, os...)
	}
	// Engine gán Score nhưng không trả ra ngoài; sắp theo ETA rồi DriverID cho ổn định.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].ETASec < out[i].ETASec ||
				(out[j].ETASec == out[i].ETASec && out[j].DriverID < out[i].DriverID) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// locationPingAt dựng một ping hợp lệ tại điểm cho trước.
func locationPingAt(driverID string, p geo.Point) location.Ping {
	return location.Ping{DriverID: driverID, Point: p, AccuracyM: 10, BatteryPc: 90, At: time.Now().UTC()}
}
