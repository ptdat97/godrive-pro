package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/geo"
)

const (
	testNATSEnv = "TEST_NATS_URL"
	testMQTTEnv = "TEST_MQTT_URL"
)

// natsBus dựng bus NATS và XOÁ stream cũ để mỗi test bắt đầu từ trạng thái sạch.
func natsBus(t *testing.T) eventbus.Bus {
	t.Helper()
	url := os.Getenv(testNATSEnv)
	if url == "" {
		t.Skipf("bỏ qua: đặt %s để chạy test tích hợp NATS", testNATSEnv)
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = js.DeleteStream(ctx, eventbus.StreamName)
	nc.Close()

	b, err := eventbus.NewNATS(url, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestNATSDeliversToNamedConsumers(t *testing.T) {
	b := natsBus(t)
	ctx := context.Background()

	var a, c atomic.Int64
	done := make(chan struct{}, 2)
	// HAI tên khác nhau trên cùng một topic => cả hai đều nhận được.
	b.Subscribe(eventbus.TopicTripRequested, "consumer-a", func(_ context.Context, e eventbus.Event) error {
		a.Add(1)
		done <- struct{}{}
		return nil
	})
	b.Subscribe(eventbus.TopicTripRequested, "consumer-b", func(_ context.Context, e eventbus.Event) error {
		c.Add(1)
		done <- struct{}{}
		return nil
	})

	if err := b.Publish(ctx, eventbus.TopicTripRequested, map[string]string{"trip_id": "trp_1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("hết giờ chờ: a=%d b=%d", a.Load(), c.Load())
		}
	}
	if a.Load() != 1 || c.Load() != 1 {
		t.Fatalf("mỗi consumer phải nhận đúng 1 lần: a=%d b=%d", a.Load(), c.Load())
	}
}

// Nhiều tiến trình cùng đăng ký MỘT cặp (topic, name) tạo thành một NHÓM:
// mỗi thông điệp xử lý đúng một lần trên toàn cụm, không phải một lần mỗi pod.
//
// Đây chính là khác biệt so với bus in-process, nơi mọi pod đều chạy mọi handler.
func TestNATSSameNameFormsQueueGroup(t *testing.T) {
	url := os.Getenv(testNATSEnv)
	if url == "" {
		t.Skipf("cần %s", testNATSEnv)
	}
	_ = natsBus(t) // dọn stream

	log := logger.New("error", false)
	var total atomic.Int64
	var mu sync.Mutex
	seen := map[string]int{}

	// Ba "pod" cùng đăng ký một tên.
	buses := make([]eventbus.Bus, 3)
	for i := range buses {
		b, err := eventbus.NewNATS(url, log)
		if err != nil {
			t.Fatal(err)
		}
		buses[i] = b
		b.Subscribe(eventbus.TopicTripCompleted, "settlement", func(_ context.Context, e eventbus.Event) error {
			var p struct {
				TripID string `json:"trip_id"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			mu.Lock()
			seen[p.TripID]++
			mu.Unlock()
			total.Add(1)
			return nil
		})
	}
	t.Cleanup(func() {
		for _, b := range buses {
			b.Close()
		}
	})

	const n = 30
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := buses[0].Publish(ctx, eventbus.TopicTripCompleted,
			map[string]string{"trip_id": fmt.Sprintf("trp_%02d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(15 * time.Second)
	for total.Load() < n && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // để lộ ra nếu có giao thừa

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n {
		t.Fatalf("phải xử lý đủ %d chuyến, được %d", n, len(seen))
	}
	for id, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("chuyến %s được xử lý %d lần — ghi sổ hai lần là mất tiền thật", id, cnt)
		}
	}
	if total.Load() != n {
		t.Fatalf("tổng số lần xử lý phải là %d, được %d", n, total.Load())
	}
}

// BẤT BIẾN mà bus in-process KHÔNG có: handler lỗi thì việc được GIAO LẠI.
//
// Với bus in-process, lỗi chỉ được ghi log rồi bỏ qua — một lần SettleTrip lỗi
// là chuyến đó vĩnh viễn không được ghi sổ.
func TestNATSRedeliversOnHandlerError(t *testing.T) {
	b := natsBus(t)
	ctx := context.Background()

	var attempts atomic.Int64
	ok := make(chan struct{})
	start := time.Now()
	b.Subscribe(eventbus.TopicPaymentSettled, "flaky", func(_ context.Context, _ eventbus.Event) error {
		if attempts.Add(1) < 3 {
			return errors.New("DB tạm thời không phản hồi")
		}
		close(ok)
		return nil
	})

	if err := b.Publish(ctx, eventbus.TopicPaymentSettled, map[string]string{"trip_id": "trp_x"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ok:
	case <-time.After(30 * time.Second):
		t.Fatalf("phải giao lại tới khi thành công, mới thử %d lần", attempts.Load())
	}
	if n := attempts.Load(); n != 3 {
		t.Fatalf("phải thử đúng 3 lần, thử %d", n)
	}
	// Phải CÓ backoff: giao lại ngay lập tức sẽ làm nghẽn thêm đúng thứ đang
	// hỏng, và đốt hết MaxDeliver trong vài mili giây.
	// Hai lần chờ đầu là 1s + 2s = 3s.
	if d := time.Since(start); d < 2500*time.Millisecond {
		t.Fatalf("phải chờ theo backoff giữa các lần giao lại, chỉ mất %v", d)
	}
}

// Handler panic cũng phải được giao lại, không được nuốt mất việc.
func TestNATSRedeliversOnPanic(t *testing.T) {
	b := natsBus(t)
	ctx := context.Background()

	var attempts atomic.Int64
	ok := make(chan struct{})
	b.Subscribe(eventbus.TopicTripStarted, "panicky", func(_ context.Context, _ eventbus.Event) error {
		if attempts.Add(1) < 2 {
			panic("handler hỏng")
		}
		close(ok)
		return nil
	})

	if err := b.Publish(ctx, eventbus.TopicTripStarted, map[string]string{"trip_id": "trp_p"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ok:
	case <-time.After(30 * time.Second):
		t.Fatalf("panic phải được giao lại, mới thử %d lần", attempts.Load())
	}
}

// Sự kiện phát ra trước khi consumer tồn tại vẫn phải tới nơi.
//
// Đây là điều bus in-process không làm được: publish khi chưa ai subscribe thì
// sự kiện bay vào hư không.
func TestNATSDeliversEventsPublishedBeforeSubscribe(t *testing.T) {
	b := natsBus(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := b.Publish(ctx, eventbus.TopicOfferCreated,
			map[string]string{"driver_id": fmt.Sprintf("drv_%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	var got atomic.Int64
	done := make(chan struct{})
	b.Subscribe(eventbus.TopicOfferCreated, "late-joiner", func(_ context.Context, _ eventbus.Event) error {
		if got.Add(1) == 5 {
			close(done)
		}
		return nil
	})
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("consumer vào sau phải nhận đủ 5 sự kiện đã phát trước đó, nhận %d", got.Load())
	}
}

// ---------------------------------------------------------------- MQTT

func TestMQTTIngestsPingAndHandlesLastWill(t *testing.T) {
	url := os.Getenv(testMQTTEnv)
	if url == "" {
		t.Skipf("bỏ qua: đặt %s để chạy test tích hợp MQTT", testMQTTEnv)
	}
	ctx := context.Background()
	clk := clock.NewMock(time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	d := &driver.Driver{
		ID: "drv_mqtt_1", KYC: driver.KYCApproved, Status: driver.StatusIdle,
		Vehicle: driver.Vehicle{Type: driver.VehicleBike},
	}
	svc := location.NewService(location.NewMemoryIndex(clk), stubDriverPort{d}, clk)

	consumer, err := location.NewMQTTConsumer(url,
		fmt.Sprintf("godrive-test-%d", time.Now().UnixNano()), svc, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()

	pub := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker(url).
		SetClientID(fmt.Sprintf("godrive-pub-%d", time.Now().UnixNano())))
	if tok := pub.Connect(); tok.WaitTimeout(10*time.Second) && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	defer pub.Disconnect(250)

	// --- ping bình thường ---
	payload, _ := json.Marshal(location.Ping{
		Point:      geo.Point{Lat: 10.7740, Lng: 106.6995},
		BearingDeg: 45, AccuracyM: 10, BatteryPc: 88, At: clk.Now(),
	})
	if tok := pub.Publish("drv/drv_mqtt_1/loc", 1, false, payload); tok.WaitTimeout(5*time.Second) && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	waitUntil(t, 10*time.Second, "ping vào chỉ mục", func() bool {
		_, ok, _ := svc.Get(ctx, "drv_mqtt_1")
		return ok
	})
	snap, _, _ := svc.Get(ctx, "drv_mqtt_1")
	if snap.BatteryPc != 88 || snap.BearingDeg != 45 {
		t.Fatalf("thuộc tính ping không đi trọn vòng: %+v", snap)
	}

	// --- driverID lấy từ TOPIC, không tin payload ---
	spoof, _ := json.Marshal(location.Ping{
		DriverID:  "drv_mqtt_1", // kẻ gian khai ID của người khác
		Point:     geo.Point{Lat: 10.9, Lng: 106.9},
		AccuracyM: 10, BatteryPc: 50, At: clk.Now(),
	})
	if tok := pub.Publish("drv/drv_khac/loc", 1, false, spoof); tok.WaitTimeout(5*time.Second) && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	time.Sleep(700 * time.Millisecond)
	snap, _, _ = svc.Get(ctx, "drv_mqtt_1")
	if snap.BatteryPc != 88 {
		t.Fatal("payload KHÔNG được ghi đè vị trí của tài xế khác — ID phải lấy từ topic")
	}

	// --- Last Will: thiết bị mất kết nối ---
	if tok := pub.Publish("drv/drv_mqtt_1/status", 1, false, []byte("offline")); tok.WaitTimeout(5*time.Second) && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	waitUntil(t, 10*time.Second, "tài xế bị gỡ khỏi chỉ mục sau Last Will", func() bool {
		_, ok, _ := svc.Get(ctx, "drv_mqtt_1")
		return !ok
	})
}

type stubDriverPort struct{ d *driver.Driver }

func (s stubDriverPort) Get(context.Context, string) (*driver.Driver, error) { return s.d, nil }

func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hết giờ chờ: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestNATSRedeliversWhenHolderDies là ĐIỀU KIỆN HOÀN THÀNH cuối cùng của GĐ 3:
// giết tiến trình đang xử lý giữa chừng thì việc KHÔNG mất, mà được giao lại.
//
// Đây chính xác là thứ bus in-process không làm được. Ở đó handler chạy trong
// một goroutine không có ack — tiến trình chết là goroutine chết theo, và sự
// kiện biến mất không dấu vết. Với `trip.completed` thì đó là một chuyến không
// bao giờ được ghi sổ.
func TestNATSRedeliversWhenHolderDies(t *testing.T) {
	url := os.Getenv(testNATSEnv)
	if url == "" {
		t.Skipf("cần %s", testNATSEnv)
	}
	_ = natsBus(t) // dọn stream
	log := logger.New("error", false)

	// AckWait ngắn để test chạy nhanh; ở production là 30 giây.
	opts := eventbus.NATSOptions{AckWait: 2 * time.Second}

	// --- Pod A nhận việc rồi "chết" giữa chừng: không bao giờ ack ---
	podA, err := eventbus.NewNATSWithOptions(url, log, opts)
	if err != nil {
		t.Fatal(err)
	}
	tookIt := make(chan struct{})
	var once sync.Once
	podA.Subscribe(eventbus.TopicTripCompleted, "settlement", func(ctx context.Context, _ eventbus.Event) error {
		once.Do(func() { close(tookIt) })
		// Mô phỏng tiến trình bị đóng băng rồi bị giết: chặn tới khi hết giờ.
		<-ctx.Done()
		return ctx.Err()
	})

	if err := podA.Publish(context.Background(), eventbus.TopicTripCompleted,
		map[string]string{"trip_id": "trp_crash"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tookIt:
	case <-time.After(10 * time.Second):
		t.Fatal("pod A phải nhận được việc trước")
	}

	// --- Pod B lên thay ---
	podB, err := eventbus.NewNATSWithOptions(url, log, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer podB.Close()

	rescued := make(chan string, 4)
	podB.Subscribe(eventbus.TopicTripCompleted, "settlement", func(_ context.Context, e eventbus.Event) error {
		var p struct {
			TripID string `json:"trip_id"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		rescued <- p.TripID
		return nil
	})

	select {
	case id := <-rescued:
		if id != "trp_crash" {
			t.Fatalf("giao lại sai chuyến: %s", id)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("việc mà pod A đang giữ phải được giao lại cho pod B sau khi hết AckWait")
	}
}
