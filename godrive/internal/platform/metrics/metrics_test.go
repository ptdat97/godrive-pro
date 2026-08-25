package metrics

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type sai: %q", ct)
	}
	return w.Body.String()
}

func mustContain(t *testing.T, body string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(body, l) {
			t.Fatalf("thiếu dòng %q trong:\n%s", l, body)
		}
	}
}

func TestCounterAndLabels(t *testing.T) {
	r := NewRegistry()
	c := NewCounter(r, "godrive_offers_total", "số lời mời theo kết cục")
	c.Inc(Labels{"outcome": "created"})
	c.Inc(Labels{"outcome": "created"})
	c.Inc(Labels{"outcome": "accepted"})

	body := scrape(t, r)
	mustContain(t, body,
		"# TYPE godrive_offers_total counter",
		`godrive_offers_total{outcome="accepted"} 1`,
		`godrive_offers_total{outcome="created"} 2`,
	)
}

// Nhãn phải sắp xếp ổn định, nếu không mỗi lần scrape lại ra một chuỗi khác và
// Prometheus coi đó là hai chuỗi số liệu riêng.
func TestLabelOrderIsStable(t *testing.T) {
	a := Labels{"b": "2", "a": "1", "c": "3"}.key()
	b := Labels{"c": "3", "a": "1", "b": "2"}.key()
	if a != b {
		t.Fatalf("thứ tự nhãn không ổn định: %q vs %q", a, b)
	}
	if a != `{a="1",b="2",c="3"}` {
		t.Fatalf("định dạng nhãn sai: %q", a)
	}
}

func TestLabelValueEscaped(t *testing.T) {
	got := Labels{"path": `/a"b\c`}.key()
	if !strings.Contains(got, `path="/a\"b\\c"`) {
		t.Fatalf("giá trị nhãn chưa được escape: %q", got)
	}
}

func TestGaugeReadsAtScrapeTime(t *testing.T) {
	r := NewRegistry()
	n := 0
	g := NewGauge(r, "godrive_drivers_idle", "số tài xế đang rảnh")
	g.Set(nil, func() float64 { return float64(n) })

	if !strings.Contains(scrape(t, r), "godrive_drivers_idle 0") {
		t.Fatal("lần scrape đầu phải là 0")
	}
	n = 7
	// Không gọi Set lại: gauge phải đọc giá trị TẠI THỜI ĐIỂM scrape.
	if !strings.Contains(scrape(t, r), "godrive_drivers_idle 7") {
		t.Fatal("gauge phải đọc lại giá trị mỗi lần scrape")
	}
}

func TestHistogramBucketsAreCumulative(t *testing.T) {
	r := NewRegistry()
	h := NewHistogram(r, "godrive_dispatch_seconds", "thời gian dispatch", []float64{0.1, 0.5, 1})
	for _, v := range []float64{0.05, 0.3, 0.7, 5} {
		h.Observe(v, nil)
	}
	body := scrape(t, r)
	mustContain(t, body,
		`godrive_dispatch_seconds_bucket{le="0.1"} 1`,
		`godrive_dispatch_seconds_bucket{le="0.5"} 2`,
		`godrive_dispatch_seconds_bucket{le="1"} 3`,
		`godrive_dispatch_seconds_bucket{le="+Inf"} 4`,
		"godrive_dispatch_seconds_count 4",
		"godrive_dispatch_seconds_sum 6.05",
	)
}

func TestHistogramWithLabels(t *testing.T) {
	r := NewRegistry()
	h := NewHistogram(r, "godrive_http_seconds", "thời gian xử lý", []float64{0.1, 1})
	h.Observe(0.05, Labels{"method": "GET"})
	body := scrape(t, r)
	mustContain(t, body,
		`godrive_http_seconds_bucket{method="GET",le="0.1"} 1`,
		`godrive_http_seconds_bucket{method="GET",le="+Inf"} 1`,
		`godrive_http_seconds_count{method="GET"} 1`,
	)
}

// Số liệu bị cập nhật từ nhiều goroutine là chuyện thường ngày.
func TestConcurrentUpdates(t *testing.T) {
	r := NewRegistry()
	c := NewCounter(r, "godrive_x_total", "x")
	h := NewHistogram(r, "godrive_y_seconds", "y", []float64{1})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Inc(Labels{"k": string(rune('a' + i%5))})
				h.Observe(0.5, nil)
			}
		}(i)
	}
	wg.Wait()

	body := scrape(t, r)
	if !strings.Contains(body, "godrive_y_seconds_count 5000") {
		t.Fatalf("histogram phải đếm đủ 5000:\n%s", body)
	}
	total := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "godrive_x_total{") {
			var n int
			_, _ = fmtSscan(line, &n)
			total += n
		}
	}
	if total != 5000 {
		t.Fatalf("counter phải cộng đủ 5000, được %d", total)
	}
}

func fmtSscan(line string, n *int) (int, error) {
	parts := strings.Fields(line)
	v := 0
	for _, ch := range parts[len(parts)-1] {
		v = v*10 + int(ch-'0')
	}
	*n = v
	return 1, nil
}
