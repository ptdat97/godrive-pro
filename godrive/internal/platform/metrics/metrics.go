// Package metrics là bộ đếm số liệu tối giản, phát ra theo định dạng văn bản
// của Prometheus (exposition format 0.0.4).
//
// Vì sao tự viết thay vì dùng prometheus/client_golang: ở đây chỉ cần vài
// counter, gauge và histogram với bucket cố định, trong khi client_golang kéo
// theo protobuf + procfs + common — một cây phụ thuộc lớn hơn nhiều lần phần
// thực sự dùng tới. Định dạng văn bản đã ổn định nhiều năm và rất đơn giản.
//
// Khi cần đến exemplar, native histogram hay push gateway thì đổi sang
// client_golang: chỗ phải sửa chỉ là package này và handler /metrics, vì code
// nghiệp vụ chỉ gọi qua Counter/Gauge/Histogram.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry giữ toàn bộ số liệu của một tiến trình.
type Registry struct {
	mu   sync.RWMutex
	cols []collector
}

type collector interface {
	write(sb *strings.Builder)
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) add(c collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cols = append(r.cols, c)
}

// Handler phát số liệu ra định dạng Prometheus.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.mu.RLock()
		cols := append([]collector(nil), r.cols...)
		r.mu.RUnlock()

		var sb strings.Builder
		for _, c := range cols {
			c.write(&sb)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(sb.String()))
	})
}

// ---------------------------------------------------------------- nhãn

// Labels là tập nhãn của một chuỗi số liệu. Số lượng giá trị nhãn phải HỮU HẠN
// và nhỏ — mỗi tổ hợp là một chuỗi riêng nằm mãi trong bộ nhớ. Đừng bao giờ đưa
// ID chuyến, ID tài xế hay đường dẫn có tham số vào đây.
type Labels map[string]string

func (l Labels) key() string {
	if len(l) == 0 {
		return ""
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(escape(l[k]))
		sb.WriteString(`"`)
	}
	sb.WriteByte('}')
	return sb.String()
}

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// ---------------------------------------------------------------- counter

// Counter chỉ tăng. Dùng cho "số lần đã xảy ra".
type Counter struct {
	name, help string
	mu         sync.RWMutex
	vals       map[string]*atomic.Int64
}

func NewCounter(r *Registry, name, help string) *Counter {
	c := &Counter{name: name, help: help, vals: map[string]*atomic.Int64{}}
	r.add(c)
	return c
}

func (c *Counter) Inc(l Labels)          { c.Add(1, l) }
func (c *Counter) Add(n int64, l Labels) { c.slot(l.key()).Add(n) }

func (c *Counter) slot(key string) *atomic.Int64 {
	c.mu.RLock()
	v, ok := c.vals[key]
	c.mu.RUnlock()
	if ok {
		return v
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok = c.vals[key]; ok {
		return v
	}
	v = &atomic.Int64{}
	c.vals[key] = v
	return v
}

func (c *Counter) write(sb *strings.Builder) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	writeHeader(sb, c.name, c.help, "counter")
	for _, k := range sortedKeys(c.vals) {
		fmt.Fprintf(sb, "%s%s %d\n", c.name, k, c.vals[k].Load())
	}
}

// ---------------------------------------------------------------- gauge

// Gauge lên xuống được. Dùng cho "hiện đang là bao nhiêu".
type Gauge struct {
	name, help string
	mu         sync.RWMutex
	fns        map[string]func() float64
}

// NewGauge nhận HÀM đọc giá trị thay vì giá trị.
//
// Gauge kiểu "số tài xế đang rảnh" hay "outbox tồn đọng" là câu hỏi hỏi lúc nào
// trả lời lúc đó; giữ một biến rồi phải nhớ cập nhật ở mọi nhánh code là cách
// chắc chắn sẽ có nhánh bị quên.
func NewGauge(r *Registry, name, help string) *Gauge {
	g := &Gauge{name: name, help: help, fns: map[string]func() float64{}}
	r.add(g)
	return g
}

func (g *Gauge) Set(l Labels, fn func() float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fns[l.key()] = fn
}

func (g *Gauge) write(sb *strings.Builder) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	writeHeader(sb, g.name, g.help, "gauge")
	for _, k := range sortedKeys(g.fns) {
		fmt.Fprintf(sb, "%s%s %s\n", g.name, k, formatFloat(g.fns[k]()))
	}
}

// ---------------------------------------------------------------- histogram

// Histogram đếm theo khoảng. Bucket cố định lúc khai báo.
type Histogram struct {
	name, help string
	buckets    []float64
	mu         sync.Mutex
	series     map[string]*histSeries
}

type histSeries struct {
	counts []int64
	sum    float64
	total  int64
}

func NewHistogram(r *Registry, name, help string, buckets []float64) *Histogram {
	h := &Histogram{name: name, help: help, buckets: buckets, series: map[string]*histSeries{}}
	r.add(h)
	return h
}

func (h *Histogram) Observe(v float64, l Labels) {
	key := l.key()
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[key]
	if !ok {
		s = &histSeries{counts: make([]int64, len(h.buckets))}
		h.series[key] = s
	}
	for i, b := range h.buckets {
		if v <= b {
			s.counts[i]++
		}
	}
	s.sum += v
	s.total++
}

func (h *Histogram) write(sb *strings.Builder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	writeHeader(sb, h.name, h.help, "histogram")
	for _, k := range sortedKeys(h.series) {
		s := h.series[k]
		inner := strings.TrimSuffix(strings.TrimPrefix(k, "{"), "}")
		sep := ""
		if inner != "" {
			sep = ","
		}
		for i, b := range h.buckets {
			fmt.Fprintf(sb, "%s_bucket{%s%sle=\"%s\"} %d\n",
				h.name, inner, sep, formatFloat(b), s.counts[i])
		}
		fmt.Fprintf(sb, "%s_bucket{%s%sle=\"+Inf\"} %d\n", h.name, inner, sep, s.total)
		fmt.Fprintf(sb, "%s_sum%s %s\n", h.name, k, formatFloat(s.sum))
		fmt.Fprintf(sb, "%s_count%s %d\n", h.name, k, s.total)
	}
}

// ---------------------------------------------------------------- tiện ích

func writeHeader(sb *strings.Builder, name, help, typ string) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
