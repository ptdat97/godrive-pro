package refund

import (
	"net/http"
	"strconv"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
)

type Handler struct {
	svc  *Service
	auth *authn.Issuer
}

func NewHandler(s *Service, a *authn.Issuer) *Handler { return &Handler{svc: s, auth: a} }

// Register: mọi route hoàn tiền chỉ dành cho quản trị viên. Khách không tự xin
// hoàn qua API — khiếu nại đi qua tổng đài, vận hành mới là người tạo yêu cầu.
func (h *Handler) Register(mux *http.ServeMux) {
	adm := h.auth.Require(authn.RoleAdmin)
	mux.Handle("POST /v1/admin/refunds", adm(http.HandlerFunc(h.request)))
	mux.Handle("GET /v1/admin/refunds", adm(http.HandlerFunc(h.list)))
	mux.Handle("GET /v1/admin/refunds/{id}", adm(http.HandlerFunc(h.get)))
	mux.Handle("POST /v1/admin/refunds/{id}/approve", adm(http.HandlerFunc(h.approve)))
	mux.Handle("POST /v1/admin/refunds/{id}/reject", adm(http.HandlerFunc(h.reject)))
}

func (h *Handler) request(w http.ResponseWriter, r *http.Request) {
	var in RequestInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out, err := h.svc.Request(r.Context(), authn.MustClaims(r.Context()).Sub, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	rs, err := h.svc.List(r.Context(), ListFilter{
		Status: Status(q.Get("status")), TripID: q.Get("trip_id"), Limit: limit,
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"refunds": rs, "count": len(rs)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type decisionReq struct {
	Note string `json:"note"`
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	var req decisionReq
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	out, err := h.svc.Approve(r.Context(), authn.MustClaims(r.Context()).Sub, r.PathValue("id"), req.Note)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	var req decisionReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	out, err := h.svc.Reject(r.Context(), authn.MustClaims(r.Context()).Sub, r.PathValue("id"), req.Note)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
