// Command loadgen sinh tải lên một backend GoDrive ĐANG CHẠY, qua đúng các
// đường mà app thật dùng: HTTP cho thao tác, MQTT cho vị trí và tin đẩy.
//
// Đo từ phía THIẾT BỊ chứ không tin số liệu của máy chủ: "lời mời tới tay tài
// xế trong bao lâu" là thứ tài xế cảm nhận, gồm cả broker và mạng — histogram
// phía máy chủ chỉ thấy được tới lúc broker nhận tin.
//
// Yêu cầu backend:
//
//	DEV_AUTH=true                       — lấy được mã OTP và nạp ví thủ công
//	ADMIN_PHONES=<-admin-phone>         — để duyệt hồ sơ tài xế ảo
//	RATE_LIMIT_RPS / RATE_LIMIT_BURST   — đủ lớn: mọi thiết bị ảo chung một IP
//
// Chạy:
//
//	go run ./cmd/loadgen -drivers 1000 -trip-rate 2 -duration 5m
//
// Xem docs/08 §8.14.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/example/godrive/pkg/geo"
)

type options struct {
	api, mqtt     string
	adminPhone    string
	drivers       int
	riders        int
	tripRate      float64
	ping          time.Duration
	duration      time.Duration
	warmup        time.Duration
	acceptProb    float64
	seedParallel  int
	center        geo.Point
	radiusM       float64
	tripStep      time.Duration
	connectSpread time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.api, "api", "http://localhost:8080", "địa chỉ backend")
	flag.StringVar(&o.mqtt, "mqtt", "tcp://localhost:1883", "địa chỉ broker MQTT")
	flag.StringVar(&o.adminPhone, "admin-phone", "0900000099", "số điện thoại có trong ADMIN_PHONES của backend")
	flag.IntVar(&o.drivers, "drivers", 100, "số tài xế ảo online")
	flag.IntVar(&o.riders, "riders", 0, "số khách ảo (0 = tự tính theo trip-rate)")
	flag.Float64Var(&o.tripRate, "trip-rate", 1, "số chuyến đặt mỗi giây")
	flag.DurationVar(&o.ping, "ping", 4*time.Second, "chu kỳ gửi vị trí của mỗi tài xế")
	flag.DurationVar(&o.duration, "duration", 2*time.Minute, "thời gian đo (sau warmup)")
	flag.DurationVar(&o.warmup, "warmup", 15*time.Second, "chờ sau khi mọi tài xế online, trước khi đặt chuyến")
	flag.Float64Var(&o.acceptProb, "accept", 0.85, "xác suất tài xế nhận một lời mời")
	flag.IntVar(&o.seedParallel, "seed-parallel", 40, "số tài khoản tạo song song")
	flag.Float64Var(&o.radiusM, "radius", 3000, "bán kính rải tài xế và điểm đón quanh tâm (m)")
	flag.DurationVar(&o.tripStep, "trip-step", 3*time.Second, "thời gian mỗi bước của chuyến (tới đón, bắt đầu, hoàn tất)")
	flag.DurationVar(&o.connectSpread, "connect-spread", 20*time.Second, "rải việc nối MQTT trong khoảng này — tránh dồn cả nghìn lần xác thực vào một giây")
	flag.Parse()
	o.center = geo.Point{Lat: 10.7769, Lng: 106.7009} // Quận 1, TP.HCM
	if o.riders == 0 {
		// Mỗi chuyến chiếm một khách khoảng 6 bước trip-step cộng thời gian ghép.
		o.riders = int(o.tripRate*(6*o.tripStep.Seconds()+30)) + 10
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	st := newStats()
	runID := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(90) + 10
	cl := newClient(o.api)

	log.Printf("chuẩn bị %d tài xế, %d khách (run %d)…", o.drivers, o.riders, runID)
	t0 := time.Now()
	adminTok, err := cl.adminLogin(ctx, o.adminPhone)
	if err != nil {
		log.Fatalf("đăng nhập quản trị: %v — số %s phải có trong ADMIN_PHONES", err, o.adminPhone)
	}
	drivers, err := seedDrivers(ctx, cl, adminTok, o, runID)
	if err != nil {
		log.Fatalf("tạo tài xế: %v", err)
	}
	riders, err := seedRiders(ctx, cl, o, runID)
	if err != nil {
		log.Fatalf("tạo khách: %v", err)
	}
	log.Printf("đã tạo tài khoản trong %s", time.Since(t0).Round(time.Second))

	// Tài xế online + nối MQTT, rải đều để không dồn xác thực.
	var wg sync.WaitGroup
	simCtx, stopSim := context.WithCancel(ctx)
	for i, d := range drivers {
		wg.Add(1)
		go func(i int, d *simDriver) {
			defer wg.Done()
			delay := time.Duration(int64(o.connectSpread) * int64(i) / int64(len(drivers)))
			select {
			case <-time.After(delay):
			case <-simCtx.Done():
				return
			}
			d.run(simCtx, cl, o, st)
		}(i, d)
	}
	time.Sleep(o.connectSpread)
	log.Printf("%d/%d tài xế đã nối MQTT, warmup %s…", st.connected.Load(), len(drivers), o.warmup)
	select {
	case <-time.After(o.warmup):
	case <-ctx.Done():
	}

	// Pha đo: đặt chuyến theo nhịp trip-rate.
	st.reset()
	st.markServer(o.api)
	log.Printf("bắt đầu đo %s ở %.1f chuyến/giây", o.duration, o.tripRate)
	measureCtx, stopMeasure := context.WithTimeout(ctx, o.duration)
	pool := newRiderPool(riders)
	runDemand(measureCtx, cl, o, pool, st)
	stopMeasure()

	// Cho chuyến đang chạy dở kết thúc để số liệu chuyến không bị cắt cụt.
	log.Printf("ngừng đặt chuyến, chờ chuyến dở dang tối đa 60 giây…")
	pool.waitIdle(60 * time.Second)
	elapsed := st.elapsed()
	stopSim()
	wg.Wait()

	st.report(os.Stdout, o, elapsed)
	if err := st.scrapeServer(os.Stdout, o.api); err != nil {
		fmt.Fprintf(os.Stdout, "\n(không đọc được /metrics: %v)\n", err)
	}
}
