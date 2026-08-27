package mqttauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/clock"
)

type fakeDrivers map[string]*DriverRef // accountID -> tài xế

func (f fakeDrivers) GetByAccount(_ context.Context, accountID string) (*DriverRef, error) {
	d, ok := f[accountID]
	if !ok {
		return nil, errors.New("không có hồ sơ tài xế")
	}
	return d, nil
}

func newSvc(t *testing.T) (*Service, *authn.Issuer, fakeDrivers) {
	t.Helper()
	iss := authn.NewIssuer("bi-mat-test", time.Hour)
	drivers := fakeDrivers{
		"acc_a":    {ID: "drv_a"},
		"acc_b":    {ID: "drv_b"},
		"acc_khoa": {ID: "drv_khoa", Suspended: true},
	}
	y, m, d := time.Now().UTC().Date()
	clk := clock.NewMock(time.Date(y, m, d, 10, 0, 0, 0, time.UTC))
	return NewService(iss, drivers, clk, ServiceAccount{
		Username: "godrive-backend", Password: "mat-khau-dich-vu",
	}), iss, drivers
}

func tokenFor(t *testing.T, iss *authn.Issuer, account string, role authn.Role) string {
	t.Helper()
	y, m, d := time.Now().UTC().Date()
	now := time.Date(y, m, d, 10, 0, 0, 0, time.UTC)
	tok, _ := iss.Issue(account, role, "thiet-bi-1", now)
	return tok
}

// Không có thông tin đăng nhập thì không vào được. Đây là toàn bộ lý do tồn tại
// của gói này: trước khi có nó, ai kết nối được tới broker cũng publish được vào
// topic của bất kỳ tài xế nào.
func TestAnonymousIsRejected(t *testing.T) {
	svc, _, _ := newSvc(t)
	for _, r := range []Request{
		{},
		{ClientID: "drv_drv_a"},
		{ClientID: "drv_drv_a", Username: "drv_a"},
		{ClientID: "drv_drv_a", Password: "gi-do"},
	} {
		if d := svc.Authenticate(context.Background(), r); d.Allow {
			t.Errorf("phải từ chối: %+v", r)
		}
	}
}

// Tài xế thật, token thật, topic của chính mình.
func TestDriverGetsOwnTopicsOnly(t *testing.T) {
	svc, iss, _ := newSvc(t)
	d := svc.Authenticate(context.Background(), Request{
		ClientID: "drv_drv_a_pixel", Username: "drv_a",
		Password: tokenFor(t, iss, "acc_a", authn.RoleDriver),
	})
	if !d.Allow {
		t.Fatalf("tài xế hợp lệ phải vào được: %s", d.Deny)
	}
	if d.Superuser {
		t.Fatal("thiết bị tài xế KHÔNG được là superuser")
	}

	want := map[string]Action{
		"drv/drv_a/loc":    ActionPublish,
		"drv/drv_a/status": ActionPublish,
		"drv/drv_a/offer":  ActionSubscribe,
		"drv/drv_a/trip":   ActionSubscribe,
	}
	got := map[string]Action{}
	denyAll := false
	for _, r := range d.Rules {
		if r.Allow {
			got[r.Topic] = r.Action
			continue
		}
		if r.Topic == "#" && r.Action == ActionAll {
			denyAll = true
		}
	}
	for topic, act := range want {
		if got[topic] != act {
			t.Errorf("thiếu quyền %s trên %s", act, topic)
		}
	}
	for topic := range got {
		if _, ok := want[topic]; !ok {
			t.Errorf("quyền thừa trên %s", topic)
		}
	}
	if !denyAll {
		t.Error("thiếu luật cấm mọi topic còn lại")
	}
	// Và tuyệt đối không được có quyền nào chạm tới tài xế khác.
	for _, r := range d.Rules {
		if r.Allow && r.Topic == "drv/drv_b/loc" {
			t.Fatal("được quyền trên topic của tài xế khác")
		}
	}
}

// Token hợp lệ của A không xin được quyền dưới danh nghĩa B.
func TestCannotClaimAnotherDriver(t *testing.T) {
	svc, iss, _ := newSvc(t)
	d := svc.Authenticate(context.Background(), Request{
		ClientID: "drv_drv_b", Username: "drv_b",
		Password: tokenFor(t, iss, "acc_a", authn.RoleDriver), // token của A
	})
	if d.Allow {
		t.Fatal("token của A không được nhận danh nghĩa B")
	}
}

