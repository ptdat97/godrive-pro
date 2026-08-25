package app

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/internal/platform/metrics"
	"github.com/example/godrive/internal/trip"
)

// Bucket chọn theo dải giá trị THẬT của từng đại lượng, không dùng chung một
// bộ mặc định: bucket sai dải thì histogram chỉ nói được "nhanh" hoặc "chậm".
var (
	httpBuckets     = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
	dispatchBuckets = []float64{0.5, 1, 2, 5, 10, 15, 30, 45, 60, 120}
	surgeBuckets    = []float64{1000, 1200, 1400, 1700, 2000}
)

type appMetrics struct {
	reg *metrics.Registry

	httpRequests *metrics.Counter
	httpDuration *metrics.Histogram

	tripsCreated  *metrics.Counter
	tripsFinished *metrics.Counter // theo trạng thái cuối
	offers        *metrics.Counter // theo kết cục
	dispatch      *metrics.Histogram
	surge         *metrics.Histogram

	ledgerPosts  *metrics.Counter // ok | error
	settleErrors *metrics.Counter
}

func newAppMetrics() *appMetrics {
	r := metrics.NewRegistry()
	return &appMetrics{
		reg: r,
		httpRequests: metrics.NewCounter(r, "godrive_http_requests_total",
			"số request HTTP theo route và mã trạng thái"),
		httpDuration: metrics.NewHistogram(r, "godrive_http_request_duration_seconds",
			"thời gian xử lý request HTTP", httpBuckets),
		tripsCreated: metrics.NewCounter(r, "godrive_trips_created_total",
			"số chuyến được tạo"),
		tripsFinished: metrics.NewCounter(r, "godrive_trips_finished_total",
			"số chuyến kết thúc, theo trạng thái cuối"),
		offers: metrics.NewCounter(r, "godrive_offers_total",
			"số lời mời theo kết cục"),
		dispatch: metrics.NewHistogram(r, "godrive_trip_dispatch_seconds",
			"thời gian từ lúc tạo chuyến tới lúc ghép được tài xế", dispatchBuckets),
		surge: metrics.NewHistogram(r, "godrive_surge_permille",
			"hệ số tăng giá đã phát ra, theo phần nghìn", surgeBuckets),
		ledgerPosts: metrics.NewCounter(r, "godrive_ledger_posts_total",
			"số lần ghi sổ cái, theo kết quả"),
		settleErrors: metrics.NewCounter(r, "godrive_settle_errors_total",
			"số lần ghi sổ chuyến thất bại"),
	}
}

// registerGauges gắn các gauge đọc trạng thái tại thời điểm scrape.
//
// Gauge nhận hàm thay vì giá trị: "số tài xế đang rảnh" là câu hỏi hỏi lúc nào
// trả lời lúc đó. Giữ một biến rồi phải nhớ cập nhật ở mọi nhánh code là cách
// chắc chắn sẽ có nhánh bị quên.
func (a *App) registerGauges() {
	m := a.Metrics
	idle := metrics.NewGauge(m.reg, "godrive_drivers_idle",
		"số tài xế đang rảnh (sẵn sàng nhận chuyến)")
	idle.Set(nil, func() float64 {
		ds, err := a.Drivers.ListByStatus(context.Background(), driver.StatusIdle, 1000)
		if err != nil {
			return -1 // -1 phân biệt "đo lỗi" với "thật sự bằng 0"
		}
		return float64(len(ds))
	})

	searching := metrics.NewGauge(m.reg, "godrive_trips_searching",
		"số chuyến đang chờ ghép tài xế")
	searching.Set(nil, func() float64 {
		ts, err := a.Trips.ListByStatus(context.Background(), trip.StatusSearching, 1000)
		if err != nil {
			return -1
		}
		return float64(len(ts))
	})

	if a.Outbox != nil {
		pending := metrics.NewGauge(m.reg, "godrive_outbox_pending",
			"số sự kiện chưa phát trong outbox")
		pending.Set(nil, func() float64 {
			n, err := a.Outbox.PendingCount(context.Background())
			if err != nil {
				return -1
			}
			return float64(n)
		})
		// Sự kiện đã thử quá MaxAttempts. Khác 0 nghĩa là sự kiện nghiệp vụ đã
		// mất — đây là gauge đáng cảnh báo nhất trong cả hệ thống.
		dead := metrics.NewGauge(m.reg, "godrive_outbox_dead",
			"số sự kiện đã vượt số lần thử tối đa")
		dead.Set(nil, func() float64 {
			n, err := a.Outbox.DeadCount(context.Background())
			if err != nil {
				return -1
			}
			return float64(n)
		})
	}

	surgeCells := metrics.NewGauge(m.reg, "godrive_surge_cells",
		"số ô lưới đang có nhu cầu được theo dõi")
	surgeCells.Set(nil, func() float64 { return float64(a.Surge.Cells()) })
}

