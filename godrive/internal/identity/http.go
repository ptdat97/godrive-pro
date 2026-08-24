package identity

import (
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
)

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/otp", h.requestOTP)
	mux.HandleFunc("POST /v1/auth/verify", h.verifyOTP)
}

type requestOTPReq struct {
	Phone string     `json:"phone"`
	Role  authn.Role `json:"role"`
}

type requestOTPResp struct {
	ChallengeID string `json:"challenge_id"`
	DevCode     string `json:"dev_code,omitempty"`
}

func (h *Handler) requestOTP(w http.ResponseWriter, r *http.Request) {
	var req requestOTPReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.Role != authn.RoleRider && req.Role != authn.RoleDriver {
		httpx.Fail(w, r, errs.Invalid("role_invalid", "Vai trò không hợp lệ."))
		return
	}
	cid, code, err := h.svc.RequestOTP(r.Context(), req.Phone, req.Role)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, requestOTPResp{ChallengeID: cid, DevCode: code})
}

type verifyReq struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	DeviceID    string `json:"device_id"`
}

func (h *Handler) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	tp, err := h.svc.VerifyOTP(r.Context(), req.ChallengeID, req.Code, req.DeviceID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tp)
}
