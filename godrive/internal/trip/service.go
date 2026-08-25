package trip

import (
	"context"
	"time"

	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/idem"
)

// FreeCancelWindow là cửa sổ huỷ miễn phí MẶC ĐỊNH: khách huỷ trong 2 phút đầu
// sau khi ghép thì không mất phí. Giá trị thực tế lấy từ cấu hình.
const FreeCancelWindow = 2 * time.Minute

// CancelFeeVND là phí huỷ trễ MẶC ĐỊNH, ghi có cho tài xế.
const CancelFeeVND = 10000

// CancelPolicy là chính sách huỷ chuyến, chỉnh được ở bảng điều khiển.
type CancelPolicy struct {
	FreeWindow time.Duration
	FeeVND     int64
}

// CancelPolicyProvider trả chính sách hiện hành.
type CancelPolicyProvider func(ctx context.Context) CancelPolicy

type PricingPort interface {
	GetQuote(ctx context.Context, quoteID string) (pricing.Quote, error)
}

type Service struct {
	repo    Repository
	pricing PricingPort
	bus     eventbus.Bus
	idem    idem.Store
	clk     clock.Clock
	policy  CancelPolicyProvider
}

// UseCancelPolicy nối nguồn chính sách huỷ động.
func (s *Service) UseCancelPolicy(p CancelPolicyProvider) { s.policy = p }

func (s *Service) cancelPolicy(ctx context.Context) CancelPolicy {
	if s.policy != nil {
		return s.policy(ctx)
	}
	return CancelPolicy{FreeWindow: FreeCancelWindow, FeeVND: CancelFeeVND}
}

func NewService(repo Repository, p PricingPort, bus eventbus.Bus, is idem.Store, clk clock.Clock) *Service {
	return &Service{repo: repo, pricing: p, bus: bus, idem: is, clk: clk}
}

type CreateInput struct {
	RiderID        string        `json:"-"`
	QuoteID        string        `json:"quote_id"`
	Pickup         Place         `json:"pickup"`
	Dropoff        Place         `json:"dropoff"`
	PaymentMethod  PaymentMethod `json:"payment_method"`
	IdempotencyKey string        `json:"-"`
}

