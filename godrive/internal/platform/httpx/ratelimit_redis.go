package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/pkg/errs"
)

// RedisRateLimit là token bucket dùng chung cho cả cụm.
//
// Bản in-process cho mỗi pod một hạn mức riêng: chạy 5 pod nghĩa là kẻ tấn công
// được gấp 5 lần hạn mức. Với endpoint gửi OTP thì đó là tiền tin nhắn thật.
type RedisRateLimit struct {
	rdb      *redis.Client
	prefix   string
	rate     float64 // token mỗi giây
	capacity float64 // burst
	// FailOpen quyết định hành vi khi Redis chết: cho qua (true) hay chặn (false).
	//
	// Mặc định CHO QUA. Redis hỏng mà chặn hết request nghĩa là biến sự cố của
	// một thành phần phụ trợ thành sự cố toàn hệ thống. Với endpoint tốn tiền
	// thật (gửi OTP) thì nên đặt false.
	FailOpen bool
}

func NewRedisRateLimit(rdb *redis.Client, prefix string, perSecond, burst float64) *RedisRateLimit {
	return &RedisRateLimit{rdb: rdb, prefix: prefix, rate: perSecond, capacity: burst, FailOpen: true}
}

// bucketScript cài token bucket nguyên tử.
//
// Đọc-tính-ghi ở phía client sẽ hỏng ngay khi có hai pod: cả hai đọc cùng một
// số token rồi cùng cho qua. Script chạy trọn trong Redis nên không có khe hở.
//
// TTL đặt bằng thời gian đổ đầy bucket: sau chừng đó không dùng thì bucket đã
// đầy lại và không còn mang thông tin gì — để Redis tự dọn thay vì viết job.
var bucketScript = redis.NewScript(`
local key      = KEYS[1]
local rate     = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now      = tonumber(ARGV[3])
local ttl      = tonumber(ARGV[4])

local data   = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts     = tonumber(data[2])

if tokens == nil then
  tokens = capacity
  ts = now
end

local delta = math.max(0, now - ts) / 1000.0
tokens = math.min(capacity, tokens + delta * rate)

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call('HSET', key, 'tokens', tokens, 'ts', now)
redis.call('PEXPIRE', key, ttl)
return allowed
`)

func (rl *RedisRateLimit) Allow(ctx context.Context, key string) (bool, error) {
	ttl := time.Duration(rl.capacity/rl.rate*1000)*time.Millisecond + time.Second
	n, err := bucketScript.Run(ctx, rl.rdb,
		[]string{rl.prefix + "rl:" + key},
		rl.rate, rl.capacity, time.Now().UnixMilli(), ttl.Milliseconds(),
	).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (rl *RedisRateLimit) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsOperationalPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ok, err := rl.Allow(r.Context(), clientIP(r))
			if err != nil {
				if !rl.FailOpen {
					Fail(w, r, errs.E(errs.KindRateLimited, "rate_limited",
						"Hệ thống đang bận, vui lòng thử lại sau."))
					return
				}
				ok = true
			}
			if !ok {
				Fail(w, r, errs.E(errs.KindRateLimited, "rate_limited",
					"Bạn thao tác quá nhanh, vui lòng thử lại sau."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
