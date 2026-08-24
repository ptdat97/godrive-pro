package pricing

import (
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/geo"
)

type Handler struct {
	svc  *Service
	auth *authn.Issuer
}

func NewHandler(s *Service, a *authn.Issuer) *Handler { return &Handler{svc: s, auth: a} }

func (h *Handler) Register(mux *http.ServeMux) {
	rider := h.auth.Require(authn.RoleRider)
	mux.Handle("POST /v1/quotes", rider(http.HandlerFunc(h.estimate)))
}

type estimateReq struct {
	Pickup      geo.Point `json:"pickup"`
	Dropoff     geo.Point `json:"dropoff"`
	VehicleType string    `json:"vehicle_type,omitempty"`
}

func (h *Handler) estimate(w http.ResponseWriter, r *http.Request) {
	var req estimateReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	quotes, err := h.svc.EstimateAll(r.Context(), req.Pickup, req.Dropoff)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"quotes": quotes})
}
