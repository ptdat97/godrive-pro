package places

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// MemoryCache là cache trong tiến trình, có trần số mục. Đủ cho một bản sao;
// nhiều bản sao thì dùng RedisCache để mọi pod chung một lần trả tiền.
type MemoryCache struct {
	mu   sync.Mutex
	max  int
	data map[string]memItem
	now  func() time.Time
}

type memItem struct {
	val []byte
	exp time.Time
}

func NewMemoryCache(max int) *MemoryCache {
	return &MemoryCache{max: max, data: map[string]memItem{}, now: time.Now}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.data[key]
	if !ok || m.now().After(it.exp) {
		delete(m.data, key)
		return nil, false, nil
	}
	return it.val, true, nil
}

// Set ghi mục mới. Đầy thì dọn mục hết hạn trước; vẫn đầy thì bỏ một mục bất
// kỳ — không cần LRU chính xác cho một cache tiết kiệm tiền, chỉ cần không
// phình bộ nhớ mãi.
func (m *MemoryCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.data) >= m.max {
		now := m.now()
		for k, it := range m.data {
			if now.After(it.exp) {
				delete(m.data, k)
			}
		}
		for k := range m.data {
			if len(m.data) < m.max {
				break
			}
			delete(m.data, k)
		}
	}
	m.data[key] = memItem{val: val, exp: m.now().Add(ttl)}
	return nil
}

// RedisCache chia cache giữa các bản sao.
type RedisCache struct {
	rdb    *redis.Client
	prefix string
}

func NewRedisCache(rdb *redis.Client, prefix string) *RedisCache {
	return &RedisCache{rdb: rdb, prefix: prefix + "places:"}
}

func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := r.rdb.Get(ctx, r.prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (r *RedisCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return r.rdb.Set(ctx, r.prefix+key, val, ttl).Err()
}
