package matching

import (
	"sort"

	"github.com/example/godrive/pkg/geo"
)

// score tính điểm phạt cho một ứng viên; điểm thấp hơn = ưu tiên hơn.
//
// Không chỉ dựa vào khoảng cách: tài xế gần nhưng hay bỏ chuyến sẽ làm khách
// chờ lâu hơn tài xế xa mà luôn nhận. Tài xế chờ lâu được ưu tiên để thu nhập
// phân bổ đều — yếu tố giữ chân tài xế quan trọng ở thị trường VN.
func score(cfg Config, c Candidate, pickup geo.Point) float64 {
	s := cfg.WeightETA * c.ETASec
	s += cfg.WeightRating * (5.0 - c.Rating)
	s += cfg.WeightAcceptance * (1.0 - c.Acceptance)
	s -= cfg.WeightIdle * c.IdleSeconds

	// Xe đang chạy ngược hướng điểm đón phải quay đầu -> phạt theo góc lệch.
	if c.DistanceM > 100 {
		toPickup := geo.BearingDeg(c.Point, pickup)
		s += cfg.WeightHeading * geo.AngleDiffDeg(c.BearingDeg, toPickup)
	}
	return s
}

func rank(cfg Config, cands []Candidate, pickup geo.Point) []Candidate {
	for i := range cands {
		cands[i].Score = score(cfg, cands[i], pickup)
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score < cands[j].Score
		}
		return cands[i].DriverID < cands[j].DriverID // ổn định để test tất định
	})
	return cands
}
