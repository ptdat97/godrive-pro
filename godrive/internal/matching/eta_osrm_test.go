package matching

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/godrive/pkg/geo"
)

var pickupPt = geo.Point{Lat: 10.7725, Lng: 106.6980}

// fakeOSRM là máy chủ OSRM giả: đếm số request và trả ma trận thời gian.
func fakeOSRM(t *testing.T, calls *atomic.Int64, durations func(n int) [][]float64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Đếm số nguồn từ RAW query, không dùng URL.Query(): từ Go 1.17, dấu ";"
		// không còn được coi là dấu phân tách tham số nên Query() bỏ luôn tham
		// số chứa nó. OSRM thật (viết bằng C++) vẫn hiểu bình thường.
		n := 1
		for _, kv := range strings.Split(r.URL.RawQuery, "&") {
			if v, ok := strings.CutPrefix(kv, "sources="); ok && v != "" {
				n = strings.Count(v, ";") + 1
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "Ok", "durations": durations(n),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rows(n int, v float64) [][]float64 {
	out := make([][]float64, n)
	for i := range out {
		out[i] = []float64{v + float64(i)}
	}
	return out
}

// Điểm mấu chốt: MỘT request cho cả lô, không phải N request.
//
// Đây là quyết định về chi phí. Dịch vụ bản đồ tính tiền theo request, và chi
// phí Maps là rủi ro số 4 của dự án — hoá đơn API có thể vượt cả tiền máy chủ.
func TestOSRMETAUsesOneRequestForWholeBatch(t *testing.T) {
	var calls atomic.Int64
	srv := fakeOSRM(t, &calls, func(n int) [][]float64 { return rows(n, 100) })
	eta := NewOSRMETA(srv.URL, nil)

	// 20 tài xế ở 20 ô lưới KHÁC nhau để cache không che mất phép đo.
	from := make([]geo.Point, 20)
	for i := range from {
		from[i] = geo.Point{Lat: pickupPt.Lat + float64(i)*0.02, Lng: pickupPt.Lng}
	}
	got, err := eta.ETASeconds(context.Background(), from, pickupPt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("phải trả 20 kết quả, được %d", len(got))
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("20 ứng viên phải chỉ tốn 1 request, tốn %d", n)
	}
	// Thứ tự phải khớp thứ tự đầu vào.
	for i := range got {
		if got[i] != 100+float64(i) {
			t.Fatalf("vị trí %d: ETA=%v, phải khớp thứ tự đầu vào", i, got[i])
		}
	}
}

// Cache theo CẶP Ô LƯỚI: hai tài xế cách nhau vài chục mét thì thời gian tới
// điểm đón gần như nhau, không cần hỏi lại.
func TestOSRMETACachesByCellPair(t *testing.T) {
	var calls atomic.Int64
	srv := fakeOSRM(t, &calls, func(n int) [][]float64 { return rows(n, 100) })
	eta := NewOSRMETA(srv.URL, nil)
	ctx := context.Background()

	from := []geo.Point{{Lat: pickupPt.Lat + 0.02, Lng: pickupPt.Lng}}
	if _, err := eta.ETASeconds(ctx, from, pickupPt); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lần đầu phải gọi OSRM, gọi %d", calls.Load())
	}

	// Cùng ô lưới (lệch ~10m) -> lấy từ cache, KHÔNG gọi lại.
	near := []geo.Point{{Lat: from[0].Lat + 0.0001, Lng: from[0].Lng}}
	if geo.CellOf(near[0]) != geo.CellOf(from[0]) {
		t.Skip("hai điểm rơi vào hai ô khác nhau, chọn lại khoảng cách")
	}
	got, err := eta.ETASeconds(ctx, near, pickupPt)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("cùng cặp ô lưới phải dùng cache, đã gọi OSRM %d lần", calls.Load())
	}
	if got[0] != 100 {
		t.Fatalf("giá trị cache sai: %v", got[0])
	}

	// Hết hạn cache -> hỏi lại, vì tình hình giao thông đổi.
	eta.CacheTTL = time.Nanosecond
	time.Sleep(time.Millisecond)
	if _, err := eta.ETASeconds(ctx, near, pickupPt); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cache hết hạn phải hỏi lại, số lần gọi %d", calls.Load())
	}
}

