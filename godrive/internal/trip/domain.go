// Package trip là máy trạng thái chuyến đi — trái tim của hệ thống.
// Mọi chuyển trạng thái đều ghi vào bảng trip_events (append-only) phục vụ
// khiếu nại khách hàng và lưu trữ hợp đồng điện tử theo Nghị định 10/2020.
package trip

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

type Status string

const (
	StatusCreated    Status = "CREATED"
	StatusSearching  Status = "SEARCHING"
	StatusAssigned   Status = "ASSIGNED"
	StatusArrived    Status = "ARRIVED" // tài xế đã tới điểm đón
	StatusInProgress Status = "IN_PROGRESS"
	StatusCompleted  Status = "COMPLETED"
	StatusPaid       Status = "PAID"
	StatusCancelled  Status = "CANCELLED"
	StatusExpired    Status = "EXPIRED" // không tìm được tài xế
)

// transitions định nghĩa đồ thị chuyển trạng thái hợp lệ.
var transitions = map[Status][]Status{
	StatusCreated:    {StatusSearching, StatusCancelled},
	StatusSearching:  {StatusAssigned, StatusCancelled, StatusExpired},
	StatusAssigned:   {StatusArrived, StatusCancelled},
	StatusArrived:    {StatusInProgress, StatusCancelled},
	StatusInProgress: {StatusCompleted},
	StatusCompleted:  {StatusPaid},
	StatusPaid:       {},
	StatusCancelled:  {},
	StatusExpired:    {},
}

func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func (s Status) IsTerminal() bool { return len(transitions[s]) == 0 }

type PaymentMethod string

const (
	PayCash    PaymentMethod = "CASH"
	PayMoMo    PaymentMethod = "MOMO"
	PayZaloPay PaymentMethod = "ZALOPAY"
	PayVNPay   PaymentMethod = "VNPAY"
	PayWallet  PaymentMethod = "WALLET"
)

func (p PaymentMethod) Valid() bool {
	switch p {
	case PayCash, PayMoMo, PayZaloPay, PayVNPay, PayWallet:
		return true
	}
	return false
}

type Place struct {
	Point   geo.Point `json:"point"`
	Address string    `json:"address"`
	Note    string    `json:"note,omitempty"` // "hẻm 123, cổng màu xanh"
}

type CancelBy string

const (
	CancelByRider  CancelBy = "RIDER"
	CancelByDriver CancelBy = "DRIVER"
	CancelBySystem CancelBy = "SYSTEM"
)

type Trip struct {
	ID       string  `json:"id"`
	RiderID  string  `json:"rider_id"`
	DriverID *string `json:"driver_id,omitempty"`

	Pickup      Place              `json:"pickup"`
	Dropoff     Place              `json:"dropoff"`
	VehicleType driver.VehicleType `json:"vehicle_type"`

	QuoteID       string        `json:"quote_id"`
	Fare          money.VND     `json:"fare"`
	PlatformFee   money.VND     `json:"platform_fee"`
	DriverEarn    money.VND     `json:"driver_earn"`
	PaymentMethod PaymentMethod `json:"payment_method"`

	Status   Status    `json:"status"`
	CancelBy *CancelBy `json:"cancel_by,omitempty"`
	Reason   string    `json:"cancel_reason,omitempty"`

	RequestedAt time.Time  `json:"requested_at"`
	AssignedAt  *time.Time `json:"assigned_at,omitempty"`
	ArrivedAt   *time.Time `json:"arrived_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`

	// Rating là điểm khách chấm cho chuyến, 1..5. nil = chưa chấm.
	Rating *int `json:"rating,omitempty"`

	Version   int       `json:"-"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Event là bản ghi bất biến của một lần chuyển trạng thái.
type Event struct {
	ID     string         `json:"id"`
	TripID string         `json:"trip_id"`
	From   Status         `json:"from"`
	To     Status         `json:"to"`
	Actor  string         `json:"actor"` // account id hoặc "system"
	Meta   map[string]any `json:"meta,omitempty"`
	At     time.Time      `json:"at"`
}

func (t *Trip) transition(to Status, now time.Time) error {
	if !CanTransition(t.Status, to) {
		return errs.Conflict("invalid_transition",
			"Không thể chuyển chuyến từ trạng thái "+string(t.Status)+" sang "+string(to)+".")
	}
	t.Status = to
	t.UpdatedAt = now
	return nil
}

// Message là một sự kiện cần phát đi sau khi lưu thành công.
type Message struct {
	Topic   string
	Payload any
}

type Repository interface {
	Create(ctx context.Context, t *Trip) error
	Get(ctx context.Context, id string) (*Trip, error)
	// Save lưu trip + event + các sự kiện msgs trong CÙNG một transaction.
	//
	// Trả về những sự kiện mà NGƯỜI GỌI vẫn phải tự phát:
	//   - Bản Postgres ghi msgs vào outbox rồi trả nil — relay sẽ phát.
	//     Nhờ vậy không còn khe hở "ghi DB xong, chết trước khi publish".
	//   - Bản bộ nhớ không có outbox nên trả lại msgs để người gọi phát ngay.
	//
	// Ai phát sự kiện là chi tiết của tầng lưu trữ, nên nó được quyết định ở
	// đây chứ không phải bằng một câu if rải trong Service.
	Save(ctx context.Context, t *Trip, e Event, msgs ...Message) ([]Message, error)
	ListByRider(ctx context.Context, riderID string, limit int) ([]*Trip, error)
	ActiveByDriver(ctx context.Context, driverID string) (*Trip, error)
	ListByStatus(ctx context.Context, s Status, limit int) ([]*Trip, error)
	Events(ctx context.Context, tripID string) ([]Event, error)
	// SetRating ghi điểm đánh giá. Chỉ ghi được MỘT lần: trả Conflict nếu
	// chuyến đã có điểm — khách không được sửa đánh giá để trả thù tài xế.
	SetRating(ctx context.Context, tripID string, rating int) error
}
