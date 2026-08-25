package pricing

import (
	"context"
	"sync"
	"time"

	"github.com/example/godrive/pkg/geo"
)

// DemandSurge tính hệ số theo tỉ lệ cầu/cung trong từng ô lưới.
// Đếm số yêu cầu chuyến và số tài xế rảnh trong cửa sổ trượt.
// SurgeStep là một bậc của thang tăng giá. RatioX10 là ngưỡng cầu/cung nhân 10.
type SurgeStep struct {
	RatioX10 int64
	Permille int64
}

// SurgeRuntime là cấu hình tăng giá chỉnh được lúc chạy.
type SurgeRuntime struct {
	Enabled       bool
	MaxPermille   int64
	Window        time.Duration
	SupplyRadiusM float64
	Steps         []SurgeStep
}

// SurgeConfigProvider trả cấu hình tăng giá hiện hành.
type SurgeConfigProvider func(ctx context.Context) SurgeRuntime

type DemandSurge struct {
	mu        sync.Mutex
	demand    map[string][]time.Time
	window    time.Duration
	supply    SupplyCounter
	lastSweep time.Time
	cfg       SurgeConfigProvider
}

// UseConfig nối nguồn cấu hình động.
func (d *DemandSurge) UseConfig(p SurgeConfigProvider) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = p
}

func (d *DemandSurge) runtime(ctx context.Context) SurgeRuntime {
	d.mu.Lock()
	fn := d.cfg
	w := d.window
	d.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return SurgeRuntime{
		Enabled: true, MaxPermille: MaxSurgePermille, Window: w, SupplyRadiusM: 2000,
		Steps: []SurgeStep{{12, 1200}, {20, 1400}, {30, 1700}, {40, 2000}},
	}
}

// SupplyCounter đếm tài xế rảnh quanh một điểm.
type SupplyCounter interface {
	IdleCount(ctx context.Context, at geo.Point, radiusM float64) (int, error)
}

func NewDemandSurge(supply SupplyCounter) *DemandSurge {
	return &DemandSurge{demand: map[string][]time.Time{}, window: 5 * time.Minute, supply: supply}
}

// RecordRequest gọi mỗi khi có yêu cầu đặt xe THẬT.
//
// Cố ý gắn vào sự kiện trip.requested chứ không phải lúc báo giá: một người bấm
// xem giá năm lần rồi thôi không phải là năm nhu cầu, và đo theo lượt xem giá sẽ
// đẩy surge lên bằng chính hành vi do surge gây ra.
func (d *DemandSurge) RecordRequest(at geo.Point, t time.Time) {
	k := geo.CellOf(at).Key()
	d.mu.Lock()
	defer d.mu.Unlock()
	cut := t.Add(-d.window)
	kept := d.demand[k][:0]
	for _, ts := range d.demand[k] {
		if ts.After(cut) {
			kept = append(kept, ts)
		}
	}
	d.demand[k] = append(kept, t)
	d.sweep(t)
}

// sweep xoá ô lưới đã nguội hẳn. Gọi khi ĐANG giữ d.mu.
//
// RecordRequest chỉ dọn đúng ô nó chạm tới, nên ô từng có khách rồi lặng hẳn
// (khu vực chỉ đông vào giờ tan tầm) sẽ giữ slice của nó vĩnh viễn.
func (d *DemandSurge) sweep(now time.Time) {
	if now.Sub(d.lastSweep) < time.Minute {
		return
	}
	d.lastSweep = now
	cut := now.Add(-d.window)
	for k, ts := range d.demand {
		if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
			delete(d.demand, k)
		}
	}
}

// Cells là số ô lưới đang theo dõi. Dùng cho test và metric.
func (d *DemandSurge) Cells() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.demand)
}

// SurgePermille trả hệ số tăng giá theo phần nghìn.
//
// Hàm BẬC THANG chứ không liên tục: dễ giải thích cho vận hành và không nhạy
// với nhiễu. Trần 2000 (×2.0) được clamp ở đây và một lần nữa ở Service.Estimate.
func (d *DemandSurge) SurgePermille(ctx context.Context, at geo.Point, t time.Time) (int64, error) {
	rt := d.runtime(ctx)
	if !rt.Enabled {
		return MinSurgePermille, nil
	}
	k := geo.CellOf(at).Key()
	d.mu.Lock()
	cut := t.Add(-rt.Window)
	n := 0
	for _, ts := range d.demand[k] {
		if ts.After(cut) {
			n++
		}
	}
	d.mu.Unlock()

	supply := 1
	if d.supply != nil {
		if c, err := d.supply.IdleCount(ctx, at, rt.SupplyRadiusM); err == nil {
			supply = c
		}
	}
	if supply < 1 {
		supply = 1
	}
	// So sánh bằng số nguyên: demand×10 với supply×ngưỡng, tránh cả phép chia
	// float lẫn việc 1.2 không biểu diễn chính xác được ở nhị phân.
	r10 := int64(n) * 10
	s10 := int64(supply)
	out := MinSurgePermille
	// Duyệt xuôi và giữ bậc CAO NHẤT thoả mãn: bậc thang đã được kiểm là tăng
	// dần lúc lưu, nhưng không dựa vào đó để tính — nếu ai đó sửa tay trong CSDL
	// thì kết quả vẫn phải hợp lý.
	for _, st := range rt.Steps {
		if r10 >= st.RatioX10*s10 && st.Permille > out {
			out = st.Permille
		}
	}
	if out > rt.MaxPermille {
		out = rt.MaxPermille
	}
	return out, nil
}
