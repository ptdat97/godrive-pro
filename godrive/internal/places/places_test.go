package places

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// fakeGoong trả phản hồi đúng định dạng tài liệu Goong, và đếm số lượt gọi —
// mỗi lượt là một lần bị tính tiền.
func fakeGoong(t *testing.T, calls *atomic.Int64, handler func(w http.ResponseWriter, r *http.Request)) *Goong {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("api_key") != "khoa-test" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewGoong(srv.URL, "khoa-test")
}

func goongOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/Place/AutoComplete":
		if strings.Contains(r.URL.Query().Get("input"), "khongcogi") {
			_, _ = w.Write([]byte(`{"predictions":[],"status":"ZERO_RESULTS"}`))
			return
		}
		_, _ = w.Write([]byte(`{"predictions":[{"place_id":"p_bt","description":"Chợ Bến Thành, Quận 1",
            "structured_formatting":{"main_text":"Chợ Bến Thành","secondary_text":"Lê Lợi, Quận 1, TP.HCM"}}],"status":"OK"}`))
	case "/Place/Detail":
		_, _ = w.Write([]byte(`{"result":{"place_id":"p_bt","name":"Chợ Bến Thành",
            "formatted_address":"Lê Lợi, Bến Thành, Quận 1, TP.HCM",
            "geometry":{"location":{"lat":10.7725,"lng":106.698}}},"status":"OK"}`))
	case "/Geocode":
		_, _ = w.Write([]byte(`{"results":[{"place_id":"g1","formatted_address":"12 Lê Lợi, Bến Nghé, Quận 1"}],"status":"OK"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

var q1 = geo.Point{Lat: 10.7769, Lng: 106.7009}

func TestAutocompleteMapsAndCaches(t *testing.T) {
	var calls atomic.Int64
	s := NewService(fakeGoong(t, &calls, goongOK), NewMemoryCache(100))
	ctx := context.Background()

	got, err := s.Autocomplete(ctx, "Chợ  Bến Thành", &q1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PlaceID != "p_bt" || got[0].Title != "Chợ Bến Thành" || got[0].Address == "" {
		t.Fatalf("gợi ý sai: %+v", got)
	}
	// Viết hoa/thường và khoảng trắng khác nhau, lệch vài trăm mét: dùng lại cache.
	near := geo.Point{Lat: q1.Lat + 0.001, Lng: q1.Lng}
	if _, err := s.Autocomplete(ctx, "chợ bến thành", &near); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lần hai phải lấy từ cache, đã gọi Goong %d lần", calls.Load())
	}
	// Ở khu vực khác (Thủ Đức) gợi ý có thể khác: phải hỏi lại.
	if _, err := s.Autocomplete(ctx, "chợ bến thành", &geo.Point{Lat: 10.85, Lng: 106.77}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("khu vực khác phải hỏi lại Goong, đã gọi %d lần", calls.Load())
	}
}

// Một ký tự thì không tốn tiền gọi; không có kết quả không phải lỗi.
func TestAutocompleteShortAndEmpty(t *testing.T) {
	var calls atomic.Int64
	s := NewService(fakeGoong(t, &calls, goongOK), NewMemoryCache(100))
	if got, err := s.Autocomplete(context.Background(), " c ", nil); err != nil || len(got) != 0 {
		t.Fatalf("từ khoá 1 ký tự phải trả rỗng, được %v %v", got, err)
	}
	if calls.Load() != 0 {
		t.Fatal("từ khoá 1 ký tự không được gọi Goong")
	}
	if got, err := s.Autocomplete(context.Background(), "khongcogi", nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ZERO_RESULTS phải là danh sách rỗng, được %v %v", got, err)
	}
}

func TestDetailAndReverse(t *testing.T) {
	var calls atomic.Int64
	s := NewService(fakeGoong(t, &calls, goongOK), NewMemoryCache(100))
	ctx := context.Background()

	p, err := s.Detail(ctx, "p_bt")
	if err != nil || p.Point.Lat != 10.7725 || p.Name != "Chợ Bến Thành" {
		t.Fatalf("chi tiết sai: %+v %v", p, err)
	}
	// GPS nhảy vài mét không được thành một lượt tính tiền mới.
	a := geo.Point{Lat: 10.776901, Lng: 106.700901}
	b := geo.Point{Lat: 10.776904, Lng: 106.700903}
	ra, err := s.Reverse(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := s.Reverse(ctx, b)
	if calls.Load() != 2 {
		t.Fatalf("hai điểm cách vài mét phải dùng chung cache, đã gọi %d lần", calls.Load())
	}
	if ra.Name != "12 Lê Lợi" || rb.Point != b {
		t.Fatalf("tên lấy từ phần đầu địa chỉ, toạ độ trả đúng điểm được hỏi: %+v / %+v", ra, rb)
	}
}

// Lỗi phía Goong không được nằm trong cache — nếu không, một lần sai key là
// hỏng tìm kiếm suốt 6 tiếng sau khi đã sửa key.
func TestProviderErrorsAreNotCached(t *testing.T) {
	var calls atomic.Int64
	var fail atomic.Bool
	fail.Store(true)
	g := fakeGoong(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			_, _ = w.Write([]byte(`{"status":"REQUEST_DENIED","predictions":[]}`))
			return
		}
		goongOK(w, r)
	})
	s := NewService(g, NewMemoryCache(100))
	if _, err := s.Autocomplete(context.Background(), "chợ bến thành", nil); errs.CodeOf(err) != "places_provider_error" {
		t.Fatalf("REQUEST_DENIED phải là lỗi nhà cung cấp, được %v", err)
	}
	fail.Store(false)
	got, err := s.Autocomplete(context.Background(), "chợ bến thành", nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("sau khi Goong ổn lại phải có kết quả, được %v %v", got, err)
	}

	bad := NewService(NewGoong(g.baseURL, "sai-khoa"), nil)
	if _, err := bad.Detail(context.Background(), "p_bt"); errs.CodeOf(err) != "places_bad_status" {
		t.Fatalf("sai key phải báo places_bad_status, được %v", err)
	}
}

type brokenCache struct{}

func (brokenCache) Get(context.Context, string) ([]byte, bool, error) { return nil, false, errors.New("redis chết") }
func (brokenCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("redis chết")
}

// Cache chết thì tốn tiền hơn, không được mất chức năng.
func TestBrokenCacheStillServes(t *testing.T) {
	var calls atomic.Int64
	s := NewService(fakeGoong(t, &calls, goongOK), brokenCache{})
	if got, err := s.Autocomplete(context.Background(), "chợ bến thành", nil); err != nil || len(got) != 1 {
		t.Fatalf("cache hỏng vẫn phải phục vụ được: %v %v", got, err)
	}
}

func TestUnconfiguredIsUnavailable(t *testing.T) {
	s := NewService(nil, NewMemoryCache(10))
	_, err := s.Detail(context.Background(), "p_bt")
	if errs.CodeOf(err) != "places_unavailable" || errs.HTTPStatus(err) != http.StatusServiceUnavailable {
		t.Fatalf("chưa cấu hình phải trả 503 places_unavailable, được %v", err)
	}
}

// Chạy với Goong THẬT khi có key: xác nhận định dạng phản hồi khớp với cách
// client này đọc. Bắt buộc chạy một lần khi bật GOONG_API_KEY lần đầu.
//
//	TEST_GOONG_API_KEY=... go test ./internal/places -run TestGoongLive -v
func TestGoongLive(t *testing.T) {
	key := os.Getenv("TEST_GOONG_API_KEY")
	if key == "" {
		t.Skip("bỏ qua: đặt TEST_GOONG_API_KEY để thử với Goong thật")
	}
	s := NewService(NewGoong(GoongBaseURL, key), nil)
	ctx := context.Background()
	sug, err := s.Autocomplete(ctx, "Chợ Bến Thành", &q1)
	if err != nil || len(sug) == 0 {
		t.Fatalf("autocomplete: %v %v", sug, err)
	}
	t.Logf("gợi ý đầu: %+v", sug[0])
	p, err := s.Detail(ctx, sug[0].PlaceID)
	if err != nil || geo.DistanceM(p.Point, geo.Point{Lat: 10.7725, Lng: 106.698}) > 1000 {
		t.Fatalf("detail phải ra gần Chợ Bến Thành: %+v %v", p, err)
	}
	r, err := s.Reverse(ctx, q1)
	if err != nil || r.Address == "" {
		t.Fatalf("reverse: %+v %v", r, err)
	}
	t.Logf("reverse: %+v", r)
}
