package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/notification"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/refund"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/money"
)

// startNotifyConsumers nối sự kiện vào thông báo đẩy (T-33).
//
// Chạy SONG SONG với MQTT chứ không thay: MQTT chỉ tới được app đang mở. Thông
// báo đẩy là đường đánh thức máy đang tắt màn hình. App nhận cả hai thì bỏ
// trùng theo mã trong Data.
func (a *App) startNotifyConsumers() {
	if !a.Notifier.Enabled() {
		return
	}
	a.Bus.Subscribe(eventbus.TopicOfferCreated, "notify-offer", a.notifyOffer)
	a.Bus.Subscribe(eventbus.TopicTripStatusChanged, "notify-trip", a.notifyTrip)
}

func (a *App) notifyOffer(ctx context.Context, e eventbus.Event) error {
	var o matching.Offer
	if err := json.Unmarshal(e.Payload, &o); err != nil {
		return err
	}
	ttl := o.ExpiresAt.Sub(a.Clock.Now())
	if ttl <= 0 || o.DriverID == "" {
		return nil // hết hạn rồi thì đánh thức tài xế cũng vô ích
	}
	d, err := a.Drivers.Get(ctx, o.DriverID)
	if err != nil {
		return err
	}
	v, err := a.Matcher.View(ctx, o)
	if err != nil {
		return err
	}
	where := v.Pickup.Address
	if where == "" {
		where = fmt.Sprintf("cách bạn %.1f km", o.PickupM/1000)
	}
	return a.Notifier.Notify(ctx, d.AccountID, notification.Message{
		Title: "Có chuyến mới · " + v.DriverEarn.String(),
		Body:  "Đón tại " + where,
		Data:  map[string]string{"type": "offer", "offer_id": o.ID, "trip_id": o.TripID},
		TTL:   ttl, Urgent: true,
	})
}

// tripNotice là thông báo cho một lần đổi trạng thái: gửi cho ai, nói gì.
// Không phải bước nào cũng đáng đánh thức máy — tài xế tự bấm "bắt đầu" thì
// không cần được báo lại.
func tripNotice(t *trip.Trip) (toRider, toDriver *notification.Message) {
	data := map[string]string{"type": "trip", "trip_id": t.ID, "status": string(t.Status)}
	msg := func(title, body string, urgent bool) *notification.Message {
		return &notification.Message{Title: title, Body: body, Data: data, Urgent: urgent}
	}
	switch t.Status {
	case trip.StatusAssigned:
		toRider = msg("Đã có tài xế", "Tài xế đang tới đón bạn.", true)
	case trip.StatusArrived:
		toRider = msg("Tài xế đã tới", "Tài xế đang chờ ở điểm đón.", true)
	case trip.StatusCompleted:
		toRider = msg("Chuyến đi hoàn tất", "Cước "+t.Fare.String()+". Cảm ơn bạn đã đi cùng GoDrive.", false)
	case trip.StatusExpired:
		toRider = msg("Chưa tìm được tài xế", "Khu vực đang thiếu xe, bạn thử đặt lại sau ít phút nhé.", false)
	case trip.StatusCancelled:
		if t.CancelBy != nil && *t.CancelBy == trip.CancelByDriver {
			toRider = msg("Tài xế đã huỷ chuyến", "Bạn vui lòng đặt lại, xin lỗi vì sự bất tiện.", true)
		} else if t.CancelBy != nil && *t.CancelBy == trip.CancelByRider && t.DriverID != nil {
			toDriver = msg("Khách đã huỷ chuyến", "Bạn có thể nhận chuyến khác.", true)
		}
	}
	return toRider, toDriver
}

func (a *App) notifyTrip(ctx context.Context, e eventbus.Event) error {
	var p struct {
		Trip *trip.Trip `json:"trip"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	if p.Trip == nil {
		return nil
	}
	toRider, toDriver := tripNotice(p.Trip)
	var err error
	if toRider != nil {
		err = a.Notifier.Notify(ctx, p.Trip.RiderID, *toRider)
	}
	if toDriver != nil {
		d, gerr := a.Drivers.Get(ctx, *p.Trip.DriverID)
		if gerr != nil {
			return gerr
		}
		if derr := a.Notifier.Notify(ctx, d.AccountID, *toDriver); err == nil {
			err = derr
		}
	}
	return err
}

// notifyRefund báo khách khi tiền hoàn đã vào ví. Gọi đồng bộ từ thao tác của
// vận hành nên lỗi chỉ ghi log: hoàn tiền đã xong, thiếu thông báo không được
// làm thao tác đó báo lỗi.
func (a *App) notifyRefund(ctx context.Context, r *refund.Refund) {
	err := a.Notifier.Notify(ctx, r.RiderID, notification.Message{
		Title: "Đã hoàn " + money.VND(r.Amount).String() + " vào ví GoDrive",
		Body:  r.Reason,
		Data:  map[string]string{"type": "refund", "refund_id": r.ID, "trip_id": r.TripID},
	})
	if err != nil {
		a.Log.Warn("không gửi được thông báo hoàn tiền", "refund_id", r.ID, "err", err)
	}
}
