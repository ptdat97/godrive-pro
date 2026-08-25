package identity

import (
	"context"
	"net/http"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
)

// TokenRevoker thu hồi token. Port khai báo ở đây (bên tiêu thụ).
type TokenRevoker interface {
	RevokeToken(ctx context.Context, c *authn.Claims, now time.Time) error
	RevokeAccount(ctx context.Context, accountID string, now time.Time, maxTTL time.Duration) error
}

type Handler struct {
	svc     *Service
	auth    *authn.Issuer
	revoker TokenRevoker
}

func NewHandler(s *Service, a *authn.Issuer, r TokenRevoker) *Handler {
	return &Handler{svc: s, auth: a, revoker: r}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/otp", h.requestOTP)
	mux.HandleFunc("POST /v1/auth/verify", h.verifyOTP)

	// Đăng xuất cần token hợp lệ để biết thu hồi cái nào.
	any := h.auth.Require(authn.RoleRider, authn.RoleDriver, authn.RoleAdmin)
	mux.Handle("POST /v1/auth/logout", any(http.HandlerFunc(h.logout)))
}

type logoutReq struct {
	// AllDevices thu hồi mọi token của tài khoản, không chỉ token hiện tại.
	// Dùng khi nghi ngờ token bị lộ.
	AllDevices bool `json:"all_devices"`
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if h.revoker == nil {
		// Không có nơi lưu danh sách thu hồi thì đăng xuất chỉ là xoá token ở
		// phía client — nói thẳng thay vì trả 200 rồi để người dùng tưởng an toàn.
		httpx.Fail(w, r, errs.E(errs.KindInternal, "revocation_unavailable",
			"Máy chủ chưa cấu hình thu hồi phiên đăng nhập."))
		return
	}
	var req logoutReq
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	c := authn.MustClaims(r.Context())
	now := time.Now().UTC()
	var err error
	if req.AllDevices {
		err = h.revoker.RevokeAccount(r.Context(), c.Sub, now, 30*24*time.Hour)
	} else {
		err = h.revoker.RevokeToken(r.Context(), c, now)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"revoked": true, "all_devices": req.AllDevices})
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
