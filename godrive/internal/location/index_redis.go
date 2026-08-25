package location

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// RedisIndex là chỉ mục vị trí tài xế trên Redis GEO.
//
// Đây là dữ liệu nóng nhất của cả hệ thống: mỗi tài xế online ghi một ping vài
// giây một lần. Postgres không hợp (ghi quá nhiều, dữ liệu sống quá ngắn), và
// bộ nhớ tiến trình thì khiến mỗi pod thấy một tập tài xế khác nhau.
//
// Redis GEO chỉ lưu được toạ độ, trong khi Filter còn cần loại xe, trạng thái,
// pin và độ tươi. Nên mỗi tài xế có hai khoá:
//
//	<prefix>geo          ZSET  — chỉ mục không gian, GEOSEARCH truy vấn ở đây
//	<prefix>drv:{id}     HASH  — thuộc tính để lọc, có TTL làm "độ tươi"
//
// TTL của HASH chính là cơ chế hết hạn: tài xế mất mạng sẽ tự rơi khỏi tập ứng
// viên mà không cần job dọn nào. ZSET không có TTL theo phần tử nên phần tử
// thừa được dọn ngay trong lúc truy vấn.
type RedisIndex struct {
	rdb    *redis.Client
	prefix string
	clk    clock.Clock
	// TTL của bản ghi thuộc tính. Dài hơn StaleAfter một chút để việc lọc theo
	// độ tươi do Filter quyết định, chứ không phải do Redis xoá mất bản ghi.
	TTL time.Duration
}

func NewRedisIndex(rdb *redis.Client, prefix string, clk clock.Clock) *RedisIndex {
	return &RedisIndex{rdb: rdb, prefix: prefix, clk: clk, TTL: 4 * StaleAfter}
}

func (r *RedisIndex) geoKey() string          { return r.prefix + "geo" }
func (r *RedisIndex) drvKey(id string) string { return r.prefix + "drv:" + id }

func (r *RedisIndex) Upsert(ctx context.Context, s Snapshot) error {
	pipe := r.rdb.TxPipeline()
	pipe.GeoAdd(ctx, r.geoKey(), &redis.GeoLocation{
		Name: s.DriverID, Longitude: s.Point.Lng, Latitude: s.Point.Lat,
	})
	pipe.HSet(ctx, r.drvKey(s.DriverID), map[string]any{
		"lat":     s.Point.Lat,
		"lng":     s.Point.Lng,
		"bearing": s.BearingDeg,
		"vtype":   string(s.VehicleType),
		"status":  string(s.Status),
		"battery": s.BatteryPc,
		"at":      s.UpdatedAt.UnixMilli(),
	})
	pipe.Expire(ctx, r.drvKey(s.DriverID), r.TTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}

func (r *RedisIndex) Remove(ctx context.Context, driverID string) error {
	pipe := r.rdb.TxPipeline()
	pipe.ZRem(ctx, r.geoKey(), driverID)
	pipe.Del(ctx, r.drvKey(driverID))
	if _, err := pipe.Exec(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	return nil
}

func (r *RedisIndex) Get(ctx context.Context, driverID string) (Snapshot, bool, error) {
	vals, err := r.rdb.HGetAll(ctx, r.drvKey(driverID)).Result()
	if err != nil {
		return Snapshot{}, false, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	if len(vals) == 0 {
		return Snapshot{}, false, nil
	}
	return snapshotFrom(driverID, vals), true, nil
}

func (r *RedisIndex) Nearby(ctx context.Context, center geo.Point, radiusM float64, f Filter) ([]Snapshot, error) {
	// GEOSEARCH trả về theo thứ tự gần dần — đúng thứ tự Engine mong đợi.
	ids, err := r.rdb.GeoSearch(ctx, r.geoKey(), &redis.GeoSearchQuery{
		Longitude: center.Lng, Latitude: center.Lat,
		Radius: radiusM, RadiusUnit: "m",
		Sort: "ASC",
	}).Result()
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	// Một vòng pipeline cho cả lô thay vì N lần khứ hồi.
	pipe := r.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, r.drvKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, errs.Wrap(errs.KindInternal, "redis_error", "redis", err)
	}

	now := r.clk.Now()
	out := make([]Snapshot, 0, len(ids))
	var orphans []any
	for i, id := range ids {
		vals, err := cmds[i].Result()
		if err != nil || len(vals) == 0 {
			// Bản ghi thuộc tính đã hết hạn nhưng phần tử vẫn còn trong ZSET:
			// ZSET không có TTL theo phần tử, nên dọn ngay tại đây.
			orphans = append(orphans, id)
			continue
		}
		s := snapshotFrom(id, vals)
		if f.match(s, now) {
			out = append(out, s)
		}
	}
	if len(orphans) > 0 {
		_ = r.rdb.ZRem(ctx, r.geoKey(), orphans...).Err()
	}
	return out, nil
}

func snapshotFrom(driverID string, v map[string]string) Snapshot {
	atMs, _ := strconv.ParseInt(v["at"], 10, 64)
	lat, _ := strconv.ParseFloat(v["lat"], 64)
	lng, _ := strconv.ParseFloat(v["lng"], 64)
	bearing, _ := strconv.ParseFloat(v["bearing"], 64)
	battery, _ := strconv.Atoi(v["battery"])
	return Snapshot{
		DriverID:    driverID,
		Point:       geo.Point{Lat: lat, Lng: lng},
		BearingDeg:  bearing,
		VehicleType: driver.VehicleType(v["vtype"]),
		Status:      driver.Status(v["status"]),
		BatteryPc:   battery,
		UpdatedAt:   time.UnixMilli(atMs).UTC(),
	}
}
