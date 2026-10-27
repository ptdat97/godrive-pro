// Package refund là quy trình hoàn tiền cho khách: ai xin, ai duyệt, hoàn
// bao nhiêu, ai chịu.
//
// Chốt 2026-10-27 (docs/07 T-36): hoàn về VÍ GODRIVE của khách, HAI CẤP DUYỆT —
// khoản đến ngưỡng (cấu hình wallet.refund_self_approve_max_vnd) thì người xin
// tự duyệt luôn; khoản trên ngưỡng cần người thứ hai.
//
// Tiền không nằm ở đây. Bảng refunds là quy trình; tiền là bút toán đảo trong
// sổ cái (wallet.PostRefund), không sửa bút toán cũ của chuyến.
package refund

import (
	"context"
	"time"

	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/money"
)

// Status là trạng thái của một yêu cầu hoàn tiền.
type Status string

const (
	StatusPending  Status = "PENDING"
	StatusApproved Status = "APPROVED"
	StatusRejected Status = "REJECTED"
)

// Refund là một yêu cầu hoàn tiền.
type Refund struct {
	ID       string          `json:"id"`
	TripID   string          `json:"trip_id"`
	RiderID  string          `json:"rider_id"`
	DriverID string          `json:"driver_id,omitempty"`
	Amount   money.VND       `json:"amount"`
	Reason   string          `json:"reason"`
	ChargeTo wallet.ChargeTo `json:"charge_to"`
	Status   Status          `json:"status"`

	RequestedBy string `json:"requested_by"`
	// SelfApproved: khoản dưới ngưỡng, người xin tự duyệt.
	SelfApproved bool       `json:"self_approved"`
	DecidedBy    string     `json:"decided_by,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	// Posted: bút toán đã nằm trong sổ cái. APPROVED mà chưa Posted là lần ghi
	// sổ trước bị lỗi giữa chừng — duyệt lại sẽ ghi bù, không ghi hai lần.
	Posted bool `json:"posted"`
}

// Store lưu yêu cầu hoàn tiền.
type Store interface {
	// Create lưu yêu cầu mới, NGUYÊN TỬ với phép kiểm "tổng các khoản chưa bị
	// từ chối của chuyến + khoản này ≤ max". Kiểm rồi mới ghi ở hai bước
	// riêng thì hai vận hành bấm cùng lúc sẽ cùng hoàn trọn cước.
	Create(ctx context.Context, r *Refund, max money.VND) error
	Get(ctx context.Context, id string) (*Refund, error)
	// Decide chuyển PENDING → to. Trả Conflict nếu đã có người quyết trước.
	Decide(ctx context.Context, id string, to Status, by, note string, at time.Time) error
	List(ctx context.Context, f ListFilter) ([]*Refund, error)
}

type ListFilter struct {
	Status Status // rỗng = mọi trạng thái
	TripID string
	Limit  int
}

// TripPort đọc chuyến cần hoàn.
type TripPort interface {
	Get(ctx context.Context, id string) (*trip.Trip, error)
	Events(ctx context.Context, tripID string) ([]trip.Event, error)
}

// LedgerPort ghi và kiểm bút toán hoàn tiền.
type LedgerPort interface {
	PostRefund(ctx context.Context, refundID, riderID, driverID, tripID string,
		amount money.VND, by wallet.ChargeTo) error
	RefundPosted(ctx context.Context, refundID string) (bool, error)
}

// Auditor ghi thao tác vào nhật ký quản trị chung.
type Auditor interface {
	RecordRefund(ctx context.Context, actorID, action, refundID string, meta map[string]any) error
}

// ThresholdProvider trả ngưỡng tự duyệt hiện hành.
type ThresholdProvider func(ctx context.Context) money.VND

// DefaultSelfApproveMax là ngưỡng tự duyệt khi chưa nối cấu hình.
const DefaultSelfApproveMax money.VND = 50_000
