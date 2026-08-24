package location

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

var (
	benThanh = geo.Point{Lat: 10.7725, Lng: 106.6980}
	nearby   = geo.Point{Lat: 10.7740, Lng: 106.6995} // ~230m
)

// stubDrivers là DriverPort giả: location chỉ cần đọc loại xe và trạng thái.
type stubDrivers struct{ d *driver.Driver }

func (s stubDrivers) Get(context.Context, string) (*driver.Driver, error) { return s.d, nil }

func newTestService(t *testing.T, st driver.Status) (*Service, *clock.Mock) {
	t.Helper()
	clk := clock.NewMock(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC))
	d := &driver.Driver{
		ID: "drv_1", KYC: driver.KYCApproved, Status: st,
		Vehicle: driver.Vehicle{Type: driver.VehicleBike},
	}
	return NewService(NewMemoryIndex(clk), stubDrivers{d}, clk), clk
}

func ping(p geo.Point, at time.Time) Ping {
	return Ping{DriverID: "drv_1", Point: p, AccuracyM: 10, BatteryPc: 90, At: at}
}

// AC §5: ping Mocked=true bị gắn cờ và KHÔNG lọt vào chỉ mục.
func TestMockedPingRejectedAndFlagged(t *testing.T) {
	svc, clk := newTestService(t, driver.StatusIdle)
	ctx := context.Background()

	p := ping(nearby, clk.Now())
	p.Mocked = true
	err := svc.Ingest(ctx, p)
	if err == nil {
		t.Fatal("ping giả lập vị trí phải bị từ chối")
	}
	if got := errs.CodeOf(err); got != "mock_location" {
		t.Fatalf("mã lỗi phải là mock_location, được %q", got)
	}
	if svc.Fraud().Count("drv_1", time.Hour) != 1 {
		t.Fatal("phải gắn đúng 1 cờ gian lận")
	}
	if _, ok, _ := svc.Get(ctx, "drv_1"); ok {
		t.Fatal("ping bị từ chối KHÔNG được lọt vào chỉ mục")
	}
}

// TELEPORT: nhảy vị trí bất khả thi giữa hai ping liên tiếp.
func TestTeleportRejected(t *testing.T) {
	svc, clk := newTestService(t, driver.StatusIdle)
	ctx := context.Background()

	if err := svc.Ingest(ctx, ping(benThanh, clk.Now())); err != nil {
		t.Fatal(err)
	}
	// 1 giây sau xuất hiện cách đó ~11km => ~11.000 m/s.
	clk.Advance(time.Second)
	far := geo.Point{Lat: 10.8725, Lng: 106.6980}
	err := svc.Ingest(ctx, ping(far, clk.Now()))
	if got := errs.CodeOf(err); got != "implausible_jump" {
		t.Fatalf("phải trả implausible_jump, được %q (err=%v)", got, err)
	}
	if svc.Fraud().Count("drv_1", time.Hour) != 1 {
		t.Fatal("phải gắn cờ TELEPORT")
	}
}

// SPEED_OUTLIER: tốc độ tự khai vượt ngưỡng — gắn cờ nhưng VẪN nhận ping,
// vì cảm biến tốc độ GPS hay nhiễu và vị trí có thể vẫn đúng.
func TestSpeedOutlierFlaggedButAccepted(t *testing.T) {
	svc, clk := newTestService(t, driver.StatusIdle)
	ctx := context.Background()

	p := ping(nearby, clk.Now())
	p.SpeedMps = 90 // ~324 km/h
	if err := svc.Ingest(ctx, p); err != nil {
		t.Fatalf("tốc độ tự khai bất thường không được làm hỏng ping: %v", err)
	}
	if svc.Fraud().Count("drv_1", time.Hour) != 1 {
		t.Fatal("phải gắn cờ SPEED_OUTLIER")
	}
	if _, ok, _ := svc.Get(ctx, "drv_1"); !ok {
		t.Fatal("ping vẫn phải vào chỉ mục")
	}
}

// StaleAfter: tài xế mất mạng phải tự rơi khỏi tập ứng viên.
// Test này chỉ viết được sau khi MemoryIndex nhận clock tiêm vào.
func TestStaleDriverDropsOutOfIndex(t *testing.T) {
	svc, clk := newTestService(t, driver.StatusIdle)
	ctx := context.Background()

	if err := svc.Ingest(ctx, ping(nearby, clk.Now())); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Nearby(ctx, benThanh, 1000, Filter{}); len(got) != 1 {
		t.Fatalf("ping tươi phải thấy được, thấy %d", len(got))
	}

	clk.Advance(StaleAfter - time.Second)
	if got, _ := svc.Nearby(ctx, benThanh, 1000, Filter{}); len(got) != 1 {
		t.Fatalf("trong ngưỡng %v vẫn phải thấy, thấy %d", StaleAfter, len(got))
	}

	clk.Advance(2 * time.Second) // vượt StaleAfter
	if got, _ := svc.Nearby(ctx, benThanh, 1000, Filter{}); len(got) != 0 {
		t.Fatalf("quá %v thì phải rơi khỏi kết quả, còn %d", StaleAfter, len(got))
	}
}

// Tài xế OFFLINE / SUSPENDED bị gỡ khỏi chỉ mục thay vì cập nhật.
func TestOfflineDriverRemovedFromIndex(t *testing.T) {
	clk := clock.NewMock(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC))
	d := &driver.Driver{ID: "drv_1", Vehicle: driver.Vehicle{Type: driver.VehicleBike}, Status: driver.StatusIdle}
	svc := NewService(NewMemoryIndex(clk), stubDrivers{d}, clk)
	ctx := context.Background()

	if err := svc.Ingest(ctx, ping(nearby, clk.Now())); err != nil {
		t.Fatal(err)
	}
	d.Status = driver.StatusSuspended
	if err := svc.Ingest(ctx, ping(nearby, clk.Now())); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.Get(ctx, "drv_1"); ok {
		t.Fatal("tài xế bị khoá phải bị gỡ khỏi chỉ mục")
	}
}

// Toạ độ ngoài lãnh thổ VN bị loại ngay ở cửa.
func TestPointOutsideVietnamRejected(t *testing.T) {
	svc, clk := newTestService(t, driver.StatusIdle)
	err := svc.Ingest(context.Background(), ping(geo.Point{Lat: 48.85, Lng: 2.35}, clk.Now()))
	if got := errs.CodeOf(err); got != "point_out_of_range" {
		t.Fatalf("phải trả point_out_of_range, được %q", got)
	}
}
