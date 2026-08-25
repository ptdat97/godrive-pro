package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/settings"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

func putSetting(t *testing.T, a *App, k settings.Key, v any, version int, reason string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Settings.Put(context.Background(), k, raw, version, "acc_admin", reason); err != nil {
		t.Fatalf("Put %s: %v", k, err)
	}
}

// Đổi biểu giá phải có hiệu lực NGAY ở lần báo giá kế tiếp.
//
// Trước khi có cấu hình động, đổi một con số nghĩa là sửa code, biên dịch lại
// và triển khai lại — vận hành không tự làm được.
func TestChangingTariffAffectsNextQuote(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	before, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}

	g := settings.DefaultPricing()
	tf := g.Tariffs["BIKE"]
	tf.PerKm *= 2 // gấp đôi đơn giá mỗi km
	g.Tariffs["BIKE"] = tf
	putSetting(t, a, settings.KeyPricing, g, 0, "thử tăng giá")

	after, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Total <= before.Total {
		t.Fatalf("gấp đôi đơn giá km phải làm giá tăng: %d → %d", before.Total, after.Total)
	}
	t.Logf("giá BIKE: %d → %d sau khi gấp đôi đơn giá km", before.Total, after.Total)

	// Loại xe KHÁC không được ảnh hưởng.
	car, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleCar4, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if car.Total <= 0 {
		t.Fatal("CAR_4 vẫn phải báo giá được")
	}
}

// BÁO GIÁ ĐÃ PHÁT KHÔNG ĐỔI khi biểu giá đổi.
//
// Quote là một lời hứa với khách. Đổi biểu giá giữa lúc khách đang bấm đặt mà
// giá nhảy là cách nhanh nhất để mất niềm tin.
func TestExistingQuoteUnaffectedByTariffChange(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := q.Total

	g := settings.DefaultPricing()
	tf := g.Tariffs["BIKE"]
	tf.PerKm *= 3
	g.Tariffs["BIKE"] = tf
	putSetting(t, a, settings.KeyPricing, g, 0, "tăng mạnh")

	again, err := a.Pricing.GetQuote(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Total != original {
		t.Fatalf("báo giá đã phát KHÔNG được đổi: %d → %d", original, again.Total)
	}
}

// Đổi hạn mức công nợ phải chặn/mở tài xế ngay.
func TestChangingDebtLimitBlocksDriverImmediately(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")

	// Nợ 150.000đ — dưới hạn mức mặc định 200.000đ.
	for i := 0; i < 15; i++ {
		if err := a.Wallet.SettleTrip(ctx, "trp_d"+string(rune('a'+i)), d.ID, 50000, 10000, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Drivers.Reserve(ctx, d.ID); err != nil {
		t.Fatalf("nợ 150k dưới hạn mức 200k thì vẫn nhận được chuyến: %v", err)
	}
	if err := a.Drivers.SetStatus(ctx, d.ID, driver.StatusIdle); err != nil {
		t.Fatal(err)
	}

	// Siết hạn mức xuống 100.000đ.
	w := settings.DefaultWallet()
	w.DebtLimitVND = 100000
	putSetting(t, a, settings.KeyWallet, w, 0, "siết công nợ")

	err := a.Drivers.Reserve(ctx, d.ID)
	if errs.CodeOf(err) != "wallet_debt_exceeded" {
		t.Fatalf("siết hạn mức phải chặn ngay, được %q", errs.CodeOf(err))
	}

	// Nới lại thì mở ngay.
	w.DebtLimitVND = 500000
	putSetting(t, a, settings.KeyWallet, w, 1, "nới lại")
	if err := a.Drivers.Reserve(ctx, d.ID); err != nil {
		t.Fatalf("nới hạn mức phải mở lại ngay: %v", err)
	}
}

// Tắt surge phải làm hệ số về 1.0 ngay, không cần khởi động lại.
func TestDisablingSurgeTakesEffect(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	a.StartWorkers(ctx)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	for i := 0; i < 12; i++ {
		seedTrip(t, a, riderID)
	}
	eventually(t, "surge tăng theo cầu", func() bool {
		q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
			VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
		})
		return err == nil && q.SurgePermille > pricing.MinSurgePermille
	})

	g := settings.DefaultSurge()
	g.Enabled = false
	putSetting(t, a, settings.KeySurge, g, 0, "tạm tắt tăng giá")

	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.SurgePermille != pricing.MinSurgePermille {
		t.Fatalf("tắt surge thì hệ số phải về %d, đang là %d",
			pricing.MinSurgePermille, q.SurgePermille)
	}
}

// Đổi bán kính ghép chuyến phải có hiệu lực ở vòng dispatch kế tiếp.
func TestChangingMatchingRadiusTakesEffect(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	// Cách điểm đón ~2.7km: ngoài bán kính mặc định 1500m.
	far := geo.Point{Lat: pickup.Lat + 0.0245, Lng: pickup.Lng}
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, far)); err != nil {
		t.Fatal(err)
	}

	tr := seedTrip(t, a, riderID)
	if sent, err := a.Matcher.DispatchRound(ctx, tr.ID, 0); err != nil || sent != 0 {
		t.Fatalf("bán kính 1500m không được với tới tài xế: sent=%d err=%v", sent, err)
	}

	g := settings.DefaultMatching()
	g.InitialRadiusM = 4000
	putSetting(t, a, settings.KeyMatching, g, 0, "nới bán kính vùng ngoại ô")

	tr2 := seedTrip(t, a, riderID)
	sent, err := a.Matcher.DispatchRound(ctx, tr2.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("nới bán kính lên 4000m phải mời được tài xế, gửi %d lời mời", sent)
	}
}

// Mọi thay đổi cấu hình phải có dấu vết trong nhật ký thao tác.
func TestSettingChangeIsAudited(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	w := settings.DefaultWallet()
	w.TaxPermille = 45
	raw, _ := json.Marshal(w)
	before, _ := a.Settings.Get(ctx, settings.KeyWallet)
	rec, err := a.Settings.Put(ctx, settings.KeyWallet, raw, 0, "acc_ke_toan", "kế toán thuế đã duyệt")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Admin.RecordSettingChange(ctx, "acc_ke_toan", string(settings.KeyWallet),
		before.Value, rec.Value, "kế toán thuế đã duyệt"); err != nil {
		t.Fatal(err)
	}

	entries, err := a.Admin.Audit(ctx, admin.AuditFilter{TargetType: admin.TargetSettings})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("phải có 1 dòng nhật ký, có %d", len(entries))
	}
	e := entries[0]
	if e.Action != admin.ActionUpdateSettings || e.TargetID != "wallet" || e.ActorID != "acc_ke_toan" {
		t.Fatalf("nhật ký thiếu thông tin truy vết: %+v", e)
	}
	if e.Payload["reason"] != "kế toán thuế đã duyệt" {
		t.Fatalf("phải ghi lý do thay đổi: %+v", e.Payload)
	}
	if e.Payload["before"] == nil || e.Payload["after"] == nil {
		t.Fatal("nhật ký phải giữ cả giá trị trước và sau")
	}

	// Và thuế phải thật sự được áp dụng.
	if got := a.Settings.Current(ctx).Wallet.TaxPermille; got != 45 {
		t.Fatalf("thuế phải là 45‰, đang là %d", got)
	}
}
