package pricing

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/pkg/errs"
)

// RedisQuoteStore lưu báo giá với TTL bằng đúng QuoteTTL.
//
// Báo giá là dữ liệu sống 5 phút, ghi nhiều đọc một lần — đúng thứ không nên
// nằm trong Postgres. Ở bộ nhớ tiến trình thì báo giá phát ở pod A không đặt
// chuyến được ở pod B, và khách nhận `quote_expired` một cách khó hiểu.
type RedisQuoteStore struct {
	rdb    *redis.Client
	prefix string
}

func NewRedisQuoteStore(rdb *redis.Client, prefix string) *RedisQuoteStore {
	return &RedisQuoteStore{rdb: rdb, prefix: prefix}
}

func (s *RedisQuoteStore) key(id string) string { return s.prefix + "quote:" + id }

func (s *RedisQuoteStore) Save(ctx context.Context, q Quote) error {
	raw, err := json.Marshal(q)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "quote_encode_failed", "redis", err)
	}
	// TTL để Redis tự dọn. GetQuote vẫn kiểm ExpiresAt vì đồng hồ hai bên có
	// thể lệch, và vì hạn báo giá là quy tắc nghiệp vụ chứ không phải chi tiết
	// lưu trữ.
	if err := s.rdb.Set(ctx, s.key(q.ID), raw, QuoteTTL).Err(); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}

func (s *RedisQuoteStore) Get(ctx context.Context, quoteID string) (Quote, error) {
	raw, err := s.rdb.Get(ctx, s.key(quoteID)).Bytes()
	if err == redis.Nil {
		return Quote{}, errs.NotFound("quote_not_found", "Báo giá không tồn tại hoặc đã hết hạn.")
	}
	if err != nil {
		return Quote{}, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	var q Quote
	if err := json.Unmarshal(raw, &q); err != nil {
		return Quote{}, errs.Wrap(errs.KindInternal, "quote_decode_failed", "redis", err)
	}
	return q, nil
}
