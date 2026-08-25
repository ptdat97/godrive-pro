package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/settings"
)

// callSettings gọi API cấu hình qua ĐÚNG router thật, kèm middleware xác thực.
func callSettings(t *testing.T, a *App, tok, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	out := map[string]any{}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w.Code, out
}

func adminToken(t *testing.T, a *App) string {
	t.Helper()
	tok, _ := tokenFor(t, a, "0900000001", authn.RoleAdmin)
	return tok
}

// Cấu hình vận hành chỉ admin được đọc và sửa.
//
// Đây là màn hình đổi được giá cước và chiết khấu của cả hệ thống; một tài
// khoản khách rò rỉ mà chạm được vào đây là mất tiền thật.
func TestSettingsAPIRequiresAdmin(t *testing.T) {
	a := newTestApp(t)
	rider, _ := tokenFor(t, a, "0901234567", authn.RoleRider)
	driver, _ := tokenFor(t, a, "0912345678", authn.RoleDriver)

	for _, tc := range []struct {
		name, tok string
		want      int
	}{
		{"không có token", "", http.StatusUnauthorized},
		{"token khách", rider, http.StatusForbidden},
		{"token tài xế", driver, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := callSettings(t, a, tc.tok, "GET", "/v1/admin/settings", nil); code != tc.want {
				t.Errorf("GET: muốn %d, được %d", tc.want, code)
			}
			code, _ := callSettings(t, a, tc.tok, "PUT", "/v1/admin/settings/wallet",
				map[string]any{"value": map[string]any{"tax_permille": 0}, "version": 0, "reason": "thử xem có chặn không"})
			if code != tc.want {
				t.Errorf("PUT: muốn %d, được %d", tc.want, code)
			}
		})
	}
}

// Lược đồ biểu mẫu phải đi kèm dữ liệu, nếu không giao diện phải tự chép nhãn
// và ngưỡng — rồi hai bên trôi khỏi nhau.
func TestSettingsAPIReturnsFormSchema(t *testing.T) {
	a := newTestApp(t)
	code, body := callSettings(t, a, adminToken(t, a), "GET", "/v1/admin/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("muốn 200, được %d: %v", code, body)
	}
	groups, _ := body["groups"].([]any)
	if len(groups) != len(settings.AllKeys) {
		t.Fatalf("phải trả đủ %d nhóm, được %d", len(settings.AllKeys), len(groups))
	}
	fields := 0
	for _, g := range groups {
		gm := g.(map[string]any)
		secs, _ := gm["sections"].([]any)
		if len(secs) == 0 {
			t.Errorf("nhóm %v không có khối nào", gm["key"])
		}
		if gm["label"] == "" || gm["description"] == "" {
			t.Errorf("nhóm %v thiếu nhãn hoặc mô tả", gm["key"])
		}
		for _, s := range secs {
			fs, _ := s.(map[string]any)["fields"].([]any)
			fields += len(fs)
		}
	}
	if fields < 40 {
		t.Fatalf("số ô sửa được quá ít (%d) — có nhóm chưa khai lược đồ", fields)
	}
}

// Không có lý do thì không được đổi, kể cả khi gọi thẳng API bằng script.
func TestSettingsAPIRequiresReason(t *testing.T) {
	a := newTestApp(t)
	tok := adminToken(t, a)
	body := func(reason string) map[string]any {
		return map[string]any{
			"value": map[string]any{"stale_after_seconds": 60}, "version": 0, "reason": reason,
		}
	}
	for _, r := range []string{"", "   ", "ok"} {
		code, out := callSettings(t, a, tok, "PUT", "/v1/admin/settings/location", body(r))
		if code != http.StatusBadRequest || out["code"] != "setting_reason_required" {
			t.Errorf("lý do %q phải bị từ chối, được %d %v", r, code, out["code"])
		}
	}
	code, out := callSettings(t, a, tok, "PUT", "/v1/admin/settings/location",
		body("nới ngưỡng vì mạng 3G ngoại thành hay rớt"))
	if code != http.StatusOK {
		t.Fatalf("có lý do thì phải lưu được: %d %v", code, out)
	}
}

// Sửa từ một tab đã cũ không được ghi đè thay đổi của người khác.
func TestSettingsAPIDetectsConcurrentEdit(t *testing.T) {
	a := newTestApp(t)
	tok := adminToken(t, a)
	put := func(v int, fee float64) (int, map[string]any) {
		return callSettings(t, a, tok, "PUT", "/v1/admin/settings/wallet", map[string]any{
			"value": map[string]any{"cancel_fee_vnd": fee}, "version": v,
			"reason": "điều chỉnh phí huỷ theo chính sách mới",
		})
	}
	if code, out := put(0, 15000); code != http.StatusOK {
		t.Fatalf("lần ghi đầu phải thành công: %d %v", code, out)
	}
	// Người thứ hai vẫn đang cầm phiên bản 0.
	code, out := put(0, 20000)
	if code != http.StatusConflict || out["code"] != "setting_version_conflict" {
		t.Fatalf("phải báo xung đột, được %d %v", code, out["code"])
	}
	// Giá trị của người ghi trước không được mất.
	if got := a.Settings.Current(context.Background()).Wallet.CancelFeeVND; got != 15000 {
		t.Fatalf("thay đổi của người ghi trước bị ghi đè: %d", got)
	}
}

// Lịch sử phải trả về đủ trước–sau để dò lại được ai đổi cái gì.
func TestSettingsAPIHistory(t *testing.T) {
	a := newTestApp(t)
	tok := adminToken(t, a)
	if code, out := callSettings(t, a, tok, "PUT", "/v1/admin/settings/wallet", map[string]any{
		"value": map[string]any{"min_payout_vnd": 100000}, "version": 0,
		"reason": "ngân hàng đổi phí chuyển khoản",
	}); code != http.StatusOK {
		t.Fatalf("lưu thất bại: %d %v", code, out)
	}

	code, body := callSettings(t, a, tok, "GET", "/v1/admin/settings/wallet/history", nil)
	if code != http.StatusOK {
		t.Fatalf("muốn 200, được %d", code)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("phải có 1 bản ghi lịch sử, được %d", len(entries))
	}
	e := entries[0].(map[string]any)
	if e["reason"] != "ngân hàng đổi phí chuyển khoản" {
		t.Errorf("thiếu lý do: %v", e["reason"])
	}
	if e["changed_by"] == "" || e["new_value"] == nil {
		t.Errorf("bản ghi thiếu người sửa hoặc giá trị mới: %v", e)
	}
	nv := e["new_value"].(map[string]any)
	if nv["min_payout_vnd"] != float64(100000) {
		t.Errorf("giá trị mới sai: %v", nv["min_payout_vnd"])
	}
	// Các trường không gửi lên vẫn phải còn nguyên trong bản ghi.
	if nv["debt_limit_vnd"] != float64(settings.DefaultWallet().DebtLimitVND) {
		t.Errorf("hạn mức công nợ bị xoá khỏi bản ghi: %v", nv["debt_limit_vnd"])
	}
}
