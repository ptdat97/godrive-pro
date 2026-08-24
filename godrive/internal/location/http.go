package location

import (
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
)

type Handler struct {
	svc      *Service
	auth     *authn.Issuer
	resolver func(r *http.Request) (string, error)
}

func NewHandler(s *Service, a *authn.Issuer, resolver func(r *http.Request) (string, error)) *Handler {
	return &Handler{svc: s, auth: a, resolver: resolver}
}

// Register: endpoint HTTP này chỉ để test và dự phòng.
// Luồng chính là MQTT — HTTP mỗi 4 giây/tài xế sẽ quá tốn pin và băng thông.
func (h *Handler) Register(mux *http.ServeMux) {
	drv := h.auth.Require(authn.RoleDriver)
	mux.Handle("POST /v1/locations/ping", drv(http.HandlerFunc(h.ping)))
}

func (h *Handler) ping(w http.ResponseWriter, r *http.Request) {
	var p Ping
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	did, err := h.resolver(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	p.DriverID = did
	if err := h.svc.Ingest(r.Context(), p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "ok"})
}
