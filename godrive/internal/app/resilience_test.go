package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/geo"
)

// panicETA mô phỏng một implementation Port bị lỗi: nó panic thay vì trả lỗi.
type panicETA struct{}

func (panicETA) ETASeconds(context.Context, []geo.Point, geo.Point) ([]float64, error) {
	panic("ETAEngine hỏng")
}

// TestDispatchPanicDoesNotKillProcess: panic trong goroutine dispatch phải được
// bắt lại, và chuyến không được kẹt ở SEARCHING.
//
// Trước T-14, `go a.Matcher.Dispatch(...)` không có recover — một panic ở đây
// giết cả tiến trình, kéo theo sổ cái, offer và chỉ mục vị trí đang giữ trong RAM.
func TestDispatchPanicDoesNotKillProcess(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	// Thay ETAEngine bằng bản panic. Phải làm TRƯỚC StartWorkers để consumer
	// trip.requested dùng đúng matcher này.
	a.Matcher = matching.NewEngine(
		matching.DefaultConfig(), a.Location, a.Drivers, a.Trips,
		matching.NewMemoryStore(a.Clock), panicETA{}, a.Wallet, a.Bus, a.Clock,
	)
	a.StartWorkers(ctx)

	riderID := login(t, a, "0905550001", authn.RoleRider)
	drvAccID := login(t, a, "0915550002", authn.RoleDriver)

	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: drvAccID, FullName: "Tài xế", Phone: "+84915550002", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-999.99"},
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
	// Phải có ứng viên trong bán kính, nếu không candidates() không gọi tới ETAEngine.
	if err := a.Location.Ingest(ctx, location.Ping{
		DriverID: d.ID, Point: nearby, AccuracyM: 10, BatteryPc: 90, At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

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

	// Tiến trình còn sống tới đây đã là một nửa khẳng định. Nửa còn lại: chuyến
	// phải rời khỏi SEARCHING nhờ cleanup, chứ không nằm chờ mãi.
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := a.Trips.Get(ctx, tr.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == trip.StatusExpired {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("chuyến phải về EXPIRED sau panic, đang ở %s", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEventHandlerPanicIsContained: một consumer panic không được làm chết
// tiến trình, và cũng không được ngăn các consumer khác của cùng topic chạy.
func TestEventHandlerPanicIsContained(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	done := make(chan struct{})
	a.Bus.Subscribe("test.panic", func(context.Context, eventbus.Event) error {
		panic("consumer hỏng")
	})
	a.Bus.Subscribe("test.panic", func(context.Context, eventbus.Event) error {
		close(done)
		return nil
	})

	if err := a.Bus.Publish(ctx, "test.panic", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer thứ hai phải chạy dù consumer thứ nhất panic")
	}
}
