package wallet

import (
	"context"
	"net/http"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

// StatementMaxRange chặn việc kéo sao kê cả năm trong một lần gọi.
const StatementMaxRange = 92 * 24 * time.Hour

type Handler struct {
	svc      *Service
	auth     *authn.Issuer
	driverID func(*http.Request) (string, error)
	// debtLimit trả ngưỡng HIỆN HÀNH cho ứng dụng tài xế. Là hàm chứ không phải
	// giá trị vì hạn mức chỉnh được từ bảng điều khiển.
	debtLimit func(ctx context.Context) money.VND
	// devTopUp mở endpoint nạp ví thủ công. CHỈ dùng ở dev.
	devTopUp bool
}

func NewHandler(s *Service, a *authn.Issuer, resolver func(*http.Request) (string, error),
	debtLimit func(ctx context.Context) money.VND, devTopUp bool) *Handler {
	return &Handler{svc: s, auth: a, driverID: resolver, debtLimit: debtLimit, devTopUp: devTopUp}
}

func (h *Handler) Register(mux *http.ServeMux) {
	drv := h.auth.Require(authn.RoleDriver)
	mux.Handle("GET /v1/drivers/me/wallet", drv(http.HandlerFunc(h.wallet)))
	mux.Handle("GET /v1/drivers/me/statement", drv(http.HandlerFunc(h.statement)))

	// Nạp ví thật phải đi qua cổng thanh toán (webhook có xác thực chữ ký).
	// Một endpoint tự ghi có vào ví mà không có đối ứng tiền thật chính là máy
	// in tiền, nên nó CHỈ tồn tại ở chế độ dev.
	if h.devTopUp {
		mux.Handle("POST /v1/drivers/me/topup", drv(http.HandlerFunc(h.topUp)))
	}
}

type walletResp struct {
	Balance    money.VND `json:"balance"`
	CashOnHand money.VND `json:"cash_on_hand"`
	DebtLimit  money.VND `json:"debt_limit"`
	// InDebt = ví âm quá hạn mức -> bị chặn nhận chuyến.
	InDebt bool `json:"in_debt"`
	// AmountToClear là số tiền cần nạp để nhận chuyến trở lại (0 nếu không nợ quá hạn).
	AmountToClear money.VND `json:"amount_to_clear"`
}

func (h *Handler) wallet(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	bal, err := h.svc.DriverBalance(r.Context(), did)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	cash, err := h.svc.CashOnHand(r.Context(), did)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	limit := h.debtLimit(r.Context())
	resp := walletResp{Balance: bal, CashOnHand: cash, DebtLimit: limit}
	if bal < limit.Neg() {
		resp.InDebt = true
		// Nạp đúng chừng này là về lại đúng hạn mức.
		resp.AmountToClear = limit.Neg() - bal
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) statement(w http.ResponseWriter, r *http.Request) {
	did, err := h.driverID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	now := time.Now().UTC()
	from, to := now.Add(-30*24*time.Hour), now
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			httpx.Fail(w, r, errs.Invalid("from_invalid", "Tham số from phải theo định dạng RFC3339."))
			return
		}
	}
	if v := q.Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			httpx.Fail(w, r, errs.Invalid("to_invalid", "Tham số to phải theo định dạng RFC3339."))
			return
		}
	}
	if !to.After(from) {
		httpx.Fail(w, r, errs.Invalid("range_invalid", "Khoảng thời gian không hợp lệ."))
		return
	}
	if to.Sub(from) > StatementMaxRange {
		httpx.Fail(w, r, errs.Invalid("range_too_wide", "Chỉ tra cứu được tối đa 92 ngày mỗi lần."))
		return
	}

	entries, err := h.svc.Statement(r.Context(), did, from, to)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if entries == nil {
		entries = []Entry{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"entries": entries, "from": from, "to": to, "count": len(entries),
	})
}

type topUpReq struct {
	Amount money.VND `json:"amount"`
}

// topUp là đường nạp ví THỦ CÔNG cho môi trường dev. Ở production, tiền vào ví
// chỉ đến từ webhook cổng thanh toán đã xác thực chữ ký.
func (h *Handler) topUp(w http.ResponseWriter, r *http.Request) {
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
	// Mã tham chiếu quyết định TxID, nên nó cũng chính là khoá idempotency:
	// gửi lại cùng Idempotency-Key sẽ không nạp tiền hai lần.
	ref := r.Header.Get("Idempotency-Key")
	if ref == "" {
		ref = id.New("dev")
	}
	if err := h.svc.TopUp(r.Context(), did, "dev:"+ref, req.Amount); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.wallet(w, r)
}
