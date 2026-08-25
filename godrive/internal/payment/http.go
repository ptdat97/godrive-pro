package payment

import (
	"io"
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// MaxWebhookBody giới hạn thân request webhook.
const MaxWebhookBody = 64 << 10

type Handler struct {
	svc      *Service
	auth     *authn.Issuer
	driverID func(*http.Request) (string, error)
}

func NewHandler(s *Service, a *authn.Issuer, resolver func(*http.Request) (string, error)) *Handler {
	return &Handler{svc: s, auth: a, driverID: resolver}
}

func (h *Handler) Register(mux *http.ServeMux) {
	drv := h.auth.Require(authn.RoleDriver)
	mux.Handle("POST /v1/payments/topup", drv(http.HandlerFunc(h.createTopUp)))
	mux.Handle("GET /v1/payments/history", drv(http.HandlerFunc(h.history)))

	// Webhook KHÔNG có middleware xác thực: cổng thanh toán không đăng nhập
	// được. Chữ ký HMAC là thứ duy nhất phân biệt thông báo thật với một
	// request bất kỳ ai cũng gửi được — xem Service.HandleWebhook.
	mux.HandleFunc("POST /v1/payments/webhook/{provider}", h.webhook)
}

type topUpReq struct {
	Provider ProviderName `json:"provider"`
	Amount   money.VND    `json:"amount"`
}

func (h *Handler) createTopUp(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var req topUpReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	t, err := h.svc.CreateTopUpIntent(r.Context(), req.Provider, did, req.Amount)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	txs, err := h.svc.History(r.Context(), did, 50)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"payments": txs, "count": len(txs)})
}

func (h *Handler) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxWebhookBody))
	if err != nil {
		httpx.Fail(w, r, errs.Invalid("invalid_body", "Không đọc được nội dung."))
		return
	}
	// VNPay gửi tham số trên query string chứ không phải body.
	header := map[string]string{"X-Query-String": r.URL.RawQuery}

	ack, err := h.svc.HandleWebhook(r.Context(), ProviderName(r.PathValue("provider")), body, header)
	if err != nil {
		// Chữ ký sai là sự kiện AN NINH, không phải lỗi thường: ghi log đầy đủ
		// để phát hiện ai đang dò webhook.
		if code := errs.CodeOf(err); code == "webhook_bad_signature" ||
			code == "webhook_wrong_partner" || code == "webhook_amount_mismatch" {
			logger.From(r.Context()).Error("webhook bị từ chối",
				"code", code, "provider", r.PathValue("provider"),
				"remote", r.RemoteAddr, "len", len(body))
		}
		httpx.Fail(w, r, err)
		return
	}
	if len(ack) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ack)
}
