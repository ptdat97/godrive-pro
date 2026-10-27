package refund

import (
	"context"
	"strings"

	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// Hành động ghi vào nhật ký quản trị.
const (
	ActionRequest = "request_refund"
	ActionApprove = "approve_refund"
	ActionReject  = "reject_refund"
)

type Service struct {
	store     Store
	trips     TripPort
	ledger    LedgerPort
	audit     Auditor
	clk       clock.Clock
	newID     func(string) string
	threshold ThresholdProvider
}

func NewService(s Store, t TripPort, l LedgerPort, a Auditor, clk clock.Clock, newID func(string) string) *Service {
	return &Service{store: s, trips: t, ledger: l, audit: a, clk: clk, newID: newID}
}

// UseThreshold nối ngưỡng tự duyệt động (trang cấu hình, nhóm wallet).
func (s *Service) UseThreshold(p ThresholdProvider) { s.threshold = p }

func (s *Service) selfApproveMax(ctx context.Context) money.VND {
	if s.threshold != nil {
		return s.threshold(ctx)
	}
	return DefaultSelfApproveMax
}

type RequestInput struct {
	TripID   string          `json:"trip_id"`
	Amount   money.VND       `json:"amount"`
	Reason   string          `json:"reason"`
	ChargeTo wallet.ChargeTo `json:"charge_to"` // rỗng = PLATFORM
}

// Request tạo yêu cầu hoàn. Khoản đến ngưỡng được người xin tự duyệt và ghi
// sổ ngay; khoản trên ngưỡng nằm chờ người thứ hai.
func (s *Service) Request(ctx context.Context, actor string, in RequestInput) (*Refund, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if len([]rune(in.Reason)) < 5 {
		return nil, errs.Invalid("reason_required", "Vui lòng ghi lý do hoàn tiền (ít nhất 5 ký tự).")
	}
	if in.Amount <= 0 {
		return nil, errs.Invalid("amount_invalid", "Số tiền hoàn phải lớn hơn 0.")
	}
	if in.ChargeTo == "" {
		in.ChargeTo = wallet.ChargePlatform
	}
	if !in.ChargeTo.Valid() {
		return nil, errs.Invalid("charge_to_invalid", "Bên chịu khoản hoàn phải là PLATFORM hoặc DRIVER.")
	}
	t, err := s.trips.Get(ctx, in.TripID)
	if err != nil {
		return nil, err
	}
	max, err := s.refundable(ctx, t)
	if err != nil {
		return nil, err
	}
	driverID := ""
	if t.DriverID != nil {
		driverID = *t.DriverID
	}
	if in.ChargeTo == wallet.ChargeDriver && driverID == "" {
		return nil, errs.Invalid("refund_no_driver", "Chuyến không có tài xế để trừ lại.")
	}

	now := s.clk.Now()
	r := &Refund{
		ID: s.newID("rfd"), TripID: t.ID, RiderID: t.RiderID, DriverID: driverID,
		Amount: in.Amount, Reason: in.Reason, ChargeTo: in.ChargeTo,
		Status: StatusPending, RequestedBy: actor, CreatedAt: now,
	}
	if in.Amount <= s.selfApproveMax(ctx) {
		r.Status, r.SelfApproved, r.DecidedBy, r.DecidedAt = StatusApproved, true, actor, &now
	}
	if err := s.store.Create(ctx, r, max); err != nil {
		return nil, err
	}
	if err := s.record(ctx, actor, ActionRequest, r, map[string]any{
		"self_approved": r.SelfApproved, "max_refundable": max,
	}); err != nil {
		return nil, err
	}
	if r.Status == StatusApproved {
		if err := s.post(ctx, r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// refundable là trần có thể hoàn của một chuyến: cước với chuyến đã xong, phí
// huỷ với chuyến bị huỷ có thu phí. Trừ đi các khoản đã xin trước đó là việc
// của Store.Create, nguyên tử.
func (s *Service) refundable(ctx context.Context, t *trip.Trip) (money.VND, error) {
	switch t.Status {
	case trip.StatusCompleted, trip.StatusPaid:
		return t.Fare, nil
	case trip.StatusCancelled:
		evs, err := s.trips.Events(ctx, t.ID)
		if err != nil {
			return 0, err
		}
		for _, e := range evs {
			if e.To == trip.StatusCancelled {
				if fee := toVND(e.Meta["cancel_fee"]); fee > 0 {
					return fee, nil
				}
			}
		}
		return 0, errs.Conflict("trip_not_refundable", "Chuyến bị huỷ không thu phí — không có gì để hoàn.")
	}
	return 0, errs.Conflict("trip_not_refundable", "Chỉ hoàn được cho chuyến đã hoàn tất hoặc chuyến huỷ có thu phí.")
}

// toVND đọc số tiền trong siêu dữ liệu sự kiện. Bản bộ nhớ giữ int64, bản
// Postgres đọc lại từ JSONB nên thành float64.
func toVND(v any) money.VND {
	switch n := v.(type) {
	case int64:
		return money.VND(n)
	case int:
		return money.VND(n)
	case float64:
		return money.VND(n)
	case money.VND:
		return n
	}
	return 0
}

// Approve là cấp duyệt thứ hai. Gọi lại trên khoản đã duyệt mà chưa ghi sổ
// (lần trước lỗi giữa chừng) sẽ ghi bù — không bao giờ ghi hai lần.
func (s *Service) Approve(ctx context.Context, actor, id, note string) (*Refund, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch r.Status {
	case StatusApproved:
		if err := s.post(ctx, r); err != nil {
			return nil, err
		}
		return r, nil
	case StatusRejected:
		return nil, errs.Conflict("refund_already_decided", "Yêu cầu này đã bị từ chối.")
	}
	if actor == r.RequestedBy {
		return nil, errs.E(errs.KindForbidden, "second_approver_required",
			"Khoản vượt ngưỡng tự duyệt cần một người khác duyệt.")
	}
	now := s.clk.Now()
	if err := s.store.Decide(ctx, id, StatusApproved, actor, strings.TrimSpace(note), now); err != nil {
		return nil, err
	}
	r.Status, r.DecidedBy, r.DecisionNote, r.DecidedAt = StatusApproved, actor, strings.TrimSpace(note), &now
	if err := s.record(ctx, actor, ActionApprove, r, nil); err != nil {
		return nil, err
	}
	if err := s.post(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Reject từ chối một yêu cầu đang chờ. Bắt buộc ghi lý do — khách sẽ hỏi.
func (s *Service) Reject(ctx context.Context, actor, id, note string) (*Refund, error) {
	note = strings.TrimSpace(note)
	if len([]rune(note)) < 5 {
		return nil, errs.Invalid("reason_required", "Vui lòng ghi lý do từ chối (ít nhất 5 ký tự).")
	}
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Status != StatusPending {
		return nil, errs.Conflict("refund_already_decided", "Yêu cầu này đã được quyết định.")
	}
	now := s.clk.Now()
	if err := s.store.Decide(ctx, id, StatusRejected, actor, note, now); err != nil {
		return nil, err
	}
	r.Status, r.DecidedBy, r.DecisionNote, r.DecidedAt = StatusRejected, actor, note, &now
	if err := s.record(ctx, actor, ActionReject, r, nil); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Refund, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return r, s.fillPosted(ctx, r)
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]*Refund, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rs, err := s.store.List(ctx, f)
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if err := s.fillPosted(ctx, r); err != nil {
			return nil, err
		}
	}
	return rs, nil
}

func (s *Service) fillPosted(ctx context.Context, r *Refund) error {
	if r.Status != StatusApproved {
		return nil
	}
	ok, err := s.ledger.RefundPosted(ctx, r.ID)
	r.Posted = ok
	return err
}

func (s *Service) post(ctx context.Context, r *Refund) error {
	if err := s.ledger.PostRefund(ctx, r.ID, r.RiderID, r.DriverID, r.TripID, r.Amount, r.ChargeTo); err != nil {
		return err
	}
	r.Posted = true
	return nil
}

func (s *Service) record(ctx context.Context, actor, action string, r *Refund, extra map[string]any) error {
	if s.audit == nil {
		return nil
	}
	meta := map[string]any{
		"trip_id": r.TripID, "amount": r.Amount, "charge_to": r.ChargeTo,
		"reason": r.Reason, "status": r.Status,
	}
	if r.DecisionNote != "" {
		meta["decision_note"] = r.DecisionNote
	}
	for k, v := range extra {
		meta[k] = v
	}
	return s.audit.RecordRefund(ctx, actor, action, r.ID, meta)
}
