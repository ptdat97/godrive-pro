package notification

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
)

// fakeGoogle đóng cả hai vai: máy chủ đổi token OAuth và máy chủ FCM.
type fakeGoogle struct {
	t          *testing.T
	pub        *rsa.PublicKey
	tokenCalls atomic.Int64
	mu         sync.Mutex
	sent       []map[string]any
	respond    func(token string) (int, string) // theo token thiết bị
}

func (g *fakeGoogle) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/token":
		g.tokenCalls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || !g.verifyJWT(r.Form.Get("assertion")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at-1","expires_in":3600,"token_type":"Bearer"}`))
	case r.URL.Path == "/v1/projects/godrive-test/messages:send":
		if r.Header.Get("Authorization") != "Bearer at-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Message map[string]any `json:"message"`
		}
		_ = json.Unmarshal(raw, &body)
		g.mu.Lock()
		g.sent = append(g.sent, body.Message)
		g.mu.Unlock()
		code, resp := 200, `{"name":"projects/godrive-test/messages/1"}`
		if g.respond != nil {
			code, resp = g.respond(body.Message["token"].(string))
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(resp))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// verifyJWT kiểm đúng như Google: chữ ký RS256 bằng khoá công khai, và các
// claim bắt buộc. Không kiểm chữ ký thì test xanh cả khi ký sai.
func (g *fakeGoogle) verifyJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(g.pub, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	return c["iss"] == "fcm@godrive-test.iam.gserviceaccount.com" && c["scope"] == fcmScope &&
		strings.HasSuffix(c["aud"].(string), "/token")
}

func newFakeFCM(t *testing.T) (*FCM, *fakeGoogle) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	g := &fakeGoogle{t: t, pub: &key.PublicKey}
	srv := httptest.NewServer(http.HandlerFunc(g.handler))
	t.Cleanup(srv.Close)
	f, err := NewFCM(ServiceAccount{
		ProjectID: "godrive-test", ClientEmail: "fcm@godrive-test.iam.gserviceaccount.com",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		TokenURI:   srv.URL + "/token",
	}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return f, g
}

func TestFCMSendsSignedRequestWithTTL(t *testing.T) {
	f, g := newFakeFCM(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := f.Send(ctx, "tok-a", Message{
			Title: "Có chuyến mới", Body: "Đón tại Chợ Bến Thành",
			Data: map[string]string{"type": "offer", "offer_id": "ofr_1"},
			TTL:  15 * time.Second, Urgent: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n := g.tokenCalls.Load(); n != 1 {
		t.Fatalf("access token phải được dùng lại trong hạn, đã đổi %d lần", n)
	}
	m := g.sent[0]
	android, _ := m["android"].(map[string]any)
	apns, _ := m["apns"].(map[string]any)
	headers, _ := apns["headers"].(map[string]any)
	if m["token"] != "tok-a" || android["priority"] != "HIGH" || android["ttl"] != "15s" ||
		headers["apns-priority"] != "10" || headers["apns-expiration"] == nil {
		t.Fatalf("tin gửi FCM thiếu ưu tiên hoặc hạn sống: %+v", m)
	}
	if d, _ := m["data"].(map[string]any); d["offer_id"] != "ofr_1" {
		t.Fatalf("thiếu data: %+v", m)
	}
}

func TestFCMClassifiesErrors(t *testing.T) {
	f, g := newFakeFCM(t)
	g.respond = func(tok string) (int, string) {
		switch tok {
		case "chet":
			return 404, `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND",
                "details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
		case "ban":
			return 503, `{"error":{"code":503,"status":"UNAVAILABLE"}}`
		case "sai":
			return 400, `{"error":{"code":400,"message":"Invalid JSON payload","status":"INVALID_ARGUMENT",
                "details":[{"errorCode":"INVALID_ARGUMENT"}]}}`
		}
		return 200, `{}`
	}
	ctx := context.Background()
	if err := f.Send(ctx, "chet", Message{Title: "x"}); !errors.Is(err, ErrTokenGone) {
		t.Fatalf("UNREGISTERED phải là ErrTokenGone, được %v", err)
	}
	if err := f.Send(ctx, "ban", Message{Title: "x"}); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("503 phải là lỗi tạm thời để thử lại, được %v", err)
	}
	if err := f.Send(ctx, "sai", Message{Title: "x"}); errs.CodeOf(err) != "fcm_rejected" {
		t.Fatalf("400 phải là bị từ chối hẳn, được %v", err)
	}
}

func TestNotifierCleansDeadTokensAndRetries(t *testing.T) {
	f, g := newFakeFCM(t)
	g.respond = func(tok string) (int, string) {
		if tok == "may-cu" {
			return 404, `{"error":{"details":[{"errorCode":"UNREGISTERED"}]}}`
		}
		if tok == "may-ban" {
			return 503, `{}`
		}
		return 200, `{}`
	}
	st := NewMemoryDeviceStore()
	ctx := context.Background()
	now := time.Now()
	for _, d := range []Device{
		{AccountID: "acc_r", DeviceID: "d1", Token: "may-cu", Platform: "android", UpdatedAt: now},
		{AccountID: "acc_r", DeviceID: "d2", Token: "may-moi", Platform: "ios", UpdatedAt: now},
		{AccountID: "acc_r", DeviceID: "d3", Token: "may-ban", Platform: "android", UpdatedAt: now},
	} {
		if err := st.Upsert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	n := NewNotifier(st, f, clock.Real(), slog.Default())
	err := n.Notify(ctx, "acc_r", Message{Title: "Tài xế đã tới"})
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("có máy lỗi tạm thời thì phải trả lỗi để bus giao lại, được %v", err)
	}
	devs, _ := st.ListByAccount(ctx, "acc_r")
	if len(devs) != 2 {
		t.Fatalf("token chết phải bị xoá, còn %d máy", len(devs))
	}
	for _, d := range devs {
		if d.Token == "may-cu" {
			t.Fatal("token UNREGISTERED vẫn còn")
		}
	}
}

// Máy cài lại app (token mới) không được để lại token cũ; máy đổi người dùng
// không được nhận thông báo của người cũ.
func TestDeviceTokenOwnership(t *testing.T) {
	st := NewMemoryDeviceStore()
	n := NewNotifier(st, nil, clock.Real(), slog.Default())
	ctx := context.Background()
	a := &authn.Claims{Sub: "acc_a", DeviceID: "dien-thoai-1"}
	_ = n.Register(ctx, a, "tok-1", "android")
	_ = n.Register(ctx, a, "tok-2", "android") // FCM xoay token
	if devs, _ := st.ListByAccount(ctx, "acc_a"); len(devs) != 1 || devs[0].Token != "tok-2" {
		t.Fatalf("một máy chỉ giữ token mới nhất: %+v", devs)
	}
	b := &authn.Claims{Sub: "acc_b", DeviceID: "dien-thoai-1"}
	_ = n.Register(ctx, b, "tok-2", "android") // máy đó giờ là của người khác
	if devs, _ := st.ListByAccount(ctx, "acc_a"); len(devs) != 0 {
		t.Fatalf("người dùng cũ không được giữ token của máy đã đổi chủ: %+v", devs)
	}
	if err := n.Register(ctx, b, "", "android"); errs.CodeOf(err) != "device_token_invalid" {
		t.Fatalf("token rỗng phải bị từ chối, được %v", err)
	}
	if err := n.Register(ctx, b, "tok-3", "windows"); errs.CodeOf(err) != "platform_invalid" {
		t.Fatalf("nền tảng lạ phải bị từ chối, được %v", err)
	}
}
