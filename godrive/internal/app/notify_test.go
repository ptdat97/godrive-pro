package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/notification"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/internal/refund"
	"github.com/example/godrive/internal/trip"
)

type sentNotice struct {
	token string
	msg   notification.Message
}

// fakeSender ghi lại mọi tin thay cho FCM.
type fakeSender struct {
	mu   sync.Mutex
	sent []sentNotice
}

func (f *fakeSender) Send(_ context.Context, token string, m notification.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentNotice{token, m})
	return nil
}

// to trả các tin đã gửi tới token, theo thứ tự.
func (f *fakeSender) to(token string) []notification.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []notification.Message
	for _, s := range f.sent {
		if s.token == token {
			out = append(out, s.msg)
		}
	}
	return out
}

func hasNotice(ms []notification.Message, key, val string) bool {
	for _, m := range ms {
		if m.Data[key] == val {
			return true
		}
	}
	return false
}

// withFakeFCM thay Notifier của app bằng bản gửi vào fakeSender. Phải gọi
// TRƯỚC StartWorkers và Router: cả hai đọc a.Notifier lúc được gọi.
func withFakeFCM(a *App) *fakeSender {
	fs := &fakeSender{}
	a.Notifier = notification.NewNotifier(notification.NewMemoryDeviceStore(), fs, a.Clock, a.Log)
	return fs
}

func registerDevice(t *testing.T, h http.Handler, a *App, accountID string, role authn.Role, device, token string) string {
	t.Helper()
	tok, _ := a.Issuer.Issue(accountID, role, device, a.Clock.Now().UTC())
	body, _ := json.Marshal(map[string]string{"token": token, "platform": "android"})
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("đăng ký thiết bị: %d %s", rec.Code, rec.Body)
	}
	return tok
}

// T-33: trọn một chuyến, ai được đánh thức bằng thông báo đẩy và lúc nào.
func TestPushNotificationsFollowTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	fs := withFakeFCM(a)
	a.StartWorkers(ctx)
	h := a.Router()

	riderID := login(t, a, "0901234567", authn.RoleRider)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	registerDevice(t, h, a, riderID, authn.RoleRider, "may-khach", "fcm-khach")
	registerDevice(t, h, a, d.AccountID, authn.RoleDriver, "may-tai", "fcm-tai")

	tr := seedTrip(t, a, riderID)
	eventually(t, "tài xế được đánh thức vì có lời mời", func() bool {
		return hasNotice(fs.to("fcm-tai"), "type", "offer")
	})
	offer := fs.to("fcm-tai")[0]
	if !offer.Urgent || offer.TTL <= 0 || offer.TTL > 15e9 {
		t.Fatalf("lời mời phải gấp và sống không quá 15 giây: %+v", offer)
	}
	offers, _ := waitForOffers(t, a, d.ID)
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
	for _, st := range []string{"ASSIGNED", "ARRIVED", "COMPLETED"} {
		st := st
		eventually(t, "khách nhận "+st, func() bool { return hasNotice(fs.to("fcm-khach"), "status", st) })
	}
	// Tài xế tự bấm bắt đầu/hoàn tất — không cần bị đánh thức lại.
	if hasNotice(fs.to("fcm-tai"), "type", "trip") {
		t.Fatalf("tài xế không cần thông báo về bước mình vừa bấm: %+v", fs.to("fcm-tai"))
	}

	// Hoàn tiền vào ví thì khách được báo.
	eventually(t, "chuyến đã ghi sổ", func() bool {
		cur, _ := a.Trips.Get(ctx, tr.ID)
		return cur.Status == trip.StatusPaid
	})
	r, err := a.Refunds.Request(ctx, opsA, refund.RequestInput{TripID: tr.ID, Amount: 5000, Reason: "xin lỗi vì chờ lâu"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasNotice(fs.to("fcm-khach"), "refund_id", r.ID) {
		t.Fatal("khách phải được báo khi tiền hoàn vào ví")
	}
}

// Máy đã gỡ đăng ký (đăng xuất) không còn nhận thông báo của người dùng cũ.
func TestForgottenDeviceGetsNothing(t *testing.T) {
	a := newTestApp(t)
	fs := withFakeFCM(a)
	a.StartWorkers(context.Background())
	h := a.Router()
	riderID := login(t, a, "0901234567", authn.RoleRider)
	tok := registerDevice(t, h, a, riderID, authn.RoleRider, "may-khach", "fcm-khach")

	req := httptest.NewRequest(http.MethodDelete, "/v1/devices/current", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("gỡ thiết bị: %d %s", rec.Code, rec.Body)
	}
	seedTrip(t, a, riderID)
	if err := a.Notifier.Notify(context.Background(), riderID, notification.Message{Title: "thử"}); err != nil {
		t.Fatal(err)
	}
	if got := fs.to("fcm-khach"); len(got) != 0 {
		t.Fatalf("máy đã gỡ không được nhận gì: %+v", got)
	}
}

// Đăng xuất (cần Redis để thu hồi phiên) cũng xoá token của máy đó.
func TestLogoutForgetsDevice(t *testing.T) {
	url := os.Getenv(testRedisEnv)
	if url == "" {
		t.Skipf("bỏ qua: đặt %s", testRedisEnv)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env, cfg.RedisURL = "test", url
	a, err := New(cfg, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	fs := withFakeFCM(a)
	h := a.Router()
	riderID := login(t, a, "0901234567", authn.RoleRider)
	tok := registerDevice(t, h, a, riderID, authn.RoleRider, "may-khach", "fcm-khach")

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("đăng xuất: %d %s", rec.Code, rec.Body)
	}
	if err := a.Notifier.Notify(context.Background(), riderID, notification.Message{Title: "sau đăng xuất"}); err != nil {
		t.Fatal(err)
	}
	if got := fs.to("fcm-khach"); len(got) != 0 {
		t.Fatalf("đăng xuất phải xoá token của máy đó, máy vẫn nhận: %+v", got)
	}
}

// Cùng luật sở hữu token như bản bộ nhớ, trên Postgres: máy xoay token thì
// token cũ biến mất; máy đổi chủ thì chủ cũ mất token.
func TestPostgresDeviceTokenOwnership(t *testing.T) {
	a, _ := newPostgresApp(t)
	fs := &fakeSender{}
	st := notification.NewPostgresDeviceStore(a.db)
	n := notification.NewNotifier(st, fs, a.Clock, a.Log)
	ctx := context.Background()
	ca := &authn.Claims{Sub: "acc_a", DeviceID: "dt1"}
	cb := &authn.Claims{Sub: "acc_b", DeviceID: "dt1"}
	for _, step := range []struct {
		c   *authn.Claims
		tok string
	}{{ca, "tok-1"}, {ca, "tok-2"}, {cb, "tok-2"}} {
		if err := n.Register(ctx, step.c, step.tok, "ios"); err != nil {
			t.Fatal(err)
		}
	}
	_ = n.Notify(ctx, "acc_a", notification.Message{Title: "a"})
	_ = n.Notify(ctx, "acc_b", notification.Message{Title: "b"})
	if len(fs.sent) != 1 || fs.sent[0].token != "tok-2" || fs.sent[0].msg.Title != "b" {
		t.Fatalf("chỉ chủ mới của máy nhận, và chỉ trên token mới nhất: %+v", fs.sent)
	}
}
