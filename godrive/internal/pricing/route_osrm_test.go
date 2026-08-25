package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/godrive/pkg/geo"
)

func TestOSRMEngineParsesRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "Ok",
			"routes": []map[string]any{
				{"distance": 6432.1, "duration": 981.4, "geometry": "abc"},
			},
		})
	}))
	defer srv.Close()

	r, err := NewOSRMEngine(srv.URL, nil).Route(context.Background(), pickup, dropoff)
	if err != nil {
		t.Fatal(err)
	}
	if r.DistanceM != 6432.1 || r.DurationS != 981.4 || r.Polyline != "abc" {
		t.Fatalf("chưa đọc đúng phản hồi OSRM: %+v", r)
	}
}

// OSRM chết không được làm chết luồng báo giá — báo giá nằm trên request path.
func TestOSRMEngineFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	r, err := NewOSRMEngine(srv.URL, NewHaversineEngine()).Route(context.Background(), pickup, dropoff)
	if err != nil {
		t.Fatalf("phải lùi về haversine: %v", err)
	}
	if r.DistanceM <= 0 || r.DurationS <= 0 {
		t.Fatalf("đường lùi phải cho kết quả hợp lệ: %+v", r)
	}

	if _, err := NewOSRMEngine(srv.URL, nil).Route(context.Background(), pickup, dropoff); err == nil {
		t.Fatal("không có Fallback thì phải trả lỗi")
	}
}

// Hai điểm không có đường đi giữa chúng: OSRM trả code khác "Ok".
func TestOSRMEngineNoRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "NoRoute", "routes": []any{}})
	}))
	defer srv.Close()

	if _, err := NewOSRMEngine(srv.URL, nil).Route(context.Background(),
		pickup, geo.Point{Lat: 9.0, Lng: 103.0}); err == nil {
		t.Fatal("không có đường đi thì phải trả lỗi")
	}
}

// Giá cước tính từ OSRM phải KHÁC giá tính từ ước lượng haversine — nếu giống
// nhau thì việc dựng OSRM chẳng để làm gì.
func TestOSRMRouteChangesFare(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Đường thật vòng hơn ước lượng đường chim bay × 1.35.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":   "Ok",
			"routes": []map[string]any{{"distance": 9000.0, "duration": 1500.0}},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	hav, _ := newSvc(Route{}, MinSurgePermille, vnTime(12, 0))
	_ = hav

	osrmSvc, _ := newSvcWithRoutes(NewOSRMEngine(srv.URL, nil), MinSurgePermille, vnTime(12, 0))
	q, err := osrmSvc.EstimateAll(ctx, pickup, dropoff)
	if err != nil {
		t.Fatal(err)
	}
	if q[0].DistanceM != 9000 {
		t.Fatalf("báo giá phải dùng quãng đường của OSRM, được %.0f", q[0].DistanceM)
	}
	if q[0].Total <= 0 {
		t.Fatalf("giá không hợp lệ: %d", q[0].Total)
	}
}
