package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/mqttauth"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/platform/metrics"
	"github.com/example/godrive/internal/trip"
)

// Downlink là đường máy chủ đẩy tin tới thiết bị.
//
// Bản thật là kết nối MQTT của backend (location.MQTTConsumer). Khai báo thành
// interface để test kiểm được "tin gì đi tới topic nào" mà không cần broker.
type Downlink interface {
	Publish(ctx context.Context, topic string, qos byte, payload []byte) error
}

// QoS của chiều xuống.
//
// Lời mời và trạng thái chuyến dùng QoS 1: mất một tin là tài xế lỡ chuyến hoặc
// khách không biết xe đã tới. Vị trí tài xế dùng QoS 0: 4 giây sau đã có bản
// mới thay thế, giao lại bản cũ chỉ tốn băng thông của máy khách.
const (
	qosReliable byte = 1
	qosLatest   byte = 0
)

// PushMessage là phong bì chung của mọi tin đẩy.
//
// Type để thiết bị phân loại mà không phải đoán theo topic; mọi tin đều mang
// trọn đối tượng chứ không chỉ "có gì đó vừa đổi", để thiết bị không phải gọi
// HTTP ngay sau mỗi tin — chính là thứ chiều xuống sinh ra để bỏ đi.
type PushMessage struct {
	Type      string                   `json:"type"` // offer | offer_withdrawn | trip | driver_loc
	Offer     *matching.OfferView      `json:"offer,omitempty"`
	Withdrawn *matching.OfferWithdrawn `json:"withdrawn,omitempty"`
	Trip      *trip.Trip               `json:"trip,omitempty"`
	From      trip.Status              `json:"from,omitempty"`
	Driver    *DriverLocPayload        `json:"driver,omitempty"`
}

// DriverLocPayload là vị trí tài xế gửi tới khách. Cố ý KHÔNG chứa pin, độ
// chính xác GPS hay cờ gian lận — những thứ đó là của nội bộ.
type DriverLocPayload struct {
	TripID     string    `json:"trip_id"`
	DriverID   string    `json:"driver_id"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	BearingDeg float64   `json:"bearing_deg"`
	At         time.Time `json:"at"`
}

// startPushConsumers nối sự kiện vào chiều xuống.
//
// Mọi tin đẩy đều PHÁT TỪ CONSUMER, không từ trong Service: module nghiệp vụ
// không biết thiết bị tồn tại, và một lần đổi trạng thái chỉ cần đẩy đi từ
// đúng một chỗ. Riêng vị trí tài xế đi đường tắt — xem location.MovementSink.
func (a *App) startPushConsumers() {
	if a.Downlink == nil {
		if !a.Cfg.InMemory() {
			a.Log.Warn("chưa có MQTT: không đẩy được tin xuống thiết bị — " +
				"tài xế chỉ biết có lời mời bằng cách hỏi GET /v1/offers")
		}
		return
	}
	a.Bus.Subscribe(eventbus.TopicOfferCreated, "push-offer", a.pushOffer)
	a.Bus.Subscribe(eventbus.TopicOfferWithdrawn, "push-offer-withdrawn", a.pushOfferWithdrawn)
	a.Bus.Subscribe(eventbus.TopicTripStatusChanged, "push-trip", a.pushTripStatus)
	a.Location.UseMovementSink(movementPusher{a})
}

// pushOffer đẩy lời mời tới đúng tài xế được chào.
func (a *App) pushOffer(ctx context.Context, e eventbus.Event) error {
	var o matching.Offer
	if err := json.Unmarshal(e.Payload, &o); err != nil {
		return err
	}
	if o.DriverID == "" {
		return nil
	}
	// Bus giao lại sau khi pod chết thì lời mời có thể đã hết hạn. Đẩy nó lúc
	// này là bắt tài xế bấm vào một thứ chắc chắn bị từ chối.
	now := a.Clock.Now()
	if !now.Before(o.ExpiresAt) {
		a.Metrics.pushes.Inc(metrics.Labels{"kind": "offer", "result": "stale"})
		return nil
	}
	v, err := a.Matcher.View(ctx, o)
	if err != nil {
		return err
	}
	err = a.push(ctx, fmt.Sprintf(mqttauth.TopicDriverOffer, o.DriverID), qosReliable,
		PushMessage{Type: "offer", Offer: &v})
	if err == nil {
		// Độ trễ tính từ lúc tạo lời mời tới lúc broker nhận tin — phần của máy
		// chủ trong mục tiêu "lời mời tới tay tài xế p95 < 1 giây".
		a.Metrics.offerPushDelay.Observe(a.Clock.Now().Sub(o.CreatedAt).Seconds(), nil)
	}
	return a.countPush("offer", err)
}

// pushOfferWithdrawn báo tài xế rằng lời mời trên màn hình đã mất hiệu lực.
// Đi cùng topic với lời mời: với app, đây là "xoá lời mời đó khỏi màn hình".
func (a *App) pushOfferWithdrawn(ctx context.Context, e eventbus.Event) error {
	var w matching.OfferWithdrawn
	if err := json.Unmarshal(e.Payload, &w); err != nil {
		return err
	}
	if w.DriverID == "" {
		return nil
	}
	err := a.push(ctx, fmt.Sprintf(mqttauth.TopicDriverOffer, w.DriverID), qosReliable,
		PushMessage{Type: "offer_withdrawn", Withdrawn: &w})
	return a.countPush("offer_withdrawn", err)
}

// pushTripStatus đẩy lần đổi trạng thái chuyến tới cả khách lẫn tài xế.
func (a *App) pushTripStatus(ctx context.Context, e eventbus.Event) error {
	var p struct {
		From trip.Status `json:"from"`
		Trip *trip.Trip  `json:"trip"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return err
	}
	if p.Trip == nil {
		return nil
	}
	msg := PushMessage{Type: "trip", Trip: p.Trip, From: p.From}

	// Gửi CẢ HAI rồi mới báo lỗi: một phía lỗi không được làm phía kia mất tin.
	// Bus giao lại thì phía đã nhận sẽ nhận trùng — thiết bị bỏ qua tin có
	// updated_at không mới hơn cái nó đang có.
	err := a.push(ctx, fmt.Sprintf(mqttauth.TopicRiderTrip, p.Trip.RiderID), qosReliable, msg)
	if p.Trip.DriverID != nil && *p.Trip.DriverID != "" {
		if derr := a.push(ctx, fmt.Sprintf(mqttauth.TopicDriverTrip, *p.Trip.DriverID), qosReliable, msg); err == nil {
			err = derr
		}
	}
	return a.countPush("trip", err)
}

