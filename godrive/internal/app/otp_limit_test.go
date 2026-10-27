package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/identity"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/redisx"
	"github.com/example/godrive/pkg/errs"
)

// fakeNow là đồng hồ tay cho bộ giới hạn bộ nhớ.
type fakeNow struct{ t time.Time }

func (f *fakeNow) now() time.Time { return f.t }

// G-22: giới hạn theo SỐ ĐIỆN THOẠI, không theo IP — kẻ bơm SMS xoay IP.
func TestOTPLimitedPerPhone(t *testing.T) {
	a := newTestApp(t)
	clk := &fakeNow{t: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	a.Identity.UseOTPLimiter(identity.NewMemoryOTPLimiter(clk.now))
	ctx := context.Background()
	req := func(phone string, role authn.Role) error {
		_, _, err := a.Identity.RequestOTP(ctx, phone, role)
		return err
	}

	if err := req("0901234567", authn.RoleRider); err != nil {
		t.Fatal(err)
	}
	// Cùng một máy viết theo hai kiểu, và đổi vai — vẫn là cùng một người bị dội tin.
	for _, p := range []struct {
		phone string
		role  authn.Role
	}{{"+84901234567", authn.RoleRider}, {"0901234567", authn.RoleDriver}} {
		if err := req(p.phone, p.role); errs.CodeOf(err) != "otp_rate_limited" {
			t.Fatalf("%s/%s trong 60 giây phải bị chặn, được %v", p.phone, p.role, err)
		}
	}
	if err := req("0909999999", authn.RoleRider); err != nil {
		t.Fatalf("số khác không bị ảnh hưởng: %v", err)
	}

	// Cách 61 giây mỗi lần: được tới lần thứ 5 trong giờ, lần 6 bị chặn.
	for i := 2; i <= 5; i++ {
		clk.t = clk.t.Add(61 * time.Second)
		if err := req("0901234567", authn.RoleRider); err != nil {
			t.Fatalf("lần %d trong giờ phải được: %v", i, err)
		}
	}
	clk.t = clk.t.Add(61 * time.Second)
	if err := req("0901234567", authn.RoleRider); errs.CodeOf(err) != "otp_rate_limited" {
		t.Fatalf("lần thứ 6 trong một giờ phải bị chặn, được %v", err)
	}
	// Qua giờ thì được tiếp, nhưng cả ngày tối đa 10.
	sent := 5
	for h := 1; h < 24 && sent < 12; h++ {
		clk.t = clk.t.Add(time.Hour)
		if err := req("0901234567", authn.RoleRider); err == nil {
			sent++
		}
	}
	if sent != identity.OTPMaxPerDay {
		t.Fatalf("cả ngày tối đa %d lần, gửi được %d", identity.OTPMaxPerDay, sent)
	}
}

// Bản Redis: cùng luật, trên toàn cụm — hai app (hai pod) dùng chung một Redis
// thì lần gửi ở pod này chặn lần gửi ở pod kia.
func TestRedisOTPLimitAcrossPods(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	podA := identity.NewRedisOTPLimiter(rdb, redisx.KeyPrefix, newTestApp(t).Log)
	podB := identity.NewRedisOTPLimiter(rdb, redisx.KeyPrefix, newTestApp(t).Log)

	if wait, err := podA.Allow(ctx, "+84901234567"); err != nil || wait != 0 {
		t.Fatalf("lần đầu phải được: %v %v", wait, err)
	}
	wait, err := podB.Allow(ctx, "+84901234567")
	if err != nil || wait <= 0 || wait > identity.OTPCooldown {
		t.Fatalf("pod khác phải thấy thời gian chờ ≤ 60 giây, được %v %v", wait, err)
	}
	// Hết thời gian chờ (xoá khoá thay vì ngủ 60 giây), đếm theo giờ vẫn giữ.
	for i := 2; i <= identity.OTPMaxPerHour; i++ {
		rdb.Del(ctx, redisx.KeyPrefix+"otp:cd:+84901234567")
		if wait, _ := podB.Allow(ctx, "+84901234567"); wait != 0 {
			t.Fatalf("lần %d phải được", i)
		}
	}
	rdb.Del(ctx, redisx.KeyPrefix+"otp:cd:+84901234567")
	if wait, _ := podA.Allow(ctx, "+84901234567"); wait <= identity.OTPCooldown {
		t.Fatalf("lần thứ 6 trong giờ phải chờ tới hết giờ, chờ %v", wait)
	}
}
