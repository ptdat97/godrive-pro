package matching

import (
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
)

type Handler struct {
	eng      *Engine
	auth     *authn.Issuer
	driverID func(r *http.Request) (string, error)
}

func NewHandler(e *Engine, a *authn.Issuer, driverID func(r *http.Request) (string, error)) *Handler {
	return &Handler{eng: e, auth: a, driverID: driverID}
}

func (h *Handler) Register(mux *http.ServeMux) {
	drv := h.auth.Require(authn.RoleDriver)
	mux.Handle("GET /v1/offers", drv(http.HandlerFunc(h.pending)))
	mux.Handle("POST /v1/offers/{id}/accept", drv(http.HandlerFunc(h.accept)))
	mux.Handle("POST /v1/offers/{id}/reject", drv(http.HandlerFunc(h.reject)))
}

func (h *Handler) pending(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	offers, err := h.eng.PendingOffers(r.Context(), did)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"offers": offers})
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	t, err := h.eng.Accept(r.Context(), r.PathValue("id"), did)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.eng.Reject(r.Context(), r.PathValue("id"), did); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}