// Lô hỗn hợp: phần đã có cache lấy từ cache, phần thiếu mới hỏi — và kết quả
// phải ghép lại ĐÚNG THỨ TỰ.
func TestOSRMETAMixedCacheKeepsOrder(t *testing.T) {
	var calls atomic.Int64
	srv := fakeOSRM(t, &calls, func(n int) [][]float64 { return rows(n, 500) })
	eta := NewOSRMETA(srv.URL, nil)
	ctx := context.Background()

	a := geo.Point{Lat: pickupPt.Lat + 0.02, Lng: pickupPt.Lng}
	b := geo.Point{Lat: pickupPt.Lat + 0.04, Lng: pickupPt.Lng}
	// Nạp cache cho a.
	first, _ := eta.ETASeconds(ctx, []geo.Point{a}, pickupPt)

	got, err := eta.ETASeconds(ctx, []geo.Point{a, b}, pickupPt)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != first[0] {
		t.Fatalf("phần tử đã có cache phải giữ nguyên giá trị: %v vs %v", got[0], first[0])
	}
	if got[1] < 0 {
		t.Fatalf("phần tử thiếu phải được tính: %v", got[1])
	}
	if calls.Load() != 2 {
		t.Fatalf("phải gọi đúng 2 lần (một cho a, một cho b), gọi %d", calls.Load())
	}
}

// OSRM chết KHÔNG được làm chết dispatch.
func TestOSRMETAFallsBackWhenServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	eta := NewOSRMETA(srv.URL, NewSimpleETA())
	from := []geo.Point{{Lat: pickupPt.Lat + 0.01, Lng: pickupPt.Lng}}
	got, err := eta.ETASeconds(context.Background(), from, pickupPt)
	if err != nil {
		t.Fatalf("phải lùi về ước lượng haversine, không được trả lỗi: %v", err)
	}
	if got[0] <= 0 {
		t.Fatalf("đường lùi phải cho ETA hợp lệ, được %v", got[0])
	}

	// Không có đường lùi thì mới được trả lỗi.
	if _, err := NewOSRMETA(srv.URL, nil).ETASeconds(context.Background(), from, pickupPt); err == nil {
		t.Fatal("không có Fallback thì phải trả lỗi")
	}
}

// OSRM trả null cho cặp không có đường đi -> ứng viên đó bị loại, không phải
// cả lô bị loại.
func TestOSRMETAHandlesUnreachable(t *testing.T) {
	var calls atomic.Int64
	srv := fakeOSRM(t, &calls, func(n int) [][]float64 {
		out := make([][]float64, n)
		for i := range out {
			if i == 1 {
				out[i] = []float64{} // không có đường đi
			} else {
				out[i] = []float64{200}
			}
		}
		return out
	})
	eta := NewOSRMETA(srv.URL, nil)
	from := []geo.Point{
		{Lat: pickupPt.Lat + 0.02, Lng: pickupPt.Lng},
		{Lat: pickupPt.Lat + 0.04, Lng: pickupPt.Lng},
		{Lat: pickupPt.Lat + 0.06, Lng: pickupPt.Lng},
	}
	got, err := eta.ETASeconds(context.Background(), from, pickupPt)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 200 || got[2] != 200 {
		t.Fatalf("ứng viên tới được phải có ETA: %v", got)
	}
	if got[1] >= 0 {
		t.Fatalf("ứng viên không tới được phải mang giá trị âm, được %v", got[1])
	}
}

// SimpleETA cũng phải theo lô và giữ thứ tự.
func TestSimpleETABatchKeepsOrder(t *testing.T) {
	eta := NewSimpleETA()
	from := []geo.Point{
		{Lat: pickupPt.Lat + 0.001, Lng: pickupPt.Lng}, // gần
		{Lat: pickupPt.Lat + 0.010, Lng: pickupPt.Lng}, // xa hơn
	}
	got, err := eta.ETASeconds(context.Background(), from, pickupPt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] >= got[1] {
		t.Fatalf("ETA phải tăng theo khoảng cách và giữ thứ tự: %v", got)
	}
	if empty, _ := eta.ETASeconds(context.Background(), nil, pickupPt); len(empty) != 0 {
		t.Fatal("lô rỗng phải trả slice rỗng")
	}
}