// clientId phải mang mã tài xế của chính mình.
//
// Không ràng buộc thì A đặt clientId trùng của B là đá được B ra khỏi broker —
// MQTT cho client sau cùng chiếm phiên. Luật topic không chặn được đường này vì
// nó xảy ra trước khi có topic nào.
func TestCannotStealAnotherDriverSession(t *testing.T) {
	svc, iss, _ := newSvc(t)
	tok := tokenFor(t, iss, "acc_a", authn.RoleDriver)
	for _, cid := range []string{"drv_drv_b", "drv_drv_b_pixel", "", "drv_", "khac"} {
		d := svc.Authenticate(context.Background(), Request{
			ClientID: cid, Username: "drv_a", Password: tok,
		})
		if d.Allow {
			t.Errorf("clientId %q phải bị từ chối", cid)
		}
	}
	// Của chính mình thì được, kể cả có hậu tố phân biệt thiết bị.
	for _, cid := range []string{"drv_drv_a", "drv_drv_a_pixel7"} {
		if d := svc.Authenticate(context.Background(), Request{
			ClientID: cid, Username: "drv_a", Password: tok,
		}); !d.Allow {
			t.Errorf("clientId %q phải được chấp nhận: %s", cid, d.Deny)
		}
	}
}

// Khoá tài xế thì lần kết nối sau bị chặn.
func TestSuspendedDriverCannotConnect(t *testing.T) {
	svc, iss, _ := newSvc(t)
	d := svc.Authenticate(context.Background(), Request{
		ClientID: "drv_drv_khoa", Username: "drv_khoa",
		Password: tokenFor(t, iss, "acc_khoa", authn.RoleDriver),
	})
	if d.Allow {
		t.Fatal("tài xế bị khoá không được kết nối")
	}
}

// Token của khách hoặc admin không mở được luồng vị trí tài xế.
func TestNonDriverRolesRejected(t *testing.T) {
	svc, iss, _ := newSvc(t)
	for _, role := range []authn.Role{authn.RoleRider, authn.RoleAdmin} {
		d := svc.Authenticate(context.Background(), Request{
			ClientID: "drv_drv_a", Username: "drv_a",
			Password: tokenFor(t, iss, "acc_a", role),
		})
		if d.Allow {
			t.Errorf("vai trò %s không được vào", role)
		}
	}
}

// Token hết hạn bị từ chối — nếu không thì thu hồi phiên chẳng có tác dụng gì
// với kết nối MQTT.
func TestExpiredTokenRejected(t *testing.T) {
	svc, iss, _ := newSvc(t)
	y, m, d0 := time.Now().UTC().Date()
	old := time.Date(y, m, d0, 10, 0, 0, 0, time.UTC).Add(-48 * time.Hour)
	tok, _ := iss.Issue("acc_a", authn.RoleDriver, "thiet-bi-1", old)
	if d := svc.Authenticate(context.Background(), Request{
		ClientID: "drv_drv_a", Username: "drv_a", Password: tok,
	}); d.Allow {
		t.Fatal("token hết hạn phải bị từ chối")
	}
}

// Tài khoản dịch vụ của backend cần quyền rộng, và chỉ nó mới được.
func TestServiceAccountIsSuperuser(t *testing.T) {
	svc, _, _ := newSvc(t)
	d := svc.Authenticate(context.Background(), Request{
		ClientID: "godrive-pod-1", Username: "godrive-backend", Password: "mat-khau-dich-vu",
	})
	if !d.Allow || !d.Superuser {
		t.Fatalf("backend phải vào được với quyền rộng: allow=%v super=%v %s", d.Allow, d.Superuser, d.Deny)
	}
	// Sai mật khẩu thì không.
	if d := svc.Authenticate(context.Background(), Request{
		ClientID: "godrive-pod-1", Username: "godrive-backend", Password: "doan-bua",
	}); d.Allow {
		t.Fatal("sai mật khẩu dịch vụ mà vẫn vào được")
	}
}

// Chưa cấu hình tài khoản dịch vụ thì KHÔNG được biến thành cửa mở.
//
// Nếu chuỗi rỗng vẫn khớp chuỗi rỗng thì một môi trường quên đặt biến sẽ cho
// bất kỳ ai gửi tên đăng nhập rỗng vào thẳng với quyền superuser.
func TestUnconfiguredServiceAccountIsNotABackdoor(t *testing.T) {
	y, m, d0 := time.Now().UTC().Date()
	clk := clock.NewMock(time.Date(y, m, d0, 10, 0, 0, 0, time.UTC))
	svc := NewService(authn.NewIssuer("bi-mat-test", time.Hour), fakeDrivers{}, clk, ServiceAccount{})
	for _, r := range []Request{
		{ClientID: "x", Username: "", Password: ""},
		{ClientID: "x", Username: "godrive-backend", Password: ""},
	} {
		if d := svc.Authenticate(context.Background(), r); d.Allow {
			t.Errorf("cấu hình rỗng không được cho vào: %+v", r)
		}
	}
}

type fakeRevoker struct {
	revoked map[string]bool // theo jti
	err     error
}

func (f fakeRevoker) IsRevoked(_ context.Context, c *authn.Claims) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.revoked[c.JTI], nil
}

