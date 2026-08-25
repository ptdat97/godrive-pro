package authn

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/pkg/errs"
)

// RedisRevoker lưu danh sách thu hồi trên Redis.
//
// Hai đường thu hồi, phục vụ hai tình huống khác nhau:
//
//	theo TOKEN     đăng xuất một thiết bị — chỉ token đó hết hiệu lực
//	theo TÀI KHOẢN khoá tài xế, đổi mật khẩu, nghi ngờ lộ token —
//	               MỌI token phát trước thời điểm đó đều hết hiệu lực
//
// Đường thứ hai quan trọng hơn: khi cần chặn một người ngay lập tức, ta không
// biết họ đang giữ bao nhiêu token trên bao nhiêu thiết bị.
type RedisRevoker struct {
	rdb    *redis.Client
	prefix string
	// Skew bù lệch đồng hồ giữa các máy khi so mốc thu hồi theo tài khoản.
	Skew time.Duration
}

func NewRedisRevoker(rdb *redis.Client, prefix string) *RedisRevoker {
	return &RedisRevoker{rdb: rdb, prefix: prefix, Skew: 2 * time.Second}
}

func (r *RedisRevoker) jtiKey(jti string) string { return r.prefix + "revoked:jti:" + jti }
func (r *RedisRevoker) subKey(sub string) string { return r.prefix + "revoked:sub:" + sub }

func (r *RedisRevoker) IsRevoked(ctx context.Context, c *Claims) (bool, error) {
	// Token cũ chưa có JTI: coi như chưa thu hồi được theo token, nhưng vẫn
	// kiểm tra được theo tài khoản.
	pipe := r.rdb.Pipeline()
	var jtiCmd *redis.IntCmd
	if c.JTI != "" {
		jtiCmd = pipe.Exists(ctx, r.jtiKey(c.JTI))
	}
	subCmd := pipe.Get(ctx, r.subKey(c.Sub))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}

	if jtiCmd != nil {
		if n, err := jtiCmd.Result(); err == nil && n > 0 {
			return true, nil
		}
	}
	cutoff, err := subCmd.Int64()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	// Token phát TRƯỚC mốc thu hồi thì hết hiệu lực.
	return c.IssuedAt().Add(r.Skew).Unix() < cutoff, nil
}

// RevokeToken thu hồi đúng một token (đăng xuất một thiết bị).
//
// TTL đặt bằng phần đời còn lại của token: sau khi token hết hạn thì bản ghi
// thu hồi không còn ý nghĩa, giữ lại chỉ tốn bộ nhớ.
func (r *RedisRevoker) RevokeToken(ctx context.Context, c *Claims, now time.Time) error {
	if c.JTI == "" {
		return errs.Invalid("token_no_jti", "Token này không thu hồi riêng được.")
	}
	ttl := c.ExpiresAt().Sub(now)
	if ttl <= 0 {
		return nil // đã hết hạn, không cần thu hồi
	}
	if err := r.rdb.Set(ctx, r.jtiKey(c.JTI), "1", ttl).Err(); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}

// RevokeAccount thu hồi MỌI token của một tài khoản phát trước thời điểm now.
//
// maxTTL phải >= hạn token dài nhất đang lưu hành, nếu không mốc thu hồi hết
// hạn trước token và những token đó sống lại.
func (r *RedisRevoker) RevokeAccount(ctx context.Context, accountID string, now time.Time, maxTTL time.Duration) error {
	if err := r.rdb.Set(ctx, r.subKey(accountID), now.Unix(), maxTTL).Err(); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}
