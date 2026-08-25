package httpx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/godrive/pkg/clock"
)

// TestRateLimitAllowsBurstThenThrottles: token bucket phải cho qua đúng burst
// rồi mới chặn, và hồi phục theo thời gian.
func TestRateLimitAllowsBurstThenThrottles(t *testing.T) {
	clk := clock.NewMock(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC))
	rl := NewRateLimit(30, 60)
	rl.Clock = clk

	for i := 0; i < 60; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request thứ %d trong burst 60 phải được cho qua", i+1)
		}
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("quá burst phải bị chặn")
	}
	// 30 token/giây: sau 1 giây phải nạp lại đủ để đi tiếp.
	clk.Advance(time.Second)
	if !rl.Allow("1.2.3.4") {
		t.Fatal("sau 1 giây phải nạp lại token")
	}
	// Khoá khác có bucket riêng.
	if !rl.Allow("5.6.7.8") {
		t.Fatal("IP khác không được ảnh hưởng")
	}
}

// TestRateLimitSweepsIdleBuckets: map bucket phải nhỏ lại, nếu không đây là
// một rò rỉ bộ nhớ theo số IP đã từng gọi.
func TestRateLimitSweepsIdleBuckets(t *testing.T) {
	clk := clock.NewMock(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC))
	rl := NewRateLimit(30, 60)
	rl.Clock = clk

	const n = 10000
	for i := 0; i < n; i++ {
		rl.Allow(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	if got := rl.Len(); got != n {
		t.Fatalf("phải giữ %d bucket, có %d", n, got)
	}

	// Vượt IdleTTL rồi có thêm một lượt gọi để kích hoạt quét.
	clk.Advance(11 * time.Minute)
	rl.Allow("192.168.0.1")

	if got := rl.Len(); got != 1 {
		t.Fatalf("bucket nguội phải bị dọn, còn lại %d (chỉ nên còn 1 bucket vừa tạo)", got)
	}
}

// TestRateLimitSweepIsRateLimited: quét là O(n), không được chạy mỗi request.
func TestRateLimitSweepIsRateLimited(t *testing.T) {
	clk := clock.NewMock(time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC))
	rl := NewRateLimit(30, 60)
	rl.Clock = clk

	rl.Allow("a")
	clk.Advance(11 * time.Minute)
	rl.Allow("b") // lượt này quét, xoá "a"
	if got := rl.Len(); got != 1 {
		t.Fatalf("sau quét đầu phải còn 1, có %d", got)
	}
	// Chưa tới SweepEvery kế tiếp: "b" nguội cũng chưa bị dọn.
	clk.Advance(11 * time.Minute)
	rl.Allow("c")
	clk.Advance(30 * time.Second) // < SweepEvery = 1 phút
	rl.Allow("d")
	if got := rl.Len(); got != 2 {
		t.Fatalf("chưa tới chu kỳ quét kế tiếp thì không được quét lại, có %d bucket", got)
	}
}

// Endpoint vận hành KHÔNG được rate limit.
//
// Chặn chúng là cách tự tạo sự cố dây chuyền: pod tải cao → health check bị
// chặn → orchestrator giết pod → tải dồn sang pod còn lại → lặp lại.
func TestOperationalPathsBypassRateLimit(t *testing.T) {
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		if !IsOperationalPath(p) {
			t.Fatalf("%s phải được miễn rate limit", p)
		}
	}
	for _, p := range []string{"/v1/trips", "/v1/auth/otp", "/", "/healthz/x"} {
		if IsOperationalPath(p) {
			t.Fatalf("%s KHÔNG được miễn rate limit", p)
		}
	}

	clk := clock.NewMock(time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	rl := NewRateLimit(1, 1)
	rl.Clock = clk
	h := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Dùng hết hạn mức bằng một request thường.
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/trips", nil))
	}
	// Health check vẫn phải qua.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/healthz phải qua kể cả khi đã hết hạn mức, được %d", w.Code)
	}
}
