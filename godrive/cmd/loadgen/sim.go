package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/example/godrive/pkg/geo"
)

// pushMsg là phần của tin đẩy mà thiết bị ảo cần đọc.
type pushMsg struct {
	Type  string `json:"type"`
	Offer *struct {
		ID        string    `json:"id"`
		TripID    string    `json:"trip_id"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"offer"`
	Withdrawn *struct {
		OfferID string `json:"offer_id"`
	} `json:"withdrawn"`
	Trip *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"trip"`
	Driver *struct {
		At time.Time `json:"at"`
	} `json:"driver"`
}

// ------------------------------------------------------------------ tài xế

type simDriver struct {
	ID   string
	sess session
	rng  *rand.Rand

	mu      sync.Mutex
	pos     geo.Point
	bearing float64
	busy    bool
	// withdrawn là lời mời đã bị thu hồi trong lúc tài xế còn đang "đọc" —
	// app thật xoá nó khỏi màn hình, nên tài xế ảo cũng không bấm nữa.
	withdrawn map[string]bool
}

func (d *simDriver) run(ctx context.Context, cl *client, o options, st *stats) {
	topicOffer := "drv/" + d.ID + "/offer"
	opts := mqtt.NewClientOptions().AddBroker(o.mqtt).
		SetClientID("drv_" + d.ID + "-lg").
		SetUsername(d.ID).SetPassword(d.sess.Token).
		SetCleanSession(true).SetAutoReconnect(true).
		SetConnectTimeout(10 * time.Second).
		SetOrderMatters(false).
		SetOnConnectHandler(func(c mqtt.Client) {
			c.Subscribe(topicOffer, 1, func(_ mqtt.Client, m mqtt.Message) {
				d.onOffer(ctx, cl, o, st, m.Payload())
			})
		}).
		SetConnectionLostHandler(func(mqtt.Client, error) { st.count("mqtt_lost_driver") })
	c := mqtt.NewClient(opts)
	if t := c.Connect(); !t.WaitTimeout(15*time.Second) || t.Error() != nil {
		st.count("mqtt_connect_failed_driver")
		return
	}
	st.connected.Add(1)
	defer c.Disconnect(100)

	// Lệch pha ngẫu nhiên: cả nghìn tài xế gửi vị trí cùng một nhịp là một
	// kiểu tải không bao giờ xảy ra ngoài đời.
	select {
	case <-time.After(time.Duration(d.rng.Int63n(int64(o.ping)))):
	case <-ctx.Done():
		return
	}
	t := time.NewTicker(o.ping)
	defer t.Stop()
	for {
		raw := d.step(o)
		tk := c.Publish("drv/"+d.ID+"/loc", 1, false, raw)
		if tk.WaitTimeout(5*time.Second) && tk.Error() == nil {
			st.count("pings")
		} else {
			st.count("ping_failed")
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

// step dịch tài xế một đoạn hợp lý rồi trả bản tin vị trí.
//
// Tốc độ ≤ 10 m/s và đổi hướng từ từ: đi nhanh hơn ngưỡng chống gian lận
// (33 m/s) là bị từ chối vì "nhảy vị trí", và cái đo được sẽ là bộ lọc gian
// lận chứ không phải hệ thống.
func (d *simDriver) step(o options) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	speed := 3 + d.rng.Float64()*7
	d.bearing = math.Mod(d.bearing+d.rng.NormFloat64()*25+360, 360)
	if geo.DistanceM(d.pos, o.center) > o.radiusM*1.2 {
		d.bearing = geo.BearingDeg(d.pos, o.center) // quay về vùng có khách
	}
	d.pos = offset(d.pos, speed*o.ping.Seconds(), d.bearing)
	raw, _ := json.Marshal(map[string]any{
		"point": d.pos, "bearing_deg": d.bearing, "speed_mps": speed,
		"accuracy_m": 8, "battery_pc": 80, "at": time.Now().UTC(),
	})
	return raw
}

func (d *simDriver) onOffer(ctx context.Context, cl *client, o options, st *stats, payload []byte) {
	var m pushMsg
	if err := json.Unmarshal(payload, &m); err != nil {
		st.count("push_decode_failed")
		return
	}
	switch m.Type {
	case "offer_withdrawn":
		st.count("offers_withdrawn")
		if m.Withdrawn != nil {
			d.mu.Lock()
			if d.withdrawn == nil {
				d.withdrawn = map[string]bool{}
			}
			d.withdrawn[m.Withdrawn.OfferID] = true
			d.mu.Unlock()
		}
		return
	case "offer":
	default:
		return
	}
	if m.Offer == nil {
		return
	}
	// Đồng hồ máy chủ và máy sinh tải là một khi chạy cùng máy. Chạy khác máy
	// thì số này gồm cả độ lệch đồng hồ — xem docs/08 §8.14.
	st.observe("offer_delivery", time.Since(m.Offer.CreatedAt))
	st.count("offers_received")

	d.mu.Lock()
	if d.busy || d.rng.Float64() > o.acceptProb {
		d.mu.Unlock()
		st.count("offers_ignored")
		return
	}
	d.busy = true
	think := 300*time.Millisecond + time.Duration(d.rng.Int63n(int64(1200*time.Millisecond)))
	d.mu.Unlock()

	go func() {
		defer func() { d.mu.Lock(); d.busy = false; d.mu.Unlock() }()
		select {
		case <-time.After(think): // người thật cần thời gian đọc rồi bấm
		case <-ctx.Done():
			return
		}
		d.mu.Lock()
		gone := d.withdrawn[m.Offer.ID]
		delete(d.withdrawn, m.Offer.ID)
		d.mu.Unlock()
		if gone {
			st.count("offers_gone_before_tap") // tin thu hồi tới trước khi kịp bấm
			return
		}
		lat, err := cl.call(ctx, "POST", "/v1/offers/"+m.Offer.ID+"/accept", d.sess.Token, nil, nil)
		st.observe("api_accept", lat)
		if err != nil {
			st.count("accept_" + errCode(err))
			return
		}
		st.count("accept_ok")
		d.drive(ctx, cl, o, st, m.Offer.TripID)
	}()
}

// drive đi trọn chuyến với các bước rút gọn theo trip-step.
func (d *simDriver) drive(ctx context.Context, cl *client, o options, st *stats, tripID string) {
	for _, stepName := range []string{"arrived", "start", "complete"} {
		select {
		case <-time.After(o.tripStep):
		case <-ctx.Done():
			return
		}
		lat, err := cl.call(ctx, "POST", "/v1/trips/"+tripID+"/"+stepName, d.sess.Token, nil, nil)
		st.observe("api_trip_"+stepName, lat)
		if err != nil {
			st.count(stepName + "_" + errCode(err))
			return
		}
	}
}

// ------------------------------------------------------------------ khách

type simRider struct {
	sess session
	rng  *rand.Rand
	n    int // số chuyến đã đặt — để clientId mỗi chuyến một khác
}

type riderPool struct {
	idle  chan *simRider
	total int
}

func newRiderPool(rs []*simRider) *riderPool {
	p := &riderPool{idle: make(chan *simRider, len(rs)), total: len(rs)}
	for _, r := range rs {
		p.idle <- r
	}
	return p
}

// waitIdle chờ mọi khách xong chuyến, tối đa timeout.
func (p *riderPool) waitIdle(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for len(p.idle) < p.total && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
}

// runDemand đặt chuyến đều theo nhịp trip-rate cho tới khi ctx hết.
func runDemand(ctx context.Context, cl *client, o options, pool *riderPool, st *stats) {
	t := time.NewTicker(time.Duration(float64(time.Second) / o.tripRate))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		select {
		case r := <-pool.idle:
			go func() {
				defer func() { pool.idle <- r }()
				// Chuyến đã đặt phải được theo tới cùng kể cả khi pha đo kết
				// thúc — dùng nền riêng, không dùng ctx của pha đặt chuyến.
				r.trip(context.Background(), cl, o, st)
			}()
		default:
			st.count("riders_exhausted") // tăng -riders nếu số này khác 0
		}
	}
}

func (r *simRider) trip(ctx context.Context, cl *client, o options, st *stats) {
	pickup := randomPoint(r.rng, o.center, o.radiusM*0.8)
	dropoff := offset(pickup, 2000+r.rng.Float64()*3000, r.rng.Float64()*360)

	var q struct {
		Quotes []struct {
			ID          string `json:"id"`
			VehicleType string `json:"vehicle_type"`
		} `json:"quotes"`
	}
	lat, err := cl.call(ctx, "POST", "/v1/quotes", r.sess.Token,
		map[string]any{"pickup": pickup, "dropoff": dropoff}, &q)
	st.observe("api_quote", lat)
	if err != nil {
		st.count("quote_" + errCode(err))
		return
	}
	quoteID := ""
	for _, x := range q.Quotes {
		if x.VehicleType == "BIKE" {
			quoteID = x.ID
		}
	}

	r.n++
	tapped := time.Now() // khách bấm "Đặt xe"
	var tr struct {
		ID string `json:"id"`
	}
	lat, err = cl.call(ctx, "POST", "/v1/trips", r.sess.Token, map[string]any{
		"quote_id": quoteID, "payment_method": "CASH",
		"pickup":  map[string]any{"point": pickup, "address": "điểm đón tải"},
		"dropoff": map[string]any{"point": dropoff, "address": "điểm đến tải"},
	}, &tr, "Idempotency-Key", fmt.Sprintf("lg-%s-%d", r.sess.AccountID, r.n))
	st.observe("api_create_trip", lat)
	if err != nil {
		st.count("create_trip_" + errCode(err))
		return
	}
	st.count("trips_created")

	done := make(chan string, 1)
	var once sync.Once
	finish := func(s string) { once.Do(func() { done <- s }) }
	var assignedOnce sync.Once
	onMsg := func(_ mqtt.Client, m mqtt.Message) {
		var p pushMsg
		if json.Unmarshal(m.Payload(), &p) != nil {
			return
		}
		switch {
		case p.Type == "driver_loc" && p.Driver != nil:
			st.observe("driver_loc_freshness", time.Since(p.Driver.At))
		case p.Type == "trip" && p.Trip != nil && p.Trip.ID == tr.ID:
			switch p.Trip.Status {
			case "ASSIGNED":
				assignedOnce.Do(func() { st.observe("dispatch_rider_seen", time.Since(tapped)) })
			case "PAID", "EXPIRED", "CANCELLED":
				finish(p.Trip.Status)
			}
		}
	}
	c := mqtt.NewClient(mqtt.NewClientOptions().AddBroker(o.mqtt).
		SetClientID(fmt.Sprintf("rdr_%s-%d", r.sess.AccountID, r.n)).
		SetUsername(r.sess.AccountID).SetPassword(r.sess.Token).
		SetCleanSession(true).SetAutoReconnect(false).SetConnectTimeout(10 * time.Second).
		SetOrderMatters(false))
	if t := c.Connect(); !t.WaitTimeout(15*time.Second) || t.Error() != nil {
		st.count("mqtt_connect_failed_rider")
	} else {
		defer c.Disconnect(50)
		c.Subscribe("rdr/"+r.sess.AccountID+"/trip", 1, onMsg).WaitTimeout(5 * time.Second)
		c.Subscribe("rdr/"+r.sess.AccountID+"/driver_loc", 0, onMsg).WaitTimeout(5 * time.Second)
	}

	// Như app thật: vừa nối xong thì hỏi trạng thái hiện tại, phòng tin đẩy
	// đã đi trước lúc đăng ký kịp có hiệu lực. Sau đó hỏi thưa làm lưới an
	// toàn — nếu chỉ tin đẩy mà một tin lỡ thì khách ảo kẹt mãi.
	poll := time.NewTicker(10 * time.Second)
	defer poll.Stop()
	timeout := time.After(3*time.Minute + 6*o.tripStep)
	for {
		var cur struct {
			Trip *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"trip"`
		}
		if _, err := cl.call(ctx, "GET", "/v1/trips/"+tr.ID, r.sess.Token, nil, &cur.Trip); err == nil && cur.Trip != nil {
			switch cur.Trip.Status {
			case "PAID", "EXPIRED", "CANCELLED":
				finish(cur.Trip.Status)
			}
		}
		select {
		case s := <-done:
			st.count("trips_" + s)
			return
		case <-poll.C:
		case <-timeout:
			st.count("trips_stuck")
			return
		}
	}
}
