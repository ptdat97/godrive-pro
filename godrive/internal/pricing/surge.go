package pricing

import (
	"context"
	"sync"
	"time"

	"github.com/example/godrive/pkg/geo"
)

// DemandSurge tính hệ số theo tỉ lệ cầu/cung trong từng ô lưới.
// Đếm số yêu cầu chuyến và số tài xế rảnh trong cửa sổ trượt.
type DemandSurge struct {
	mu        sync.Mutex
	demand    map[string][]time.Time
	window    time.Duration
	supply    SupplyCounter
	lastSweep time.Time
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
	k := geo.CellOf(at).Key()
	d.mu.Lock()
	cut := t.Add(-d.window)
	n := 0
	for _, ts := range d.demand[k] {
		if ts.After(cut) {
			n++
		}
	}
	d.mu.Unlock()

	supply := 1
	if d.supply != nil {
		if c, err := d.supply.IdleCount(ctx, at, 2000); err == nil {
			supply = c
		}
	}
	if supply < 1 {
		supply = 1
	}
	// So sánh bằng số nguyên: demand×10 với supply×ngưỡng×10, tránh cả phép
	// chia float lẫn việc 1.2 không biểu diễn chính xác được ở nhị phân.
	r10 := int64(n) * 10
	s10 := int64(supply)
	switch {
	case r10 >= 40*s10:
		return 2000, nil
	case r10 >= 30*s10:
		return 1700, nil
	case r10 >= 20*s10:
		return 1400, nil
	case r10 >= 12*s10:
		return 1200, nil
	default:
		return MinSurgePermille, nil
	}
}
