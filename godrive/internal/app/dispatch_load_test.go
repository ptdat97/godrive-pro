package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/geo"
)

// countingDrivers đếm số lần dispatcher tra hồ sơ tài xế — mỗi lần là một
// truy vấn CSDL ở chế độ Postgres.
type countingDrivers struct {
	matching.DriverPort
	gets *atomic.Int64
}

func (c countingDrivers) Get(ctx context.Context, id string) (*driver.Driver, error) {
	c.gets.Add(1)
	return c.DriverPort.Get(ctx, id)
}

// T-35 / G-20: số truy vấn của một vòng ghép phải bị chặn trên bởi
// MaxCandidates, KHÔNG tăng theo số tài xế trong bán kính.
//
// Trước bản sửa, mọi tài xế trong bán kính đều bị tra hồ sơ và số dư. Test tải
// với 5.000 tài xế: CSDL nghẽn, API chậm cả giây, broker MQTT quá hạn khi hỏi
// xác thực rồi ngắt kết nối thiết bị hàng loạt.
func TestDispatchLooksUpOnlyNearestCandidates(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	// 30 tài xế xếp thành hàng ra xa điểm đón, mỗi người cách nhau ~33m.
	ids := make([]string, 30)
	for i := range ids {
		d := seedDriver(t, a, fmt.Sprintf("09123450%02d", i), "Tài", fmt.Sprintf("59X1-1%02d.45", i))
		p := geo.Point{Lat: pickup.Lat + float64(i+1)*0.0003, Lng: pickup.Lng}
		if err := a.Location.Ingest(ctx, locationPingAt(d.ID, p)); err != nil {
			t.Fatal(err)
		}
		ids[i] = d.ID
	}
	nearest := map[string]bool{}
	for _, id := range ids[:5] {
		nearest[id] = true
	}

	run := func(maxCands int) (int64, []matching.Offer) {
		cfg := matching.DefaultConfig()
		cfg.BatchSize, cfg.MaxCandidates, cfg.OfferTTL = 3, maxCands, time.Minute
		var gets atomic.Int64
		store := matching.NewMemoryStore(a.Clock)
		m := matching.NewEngine(cfg, a.Location, countingDrivers{a.Drivers, &gets}, a.Trips,
			store, matching.NewSimpleETA(), a.Wallet, a.Bus, a.Clock)
		tr := seedTrip(t, a, riderID)
		if sent, err := m.DispatchRound(ctx, tr.ID, 0); err != nil || sent != 3 {
			t.Fatalf("phải mời đúng 3 người: sent=%d err=%v", sent, err)
		}
		var offers []matching.Offer
		for _, id := range ids {
			os, _ := m.PendingOffers(ctx, id)
			offers = append(offers, os...)
		}
		return gets.Load(), offers
	}

	gets, offers := run(5)
	if gets != 5 {
		t.Fatalf("MaxCandidates=5 thì chỉ được tra 5 hồ sơ, đã tra %d", gets)
	}
	for _, o := range offers {
		if !nearest[o.DriverID] {
			t.Fatalf("lời mời phải thuộc 5 người gần nhất, %s thì không", o.DriverID)
		}
	}
	// Đối chứng: không giới hạn thì tra cả 30 — chính là hành vi cũ.
	if gets, _ := run(0); gets != 30 {
		t.Fatalf("không giới hạn thì phải tra cả 30, đã tra %d", gets)
	}
}
