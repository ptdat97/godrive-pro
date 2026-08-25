package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
)

// Auditor ghi nhật ký thao tác quản trị. Port khai báo ở đây (bên tiêu thụ).
type Auditor interface {
	RecordSettingChange(ctx context.Context, actorID string, key string,
		oldValue, newValue json.RawMessage, reason string) error
}

type Handler struct {
	svc   *Service
	auth  *authn.Issuer
	audit Auditor
}

func NewHandler(s *Service, a *authn.Issuer, audit Auditor) *Handler {
	return &Handler{svc: s, auth: a, audit: audit}
}

func (h *Handler) Register(mux *http.ServeMux) {
	adm := h.auth.Require(authn.RoleAdmin)
	mux.Handle("GET /v1/admin/settings", adm(http.HandlerFunc(h.list)))
	mux.Handle("GET /v1/admin/settings/{key}", adm(http.HandlerFunc(h.get)))
	mux.Handle("PUT /v1/admin/settings/{key}", adm(http.HandlerFunc(h.put)))
	mux.Handle("GET /v1/admin/settings/{key}/history", adm(http.HandlerFunc(h.history)))
}

// groupMeta mô tả một nhóm cho giao diện: nhãn tiếng Việt và cảnh báo nếu có.
type groupMeta struct {
	Key         Key    `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Warning hiện nổi bật trên giao diện. Chỉ đặt khi thay đổi có hệ quả
	// pháp lý hoặc tài chính mà người chỉnh cần biết TRƯỚC khi bấm lưu.
	Warning string `json:"warning,omitempty"`
}

var groupMetas = map[Key]groupMeta{
	KeyPricing: {
		Key: KeyPricing, Label: "Biểu giá",
		Description: "Giá mở cửa, đơn giá theo km và phút, phụ phí đêm, chiết khấu nền tảng.",
		Warning: "Giá cước phải khớp hồ sơ kê khai giá cước đã nộp cho Sở GTVT. " +
			"Đổi ở đây mà chưa nộp hồ sơ mới là vi phạm. Thay đổi KHÔNG hồi tố lên " +
			"báo giá đã phát và chuyến đang chạy.",
	},
	KeySurge: {
		Key: KeySurge, Label: "Tăng giá theo cầu",
		Description: "Bậc thang hệ số theo tỉ lệ cầu/cung, trần và cửa sổ đếm.",
		Warning:     "Trần tăng giá cao làm tăng rủi ro truyền thông. Bậc thang phải tăng dần.",
	},
	KeyMatching: {
		Key: KeyMatching, Label: "Ghép chuyến",
		Description: "Bán kính tìm tài xế, số vòng chào mời, và trọng số chấm điểm.",
		Warning: "Trọng số chấm điểm nên chỉ đổi khi có dữ liệu thật hoặc kết quả A/B test. " +
			"Đổi mò sẽ làm thu nhập tài xế biến động mà không ai giải thích được vì sao.",
	},
	KeyWallet: {
		Key: KeyWallet, Label: "Ví & công nợ",
		Description: "Hạn mức công nợ tiền mặt, thuế khấu trừ, ngưỡng chi trả, phí huỷ chuyến.",
		Warning: "Thuế khấu trừ tại nguồn cần kế toán thuế xác nhận trước khi bật. " +
			"Hạ hạn mức công nợ sẽ chặn ngay những tài xế đang nợ quá mức mới.",
	},
	KeyLocation: {
		Key: KeyLocation, Label: "Vị trí & chống gian lận",
		Description: "Ngưỡng ping quá hạn, tốc độ tối đa hợp lý, sai số GPS chấp nhận được.",
	},
}

type groupView struct {
	groupMeta
	// Sections là lược đồ biểu mẫu: giao diện vẽ ô nhập từ đây chứ không tự
	// chép lại nhãn và ngưỡng. Xem schema.go.
	Sections  []Section       `json:"sections"`
	Value     json.RawMessage `json:"value"`
	Version   int             `json:"version"`
	UpdatedBy string          `json:"updated_by,omitempty"`
	UpdatedAt string          `json:"updated_at,omitempty"`
	// IsDefault = true nghĩa là chưa từng lưu, đang chạy bằng giá trị mặc định.
	IsDefault bool `json:"is_default"`
}

func (h *Handler) viewFor(r *http.Request, k Key) (groupView, error) {
	rec, err := h.svc.Get(r.Context(), k)
	if err != nil {
		return groupView{}, err
	}
	v := groupView{
		groupMeta: groupMetas[k], Sections: SchemaFor(k),
		Value: rec.Value, Version: rec.Version,
		UpdatedBy: rec.UpdatedBy, IsDefault: rec.Version == 0,
	}
	if rec.Version > 0 {
		v.UpdatedAt = rec.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return v, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out := make([]groupView, 0, len(AllKeys))
	for _, k := range AllKeys {
		v, err := h.viewFor(r, k)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		out = append(out, v)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewFor(r, Key(r.PathValue("key")))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

type putReq struct {
	Value json.RawMessage `json:"value"`
	// Version là phiên bản giao diện đang xem. Sai nghĩa là có người vừa đổi.
	Version int `json:"version"`
	// Reason là lý do thay đổi, ghi vào lịch sử.
	Reason string `json:"reason"`
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var req putReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(req.Value) == 0 {
		httpx.Fail(w, r, errs.Invalid("setting_value_required", "Thiếu nội dung cấu hình."))
		return
	}
	// Bắt buộc có lý do, ở TẦNG API chứ không chỉ trên giao diện: ai gọi thẳng
	// bằng script cũng phải để lại lời giải thích. Vài tháng sau, dòng này là
	// thứ duy nhất còn lại để trả lời "vì sao chiết khấu lại là 25%".
	if len(strings.TrimSpace(req.Reason)) < 5 {
		httpx.Fail(w, r, errs.Invalid("setting_reason_required",
			"Cần ghi lý do thay đổi (ít nhất 5 ký tự)."))
		return
	}
	k := Key(r.PathValue("key"))
	actor := authn.MustClaims(r.Context()).Sub

	// Đọc giá trị CŨ trước khi ghi, để nhật ký có cả trước lẫn sau.
	before, err := h.svc.Get(r.Context(), k)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	rec, err := h.svc.Put(r.Context(), k, req.Value, req.Version, actor, req.Reason)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if h.audit != nil {
		// Ghi nhật ký lỗi KHÔNG làm hỏng thay đổi đã ghi — settings_history đã
		// giữ dấu vết đầy đủ; admin_audit_log là bản sao cho luồng xem chung.
		_ = h.audit.RecordSettingChange(r.Context(), actor, string(k),
			before.Value, rec.Value, req.Reason)
	}

	v, err := h.viewFor(r, k)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.History(r.Context(), Key(r.PathValue("key")), 20)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries, "count": len(entries)})
}
