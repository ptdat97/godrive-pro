package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
)

type Middleware func(http.Handler) http.Handler

// Chain áp dụng middleware theo thứ tự khai báo (cái đầu chạy ngoài cùng).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := r.Header.Get("X-Request-Id")
			if rid == "" {
				rid = id.New("req")
			}
			w.Header().Set("X-Request-Id", rid)
			next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), rid)))
		})
	}
}

func Logging(l *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rl := l.With("request_id", RequestIDFrom(r.Context()))
			ctx := logger.With(r.Context(), rl)
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r.WithContext(ctx))
			rl.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytes,
				"dur_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.From(r.Context()).Error("panic",
						"recover", rec, "stack", string(debug.Stack()))
					Fail(w, r, errs.E(errs.KindInternal, "internal_error", "internal"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit là token bucket in-process theo khoá (IP hoặc số điện thoại).
// Production: chuyển sang Redis để giới hạn toàn cụm.
type RateLimit struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64
	capacity float64

	// Clock tiêm được để test tất định. Mặc định là đồng hồ thật.
	Clock clock.Clock
	// IdleTTL: bucket không được chạm tới lâu hơn khoảng này sẽ bị dọn.
	// Một bucket đã đầy lại thì không còn mang thông tin gì — giữ nó chỉ tốn bộ nhớ.
	IdleTTL time.Duration
	// SweepEvery giới hạn tần suất quét để chi phí O(n) được phân bổ đều.
	SweepEvery time.Duration
	lastSweep  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimit(perSecond, burst float64) *RateLimit {
	return &RateLimit{
		buckets: map[string]*bucket{}, rate: perSecond, capacity: burst,
		Clock:      clock.Real(),
		IdleTTL:    10 * time.Minute,
		SweepEvery: time.Minute,
	}
}

// sweep xoá bucket đã nguội. Gọi khi ĐANG giữ rl.mu.
//
// Không dọn thì map lớn dần theo số IP đã từng gọi và không bao giờ nhỏ lại —
// rò rỉ chậm nhưng chắc chắn trên tiến trình sống lâu.
func (rl *RateLimit) sweep(now time.Time) {
	if rl.SweepEvery <= 0 || now.Sub(rl.lastSweep) < rl.SweepEvery {
		return
	}
	rl.lastSweep = now
	if rl.IdleTTL <= 0 {
		return
	}
	for k, b := range rl.buckets {
		if now.Sub(b.last) > rl.IdleTTL {
			delete(rl.buckets, k)
		}
	}
}

// Len là số bucket đang giữ. Dùng cho test và metric.
func (rl *RateLimit) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}

func (rl *RateLimit) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.Clock.Now()
	if rl.lastSweep.IsZero() {
		rl.lastSweep = now
	}
	rl.sweep(now)
	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.capacity - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *RateLimit) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.Allow(clientIP(r)) {
				Fail(w, r, errs.E(errs.KindRateLimited, "rate_limited",
					"Bạn thao tác quá nhanh, vui lòng thử lại sau."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		return host[:i]
	}
	return host
}
