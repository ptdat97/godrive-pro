package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/trip"
)

type sentPush struct {
	topic string
	qos   byte
	msg   PushMessage
}

// fakeDownlink ghi lại mọi tin đẩy thay cho broker.
type fakeDownlink struct {
	mu   sync.Mutex
	sent []sentPush
	fail map[string]bool // topic -> giả lỗi
}

func (f *fakeDownlink) Publish(_ context.Context, topic string, qos byte, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[topic] {
		return errors.New("broker từ chối")
	}
	var m PushMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return err
	}
	f.sent = append(f.sent, sentPush{topic: topic, qos: qos, msg: m})
	return nil
}

func (f *fakeDownlink) all() []sentPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentPush(nil), f.sent...)
}

func (f *fakeDownlink) on(topic string) []PushMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []PushMessage
	for _, s := range f.sent {
		if s.topic == topic {
			out = append(out, s.msg)
		}
	}
	return out
}

func (f *fakeDownlink) topics() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, s := range f.sent {
		out[s.topic] = true
	}
	return out
}

// tripStatuses là chuỗi trạng thái chuyến đã đẩy tới một topic, theo thứ tự nhận.
func (f *fakeDownlink) tripStatuses(topic string) []trip.Status {
	var out []trip.Status
	for _, m := range f.on(topic) {
		if m.Type == "trip" && m.Trip != nil {
			out = append(out, m.Trip.Status)
		}
	}
	return out
}

func hasStatus(ss []trip.Status, want trip.Status) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func pushApp(t *testing.T) (*App, *fakeDownlink) {
	t.Helper()
	a := newTestApp(t)
	dl := &fakeDownlink{}
	a.Downlink = dl // PHẢI trước StartWorkers: consumer đẩy tin chỉ đăng ký khi có Downlink
	a.StartWorkers(context.Background())
	return a, dl
}

