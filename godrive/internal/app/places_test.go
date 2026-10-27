package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/logger"
)

func placesApp(t *testing.T, goongURL string) *App {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = "test"
	if goongURL != "" {
		cfg.GoongURL, cfg.GoongAPIKey = goongURL, "khoa-test"
	}
	a, err := New(cfg, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func getPlaces(t *testing.T, a *App, tok, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// T-38 qua HTTP: chỉ người đã đăng nhập dùng được (mỗi lượt tốn tiền); chưa có
// key thì 503 để app lùi về chọn trên bản đồ; có key thì trả gợi ý.
func TestPlacesHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"predictions":[{"place_id":"p_bt","description":"Chợ Bến Thành",
            "structured_formatting":{"main_text":"Chợ Bến Thành","secondary_text":"Quận 1"}}],"status":"OK"}`))
	}))
	defer srv.Close()

	off := placesApp(t, "")
	riderID := login(t, off, "0901234567", authn.RoleRider)
	tok, _ := off.Issuer.Issue(riderID, authn.RoleRider, "may", off.Clock.Now().UTC())
	if code, _ := getPlaces(t, off, "", "/v1/places/autocomplete?q=cho"); code != http.StatusUnauthorized {
		t.Fatalf("chưa đăng nhập phải 401, được %d", code)
	}
	if code, body := getPlaces(t, off, tok, "/v1/places/autocomplete?q=cho+ben+thanh"); code != http.StatusServiceUnavailable ||
		body["code"] != "places_unavailable" {
		t.Fatalf("chưa có key phải 503 places_unavailable, được %d %v", code, body)
	}

	on := placesApp(t, srv.URL)
	riderID = login(t, on, "0901234567", authn.RoleRider)
	tok, _ = on.Issuer.Issue(riderID, authn.RoleRider, "may", on.Clock.Now().UTC())
	code, body := getPlaces(t, on, tok, "/v1/places/autocomplete?q=cho+ben+thanh&lat=10.7769&lng=106.7009")
	sug, _ := body["suggestions"].([]any)
	if code != http.StatusOK || len(sug) != 1 {
		t.Fatalf("có key phải trả 1 gợi ý, được %d %v", code, body)
	}
	if code, _ := getPlaces(t, on, tok, "/v1/places/reverse?lat=abc&lng=1"); code != http.StatusBadRequest {
		t.Fatalf("toạ độ hỏng phải 400, được %d", code)
	}
}
