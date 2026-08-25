package matching

import (
	"context"

	"github.com/example/godrive/pkg/geo"
)

// SimpleETA ước lượng ETA theo đường chim bay có hệ số uốn khúc.
// Production: gọi OSRM table service để lấy ma trận thời gian một lần cho cả lô
// ứng viên (1 request thay vì N), sau đó cache theo cặp ô lưới.
type SimpleETA struct {
	DetourFactor float64
	SpeedKph     float64
}

func NewSimpleETA() *SimpleETA {
	// 18 km/h phản ánh tốc độ trung bình xe máy giờ cao điểm ở TP.HCM.
	return &SimpleETA{DetourFactor: 1.35, SpeedKph: 18}
}

func (s *SimpleETA) ETASeconds(_ context.Context, from []geo.Point, to geo.Point) ([]float64, error) {
	out := make([]float64, len(from))
	for i, p := range from {
		d := geo.DistanceM(p, to) * s.DetourFactor
		out[i] = d / (s.SpeedKph * 1000 / 3600)
	}
	return out, nil
}
