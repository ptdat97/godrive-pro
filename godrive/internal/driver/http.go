package driver

import (
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
)

type Handler struct {
	svc  *Service
	auth *authn.Issuer
}

func NewHandler(s *Service, a *authn.Issuer) *Handler { return &Handler{svc: s, auth: a} }

// Register gắn route. Mọi endpoint đều yêu cầu vai trò driver.
func (h *Handler) Register(mux *http.ServeMux) {
	drv := h.auth.Require(authn.RoleDriver)
	mux.Handle("POST /v1/drivers/register", drv(http.HandlerFunc(h.register)))
	mux.Handle("GET /v1/drivers/me", drv(http.HandlerFunc(h.me)))
	mux.Handle("POST /v1/drivers/me/online", drv(http.HandlerFunc(h.online)))
	mux.Handle("POST /v1/drivers/me/offline", drv(http.HandlerFunc(h.offline)))
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in OnboardInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	in.AccountID = authn.MustClaims(r.Context()).Sub
	d, err := h.svc.Onboard(r.Context(), in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.GetByAccount(r.Context(), authn.MustClaims(r.Context()).Sub)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

func (h *Handler) online(w http.ResponseWriter, r *http.Request)  { h.toggle(w, r, true) }
func (h *Handler) offline(w http.ResponseWriter, r *http.Request) { h.toggle(w, r, false) }

func (h *Handler) toggle(w http.ResponseWriter, r *http.Request, on bool) {
	d, err := h.svc.GetByAccount(r.Context(), authn.MustClaims(r.Context()).Sub)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if on {
		err = h.svc.GoOnline(r.Context(), d.ID)
	} else {
		err = h.svc.GoOffline(r.Context(), d.ID)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"online": on})
}
