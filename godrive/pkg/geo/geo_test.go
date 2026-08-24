package geo

import (
	"math"
	"testing"
)

var (
	benThanh   = Point{Lat: 10.7725, Lng: 106.6980}
	tanSonNhat = Point{Lat: 10.8188, Lng: 106.6520}
)

func TestDistance(t *testing.T) {
	d := DistanceM(benThanh, tanSonNhat)
	if d < 6000 || d > 8000 {
		t.Fatalf("Bến Thành -> Tân Sơn Nhất = %.0fm, ngoài khoảng kỳ vọng", d)
	}
}

func TestCellRingCoversRadius(t *testing.T) {
	c := CellOf(benThanh)
	k := RingsForRadius(1500)
	cells := Ring(c, k)
	if len(cells) != (2*k+1)*(2*k+1) {
		t.Fatalf("kích thước Ring sai: %d", len(cells))
	}
	near := Point{Lat: benThanh.Lat + 0.01, Lng: benThanh.Lng}
	target := CellOf(near)
	found := false
	for _, cc := range cells {
		if cc == target {
			found = true
		}
	}
	if !found {
		t.Fatal("ô của điểm cách ~1.1km không nằm trong ring")
	}
}

func TestBearing(t *testing.T) {
	if b := BearingDeg(Point{0, 0}, Point{1, 0}); math.Abs(b) > 1 {
		t.Fatalf("hướng bắc phải ~0, được %.2f", b)
	}
	if AngleDiffDeg(350, 10) != 20 {
		t.Fatal("AngleDiffDeg sai")
	}
}