// Create tạo chuyến từ một báo giá đã phát hành. Idempotent theo header
// Idempotency-Key: app mobile retry khi mạng chập chờn sẽ không tạo 2 chuyến.
func (s *Service) Create(ctx context.Context, in CreateInput) (t *Trip, retErr error) {
	if !in.PaymentMethod.Valid() {
		return nil, errs.Invalid("payment_method_invalid", "Phương thức thanh toán không hợp lệ.")
	}
	// Nhả khoá idempotency nếu tạo chuyến thất bại. Không có bước này thì một
	// lần lỗi (báo giá hết hạn, mạng đứt giữa chừng) sẽ khoá chết khoá đó suốt
	// 24 giờ: client sửa lỗi rồi gửi lại vẫn nhận request_in_flight.
	idemKey := ""
	if in.IdempotencyKey != "" {
		idemKey = "trip:create:" + in.IdempotencyKey
		rec, existed, err := s.idem.Reserve(ctx, idemKey, 24*time.Hour)
		if err != nil {
			return nil, err
		}
		if existed {
			if len(rec.Response) > 0 {
				return s.repo.Get(ctx, string(rec.Response))
			}
			return nil, errs.Conflict("request_in_flight", "Yêu cầu đang được xử lý, vui lòng chờ.")
		}
		defer func() {
			if retErr != nil {
				_ = s.idem.Release(ctx, idemKey)
			}
		}()
	}

	q, err := s.pricing.GetQuote(ctx, in.QuoteID)
	if err != nil {
		return nil, err
	}

	now := s.clk.Now()
	t = &Trip{
		ID:            id.New("trp"),
		RiderID:       in.RiderID,
		Pickup:        in.Pickup,
		Dropoff:       in.Dropoff,
		VehicleType:   q.VehicleType,
		QuoteID:       q.ID,
		Fare:          q.Total,
		PlatformFee:   q.PlatformFee,
		DriverEarn:    q.DriverEarn,
		PaymentMethod: in.PaymentMethod,
		Status:        StatusCreated,
		RequestedAt:   now,
		UpdatedAt:     now,
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	if err := s.apply(ctx, t, StatusSearching, in.RiderID, nil, Message{
		Topic: eventbus.TopicTripRequested,
		Payload: map[string]any{
			"trip_id": t.ID, "pickup": t.Pickup.Point, "vehicle_type": t.VehicleType,
		},
	}); err != nil {
		return nil, err
	}
	if idemKey != "" {
		_ = s.idem.Complete(ctx, idemKey, []byte(t.ID))
	}
	return t, nil
}

// Assign gắn tài xế vào chuyến. Chỉ dispatcher gọi, sau khi đã Reserve tài xế.
func (s *Service) Assign(ctx context.Context, tripID, driverID string) (*Trip, error) {
	t, err := s.repo.Get(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusSearching {
		return nil, errs.Conflict("trip_not_searching", "Chuyến này không còn chờ ghép tài xế.")
	}
	now := s.clk.Now()
	t.DriverID = &driverID
	t.AssignedAt = &now
	if err := s.apply(ctx, t, StatusAssigned, "system", map[string]any{"driver_id": driverID}, Message{
		Topic:   eventbus.TopicTripAssigned,
		Payload: map[string]any{"trip_id": t.ID, "driver_id": driverID},
	}); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) MarkArrived(ctx context.Context, tripID, driverID string) (*Trip, error) {
	t, err := s.getForDriver(ctx, tripID, driverID)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	t.ArrivedAt = &now
	if err := s.apply(ctx, t, StatusArrived, driverID, nil); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) Start(ctx context.Context, tripID, driverID string) (*Trip, error) {
	t, err := s.getForDriver(ctx, tripID, driverID)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	t.StartedAt = &now
	if err := s.apply(ctx, t, StatusInProgress, driverID, nil, Message{
		Topic:   eventbus.TopicTripStarted,
		Payload: map[string]any{"trip_id": t.ID, "driver_id": driverID},
	}); err != nil {
		return nil, err
	}
	return t, nil
}

// Complete kết thúc chuyến. Ghi sổ tiền do worker xử lý qua sự kiện
// trip.completed để không giữ transaction dài.
func (s *Service) Complete(ctx context.Context, tripID, driverID string) (*Trip, error) {
	t, err := s.getForDriver(ctx, tripID, driverID)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	t.EndedAt = &now
	if err := s.apply(ctx, t, StatusCompleted, driverID, nil, Message{
		Topic: eventbus.TopicTripCompleted,
		Payload: map[string]any{
			"trip_id":        t.ID,
			"driver_id":      driverID,
			"rider_id":       t.RiderID,
			"fare":           t.Fare.Int64(),
			"platform_fee":   t.PlatformFee.Int64(),
			"payment_method": string(t.PaymentMethod),
		},
	}); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) MarkPaid(ctx context.Context, tripID string) (*Trip, error) {
	t, err := s.repo.Get(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if err := s.apply(ctx, t, StatusPaid, "system", nil); err != nil {
		return nil, err
	}
	return t, nil
}

type CancelInput struct {
	TripID string
	By     CancelBy
	Actor  string
	Reason string
}

func (s *Service) Cancel(ctx context.Context, in CancelInput) (*Trip, error) {
	t, err := s.repo.Get(ctx, in.TripID)
	if err != nil {
		return nil, err
	}
	if t.Status.IsTerminal() {
		return nil, errs.Conflict("trip_already_final", "Chuyến đã kết thúc, không thể huỷ.")
	}
	if in.By == CancelByRider && t.RiderID != in.Actor {
		return nil, errs.E(errs.KindForbidden, "not_your_trip", "Bạn không có quyền huỷ chuyến này.")
	}
	t.CancelBy = &in.By
	t.Reason = in.Reason
	// Tính MỘT lần rồi dùng lại. Gọi hai lần thì đồng hồ có thể nhích qua ranh
	// giới cửa sổ miễn phí giữa hai lần, làm nhật ký và sự kiện mâu thuẫn nhau —
	// đúng loại sai lệch không giải thích nổi khi tài xế khiếu nại.
	fee := s.cancelFee(ctx, t)
	meta := map[string]any{"by": string(in.By), "reason": in.Reason}
	if fee > 0 {
		meta["cancel_fee"] = fee
	}
	if err := s.apply(ctx, t, StatusCancelled, in.Actor, meta, Message{
		Topic: eventbus.TopicTripCancelled,
		Payload: map[string]any{
			"trip_id":    t.ID,
			"by":         string(in.By),
			"rider_id":   t.RiderID,
			"driver_id":  t.DriverID,
			"cancel_fee": fee,
		},
	}); err != nil {
		return nil, err
	}
	return t, nil
}

// cancelFee: khách huỷ sau khi tài xế đã nhận và quá cửa sổ miễn phí.
func (s *Service) cancelFee(ctx context.Context, t *Trip) int64 {
	if t.CancelBy == nil || *t.CancelBy != CancelByRider || t.AssignedAt == nil {
		return 0
	}
	p := s.cancelPolicy(ctx)
	if s.clk.Now().Sub(*t.AssignedAt) <= p.FreeWindow {
		return 0
	}
	return p.FeeVND
}

// Expire đánh dấu chuyến không tìm được tài xế.
func (s *Service) Expire(ctx context.Context, tripID string) error {
	t, err := s.repo.Get(ctx, tripID)
	if err != nil {
		return err
	}
	if t.Status != StatusSearching {
		return nil
	}
	return s.apply(ctx, t, StatusExpired, "system", nil)
}

// Rate ghi điểm khách chấm cho chuyến, 1..5.
//
// Chỉ chấm được sau khi chuyến kết thúc bình thường (COMPLETED/PAID) và chỉ
// chấm được một lần. Sự kiện trip.rated để module driver cộng dồn thống kê —
// trip không biết gì về hồ sơ tài xế.
func (s *Service) Rate(ctx context.Context, tripID, riderID string, rating int) (*Trip, error) {
	if rating < 1 || rating > 5 {
		return nil, errs.Invalid("rating_invalid", "Điểm đánh giá phải từ 1 đến 5.")
	}
	t, err := s.repo.Get(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if t.RiderID != riderID {
		return nil, errs.E(errs.KindForbidden, "not_your_trip", "Chuyến này không thuộc về bạn.")
	}
	if t.Status != StatusCompleted && t.Status != StatusPaid {
		return nil, errs.Conflict("trip_not_finished", "Chỉ đánh giá được sau khi chuyến kết thúc.")
	}
	if t.DriverID == nil {
		return nil, errs.Conflict("trip_has_no_driver", "Chuyến này không có tài xế để đánh giá.")
	}
	if err := s.repo.SetRating(ctx, tripID, rating); err != nil {
		return nil, err
	}
	t.Rating = &rating
	_ = s.bus.Publish(ctx, eventbus.TopicTripRated, map[string]any{
		"trip_id": t.ID, "driver_id": *t.DriverID, "rating": rating,
	})
	return t, nil
}

// HasActiveTrip cho biết tài xế có chuyến nào chưa kết thúc không.
//
// Dùng ActiveByDriver — phương thức đã được khai báo và cài đặt ở cả hai repo
// từ đầu nhưng chưa từng có ai gọi.
func (s *Service) HasActiveTrip(ctx context.Context, driverID string) (bool, error) {
	t, err := s.repo.ActiveByDriver(ctx, driverID)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return false, nil
		}
		return false, err
	}
	return t != nil && !t.Status.IsTerminal() && t.Status != StatusPaid, nil
}

func (s *Service) Get(ctx context.Context, tripID string) (*Trip, error) {
	return s.repo.Get(ctx, tripID)
}

func (s *Service) ListByRider(ctx context.Context, riderID string, limit int) ([]*Trip, error) {
	return s.repo.ListByRider(ctx, riderID, limit)
}

func (s *Service) ListSearching(ctx context.Context, limit int) ([]*Trip, error) {
	return s.ListByStatus(ctx, StatusSearching, limit)
}

// ListByStatus liệt kê chuyến theo trạng thái. Dùng cho dispatcher và bảng
// điều khiển vận hành.
func (s *Service) ListByStatus(ctx context.Context, st Status, limit int) ([]*Trip, error) {
	return s.repo.ListByStatus(ctx, st, limit)
}

func (s *Service) Events(ctx context.Context, tripID string) ([]Event, error) {
	return s.repo.Events(ctx, tripID)
}

func (s *Service) getForDriver(ctx context.Context, tripID, driverID string) (*Trip, error) {
	t, err := s.repo.Get(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if t.DriverID == nil || *t.DriverID != driverID {
		return nil, errs.E(errs.KindForbidden, "not_your_trip", "Chuyến này không thuộc về bạn.")
	}
	return t, nil
}

// apply chuyển trạng thái và lưu trip + event + sự kiện trong cùng transaction.
//
// msgs là những sự kiện phải phát đi CÙNG với thay đổi này. Đưa chúng vào đây
// thay vì publish sau khi Save trả về là điểm mấu chốt: ở chế độ Postgres chúng
// được ghi vào outbox trong chính transaction đó, nên không thể có chuyện trạng
// thái đã đổi mà sự kiện thì mất.
func (s *Service) apply(ctx context.Context, t *Trip, to Status, actor string, meta map[string]any, msgs ...Message) error {
	from := t.Status
	now := s.clk.Now()
	if err := t.transition(to, now); err != nil {
		return err
	}
	pending, err := s.repo.Save(ctx, t, Event{
		ID: id.New("evt"), TripID: t.ID, From: from, To: to, Actor: actor, Meta: meta, At: now,
	}, msgs...)
	if err != nil {
		return err
	}
	// pending chỉ khác rỗng khi tầng lưu trữ không có outbox (chế độ bộ nhớ).
	for _, m := range pending {
		_ = s.bus.Publish(ctx, m.Topic, m.Payload)
	}
	return nil
}