// T-31: trọn vòng đời một chuyến, mọi tin đẩy đều tới ĐÚNG người và CHỈ tới
// người đó.
func TestPushFollowsTripLifecycle(t *testing.T) {
	ctx := context.Background()
	a, dl := pushApp(t)

	riderID := login(t, a, "0901234567", authn.RoleRider)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	tr := seedTrip(t, a, riderID)

	offerTopic := "drv/" + d.ID + "/offer"
	riderTrip := "rdr/" + riderID + "/trip"
	driverTrip := "drv/" + d.ID + "/trip"
	riderLoc := "rdr/" + riderID + "/driver_loc"

	// 1. Lời mời tới tài xế mà không ai phải hỏi.
	waitFor(t, "lời mời được đẩy tới tài xế", func() bool { return len(dl.on(offerTopic)) > 0 })
	pushed := dl.on(offerTopic)[0]
	if pushed.Offer == nil || pushed.Offer.TripID != tr.ID || pushed.Offer.DriverID != d.ID {
		t.Fatalf("lời mời đẩy đi sai nội dung: %+v", pushed.Offer)
	}
	// Tin đẩy và GET /v1/offers phải nói cùng một điều — đường dự phòng không
	// được lệch với đường chính.
	pending, err := a.Matcher.PendingOffers(ctx, d.ID)
	if err != nil || len(pending) != 1 || pending[0].ID != pushed.Offer.ID {
		t.Fatalf("lời mời đẩy đi (%s) khác lời mời trong kho (%+v, %v)", pushed.Offer.ID, pending, err)
	}

	// 2. Nhận chuyến -> cả hai phía biết ASSIGNED.
	if _, err := a.Matcher.Accept(ctx, pushed.Offer.ID, d.ID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	waitFor(t, "khách biết đã có tài xế", func() bool { return hasStatus(dl.tripStatuses(riderTrip), trip.StatusAssigned) })
	waitFor(t, "tài xế biết chuyến đã gán", func() bool { return hasStatus(dl.tripStatuses(driverTrip), trip.StatusAssigned) })

	// 3. Tài xế chạy tới -> khách thấy xe.
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, nearby)); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	locs := dl.on(riderLoc)
	if len(locs) == 0 || locs[0].Driver == nil || locs[0].Driver.TripID != tr.ID {
		t.Fatalf("khách phải nhận được vị trí tài xế của chuyến mình, được %+v", locs)
	}
	for _, s := range dl.all() {
		if s.topic == riderLoc && s.qos != qosLatest {
			t.Errorf("vị trí tài xế phải đi QoS 0 (bản mới thay bản cũ), được %d", s.qos)
		}
	}

	// 4. Những bước không có sự kiện nghiệp vụ riêng (ARRIVED, PAID) vẫn phải tới.
	if _, err := a.Trips.MarkArrived(ctx, tr.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Trips.Start(ctx, tr.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "tài xế ON_TRIP", func() bool {
		got, _ := a.Drivers.Get(ctx, d.ID)
		return got.Status == driver.StatusOnTrip
	})
	if _, err := a.Trips.Complete(ctx, tr.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	for _, st := range []trip.Status{trip.StatusArrived, trip.StatusInProgress, trip.StatusCompleted, trip.StatusPaid} {
		st := st
		waitFor(t, "khách nhận "+string(st), func() bool { return hasStatus(dl.tripStatuses(riderTrip), st) })
		waitFor(t, "tài xế nhận "+string(st), func() bool { return hasStatus(dl.tripStatuses(driverTrip), st) })
	}

	// 5. Chuyến xong -> tài xế đi tiếp thì khách cũ KHÔNG còn thấy.
	waitFor(t, "tài xế về IDLE", func() bool {
		got, _ := a.Drivers.Get(ctx, d.ID)
		return got.Status == driver.StatusIdle
	})
	before := len(dl.on(riderLoc))
	if err := a.Location.Ingest(ctx, locationPingAt(d.ID, nearby)); err != nil {
		t.Fatal(err)
	}
	if after := len(dl.on(riderLoc)); after != before {
		t.Fatal("chuyến đã xong mà khách vẫn nhận vị trí tài xế")
	}

	// 6. Không một tin nào đi ra ngoài bốn topic của hai người này.
	allowed := map[string]bool{offerTopic: true, riderTrip: true, driverTrip: true, riderLoc: true}
	for topic := range dl.topics() {
		if !allowed[topic] {
			t.Errorf("tin đẩy lọt ra topic không liên quan: %s", topic)
		}
	}
}

// Tài xế thứ hai được chào cùng chuyến nhận lời mời của MÌNH, không thấy gì
// về chuyến sau khi người khác đã nhận.
func TestPushOnlyReachesOfferedDrivers(t *testing.T) {
	ctx := context.Background()
	a, dl := pushApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	d1 := seedDriver(t, a, "0912345678", "Một", "59X1-111.11")
	d2 := seedDriver(t, a, "0912345679", "Hai", "59X1-222.22")
	seedTrip(t, a, riderID)

	waitFor(t, "cả hai được chào", func() bool {
		return len(dl.on("drv/"+d1.ID+"/offer")) > 0 && len(dl.on("drv/"+d2.ID+"/offer")) > 0
	})
	o1 := dl.on("drv/" + d1.ID + "/offer")[0].Offer
	o2 := dl.on("drv/" + d2.ID + "/offer")[0].Offer
	if o1.DriverID != d1.ID || o2.DriverID != d2.ID || o1.ID == o2.ID {
		t.Fatalf("mỗi tài xế phải nhận đúng lời mời của mình: %+v / %+v", o1, o2)
	}

	if _, err := a.Matcher.Accept(ctx, o1.ID, d1.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "tài xế 1 nhận ASSIGNED", func() bool {
		return hasStatus(dl.tripStatuses("drv/"+d1.ID+"/trip"), trip.StatusAssigned)
	})
	if got := dl.on("drv/" + d2.ID + "/trip"); len(got) != 0 {
		t.Fatalf("tài xế không được ghép không được nhận tin chuyến: %+v", got)
	}
}

// Bus giao lại muộn (pod chết, NATS giao lại) thì lời mời có thể đã hết hạn.
// Đẩy nó lúc này là bắt tài xế bấm vào thứ chắc chắn bị từ chối.
func TestExpiredOfferIsNotPushed(t *testing.T) {
	a := newTestApp(t)
	dl := &fakeDownlink{}
	a.Downlink = dl
	now := a.Clock.Now()
	e, _ := eventbus.NewEvent(eventbus.TopicOfferCreated, matching.Offer{
		ID: "ofr_cu", TripID: "trp_x", DriverID: "drv_x",
		CreatedAt: now.Add(-20 * time.Second), ExpiresAt: now.Add(-5 * time.Second),
	})
	if err := a.pushOffer(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got := dl.all(); len(got) != 0 {
		t.Fatalf("lời mời hết hạn không được đẩy: %+v", got)
	}
}

// Một phía lỗi không được làm phía kia mất tin, và lỗi phải trả về để bus
// giao lại.
func TestTripPushFailureOnOneSideStillReachesOther(t *testing.T) {
	a := newTestApp(t)
	drv := "drv_x"
	dl := &fakeDownlink{fail: map[string]bool{"rdr/acc_r/trip": true}}
	a.Downlink = dl
	e, _ := eventbus.NewEvent(eventbus.TopicTripStatusChanged, map[string]any{
		"from": trip.StatusAssigned,
		"trip": trip.Trip{ID: "trp_x", RiderID: "acc_r", DriverID: &drv, Status: trip.StatusArrived},
	})
	if err := a.pushTripStatus(context.Background(), e); err == nil {
		t.Fatal("phía khách lỗi thì handler phải trả lỗi để bus giao lại")
	}
	if got := dl.tripStatuses("drv/drv_x/trip"); len(got) != 1 || got[0] != trip.StatusArrived {
		t.Fatalf("phía tài xế vẫn phải nhận tin, được %v", got)
	}
}

// Vào lại sau khi mất sóng: GET /v1/trips/current trả trạng thái hiện tại cho
// cả hai phía — tin đẩy lỡ lúc mất kết nối không được gửi lại.
func TestCurrentTripRecoversStateAfterReconnect(t *testing.T) {
	ctx := context.Background()
	a, dl := pushApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	tr := seedTrip(t, a, riderID)

	h := a.Router()
	get := func(sub string, role authn.Role) (int, map[string]any) {
		tok, _ := a.Issuer.Issue(sub, role, "may-test", a.Clock.Now().UTC())
		req := httptest.NewRequest(http.MethodGet, "/v1/trips/current", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&body)
		return rec.Code, body
	}
	tripStatus := func(body map[string]any) string {
		m, _ := body["trip"].(map[string]any)
		if m == nil {
			return ""
		}
		s, _ := m["status"].(string)
		return s
	}

	if code, body := get(riderID, authn.RoleRider); code != 200 || tripStatus(body) != string(trip.StatusSearching) {
		t.Fatalf("khách đang chờ ghép phải thấy SEARCHING: %d %v", code, body)
	}
	if code, body := get(d.AccountID, authn.RoleDriver); code != 200 || body["trip"] != nil {
		t.Fatalf("tài xế chưa nhận chuyến thì trip phải null: %d %v", code, body)
	}

	waitFor(t, "lời mời", func() bool { return len(dl.on("drv/"+d.ID+"/offer")) > 0 })
	if _, err := a.Matcher.Accept(ctx, dl.on("drv/" + d.ID + "/offer")[0].Offer.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Trips.MarkArrived(ctx, tr.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		sub  string
		role authn.Role
	}{{riderID, authn.RoleRider}, {d.AccountID, authn.RoleDriver}} {
		if code, body := get(c.sub, c.role); code != 200 || tripStatus(body) != string(trip.StatusArrived) {
			t.Fatalf("%s vào lại phải thấy ARRIVED: %d %v", c.role, code, body)
		}
	}

	// Khách khác không có chuyến: null, không phải chuyến của người khác.
	other := login(t, a, "0909999999", authn.RoleRider)
	if code, body := get(other, authn.RoleRider); code != 200 || body["trip"] != nil {
		t.Fatalf("khách không có chuyến phải nhận null: %d %v", code, body)
	}
}
