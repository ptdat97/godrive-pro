package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/pkg/errs"
)

// Giới hạn gửi OTP cho MỘT số điện thoại (G-22), chung cho mọi vai.
//
// Rate limit theo IP không đủ: kẻ "bơm SMS" xoay IP, còn nạn nhân là một số
// điện thoại bị dội tin — và mỗi tin là tiền của nền tảng.
const (
	OTPCooldown   = 60 * time.Second // khoảng cách tối thiểu giữa hai lần gửi
	OTPMaxPerHour = 5
	OTPMaxPerDay  = 10
)

// OTPLimiter quyết định có được gửi OTP tới số này lúc này không. Trả 0 nếu
// được; ngược lại là thời gian phải chờ.
type OTPLimiter interface {
	Allow(ctx context.Context, phone string) (time.Duration, error)
}

func errOTPLimited(wait time.Duration) error {
	secs := int(wait.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	msg := fmt.Sprintf("Bạn vừa yêu cầu mã. Vui lòng thử lại sau %d giây.", secs)
	if wait > 2*time.Minute {
		msg = fmt.Sprintf("Số này đã yêu cầu mã quá nhiều lần. Vui lòng thử lại sau %d phút.",
			int((wait+time.Minute-1)/time.Minute))
	}
	return errs.E(errs.KindRateLimited, "otp_rate_limited", msg)
}

// ------------------------------------------------------------------ bộ nhớ

// MemoryOTPLimiter chỉ đúng với một bản sao.
type MemoryOTPLimiter struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*otpWindow
}

type otpWindow struct {
	last                time.Time
	hourStart, dayStart time.Time
	hour, day           int
}

func NewMemoryOTPLimiter(now func() time.Time) *MemoryOTPLimiter {
	return &MemoryOTPLimiter{now: now, m: map[string]*otpWindow{}}
}

func (l *MemoryOTPLimiter) Allow(_ context.Context, phone string) (time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, ok := l.m[phone]
	if !ok {
		w = &otpWindow{hourStart: now, dayStart: now}
		l.m[phone] = w
	}
	if !w.last.IsZero() && now.Sub(w.last) < OTPCooldown {
		return OTPCooldown - now.Sub(w.last), nil
	}
	if now.Sub(w.hourStart) >= time.Hour {
		w.hourStart, w.hour = now, 0
	}
	if now.Sub(w.dayStart) >= 24*time.Hour {
		w.dayStart, w.day = now, 0
	}
	if w.hour >= OTPMaxPerHour {
		return w.hourStart.Add(time.Hour).Sub(now), nil
	}
	if w.day >= OTPMaxPerDay {
		return w.dayStart.Add(24 * time.Hour).Sub(now), nil
	}
	w.last = now
	w.hour++
	w.day++
	return 0, nil
}

// Sweep dọn số đã quá một ngày không gửi — chống phình bộ nhớ.
func (l *MemoryOTPLimiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, w := range l.m {
		if now.Sub(w.dayStart) >= 24*time.Hour && now.Sub(w.last) >= OTPCooldown {
			delete(l.m, k)
		}
	}
}

// ------------------------------------------------------------------ Redis

// RedisOTPLimiter giới hạn trên toàn cụm. Kiểm và đếm trong MỘT script Lua:
// làm ở phía client thì hai pod cùng đọc "còn 1 lượt" và cùng gửi.
type RedisOTPLimiter struct {
	rdb    *redis.Client
	prefix string
	log    *slog.Logger
}

func NewRedisOTPLimiter(rdb *redis.Client, prefix string, log *slog.Logger) *RedisOTPLimiter {
	return &RedisOTPLimiter{rdb: rdb, prefix: prefix + "otp:", log: log}
}

// KEYS: cooldown, giờ, ngày. ARGV: cooldown_ms, giờ_ms, trần_giờ, ngày_ms, trần_ngày.
// Trả 0 nếu được gửi, ngược lại số mili giây phải chờ.
var otpScript = redis.NewScript(`
local cd = redis.call('PTTL', KEYS[1])
if cd > 0 then return cd end
local h = tonumber(redis.call('GET', KEYS[2]) or '0')
if h >= tonumber(ARGV[3]) then return redis.call('PTTL', KEYS[2]) end
local d = tonumber(redis.call('GET', KEYS[3]) or '0')
if d >= tonumber(ARGV[5]) then return redis.call('PTTL', KEYS[3]) end
if redis.call('INCR', KEYS[2]) == 1 then redis.call('PEXPIRE', KEYS[2], ARGV[2]) end
if redis.call('INCR', KEYS[3]) == 1 then redis.call('PEXPIRE', KEYS[3], ARGV[4]) end
redis.call('SET', KEYS[1], '1', 'PX', ARGV[1])
return 0
`)

// Allow: Redis lỗi thì CHO gửi và ghi cảnh báo — chặn mọi đăng nhập trong lúc
// Redis chập chờn là sự cố lớn hơn vài tin bị lạm dụng, và rate limit theo IP
// vẫn đứng phía trước.
func (r *RedisOTPLimiter) Allow(ctx context.Context, phone string) (time.Duration, error) {
	ms, err := otpScript.Run(ctx, r.rdb,
		[]string{r.prefix + "cd:" + phone, r.prefix + "h:" + phone, r.prefix + "d:" + phone},
		OTPCooldown.Milliseconds(), time.Hour.Milliseconds(), OTPMaxPerHour,
		(24 * time.Hour).Milliseconds(), OTPMaxPerDay).Int64()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		r.log.Warn("không kiểm được giới hạn OTP theo số điện thoại — cho gửi", "err", err)
		return 0, nil
	}
	return time.Duration(ms) * time.Millisecond, nil
}
