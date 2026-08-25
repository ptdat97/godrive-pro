package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/redisx"
	"github.com/example/godrive/pkg/errs"
)

// tokenFor đăng nhập và trả về cả token lẫn claims.
func tokenFor(t *testing.T, a *App, phone string, role authn.Role) (string, *authn.Claims) {
	t.Helper()
	ctx := context.Background()
	cid, code, err := a.Identity.RequestOTP(ctx, phone, role)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := a.Identity.VerifyOTP(ctx, cid, code, "thiet-bi-1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Issuer.Parse(tp.AccessToken, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return tp.AccessToken, c
}

// Mọi token phát ra phải có JTI — không có nó thì không thu hồi riêng được.
func TestTokenHasJTI(t *testing.T) {
	a := newTestApp(t)
	_, c := tokenFor(t, a, "0901234567", authn.RoleRider)
	if c.JTI == "" {
		t.Fatal("token phải mang JTI")
	}
	_, c2 := tokenFor(t, a, "0901234567", authn.RoleRider)
	if c.JTI == c2.JTI {
		t.Fatal("hai token phải có JTI khác nhau")
	}
}

// Thu hồi MỘT token: token đó hết hiệu lực, token khác của cùng người vẫn dùng được.
func TestRevokeSingleToken(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	a := newTestApp(t)
	rev := authn.NewRedisRevoker(rdb, redisx.KeyPrefix)
	a.Issuer.UseRevoker(rev)

	_, c1 := tokenFor(t, a, "0901234567", authn.RoleRider)
	_, c2 := tokenFor(t, a, "0901234567", authn.RoleRider)

	if ok, _ := rev.IsRevoked(ctx, c1); ok {
		t.Fatal("token mới không được coi là đã thu hồi")
	}
	if err := rev.RevokeToken(ctx, c1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if ok, err := rev.IsRevoked(ctx, c1); err != nil || !ok {
		t.Fatalf("token đã thu hồi phải bị chặn: ok=%v err=%v", ok, err)
	}
	if ok, _ := rev.IsRevoked(ctx, c2); ok {
		t.Fatal("thu hồi một token KHÔNG được ảnh hưởng token khác")
	}
}

// Thu hồi theo TÀI KHOẢN: mọi token phát trước thời điểm đó đều hết hiệu lực.
//
// Đây mới là đường quan trọng: khi cần chặn một người ngay, ta không biết họ
// đang giữ bao nhiêu token trên bao nhiêu thiết bị.
func TestRevokeAllTokensOfAccount(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	a := newTestApp(t)
	rev := authn.NewRedisRevoker(rdb, redisx.KeyPrefix)
	rev.Skew = 0
	a.Issuer.UseRevoker(rev)

	_, c1 := tokenFor(t, a, "0901234567", authn.RoleRider)
	_, c2 := tokenFor(t, a, "0901234567", authn.RoleRider)
	_, other := tokenFor(t, a, "0909999999", authn.RoleRider)

	cutoff := time.Now().UTC().Add(time.Second)
	if err := rev.RevokeAccount(ctx, c1.Sub, cutoff, time.Hour); err != nil {
		t.Fatal(err)
	}
	for i, c := range []*authn.Claims{c1, c2} {
		if ok, err := rev.IsRevoked(ctx, c); err != nil || !ok {
			t.Fatalf("token %d của tài khoản bị thu hồi phải bị chặn: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, _ := rev.IsRevoked(ctx, other); ok {
		t.Fatal("tài khoản khác KHÔNG được ảnh hưởng")
	}

	// Token phát SAU mốc thu hồi thì vẫn hợp lệ — đăng nhập lại phải dùng được.
	time.Sleep(1100 * time.Millisecond)
	_, fresh := tokenFor(t, a, "0901234567", authn.RoleRider)
	if ok, err := rev.IsRevoked(ctx, fresh); err != nil || ok {
		t.Fatalf("token phát sau mốc thu hồi phải hợp lệ: ok=%v err=%v", ok, err)
	}
}

// Middleware phải THẬT SỰ chặn token đã thu hồi, không chỉ hàm kiểm tra làm việc.
func TestMiddlewareBlocksRevokedToken(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	a := newTestApp(t)
	rev := authn.NewRedisRevoker(rdb, redisx.KeyPrefix)
	a.Issuer.UseRevoker(rev)

	tok, c := tokenFor(t, a, "0901234567", authn.RoleRider)
	router := a.Router()

	call := func() int {
		req := httptest.NewRequest("GET", "/v1/trips", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	if code := call(); code != http.StatusOK {
		t.Fatalf("token hợp lệ phải qua, được %d", code)
	}
	if err := rev.RevokeToken(ctx, c, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if code := call(); code != http.StatusUnauthorized {
		t.Fatalf("token đã thu hồi phải bị chặn với 401, được %d", code)
	}
}

// Từ chối hồ sơ phải thu hồi phiên NGAY, không chờ token hết hạn.
func TestRejectingKYCRevokesDriverSessions(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	a := newTestApp(t)
	rev := authn.NewRedisRevoker(rdb, redisx.KeyPrefix)
	rev.Skew = 0
	a.Issuer.UseRevoker(rev)
	a.Admin.UseRevoker(rev)

	tok, c := tokenFor(t, a, "0912345678", authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: c.Sub, FullName: "Tài", Phone: "0912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45"},
		Documents: driver.Documents{NationalID: "079", DriverLicense: "790"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tok

	if ok, _ := rev.IsRevoked(ctx, c); ok {
		t.Fatal("chưa từ chối thì chưa thu hồi")
	}
	time.Sleep(1100 * time.Millisecond) // để mốc thu hồi lớn hơn thời điểm phát token
	if _, err := a.Admin.ReviewKYC(ctx, admin.Actor{AccountID: "acc_admin"}, d.ID, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := rev.IsRevoked(ctx, c); err != nil || !ok {
		t.Fatalf("từ chối hồ sơ phải thu hồi phiên NGAY: ok=%v err=%v", ok, err)
	}

	// Duyệt (approved=true) thì KHÔNG thu hồi — tài xế đang làm việc bình thường.
	tok2, c2 := tokenFor(t, a, "0987654321", authn.RoleDriver)
	_ = tok2
	d2, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: c2.Sub, FullName: "Tài B", Phone: "0987654321", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X2-222.22"},
		Documents: driver.Documents{NationalID: "080", DriverLicense: "791"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Admin.ReviewKYC(ctx, admin.Actor{AccountID: "acc_admin"}, d2.ID, true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rev.IsRevoked(ctx, c2); ok {
		t.Fatal("duyệt hồ sơ KHÔNG được thu hồi phiên")
	}
}

// Không kiểm tra được thu hồi thì phải TỪ CHỐI, không được cho qua.
//
// Cho qua khi Redis chết nghĩa là mọi token đã thu hồi lại có hiệu lực trở lại,
// đúng vào lúc hệ thống đang có sự cố.
func TestRevocationCheckFailsClosed(t *testing.T) {
	a := newTestApp(t)
	a.Issuer.UseRevoker(brokenRevoker{})
	tok, _ := tokenFor(t, a, "0901234567", authn.RoleRider)

	req := httptest.NewRequest("GET", "/v1/trips", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("không kiểm tra được thu hồi thì phải từ chối, được %d", w.Code)
	}
}

type brokenRevoker struct{}

func (brokenRevoker) IsRevoked(context.Context, *authn.Claims) (bool, error) {
	return false, errs.E(errs.KindInternal, "redis_error", "Redis chết")
}
