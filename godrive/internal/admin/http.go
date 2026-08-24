package admin

import (
	"net/http"
	"strconv"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/geo"
)

type Handler struct {
	svc   *Service
	auth  *authn.Issuer
	login *Auth
}

func NewHandler(s *Service, a *authn.Issuer, login *Auth) *Handler {
	return &Handler{svc: s, auth: a, login: login}
}

// Register gắn route. Trừ hai endpoint đăng nhập, mọi endpoint đều yêu cầu
// vai trò admin.
func (h *Handler) Register(mux *http.ServeMux) {
	adm := h.auth.Require(authn.RoleAdmin)

	mux.HandleFunc("POST /v1/admin/auth/otp", h.authOTP)
	mux.HandleFunc("POST /v1/admin/auth/verify", h.authVerify)

	mux.Handle("GET /v1/admin/me", adm(http.HandlerFunc(h.me)))
	mux.Handle("GET /v1/admin/overview", adm(http.HandlerFunc(h.overview)))
	mux.Handle("GET /v1/admin/drivers", adm(http.HandlerFunc(h.listDrivers)))
	mux.Handle("GET /v1/admin/drivers/{id}", adm(http.HandlerFunc(h.getDriver)))
	mux.Handle("POST /v1/admin/drivers/{id}/kyc", adm(http.HandlerFunc(h.reviewKYC)))
	mux.Handle("GET /v1/admin/trips", adm(http.HandlerFunc(h.listTrips)))
	mux.Handle("GET /v1/admin/trips/{id}", adm(http.HandlerFunc(h.getTrip)))
	mux.Handle("GET /v1/admin/trips/{id}/events", adm(http.HandlerFunc(h.tripEvents)))
	mux.Handle("GET /v1/admin/live-map", adm(http.HandlerFunc(h.liveMap)))
	mux.Handle("GET /v1/admin/audit", adm(http.HandlerFunc(h.audit)))
}

type authOTPReq struct {
	Phone string `json:"phone"`
}

type authOTPResp struct {
	ChallengeID string `json:"challenge_id"`
	DevCode     string `json:"dev_code,omitempty"`
}

func (h *Handler) authOTP(w http.ResponseWriter, r *http.Request) {
	var req authOTPReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	cid, code, err := h.login.RequestOTP(r.Context(), req.Phone)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, authOTPResp{ChallengeID: cid, DevCode: code})
}

type authVerifyReq struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	DeviceID    string `json:"device_id"`
}

func (h *Handler) authVerify(w http.ResponseWriter, r *http.Request) {
	var req authVerifyReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	tp, err := h.login.VerifyOTP(r.Context(), req.ChallengeID, req.Code, req.DeviceID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tp)
}

// me trả thông tin phiên hiện tại — giao diện dùng để xác thực token còn sống.
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	c := authn.MustClaims(r.Context())
	httpx.JSON(w, http.StatusOK, map[string]any{
		"account_id": c.Sub,
		"role":       c.Role,
		"expires_at": c.Exp,
	})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ov, err := h.svc.Overview(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ov)
}

func (h *Handler) listDrivers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := h.svc.ListDrivers(r.Context(), ListDriversInput{
		Status:   q.Get("status"),
		KYC:      q.Get("kyc"),
		City:     q.Get("city"),
		Query:    q.Get("q"),
		OnlyDebt: q.Get("debt") == "1",
		Limit:    atoiOr(q.Get("limit"), 0),
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"drivers": rows, "count": len(rows)})
}

func (h *Handler) getDriver(w http.ResponseWriter, r *http.Request) {
	row, err := h.svc.GetDriver(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, row)
}

type reviewKYCReq struct {
	Approved bool `json:"approved"`
}

func (h *Handler) reviewKYC(w http.ResponseWriter, r *http.Request) {
	var req reviewKYCReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	c := authn.MustClaims(r.Context())
	row, err := h.svc.ReviewKYC(r.Context(), Actor{AccountID: c.Sub}, r.PathValue("id"), req.Approved)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, row)
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries, err := h.svc.Audit(r.Context(), AuditFilter{
		ActorID:    q.Get("actor"),
		TargetType: q.Get("target_type"),
		TargetID:   q.Get("target_id"),
		Limit:      atoiOr(q.Get("limit"), 0),
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries, "count": len(entries)})
}

func (h *Handler) listTrips(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := h.svc.ListTrips(r.Context(), ListTripsInput{
		Status: q.Get("status"),
		Limit:  atoiOr(q.Get("limit"), 0),
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"trips": rows, "count": len(rows)})
}

func (h *Handler) getTrip(w http.ResponseWriter, r *http.Request) {
	row, err := h.svc.GetTrip(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, row)
}

func (h *Handler) tripEvents(w http.ResponseWriter, r *http.Request) {
	evs, err := h.svc.TripEvents(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": evs})
}

func (h *Handler) liveMap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.svc.LiveMap(r.Context(), LiveMapInput{
		Center: geo.Point{
			Lat: atofOr(q.Get("lat"), DefaultMapLat),
			Lng: atofOr(q.Get("lng"), DefaultMapLng),
		},
		RadiusM:  atofOr(q.Get("radius"), DefaultMapRadiusM),
		OnlyIdle: q.Get("idle") == "1",
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func atofOr(s string, def float64) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return def
}
