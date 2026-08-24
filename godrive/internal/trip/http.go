package trip

import (
	"net/http"
	"strconv"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
)

type Handler struct {
	svc      *Service
	auth     *authn.Issuer
	driverID func(r *http.Request) (string, error)
}

func NewHandler(s *Service, a *authn.Issuer, driverID func(r *http.Request) (string, error)) *Handler {
	return &Handler{svc: s, auth: a, driverID: driverID}
}

func (h *Handler) Register(mux *http.ServeMux) {
	rider := h.auth.Require(authn.RoleRider)
	drv := h.auth.Require(authn.RoleDriver)
	any := h.auth.Require(authn.RoleRider, authn.RoleDriver)

	mux.Handle("POST /v1/trips", rider(http.HandlerFunc(h.create)))
	mux.Handle("GET /v1/trips", rider(http.HandlerFunc(h.list)))
	mux.Handle("GET /v1/trips/{id}", any(http.HandlerFunc(h.get)))
	mux.Handle("GET /v1/trips/{id}/events", any(http.HandlerFunc(h.events)))
	mux.Handle("POST /v1/trips/{id}/cancel", any(http.HandlerFunc(h.cancel)))
	mux.Handle("POST /v1/trips/{id}/rate", rider(http.HandlerFunc(h.rate)))

	mux.Handle("POST /v1/trips/{id}/arrived", drv(http.HandlerFunc(h.arrived)))
	mux.Handle("POST /v1/trips/{id}/start", drv(http.HandlerFunc(h.start)))
	mux.Handle("POST /v1/trips/{id}/complete", drv(http.HandlerFunc(h.complete)))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	in.RiderID = authn.MustClaims(r.Context()).Sub
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	t, err := h.svc.Create(r.Context(), in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.authorize(r, t); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	ts, err := h.svc.ListByRider(r.Context(), authn.MustClaims(r.Context()).Sub, limit)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"trips": ts})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.authorize(r, t); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	evs, err := h.svc.Events(r.Context(), t.ID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": evs})
}

type cancelReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	var req cancelReq
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	c := authn.MustClaims(r.Context())
	in := CancelInput{TripID: r.PathValue("id"), Reason: req.Reason}
	if c.Role == authn.RoleDriver {
		did, err := h.driverID(r)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		in.By, in.Actor = CancelByDriver, did
	} else {
		in.By, in.Actor = CancelByRider, c.Sub
	}
	t, err := h.svc.Cancel(r.Context(), in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

type rateReq struct {
	Rating int `json:"rating"`
}

func (h *Handler) rate(w http.ResponseWriter, r *http.Request) {
	var req rateReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	t, err := h.svc.Rate(r.Context(), r.PathValue("id"),
		authn.MustClaims(r.Context()).Sub, req.Rating)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (h *Handler) arrived(w http.ResponseWriter, r *http.Request) {
	h.driverAction(w, r, h.svc.MarkArrived)
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	h.driverAction(w, r, h.svc.Start)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	h.driverAction(w, r, h.svc.Complete)
}

func (h *Handler) driverAction(w http.ResponseWriter, r *http.Request, fn func(ctx contextT, tripID, driverID string) (*Trip, error)) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	t, err := fn(r.Context(), r.PathValue("id"), did)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (h *Handler) authorize(r *http.Request, t *Trip) error {
	c := authn.MustClaims(r.Context())
	if c.Role == authn.RoleRider && t.RiderID == c.Sub {
		return nil
	}
	if c.Role == authn.RoleDriver {
		did, err := h.driverID(r)
		if err == nil && t.DriverID != nil && *t.DriverID == did {
			return nil
		}
	}
	return errs.E(errs.KindForbidden, "not_your_trip", "Bạn không có quyền xem chuyến này.")
}