// startMetricsConsumers đăng ký consumer chỉ để đo. Tách khỏi consumer nghiệp
// vụ: một lỗi khi đo không được làm hỏng việc thật.
func (a *App) startMetricsConsumers() {
	m := a.Metrics
	a.Bus.Subscribe(eventbus.TopicTripRequested, func(_ context.Context, _ eventbus.Event) error {
		m.tripsCreated.Inc(nil)
		return nil
	})
	a.Bus.Subscribe(eventbus.TopicOfferCreated, func(_ context.Context, _ eventbus.Event) error {
		m.offers.Inc(metrics.Labels{"outcome": "created"})
		return nil
	})
	a.Bus.Subscribe(eventbus.TopicOfferAccepted, func(_ context.Context, _ eventbus.Event) error {
		m.offers.Inc(metrics.Labels{"outcome": "accepted"})
		return nil
	})
	a.Bus.Subscribe(eventbus.TopicTripAssigned, a.observeDispatchLatency)
	a.Bus.Subscribe(eventbus.TopicTripCompleted, func(_ context.Context, _ eventbus.Event) error {
		m.tripsFinished.Inc(metrics.Labels{"status": "completed"})
		return nil
	})
	a.Bus.Subscribe(eventbus.TopicTripCancelled, func(_ context.Context, e eventbus.Event) error {
		var p struct {
			By string `json:"by"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		m.tripsFinished.Inc(metrics.Labels{"status": "cancelled", "by": p.By})
		return nil
	})
	a.Bus.Subscribe(eventbus.TopicPaymentSettled, func(_ context.Context, _ eventbus.Event) error {
		m.ledgerPosts.Inc(metrics.Labels{"result": "ok"})
		return nil
	})
}

// observeDispatchLatency đo thời gian từ lúc khách đặt tới lúc ghép được tài xế.
// Đây là con số quan trọng nhất với trải nghiệm khách hàng.
func (a *App) observeDispatchLatency(ctx context.Context, e eventbus.Event) error {
	var p struct {
		TripID string `json:"trip_id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.TripID == "" {
		return nil
	}
	t, err := a.Trips.Get(ctx, p.TripID)
	if err != nil || t.AssignedAt == nil {
		return nil
	}
	a.Metrics.dispatch.Observe(t.AssignedAt.Sub(t.RequestedAt).Seconds(), nil)
	return nil
}

// ObserveSurge ghi nhận một hệ số tăng giá đã phát ra.
func (a *App) ObserveSurge(permille int64) {
	a.Metrics.surge.Observe(float64(permille), nil)
}

// ---------------------------------------------------------------- readiness

type healthCheck struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Err  string `json:"error,omitempty"`
}

// readyz kiểm tra THẬT các phụ thuộc, không chỉ trả "ok".
//
// /healthz trả 200 ngay khi tiến trình còn sống (liveness — trả lời câu hỏi
// "có cần restart không"). /readyz trả 503 khi phụ thuộc chết, để bộ cân bằng
// tải ngừng đẩy request vào (readiness — "có nên nhận việc không"). Gộp hai
// thứ này làm một sẽ khiến pod bị restart liên tục mỗi khi CSDL chậm.
func (a *App) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := []healthCheck{}
	ready := true
	add := func(name string, err error) {
		c := healthCheck{Name: name, OK: err == nil}
		if err != nil {
			c.Err = err.Error()
			ready = false
		}
		checks = append(checks, c)
	}

	if a.db != nil {
		add("postgres", a.db.PingContext(ctx))
	}
	if a.Redis != nil {
		add("redis", a.Redis.Ping(ctx))
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	httpx.JSON(w, status, map[string]any{"ready": ready, "checks": checks})
}

// ---------------------------------------------------------------- middleware

// metricsMiddleware đếm request và đo thời gian xử lý.
func (a *App) metricsMiddleware() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// /metrics tự đo mình sẽ làm nhiễu chính số liệu nó phát ra.
			if r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			sw := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(sw, r)

			route := routeTemplate(r.URL.Path)
			labels := metrics.Labels{
				"method": r.Method,
				"route":  route,
				"status": strconv.Itoa(sw.status()),
			}
			a.Metrics.httpRequests.Inc(labels)
			a.Metrics.httpDuration.Observe(time.Since(start).Seconds(),
				metrics.Labels{"method": r.Method, "route": route})
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(c int) {
	s.code = c
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusRecorder) status() int {
	if s.code == 0 {
		return http.StatusOK
	}
	return s.code
}

// idSegment khớp ID sinh bởi pkg/id: 3 chữ thường + "_" + 26 ký tự base32.
var idSegment = regexp.MustCompile(`^[a-z]{3}_[0-9A-Z]{26}$`)

// routeTemplate đưa đường dẫn về dạng khuôn mẫu để nhãn có SỐ GIÁ TRỊ HỮU HẠN.
//
// Đưa thẳng URL.Path vào nhãn là cách chắc chắn làm nổ Prometheus: mỗi chuyến
// đi sẽ tạo một chuỗi số liệu mới và không bao giờ được thu hồi.
func routeTemplate(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if idSegment.MatchString(p) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}
