package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"

	"github.com/example/godrive/pkg/geo"
)

// seedDrivers tạo tài xế đã duyệt, đã nạp ví, đang ONLINE.
//
// Đi qua API như tài xế thật thay vì ghi thẳng CSDL: thời gian tạo tài khoản
// cũng là một số đo, và ghi thẳng CSDL thì bỏ qua đúng những đoạn mã cần thử.
func seedDrivers(ctx context.Context, cl *client, adminTok string, o options, runID int) ([]*simDriver, error) {
	out := make([]*simDriver, o.drivers)
	err := parallel(ctx, o.drivers, o.seedParallel, func(ctx context.Context, i int) error {
		phone := fmt.Sprintf("09%02d%06d", runID, i)
		s, err := cl.login(ctx, phone, "driver")
		if err != nil {
			return fmt.Errorf("đăng nhập: %w", err)
		}
		// Số giấy tờ và biển số suy từ số điện thoại để duy nhất trong một lần
		// chạy (CSDL có ràng buộc duy nhất trên cả hai).
		digits := phone[2:]
		var d struct {
			ID string `json:"id"`
		}
		if _, err := cl.call(ctx, "POST", "/v1/drivers/register", s.Token, map[string]any{
			"full_name": "Tài xế tải " + digits, "phone": "+84" + phone[1:], "city": "HCM",
			"vehicle": map[string]any{
				"type":  "BIKE",
				"plate": fmt.Sprintf("%sA%s-%s.%s", digits[:2], digits[2:3], digits[3:6], digits[6:8]),
			},
			"documents": map[string]any{
				"national_id": "0790" + digits, "driver_license": "7901" + digits,
			},
		}, &d); err != nil {
			return fmt.Errorf("đăng ký: %w", err)
		}
		if _, err := cl.call(ctx, "POST", "/v1/admin/drivers/"+d.ID+"/kyc", adminTok,
			map[string]bool{"approved": true}, nil); err != nil {
			return fmt.Errorf("duyệt hồ sơ: %w", err)
		}
		// Nạp ví đủ để không bị chặn vì nợ chiết khấu tiền mặt giữa chừng —
		// cổng chặn nợ không phải thứ đang được đo.
		if _, err := cl.call(ctx, "POST", "/v1/drivers/me/topup", s.Token,
			map[string]int64{"amount": 5_000_000}, nil, "Idempotency-Key", "loadgen-"+d.ID); err != nil {
			return fmt.Errorf("nạp ví (cần DEV_AUTH và chưa bật cổng thanh toán): %w", err)
		}
		if _, err := cl.call(ctx, "POST", "/v1/drivers/me/online", s.Token, nil, nil); err != nil {
			return fmt.Errorf("bật nhận chuyến: %w", err)
		}
		rng := rand.New(rand.NewSource(int64(runID*1_000_000 + i)))
		out[i] = &simDriver{ID: d.ID, sess: s, pos: randomPoint(rng, o.center, o.radiusM), rng: rng}
		return nil
	})
	return out, err
}

func seedRiders(ctx context.Context, cl *client, o options, runID int) ([]*simRider, error) {
	out := make([]*simRider, o.riders)
	err := parallel(ctx, o.riders, o.seedParallel, func(ctx context.Context, i int) error {
		s, err := cl.login(ctx, fmt.Sprintf("08%02d%06d", runID, i), "rider")
		if err != nil {
			return err
		}
		out[i] = &simRider{sess: s, rng: rand.New(rand.NewSource(int64(runID*1_000_000 + 500_000 + i)))}
		return nil
	})
	return out, err
}

// randomPoint lấy một điểm ngẫu nhiên phân bố đều trong hình tròn bán kính r.
func randomPoint(rng *rand.Rand, c geo.Point, r float64) geo.Point {
	d := r * math.Sqrt(rng.Float64())
	return offset(c, d, rng.Float64()*360)
}

// offset dịch điểm c đi d mét theo hướng bearing (độ). Đủ chính xác ở vài km.
func offset(c geo.Point, d, bearing float64) geo.Point {
	rad := bearing * math.Pi / 180
	dLat := d * math.Cos(rad) / 111_320
	dLng := d * math.Sin(rad) / (111_320 * math.Cos(c.Lat*math.Pi/180))
	return geo.Point{Lat: c.Lat + dLat, Lng: c.Lng + dLng}
}
