package places

import (
	"net/http"
	"strconv"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

type Handler struct {
	svc  *Service
	auth *authn.Issuer
}

func NewHandler(s *Service, a *authn.Issuer) *Handler { return &Handler{svc: s, auth: a} }

// Register: chỉ người dùng đã đăng nhập. Mỗi lượt gọi tốn tiền — để mở công
// khai là mời người ngoài dùng hạn mức của mình.
func (h *Handler) Register(mux *http.ServeMux) {
	any := h.auth.Require(authn.RoleRider, authn.RoleDriver)
	mux.Handle("GET /v1/places/autocomplete", any(http.HandlerFunc(h.autocomplete)))
	mux.Handle("GET /v1/places/reverse", any(http.HandlerFunc(h.reverse)))
	mux.Handle("GET /v1/places/{id}", any(http.HandlerFunc(h.detail)))
}

// pointParam đọc lat/lng tuỳ chọn; vắng cả hai thì trả nil.
func pointParam(r *http.Request) (*geo.Point, error) {
	q := r.URL.Query()
	if q.Get("lat") == "" && q.Get("lng") == "" {
		return nil, nil
	}
	lat, err1 := strconv.ParseFloat(q.Get("lat"), 64)
	lng, err2 := strconv.ParseFloat(q.Get("lng"), 64)
	if err1 != nil || err2 != nil {
		return nil, errs.Invalid("point_invalid", "Toạ độ không hợp lệ.")
	}
	return &geo.Point{Lat: lat, Lng: lng}, nil
}

func (h *Handler) autocomplete(w http.ResponseWriter, r *http.Request) {
	near, err := pointParam(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out, err := h.svc.Autocomplete(r.Context(), r.URL.Query().Get("q"), near)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Detail(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) reverse(w http.ResponseWriter, r *http.Request) {
	p, err := pointParam(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if p == nil {
		httpx.Fail(w, r, errs.Invalid("point_required", "Cần lat và lng."))
		return
	}
	out, err := h.svc.Reverse(r.Context(), *p)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
