// Package geo cung cấp kiểu toạ độ, khoảng cách và lưới ô (cell) để lập chỉ mục
// vị trí tài xế. Lưới ở đây là bản thay thế thuần stdlib cho H3 — khi lên
// production đổi CellOf/Ring sang github.com/uber/h3-go/v4 (resolution 8-9).
package geo

import (
	"fmt"
	"math"
)

const earthRadiusM = 6371000.0

type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (p Point) Valid() bool {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lng) {
		return false
	}
	return p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// InVietnam kiểm tra thô toạ độ có nằm trong hộp bao lãnh thổ VN không.
// Dùng để loại ping rác / GPS lỗi trước khi ghi vào chỉ mục.
func (p Point) InVietnam() bool {
	return p.Lat >= 8.0 && p.Lat <= 23.6 && p.Lng >= 102.0 && p.Lng <= 110.0
}

// DistanceM trả về khoảng cách đường chim bay (mét).
func DistanceM(a, b Point) float64 {
	la1 := a.Lat * math.Pi / 180
	la2 := b.Lat * math.Pi / 180
	dLa := la2 - la1
	dLo := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dLa/2)*math.Sin(dLa/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dLo/2)*math.Sin(dLo/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// BearingDeg trả về hướng từ a tới b, đơn vị độ (0 = Bắc).
func BearingDeg(a, b Point) float64 {
	la1 := a.Lat * math.Pi / 180
	la2 := b.Lat * math.Pi / 180
	dLo := (b.Lng - a.Lng) * math.Pi / 180
	y := math.Sin(dLo) * math.Cos(la2)
	x := math.Cos(la1)*math.Sin(la2) - math.Sin(la1)*math.Cos(la2)*math.Cos(dLo)
	d := math.Atan2(y, x) * 180 / math.Pi
	return math.Mod(d+360, 360)
}

// AngleDiffDeg trả về chênh lệch góc nhỏ nhất giữa hai hướng (0..180).
func AngleDiffDeg(a, b float64) float64 {
	return math.Abs(math.Mod(a-b+540, 360) - 180)
}

// Cell là một ô lưới đều theo kinh/vĩ độ.
type Cell struct {
	X int
	Y int
}

func (c Cell) Key() string { return fmt.Sprintf("%d:%d", c.X, c.Y) }

// CellSizeDeg ~ 0.005 độ ≈ 550m, hợp với bán kính tìm tài xế trong đô thị VN.
const CellSizeDeg = 0.005

func CellOf(p Point) Cell {
	return Cell{
		X: int(math.Floor(p.Lng / CellSizeDeg)),
		Y: int(math.Floor(p.Lat / CellSizeDeg)),
	}
}

// Ring trả về mọi ô trong bán kính k ô quanh tâm (gồm cả ô tâm).
func Ring(c Cell, k int) []Cell {
	if k < 0 {
		k = 0
	}
	out := make([]Cell, 0, (2*k+1)*(2*k+1))
	for dx := -k; dx <= k; dx++ {
		for dy := -k; dy <= k; dy++ {
			out = append(out, Cell{X: c.X + dx, Y: c.Y + dy})
		}
	}
	return out
}

// RingsForRadius ước lượng số vòng ô cần quét cho bán kính (mét).
func RingsForRadius(radiusM float64) int {
	const metersPerCell = CellSizeDeg * 111000
	return int(math.Ceil(radiusM/metersPerCell)) + 1
}