// Đăng xuất hoặc khoá tài khoản phải cắt được đường MQTT.
//
// authn.Issuer.Parse chỉ kiểm chữ ký và hạn dùng; việc thu hồi nằm ở middleware
// HTTP, mà kết nối MQTT không đi qua middleware nào. Thiếu bước này thì thu hồi
// phiên chỉ đóng được cửa HTTP, còn thiết bị vẫn đẩy vị trí bình thường cho tới
// khi token hết hạn.
func TestRevokedSessionCannotConnect(t *testing.T) {
	svc, iss, _ := newSvc(t)
	y, m, d0 := time.Now().UTC().Date()
	now := time.Date(y, m, d0, 10, 0, 0, 0, time.UTC)
	tok, _, jti := iss.IssueWithID("acc_a", authn.RoleDriver, "thiet-bi-1", now)

	req := Request{ClientID: "drv_drv_a", Username: "drv_a", Password: tok}
	if d := svc.Authenticate(context.Background(), req); !d.Allow {
		t.Fatalf("trước khi thu hồi phải vào được: %s", d.Deny)
	}

	svc.UseRevoker(fakeRevoker{revoked: map[string]bool{jti: true}})
	if d := svc.Authenticate(context.Background(), req); d.Allow {
		t.Fatal("phiên đã thu hồi vẫn kết nối được")
	}
}

// Không kiểm tra được thu hồi thì TỪ CHỐI, không cho qua.
//
// Cùng lựa chọn fail-closed như middleware HTTP: cho qua khi Redis chết nghĩa là
// mọi phiên đã thu hồi sống lại đúng lúc hệ thống đang có sự cố.
func TestRevocationCheckFailsClosed(t *testing.T) {
	svc, iss, _ := newSvc(t)
	svc.UseRevoker(fakeRevoker{err: errors.New("redis chết")})
	if d := svc.Authenticate(context.Background(), Request{
		ClientID: "drv_drv_a", Username: "drv_a",
		Password: tokenFor(t, iss, "acc_a", authn.RoleDriver),
	}); d.Allow {
		t.Fatal("không kiểm được thu hồi thì phải từ chối")
	}
}

// Phân quyền: tài xế chỉ đụng được vào topic của chính mình.
//
// Bước này KHÔNG có token trong tay — broker chỉ gửi tên đăng nhập. Nó tin vào
// việc bước xác thực đã chốt tên đăng nhập chính là mã tài xế của token.
func TestAuthorizeConfinesDriverToOwnTopics(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	q := func(user, topic string, act Action) bool {
		return svc.Authorize(ctx, AuthzRequest{
			ClientID: "drv_" + user, Username: user, Topic: topic, Action: act,
		})
	}

	for _, c := range []struct {
		topic string
		act   Action
		want  bool
		why   string
	}{
		{"drv/drv_a/loc", ActionPublish, true, "gửi vị trí của chính mình"},
		{"drv/drv_a/status", ActionPublish, true, "Last Will của chính mình"},
		{"drv/drv_a/offer", ActionSubscribe, true, "nhận lời mời của chính mình"},
		{"drv/drv_a/trip", ActionSubscribe, true, "nhận trạng thái chuyến của mình"},

		{"drv/drv_b/loc", ActionPublish, false, "giả vị trí tài xế khác"},
		{"drv/drv_b/offer", ActionSubscribe, false, "nghe lén lời mời của người khác"},
		{"drv/drv_a/loc", ActionSubscribe, false, "nghe topic chiều lên của chính mình"},
		{"drv/drv_a/offer", ActionPublish, false, "tự bịa lời mời cho chính mình"},
		{"drv/+/loc", ActionSubscribe, false, "ký tự đại diện: nghe mọi tài xế"},
		{"drv/#", ActionSubscribe, false, "ký tự đại diện: nghe tất cả"},
		{"#", ActionSubscribe, false, "nghe toàn bộ broker"},
		{"$SYS/#", ActionSubscribe, false, "topic nội bộ của broker"},
	} {
		if got := q("drv_a", c.topic, c.act); got != c.want {
			t.Errorf("%s %s (%s): được %v, muốn %v", c.act, c.topic, c.why, got, c.want)
		}
	}
}

// Tài khoản dịch vụ đọc được topic của mọi tài xế — nhưng chỉ nó.
func TestAuthorizeServiceAccount(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	if !svc.Authorize(ctx, AuthzRequest{
		Username: "godrive-backend", Topic: "drv/bat-ky/loc", Action: ActionSubscribe,
	}) {
		t.Error("backend phải đọc được topic của mọi tài xế")
	}
	// Tên rỗng không được hưởng đặc quyền của tài khoản dịch vụ chưa cấu hình.
	empty := NewService(nil, nil, nil, ServiceAccount{})
	if empty.Authorize(ctx, AuthzRequest{Username: "", Topic: "drv/x/loc", Action: ActionPublish}) {
		t.Error("tên đăng nhập rỗng không được cho qua")
	}
}
