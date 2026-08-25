package matching

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
)

// RedisStore lưu lời mời và khoá giành chuyến trên Redis.
//
// So với bản Postgres: khoá chuyến sống 30 giây và bị ghi rất nhiều lúc cao
// điểm — đúng loại tải Redis làm tốt hơn hẳn. Lời mời cũng vậy (sống 15 giây).
// Bản Postgres vẫn giữ lại vì `offers_one_accepted_per_trip` là chốt chặn cuối
// mà Redis không thay thế được; hai bản dùng cho hai mục đích khác nhau.
type RedisStore struct {
	rdb    *redis.Client
	prefix string
	clk    clock.Clock
	// OfferTTL là hạn lưu bản ghi lời mời trong Redis. Phải DÀI HƠN Offer.ExpiresAt
	// để tài xế bấm nhận sát giờ vẫn đọc được lời mời rồi nhận đúng lỗi
	// offer_expired, thay vì nhận offer_not_found gây hoang mang.
	OfferTTL time.Duration
}

func NewRedisStore(rdb *redis.Client, prefix string, clk clock.Clock) *RedisStore {
	return &RedisStore{rdb: rdb, prefix: prefix, clk: clk, OfferTTL: 10 * time.Minute}
}

func (s *RedisStore) offerKey(id string) string  { return s.prefix + "offer:" + id }
func (s *RedisStore) claimKey(id string) string  { return s.prefix + "claim:" + id }
func (s *RedisStore) driverKey(id string) string { return s.prefix + "drvoffers:" + id }
func (s *RedisStore) tripKey(id string) string   { return s.prefix + "tripoffers:" + id }

func (s *RedisStore) SaveOffers(ctx context.Context, offers []Offer) error {
	if len(offers) == 0 {
		return nil
	}
	pipe := s.rdb.TxPipeline()
	for _, o := range offers {
		raw, err := json.Marshal(o)
		if err != nil {
			return errs.Wrap(errs.KindInternal, "offer_encode_failed", "redis", err)
		}
		pipe.Set(ctx, s.offerKey(o.ID), raw, s.OfferTTL)
		// Hai chỉ mục ngược: "lời mời của tài xế này" và "lời mời của chuyến này".
		// Redis không có truy vấn theo trường nên phải tự dựng.
		pipe.SAdd(ctx, s.driverKey(o.DriverID), o.ID)
		pipe.Expire(ctx, s.driverKey(o.DriverID), s.OfferTTL)
		pipe.SAdd(ctx, s.tripKey(o.TripID), o.ID)
		pipe.Expire(ctx, s.tripKey(o.TripID), s.OfferTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}

func (s *RedisStore) GetOffer(ctx context.Context, offerID string) (Offer, error) {
	raw, err := s.rdb.Get(ctx, s.offerKey(offerID)).Bytes()
	if err == redis.Nil {
		return Offer{}, errs.NotFound("offer_not_found", "Không tìm thấy lời mời.")
	}
	if err != nil {
		return Offer{}, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	var o Offer
	if err := json.Unmarshal(raw, &o); err != nil {
		return Offer{}, errs.Wrap(errs.KindInternal, "offer_decode_failed", "redis", err)
	}
	return o, nil
}

// claimScript giành khoá chuyến một cách NGUYÊN TỬ.
//
// Đây là bất biến quan trọng nhất của cả hệ thống: nó quyết định trong nhiều
// tài xế cùng bấm "Nhận chuyến" thì ai thắng.
//
// Phải là Lua chứ không phải SETNX rồi GET: giữa hai lệnh đó khoá có thể hết
// hạn, và ta sẽ đọc ra kết quả của một thế giới khác. Script chạy trọn vẹn
// trong Redis nên không có khe hở nào.
//
// Chính chủ gọi lại vẫn thắng (và được gia hạn): app mobile trên mạng 4G chập
// chờn retry liên tục, mỗi lần retry mà thành "chuyến đã có người khác nhận"
// thì tài xế sẽ mất chuyến vì chính mạng của mình.
var claimScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner == false then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return 1
end
if owner == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
return 0
`)

func (s *RedisStore) ClaimTrip(ctx context.Context, tripID, driverID string, ttl time.Duration) (bool, error) {
	won, err := claimScript.Run(ctx, s.rdb,
		[]string{s.claimKey(tripID)}, driverID, ttl.Milliseconds()).Int()
	if err != nil {
		return false, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return won == 1, nil
}

func (s *RedisStore) UpdateStatus(ctx context.Context, offerID string, st OfferStatus) error {
	o, err := s.GetOffer(ctx, offerID)
	if err != nil {
		return err
	}
	o.Status = st
	raw, err := json.Marshal(o)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "offer_encode_failed", "redis", err)
	}
	// KEEPTTL: đổi trạng thái không được kéo dài tuổi thọ bản ghi.
	if err := s.rdb.Set(ctx, s.offerKey(offerID), raw, redis.KeepTTL).Err(); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}

func (s *RedisStore) PendingForDriver(ctx context.Context, driverID string) ([]Offer, error) {
	ids, err := s.rdb.SMembers(ctx, s.driverKey(driverID)).Result()
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	now := s.clk.Now()
	var out []Offer
	var stale []string
	for _, id := range ids {
		o, err := s.GetOffer(ctx, id)
		if err != nil {
			// Bản ghi đã hết hạn nhưng id còn trong tập chỉ mục — dọn dần.
			stale = append(stale, id)
			continue
		}
		if o.Status == OfferPending && now.Before(o.ExpiresAt) {
			out = append(out, o)
		}
	}
	if len(stale) > 0 {
		_ = s.rdb.SRem(ctx, s.driverKey(driverID), toAny(stale)...).Err()
	}
	return out, nil
}

func (s *RedisStore) ExpireOffers(ctx context.Context, tripID, except string) error {
	ids, err := s.rdb.SMembers(ctx, s.tripKey(tripID)).Result()
	if err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	for _, id := range ids {
		if id == except {
			continue
		}
		o, err := s.GetOffer(ctx, id)
		if err != nil || o.Status != OfferPending {
			continue
		}
		if err := s.UpdateStatus(ctx, id, OfferLost); err != nil {
			return err
		}
	}
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
