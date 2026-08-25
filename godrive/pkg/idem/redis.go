package idem

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore là khoá idempotency dùng chung cho cả cụm.
//
// Bản bộ nhớ chỉ chống trùng trong phạm vi MỘT tiến trình: hai pod nhận cùng
// một Idempotency-Key sẽ cùng cho qua và tạo hai chuyến.
//
// Giá trị lưu trong khoá chính là phản hồi đã hoàn tất; chuỗi rỗng nghĩa là
// "đã giữ chỗ nhưng chưa xong" — đúng ngữ nghĩa request_in_flight.
type RedisStore struct {
	rdb    *redis.Client
	prefix string
}

func NewRedisStore(rdb *redis.Client, prefix string) *RedisStore {
	return &RedisStore{rdb: rdb, prefix: prefix}
}

func (s *RedisStore) key(k string) string { return s.prefix + "idem:" + k }

// Reserve giữ chỗ bằng SET NX — nguyên tử trên toàn cụm.
func (s *RedisStore) Reserve(ctx context.Context, key string, ttl time.Duration) (*Record, bool, error) {
	k := s.key(key)
	ok, err := s.rdb.SetNX(ctx, k, "", ttl).Result()
	if err != nil {
		return nil, false, err
	}
	if ok {
		return &Record{Key: key, CreatedAt: time.Now().UTC()}, false, nil
	}
	// Đã có người giữ. Đọc phản hồi để biết họ đã xong chưa.
	val, err := s.rdb.Get(ctx, k).Result()
	if err == redis.Nil {
		// Khoá vừa hết hạn giữa SET NX và GET. Coi như chưa ai giữ và thử lại
		// một lượt; nếu thua tiếp thì trả về "đang xử lý".
		if ok, err := s.rdb.SetNX(ctx, k, "", ttl).Result(); err != nil {
			return nil, false, err
		} else if ok {
			return &Record{Key: key, CreatedAt: time.Now().UTC()}, false, nil
		}
		return &Record{Key: key}, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	rec := &Record{Key: key}
	if val != "" {
		rec.Response = []byte(val)
	}
	return rec, true, nil
}

// Complete ghi phản hồi vào khoá, GIỮ NGUYÊN hạn còn lại.
//
// KEEPTTL là điểm mấu chốt: đặt lại TTL sẽ kéo dài khoá mỗi lần retry, còn bỏ
// TTL thì khoá nằm lại vĩnh viễn.
func (s *RedisStore) Complete(ctx context.Context, key string, response []byte) error {
	return s.rdb.Set(ctx, s.key(key), string(response), redis.KeepTTL).Err()
}

// Release nhả khoá khi thao tác thất bại — nhưng KHÔNG nhả nếu đã Complete.
func (s *RedisStore) Release(ctx context.Context, key string) error {
	// Xoá có điều kiện, chạy nguyên tử: giữa GET và DEL ở phía client, một
	// request khác hoàn toàn có thể vừa Complete xong.
	return releaseScript.Run(ctx, s.rdb, []string{s.key(key)}).Err()
}

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == '' then
  return redis.call('DEL', KEYS[1])
end
return 0
`)
