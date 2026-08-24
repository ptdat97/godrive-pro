package matching

import (
	"testing"

	"github.com/example/godrive/pkg/geo"
)

var pickup = geo.Point{Lat: 10.7725, Lng: 106.6980}

func TestRankPrefersReliableDriverOverSlightlyCloser(t *testing.T) {
	cfg := DefaultConfig()
	cands := []Candidate{
		// Gần hơn 30 giây nhưng hay bỏ chuyến và đánh giá thấp.
		{DriverID: "gan_nhung_kem", Point: pickup, ETASec: 120, Rating: 3.5, Acceptance: 0.3},
		// Xa hơn một chút nhưng đáng tin cậy.
		{DriverID: "xa_nhung_tot", Point: pickup, ETASec: 150, Rating: 5.0, Acceptance: 0.95},
	}
	got := rank(cfg, cands, pickup)
	if got[0].DriverID != "xa_nhung_tot" {
		t.Fatalf("phải ưu tiên tài xế đáng tin cậy, được %s", got[0].DriverID)
	}
}

func TestRankPrefersCloserWhenEqualQuality(t *testing.T) {
	cfg := DefaultConfig()
	cands := []Candidate{
		{DriverID: "xa", Point: pickup, ETASec: 300, Rating: 5, Acceptance: 0.9},
		{DriverID: "gan", Point: pickup, ETASec: 60, Rating: 5, Acceptance: 0.9},
	}
	if got := rank(cfg, cands, pickup); got[0].DriverID != "gan" {
		t.Fatalf("cùng chất lượng thì phải chọn tài xế gần hơn, được %s", got[0].DriverID)
	}
}

func TestIdleDriverGetsPriority(t *testing.T) {
	cfg := DefaultConfig()
	cands := []Candidate{
		{DriverID: "vua_tra_khach", Point: pickup, ETASec: 100, Rating: 5, Acceptance: 0.9, IdleSeconds: 0},
		{DriverID: "cho_lau", Point: pickup, ETASec: 130, Rating: 5, Acceptance: 0.9, IdleSeconds: 600},
	}
	if got := rank(cfg, cands, pickup); got[0].DriverID != "cho_lau" {
		t.Fatalf("tài xế chờ lâu phải được ưu tiên, được %s", got[0].DriverID)
	}
}

func TestHeadingPenaltyForWrongDirection(t *testing.T) {
	cfg := DefaultConfig()
	away := geo.Point{Lat: pickup.Lat - 0.01, Lng: pickup.Lng} // cách ~1,1km
	base := Candidate{Point: away, DistanceM: 1100, ETASec: 100, Rating: 5, Acceptance: 0.9}

	toBearing := geo.BearingDeg(away, pickup)
	toward := base
	toward.BearingDeg = toBearing
	against := base
	against.BearingDeg = toBearing + 180 // quay lưng lại điểm đón

	if score(cfg, toward, pickup) >= score(cfg, against, pickup) {
		t.Fatal("xe đang hướng về điểm đón phải có điểm tốt hơn xe đi ngược")
	}
}
