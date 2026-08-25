package app

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/internal/platform/redisx"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/idem"
)

// Test Redis bật bằng biến RIÊNG: chúng XOÁ mọi khoá có tiền tố godrive:,
// nên không bao giờ được phép trỏ nhầm vào Redis thật.
//
//	TEST_REDIS_URL="redis://localhost:6379/15" go test ./internal/app/
const testRedisEnv = "TEST_REDIS_URL"

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv(testRedisEnv)
	if url == "" {
		t.Skipf("bỏ qua: đặt %s để chạy test tích hợp Redis", testRedisEnv)
	}
	c, err := redisx.New(url)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Dọn sạch không gian khoá của mình, không đụng khoá khác.
	ctx := context.Background()
	iter := c.Raw().Scan(ctx, 0, redisx.KeyPrefix+"*", 1000).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		c.Raw().Del(ctx, keys...)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c.Raw()
}

// BẤT BIẾN QUAN TRỌNG NHẤT: nhiều tài xế cùng bấm nhận, đúng một người thắng.
//
// Script Lua phải nguyên tử. SETNX rồi GET ở phía client sẽ hỏng vì khoá có thể
// hết hạn giữa hai lệnh.
func TestRedisClaimTripExactlyOneWinner(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	store := matching.NewRedisStore(rdb, redisx.KeyPrefix, clock.Real())

	const n = 64
	var wg sync.WaitGroup
	wins := make(chan string, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // thả cùng lúc để tranh chấp thật sự xảy ra
			did := "drv_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			won, err := store.ClaimTrip(ctx, "trp_race", did, 30*time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			if won {
				wins <- did
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(wins)

	var winners []string
	for w := range wins {
		winners = append(winners, w)
	}
	if len(winners) != 1 {
		t.Fatalf("đúng MỘT tài xế được thắng, có %d: %v", len(winners), winners)
	}

	// Chính chủ gọi lại vẫn thắng — app mobile retry trên 4G chập chờn không
	// được biến thành "chuyến đã có người khác nhận".
	for i := 0; i < 5; i++ {
		won, err := store.ClaimTrip(ctx, "trp_race", winners[0], 30*time.Second)
		if err != nil || !won {
			t.Fatalf("chính chủ retry lần %d phải thắng: won=%v err=%v", i, won, err)
		}
	}
	// Người khác thì không.
	if won, _ := store.ClaimTrip(ctx, "trp_race", "drv_khac", 30*time.Second); won {
		t.Fatal("người khác không được giành khoá đang có chủ")
	}
}

// Khoá hết hạn thì người sau giành được — nếu không, một pod chết giữa chừng
// sẽ khoá chuyến đó vĩnh viễn.
func TestRedisClaimExpires(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	store := matching.NewRedisStore(rdb, redisx.KeyPrefix, clock.Real())

	if won, _ := store.ClaimTrip(ctx, "trp_ttl", "drv_1", 150*time.Millisecond); !won {
		t.Fatal("lần giành đầu phải thắng")
	}
	if won, _ := store.ClaimTrip(ctx, "trp_ttl", "drv_2", time.Second); won {
		t.Fatal("khoá còn hạn thì người khác không giành được")
	}
	time.Sleep(250 * time.Millisecond)
	if won, _ := store.ClaimTrip(ctx, "trp_ttl", "drv_2", time.Second); !won {
		t.Fatal("khoá hết hạn thì người sau phải giành được")
	}
}

// Chỉ mục GEO phải lọc đúng bán kính và đủ các tiêu chí của Filter.
func TestRedisGeoIndexNearbyAndFilter(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	clk := clock.NewMock(mockBase())
	idx := location.NewRedisIndex(rdb, redisx.KeyPrefix, clk)

	put := func(id string, p geo.Point, vt driver.VehicleType, st driver.Status, batt int) {
		t.Helper()
		if err := idx.Upsert(ctx, location.Snapshot{
			DriverID: id, Point: p, VehicleType: vt, Status: st,
			BatteryPc: batt, UpdatedAt: clk.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	near := geo.Point{Lat: pickup.Lat + 0.0018, Lng: pickup.Lng} // ~200m
	mid := geo.Point{Lat: pickup.Lat + 0.0090, Lng: pickup.Lng}  // ~1000m
	far := geo.Point{Lat: pickup.Lat + 0.0900, Lng: pickup.Lng}  // ~10km

	put("drv_near", near, driver.VehicleBike, driver.StatusIdle, 90)
	put("drv_mid", mid, driver.VehicleBike, driver.StatusIdle, 90)
	put("drv_far", far, driver.VehicleBike, driver.StatusIdle, 90)
	put("drv_car", near, driver.VehicleCar4, driver.StatusIdle, 90)
	put("drv_busy", near, driver.VehicleBike, driver.StatusOnTrip, 90)
	put("drv_lowbat", near, driver.VehicleBike, driver.StatusIdle, 5)

	ids := func(ss []location.Snapshot) map[string]bool {
		m := map[string]bool{}
		for _, s := range ss {
			m[s.DriverID] = true
		}
		return m
	}

	// Bán kính 1500m: near + mid, không có far.
	got, err := idx.Nearby(ctx, pickup, 1500, location.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	g := ids(got)
	if !g["drv_near"] || !g["drv_mid"] || g["drv_far"] {
		t.Fatalf("lọc bán kính sai: %v", g)
	}

	// Lọc đủ 4 tiêu chí cùng lúc, đúng như dispatcher dùng.
	got, err = idx.Nearby(ctx, pickup, 1500, location.Filter{
		VehicleTypes: []driver.VehicleType{driver.VehicleBike},
		Statuses:     []driver.Status{driver.StatusIdle},
		MinBatteryPc: 15,
		FreshWithin:  location.StaleAfter,
	})
	if err != nil {
		t.Fatal(err)
	}
	g = ids(got)
	if g["drv_car"] || g["drv_busy"] || g["drv_lowbat"] {
		t.Fatalf("Filter chưa loại hết: %v", g)
	}
	if !g["drv_near"] || !g["drv_mid"] {
		t.Fatalf("Filter loại nhầm: %v", g)
	}

	// GEOSEARCH trả theo thứ tự gần dần.
	if len(got) >= 2 && got[0].DriverID != "drv_near" {
		t.Fatalf("phải sắp theo khoảng cách tăng dần, đầu tiên là %s", got[0].DriverID)
	}

	// Ping cũ hơn StaleAfter phải rơi khỏi kết quả.
	clk.Advance(location.StaleAfter + time.Second)
	got, _ = idx.Nearby(ctx, pickup, 1500, location.Filter{FreshWithin: location.StaleAfter})
	if len(got) != 0 {
		t.Fatalf("ping quá hạn độ tươi phải rơi hết, còn %d", len(got))
	}
}

func TestRedisGeoIndexGetAndRemove(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	clk := clock.NewMock(mockBase())
	idx := location.NewRedisIndex(rdb, redisx.KeyPrefix, clk)

	snap := location.Snapshot{
		DriverID: "drv_1", Point: pickup, BearingDeg: 45,
		VehicleType: driver.VehicleBike, Status: driver.StatusIdle,
		BatteryPc: 77, UpdatedAt: clk.Now(),
	}
	if err := idx.Upsert(ctx, snap); err != nil {
		t.Fatal(err)
	}
	got, ok, err := idx.Get(ctx, "drv_1")
	if err != nil || !ok {
		t.Fatalf("phải đọc lại được: ok=%v err=%v", ok, err)
	}
	if got.BatteryPc != 77 || got.BearingDeg != 45 || got.VehicleType != driver.VehicleBike {
		t.Fatalf("thuộc tính không đi trọn vòng ghi->đọc: %+v", got)
	}
	if !got.UpdatedAt.Equal(snap.UpdatedAt) {
		t.Fatalf("mốc thời gian lệch: %v vs %v", got.UpdatedAt, snap.UpdatedAt)
	}

	if err := idx.Remove(ctx, "drv_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := idx.Get(ctx, "drv_1"); ok {
		t.Fatal("đã xoá thì không được đọc ra nữa")
	}
	// Và phải rơi khỏi cả chỉ mục không gian, không chỉ khỏi bản ghi thuộc tính.
	if got, _ := idx.Nearby(ctx, pickup, 5000, location.Filter{}); len(got) != 0 {
		t.Fatalf("phần tử phải bị gỡ khỏi ZSET, còn %d", len(got))
	}
}

// Khoá idempotency phải nguyên tử trên toàn cụm.
func TestRedisIdempotencyReserveIsAtomic(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	store := idem.NewRedisStore(rdb, redisx.KeyPrefix)

	const n = 40
	var wg sync.WaitGroup
	firsts := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, existed, err := store.Reserve(ctx, "k1", time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			firsts <- !existed
		}()
	}
	wg.Wait()
	close(firsts)
	won := 0
	for f := range firsts {
		if f {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("đúng MỘT lời gọi được coi là lần đầu, có %d", won)
	}

	// Complete rồi thì Reserve trả về phản hồi cũ.
	if err := store.Complete(ctx, "k1", []byte("trp_123")); err != nil {
		t.Fatal(err)
	}
	rec, existed, err := store.Reserve(ctx, "k1", time.Minute)
	if err != nil || !existed || string(rec.Response) != "trp_123" {
		t.Fatalf("phải trả lại phản hồi đã hoàn tất: existed=%v rec=%+v err=%v", existed, rec, err)
	}

	// Release KHÔNG được xoá khoá đã hoàn tất.
	if err := store.Release(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	rec, existed, _ = store.Reserve(ctx, "k1", time.Minute)
	if !existed || string(rec.Response) != "trp_123" {
		t.Fatal("Release không được xoá khoá đã Complete")
	}

	// Nhưng Release PHẢI nhả khoá chưa hoàn tất.
	if _, existed, _ = store.Reserve(ctx, "k2", time.Minute); existed {
		t.Fatal("k2 phải là lần đầu")
	}
	if err := store.Release(ctx, "k2"); err != nil {
		t.Fatal(err)
	}
	if _, existed, _ = store.Reserve(ctx, "k2", time.Minute); existed {
		t.Fatal("Release phải nhả được khoá chưa hoàn tất")
	}
}

func TestRedisQuoteStoreRoundTrip(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	store := pricing.NewRedisQuoteStore(rdb, redisx.KeyPrefix)

	q := pricing.Quote{
		ID: "qte_1", VehicleType: driver.VehicleBike,
		Pickup: pickup, Dropoff: dropoff,
		DistanceM: 4740, DurationS: 776,
		BaseFare: 26000, SurgePermille: 1400, SurgeMult: 1.4,
		Total: 37000, PlatformFee: 7400, DriverEarn: 29600,
		ExpiresAt: time.Now().Add(pricing.QuoteTTL).UTC(),
	}
	if err := store.Save(ctx, q); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "qte_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != q.Total || got.SurgePermille != q.SurgePermille || got.DriverEarn != q.DriverEarn {
		t.Fatalf("báo giá không đi trọn vòng: %+v", got)
	}
	if _, err := store.Get(ctx, "qte_khong_co"); err == nil {
		t.Fatal("báo giá không tồn tại phải trả lỗi")
	}
}

// Rate limit toàn cụm: hai "pod" dùng chung một hạn mức.
func TestRedisRateLimitIsSharedAcrossProcesses(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	// 1 token/giây, burst 5 — hai instance mô phỏng hai pod.
	podA := httpx.NewRedisRateLimit(rdb, redisx.KeyPrefix, 1, 5)
	podB := httpx.NewRedisRateLimit(rdb, redisx.KeyPrefix, 1, 5)

	allowed := 0
	for i := 0; i < 10; i++ {
		rl := podA
		if i%2 == 1 {
			rl = podB
		}
		ok, err := rl.Allow(ctx, "1.2.3.4")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("hai pod phải dùng CHUNG hạn mức burst 5, cho qua %d", allowed)
	}
	// IP khác có bucket riêng.
	if ok, _ := podA.Allow(ctx, "5.6.7.8"); !ok {
		t.Fatal("IP khác phải có bucket riêng")
	}
}

// newReplica dựng một App riêng biệt trỏ vào CÙNG Postgres và CÙNG Redis —
// mô phỏng một pod thứ hai.
func newReplica(t *testing.T) *App {
	t.Helper()
	if os.Getenv(testDBEnv) == "" || os.Getenv(testRedisEnv) == "" {
		t.Skipf("cần cả %s và %s", testDBEnv, testRedisEnv)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = "test"
	cfg.DatabaseURL = os.Getenv(testDBEnv)
	cfg.RedisURL = os.Getenv(testRedisEnv)
	cfg.DevAuth = true

	a, err := New(cfg, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestTwoReplicasShareState là ĐIỀU KIỆN HOÀN THÀNH của Giai đoạn 3.
//
// Trước GĐ 3, năm loại dữ liệu nóng nằm trong bộ nhớ tiến trình, nên hai pod
// thấy hai thế giới khác nhau: báo giá phát ở pod A không đặt được chuyến ở
// pod B, và chống ghép trùng chỉ đúng trong phạm vi một tiến trình.
func TestTwoReplicasShareState(t *testing.T) {
	ctx := context.Background()
	// Dọn cả Postgres lẫn Redis trước khi bắt đầu.
	podA, db := newPostgresApp(t)
	testRedis(t)
	podA = newReplica(t)
	podB := newReplica(t)
	_ = db

	riderID := login(t, podA, "0901234567", authn.RoleRider)

	// --- 1. Báo giá phát ở pod A, đặt chuyến ở pod B ---
	q, err := podA.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := podB.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatalf("báo giá của pod A phải dùng được ở pod B: %v", err)
	}

	// --- 2. Idempotency dùng chung: cùng khoá ở hai pod chỉ tạo một chuyến ---
	const key = "shared-key"
	t1, err := podA.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuoteOn(t, podA).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := podB.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: mustQuoteOn(t, podB).ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("pod B với cùng Idempotency-Key phải trả về chuyến cũ: %v", err)
	}
	if t1.ID != t2.ID {
		t.Fatalf("cùng khoá idempotency ở hai pod phải cho cùng một chuyến: %s vs %s", t1.ID, t2.ID)
	}

	// --- 3. Ping ở pod A, dispatcher ở pod B nhìn thấy ---
	d := seedDriver(t, podA, "0912345678", "Tài", "59X1-123.45")
	if err := podA.Location.Ingest(ctx, locationPingAt(d.ID, nearby)); err != nil {
		t.Fatal(err)
	}
	snaps, err := podB.Location.Nearby(ctx, pickup, 1500, location.Filter{
		Statuses: []driver.Status{driver.StatusIdle}, FreshWithin: location.StaleAfter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].DriverID != d.ID {
		t.Fatalf("pod B phải thấy tài xế mà pod A vừa nhận ping: %+v", snaps)
	}

	// --- 4. Chống ghép trùng XUYÊN pod ---
	sent, err := podA.Matcher.DispatchRound(ctx, tr.ID, 0)
	if err != nil || sent == 0 {
		t.Fatalf("pod A phải gửi được lời mời: sent=%d err=%v", sent, err)
	}
	offers, err := podB.Matcher.PendingOffers(ctx, d.ID)
	if err != nil || len(offers) == 0 {
		t.Fatalf("pod B phải đọc được lời mời pod A vừa tạo: %d, %v", len(offers), err)
	}

	// Hai pod cùng nhận một lời mời: đúng một bên thắng.
	type res struct{ err error }
	out := make(chan res, 2)
	for _, p := range []*App{podA, podB} {
		go func(p *App) {
			_, err := p.Matcher.Accept(ctx, offers[0].ID, d.ID)
			out <- res{err}
		}(p)
	}
	wins, losses := 0, 0
	for i := 0; i < 2; i++ {
		if (<-out).err == nil {
			wins++
		} else {
			losses++
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("hai pod cùng nhận một lời mời: phải 1 thắng 1 thua, được %d/%d", wins, losses)
	}
}

func mustQuoteOn(t *testing.T, a *App) pricing.Quote {
	t.Helper()
	q, err := a.Pricing.Estimate(context.Background(), pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q
}