func (a *App) push(ctx context.Context, topic string, qos byte, m PushMessage) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return a.Downlink.Publish(ctx, topic, qos, raw)
}

func (a *App) countPush(kind string, err error) error {
	result := "ok"
	if err != nil {
		result = "error"
	}
	a.Metrics.pushes.Inc(metrics.Labels{"kind": kind, "result": result})
	return err
}

// movementPusher đưa vị trí tài xế tới khách của chuyến đang chạy.
type movementPusher struct{ a *App }

// DriverMoved tra chuyến hiện tại của tài xế MỖI LẦN chứ không nhớ đệm.
//
// Nhớ đệm "tài xế này đang chở khách nào" sẽ sai ngay khi tài xế xong chuyến
// này và nhận chuyến kế trong lúc bộ đệm chưa hết hạn: khách CŨ tiếp tục thấy
// tài xế chạy đi đón người khác. Đó là lộ vị trí, không phải chậm vài giây.
// Truy vấn dùng chỉ mục trips_driver_idx và chỉ chạy cho tài xế đang có khách.
func (m movementPusher) DriverMoved(ctx context.Context, s location.Snapshot) {
	a := m.a
	t, err := a.Trips.CurrentForDriver(ctx, s.DriverID)
	if err != nil {
		a.Log.Error("không tra được chuyến để đẩy vị trí", "driver_id", s.DriverID, "err", err)
		return
	}
	if t == nil {
		return
	}
	switch t.Status {
	case trip.StatusAssigned, trip.StatusArrived, trip.StatusInProgress:
	default:
		return // COMPLETED: khách đã xuống xe, không còn lý do theo dõi tài xế
	}
	err = a.push(ctx, fmt.Sprintf(mqttauth.TopicRiderDriverLoc, t.RiderID), qosLatest, PushMessage{
		Type: "driver_loc",
		Driver: &DriverLocPayload{
			TripID: t.ID, DriverID: s.DriverID,
			Lat: s.Point.Lat, Lng: s.Point.Lng, BearingDeg: s.BearingDeg, At: s.UpdatedAt,
		},
	})
	if err != nil {
		a.Log.Debug("không đẩy được vị trí tài xế", "trip_id", t.ID, "err", err)
	}
	_ = a.countPush("driver_loc", err)
}
