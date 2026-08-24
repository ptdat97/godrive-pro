package app

import (
	"context"
	"testing"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/trip"
)

// Nghi vấn: trip.started và trip.completed chạy bất đồng bộ; nếu handler của
// completed xong TRƯỚC handler của started thì tài xế kẹt ở ON_TRIP.
// TestDriverStatusAfterBackToBackStartComplete là hồi quy cho một lỗi thật:
// trip.started và trip.completed chạy bất đồng bộ, và khi handler của completed
// xong TRƯỚC handler của started thì tài xế kẹt vĩnh viễn ở ON_TRIP — không
// nhận được chuyến mới, không có lỗi nào được ghi. Đo trước khi sửa: 2/20 lần.
func TestDriverStatusAfterBackToBackStartComplete(t *testing.T) {
	ctx := context.Background()
	stuck := 0
	const runs = 20
	for i := 0; i < runs; i++ {
		a := newTestApp(t)
		a.StartWorkers(ctx)
		d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
		riderID := login(t, a, "0901234567", authn.RoleRider)

		tr := seedTrip(t, a, riderID)
		offers, _ := waitForOffers(t, a, d.ID)
		if len(offers) == 0 {
			t.Fatal("cần lời mời")
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
		a.Bus.Close() // chờ mọi handler chạy xong
		cur, err := a.Drivers.Get(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Status != driver.StatusIdle {
			stuck++
			t.Logf("lần %d: tài xế kẹt ở %s sau khi chuyến hoàn tất", i, cur.Status)
		}
	}
	if stuck > 0 {
		t.Fatalf("%d/%d lần tài xế KHÔNG quay lại IDLE — không nhận chuyến mới được nữa", stuck, runs)
	}
}

// Đường thoát khi tài xế kẹt trạng thái: nếu KHÔNG còn chuyến nào đang chạy thì
// bật nhận chuyến lại phải được, không cần ai sửa tay trong CSDL.
func TestStuckDriverCanRecoverByGoingOnline(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")

	// Mô phỏng trạng thái kẹt: ON_TRIP nhưng không có chuyến nào.
	if err := a.Drivers.SetStatus(ctx, d.ID, driver.StatusOnTrip); err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatalf("không còn chuyến nào thì phải tự thoát được: %v", err)
	}
	cur, _ := a.Drivers.Get(ctx, d.ID)
	if cur.Status != driver.StatusIdle {
		t.Fatalf("phải về IDLE, đang %s", cur.Status)
	}
}

// Nhưng tài xế ĐANG thật sự chở khách thì không được tự bật lại.
func TestDriverWithActiveTripCannotForceOnline(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	riderID := login(t, a, "0901234567", authn.RoleRider)

	tr := seedTrip(t, a, riderID)
	offers, _ := waitForOffers(t, a, d.ID)
	if len(offers) == 0 {
		t.Fatal("cần lời mời")
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatal(err)
	}
	// Máy trạng thái bắt buộc ASSIGNED -> ARRIVED -> IN_PROGRESS.
	if _, err := a.Trips.MarkArrived(ctx, tr.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Trips.Start(ctx, tr.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.GoOnline(ctx, d.ID); err == nil {
		t.Fatal("đang chở khách thì không được tự bật nhận chuyến")
	}
}
