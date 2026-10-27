package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

// stats gom số đo từ mọi thiết bị ảo. Giữ nguyên từng mẫu thay vì histogram:
// vài trăm nghìn mẫu vẫn nhỏ, và phân vị tính từ mẫu thật không phụ thuộc
// việc chọn bucket.
type stats struct {
	connected atomic.Int64

	mu       sync.Mutex
	start    time.Time
	samples  map[string][]time.Duration
	counters map[string]int64
	server   map[string]map[string]float64 // histogram máy chủ lúc bắt đầu đo
}

func newStats() *stats {
	return &stats{start: time.Now(), samples: map[string][]time.Duration{}, counters: map[string]int64{}}
}

// reset xoá số liệu của pha chuẩn bị: thời gian tạo tài khoản và lúc cả nghìn
// thiết bị cùng nối không phải là tải ổn định cần đo.
func (s *stats) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start = time.Now()
	s.samples = map[string][]time.Duration{}
	s.counters = map[string]int64{}
}

func (s *stats) elapsed() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.start)
}

func (s *stats) observe(name string, d time.Duration) {
	s.mu.Lock()
	s.samples[name] = append(s.samples[name], d)
	s.mu.Unlock()
}

func (s *stats) count(name string) {
	s.mu.Lock()
	s.counters[name]++
	s.mu.Unlock()
}

func pct(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func ms(d time.Duration) string { return fmt.Sprintf("%.0f ms", float64(d)/float64(time.Millisecond)) }

// Thứ tự in, và ngưỡng mục tiêu trong tài liệu (0 = không có mục tiêu).
var reportOrder = []struct {
	name   string
	target time.Duration
	desc   string
}{
	{"offer_delivery", time.Second, "lời mời: tạo ở máy chủ → tới thiết bị tài xế"},
	{"dispatch_rider_seen", 0, "khách bấm đặt → thấy đã có tài xế (gồm thời gian tài xế nghĩ)"},
	{"driver_loc_freshness", 3 * time.Second, "vị trí: tài xế gửi → tới máy khách"},
	{"api_quote", 100 * time.Millisecond, "POST /v1/quotes"},
	{"api_create_trip", 100 * time.Millisecond, "POST /v1/trips"},
	{"api_accept", 100 * time.Millisecond, "POST /v1/offers/{id}/accept"},
	{"api_trip_arrived", 100 * time.Millisecond, "POST /v1/trips/{id}/arrived"},
	{"api_trip_start", 100 * time.Millisecond, "POST /v1/trips/{id}/start"},
	{"api_trip_complete", 100 * time.Millisecond, "POST /v1/trips/{id}/complete"},
}

func (s *stats) report(w io.Writer, o options, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Fprintf(w, "\n=== GoDrive loadgen — %d tài xế, ping %s, %.1f chuyến/giây, đo %s ===\n\n",
		o.drivers, o.ping, o.tripRate, elapsed.Round(time.Second))
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "số đo\tn\tp50\tp95\tp99\tmax\tmục tiêu p95\t")
	for _, r := range reportOrder {
		xs := append([]time.Duration(nil), s.samples[r.name]...)
		if len(xs) == 0 {
			continue
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
		target := "—"
		if r.target > 0 {
			mark := "✅"
			if pct(xs, 0.95) > r.target {
				mark = "❌"
			}
			target = fmt.Sprintf("< %s %s", ms(r.target), mark)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t\n", r.name, len(xs),
			ms(pct(xs, 0.5)), ms(pct(xs, 0.95)), ms(pct(xs, 0.99)), ms(xs[len(xs)-1]), target)
	}
	tw.Flush()
	fmt.Fprintln(w)
	for _, r := range reportOrder {
		fmt.Fprintf(w, "  %-22s %s\n", r.name, r.desc)
	}

	fmt.Fprintf(w, "\nThông lượng: %.0f bản tin vị trí/phút (%d thiết bị nối MQTT)\n",
		float64(s.counters["pings"])/elapsed.Minutes(), s.connected.Load())
	keys := make([]string, 0, len(s.counters))
	for k := range s.counters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "\nBộ đếm:")
	for _, k := range keys {
		fmt.Fprintf(w, "  %-32s %d\n", k, s.counters[k])
	}
}

// ------------------------------------------------------------------ máy chủ

// serverHists là các histogram của máy chủ đáng đọc cùng số đo phía thiết bị.
var serverHists = []string{
	"godrive_offer_push_delay_seconds",
	"godrive_trip_dispatch_seconds",
	"godrive_http_request_duration_seconds",
}

// readHists đọc /metrics, cộng dồn bucket của mỗi histogram qua mọi nhãn.
func readHists(api string) (map[string]map[string]float64, error) {
	resp, err := http.Get(api + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := map[string]map[string]float64{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		for _, h := range serverHists {
			if !strings.HasPrefix(line, h+"_bucket{") {
				continue
			}
			i := strings.Index(line, `le="`)
			j := strings.LastIndex(line, " ")
			if i < 0 || j < 0 {
				continue
			}
			le := line[i+4 : strings.Index(line[i+4:], `"`)+i+4]
			v, _ := strconv.ParseFloat(line[j+1:], 64)
			if out[h] == nil {
				out[h] = map[string]float64{}
			}
			out[h][le] += v
		}
	}
	return out, sc.Err()
}

// markServer chụp histogram máy chủ lúc bắt đầu đo, để báo cáo chỉ tính phần
// phát sinh trong pha đo.
func (s *stats) markServer(api string) {
	h, err := readHists(api)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.server = h
	s.mu.Unlock()
}

// scrapeServer in phân vị ước lượng từ bucket — chỉ là CẬN TRÊN: p95 nằm ở
// bucket nào thì in giới hạn trên của bucket đó.
func (s *stats) scrapeServer(w io.Writer, api string) error {
	after, err := readHists(api)
	if err != nil {
		return err
	}
	s.mu.Lock()
	before := s.server
	s.mu.Unlock()
	fmt.Fprintln(w, "\nPhía máy chủ (từ /metrics, chỉ phần phát sinh trong pha đo; cận trên theo bucket):")
	for _, h := range serverHists {
		b := after[h]
		if b == nil {
			continue
		}
		type bk struct {
			le float64
			s  string
			n  float64
		}
		var bs []bk
		for le, n := range b {
			n -= before[h][le]
			f, err := strconv.ParseFloat(le, 64)
			if le == "+Inf" || err != nil {
				f = math.Inf(1)
			}
			bs = append(bs, bk{f, le, n})
		}
		sort.Slice(bs, func(i, j int) bool { return bs[i].le < bs[j].le })
		total := bs[len(bs)-1].n
		if total == 0 {
			continue
		}
		q := func(p float64) string {
			for _, x := range bs {
				if x.n >= p*total {
					return "≤ " + x.s + "s"
				}
			}
			return "?"
		}
		fmt.Fprintf(w, "  %-40s n=%-8.0f p50 %-10s p95 %-10s p99 %s\n", h, total, q(.5), q(.95), q(.99))
	}
	return nil
}
