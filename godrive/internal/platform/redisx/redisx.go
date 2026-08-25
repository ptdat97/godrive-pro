// Package redisx là lớp mỏng quanh client Redis dùng chung cho cả tiến trình.
//
// Redis giữ các dữ liệu NÓNG và NGẮN HẠN mà Postgres không hợp: chỉ mục vị trí
// tài xế (ghi mỗi 4 giây mỗi tài xế), khoá giành chuyến (sống 30 giây), báo giá
// (5 phút), khoá idempotency, và bộ đếm rate limit toàn cụm.
//
// Đây cũng là thứ gỡ bỏ ràng buộc "chỉ chạy được một bản sao": trước khi có nó,
// năm loại dữ liệu trên nằm trong bộ nhớ tiến trình nên hai pod sẽ thấy hai thế
// giới khác nhau.
package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/pkg/errs"
)

// KeyPrefix đứng trước mọi khoá để nhiều môi trường dùng chung một instance
// Redis mà không giẫm lên nhau.
const KeyPrefix = "godrive:"

type Client struct {
	rdb *redis.Client
}

// New mở kết nối theo URL dạng redis://host:port/db.
func New(url string) (*Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "redis_url_invalid", "REDIS_URL không hợp lệ", err)
	}
	// Thao tác Redis ở đây đều nằm trên đường đi của request; thà lỗi nhanh còn
	// hơn để một Redis chậm kéo theo toàn bộ API.
	opt.DialTimeout = 3 * time.Second
	opt.ReadTimeout = 2 * time.Second
	opt.WriteTimeout = 2 * time.Second
	opt.PoolSize = 50
	return &Client{rdb: redis.NewClient(opt)}, nil
}

// Raw trả client gốc cho các store cần lệnh chuyên biệt (GEO, Lua...).
func (c *Client) Raw() *redis.Client { return c.rdb }

// Key ghép khoá đã có tiền tố.
func Key(parts ...string) string {
	s := KeyPrefix
	for i, p := range parts {
		if i > 0 {
			s += ":"
		}
		s += p
	}
	return s
}

func (c *Client) Ping(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_unavailable", "Redis không phản hồi", err)
	}
	return nil
}

func (c *Client) Close() error { return c.rdb.Close() }

// Wrap đổi lỗi Redis sang lỗi nghiệp vụ. redis.Nil ("không có khoá") KHÔNG phải
// lỗi — nơi gọi tự quyết định nó nghĩa là gì.
func Wrap(err error) error {
	if err == nil || err == redis.Nil {
		return nil
	}
	return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
}

// IsNil cho biết khoá không tồn tại.
func IsNil(err error) bool { return err == redis.Nil }
