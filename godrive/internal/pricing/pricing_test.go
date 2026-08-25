package pricing

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
	"github.com/example/godrive/pkg/money"
)

var (
	pickup  = geo.Point{Lat: 10.7725, Lng: 106.6980}
	dropoff = geo.Point{Lat: 10.8014, Lng: 106.7109}
	bike    = DefaultTariffs()[driver.VehicleBike]
)

// fixedRoute trả đúng quãng đường/thời lượng đặt sẵn — test giá phải tất định,
// không phụ thuộc công thức haversine.
type fixedRoute struct{ r Route }

func (f fixedRoute) Route(context.Context, geo.Point, geo.Point) (Route, error) { return f.r, nil }

// fixedSurge trả đúng hệ số đặt sẵn, kể cả giá trị vô lý — dùng để kiểm chốt clamp.
type fixedSurge struct{ permille int64 }

func (f fixedSurge) SurgePermille(context.Context, geo.Point, time.Time) (int64, error) {
	return f.permille, nil
}

func newSvc(r Route, surgePermille int64, at time.Time) (*Service, *clock.Mock) {
	return newSvcWithRoutes(fixedRoute{r}, surgePermille, at)
}

func newSvcWithRoutes(re RouteEngine, surgePermille int64, at time.Time) (*Service, *clock.Mock) {
	clk := clock.NewMock(at)
	return NewService(re, fixedSurge{surgePermille}, NewMemoryQuoteStore(), clk), clk
}

// vnTime dựng thời điểm theo giờ Việt Nam (UTC+7).
func vnTime(h, m int) time.Time {
	return time.Date(2026, 8, 24, h, m, 0, 0, time.FixedZone("ICT", 7*3600))
}

// ---------------------------------------------------------------- computeBase

func TestComputeBase(t *testing.T) {
	cases := []struct {
		name string
		r    Route
		want money.VND
	}{
		// Giá mở cửa đã bao gồm 2km đầu: dưới ngưỡng không tính thêm km nào.
		{"cự ly 0", Route{}, 12000},
		{"đúng bằng giá mở cửa", Route{DistanceM: 2000}, 12000},
		{"dưới giá mở cửa", Route{DistanceM: 1500}, 12000},
		// 1km vượt ngưỡng = 4.300đ.
		{"vượt 1km", Route{DistanceM: 3000}, 12000 + 4300},
		// Nửa km = 2.150đ; 90 giây = 450đ.
		{"vượt 500m + 90 giây", Route{DistanceM: 2500, DurationS: 90}, 12000 + 2150 + 450},
		{"chỉ tính thời gian", Route{DistanceM: 2000, DurationS: 600}, 12000 + 3000},
		{"cự ly dài 20km", Route{DistanceM: 22000}, 12000 + 4300*20},
		// Lẻ mét: 1 mét = 4,3đ -> làm tròn nửa lên = 4đ.
		{"lẻ 1 mét", Route{DistanceM: 2001}, 12000 + 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeBase(bike, c.r); got != c.want {
				t.Fatalf("computeBase = %d, muốn %d", got, c.want)
			}
		})
	}
}

// computeBase là hàm THUẦN — cùng đầu vào phải luôn cho cùng kết quả.
func TestComputeBaseIsPure(t *testing.T) {
	r := Route{DistanceM: 7345.7, DurationS: 923.4}
	first := computeBase(bike, r)
	for i := 0; i < 1000; i++ {
		if got := computeBase(bike, r); got != first {
			t.Fatalf("lần %d cho %d, lần đầu cho %d", i, got, first)
		}
	}
}

// ------------------------------------------------------------------ giờ đêm

func TestNightSurchargeBoundaries(t *testing.T) {
	// Phụ phí đêm 22:00–05:00 giờ VN, 10% giá cơ bản.
	cases := []struct {
		h, m  int
		night bool
	}{
		{21, 59, false}, {22, 0, true}, {23, 30, true},
		{0, 0, true}, {4, 59, true}, {5, 0, false}, {12, 0, false},
	}
	for _, c := range cases {
		at := vnTime(c.h, c.m)
		if got := isNight(at); got != c.night {
			t.Fatalf("%02d:%02d giờ VN: isNight=%v, muốn %v", c.h, c.m, got, c.night)
		}

		svc, _ := newSvc(Route{DistanceM: 2000}, MinSurgePermille, at)
		q, err := svc.Estimate(context.Background(), EstimateInput{
			VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
		})
		if err != nil {
			t.Fatal(err)
		}
		wantFee := money.VND(0)
		if c.night {
			wantFee = 1200 // 10% của 12.000
		}
		if q.NightFee != wantFee {
			t.Fatalf("%02d:%02d giờ VN: NightFee=%d, muốn %d", c.h, c.m, q.NightFee, wantFee)
		}
	}
}

// isNight phải tính theo giờ VN chứ không theo giờ máy chủ.
func TestNightUsesVietnamTimeNotServerTime(t *testing.T) {
	// 16:00 UTC = 23:00 giờ VN -> là ban đêm, dù giờ UTC thì không phải.
	if !isNight(time.Date(2026, 8, 24, 16, 0, 0, 0, time.UTC)) {
		t.Fatal("16:00 UTC là 23:00 giờ VN, phải tính là ban đêm")
	}
	// 20:00 giờ VN = 13:00 UTC -> chưa phải ban đêm.
	if isNight(time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)) {
		t.Fatal("13:00 UTC là 20:00 giờ VN, chưa phải ban đêm")
	}
}

// --------------------------------------------------------------------- surge

func TestSurgeStaircase(t *testing.T) {
	// demand/supply -> hệ số. Bậc thang, không liên tục.
	cases := []struct {
		demand, supply int
		want           int64
	}{
		{0, 5, 1000}, {5, 5, 1000}, // ratio 0 và 1.0
		{5, 5, 1000},
		{6, 5, 1200},   // ratio 1.2 — đúng ngưỡng
		{59, 50, 1000}, // ratio 1.18 — dưới ngưỡng một chút
		{60, 50, 1200}, // ratio 1.2
		{10, 5, 1400},  // ratio 2.0
		{15, 5, 1700},  // ratio 3.0
		{20, 5, 2000},  // ratio 4.0
		{100, 1, 2000}, // ratio 100 — vẫn chặn ở trần
	}
	for _, c := range cases {
		ds := NewDemandSurge(stubSupply(c.supply))
		now := time.Now().UTC()
		for i := 0; i < c.demand; i++ {
			ds.RecordRequest(pickup, now)
		}
		got, err := ds.SurgePermille(context.Background(), pickup, now)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Fatalf("demand=%d supply=%d: surge=%d, muốn %d", c.demand, c.supply, got, c.want)
		}
		if got > MaxSurgePermille {
			t.Fatalf("surge %d vượt trần %d", got, MaxSurgePermille)
		}
	}
}

type stubSupply int

func (s stubSupply) IdleCount(context.Context, geo.Point, float64) (int, error) { return int(s), nil }

// Cửa sổ trượt 5 phút: yêu cầu cũ hơn phải rơi ra khỏi phép đếm.
func TestSurgeDemandWindowSlides(t *testing.T) {
	ds := NewDemandSurge(stubSupply(1))
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		ds.RecordRequest(pickup, base)
	}
	if got, _ := ds.SurgePermille(context.Background(), pickup, base); got != 2000 {
		t.Fatalf("10 yêu cầu / 1 tài xế phải là trần 2000, được %d", got)
	}
	// 6 phút sau, toàn bộ yêu cầu đã ra khỏi cửa sổ.
	later := base.Add(6 * time.Minute)
	if got, _ := ds.SurgePermille(context.Background(), pickup, later); got != MinSurgePermille {
		t.Fatalf("quá cửa sổ 5 phút phải về %d, được %d", MinSurgePermille, got)
	}
}

// Chốt clamp THỨ HAI: dù SurgeProvider trả giá trị vô lý, Estimate vẫn chặn ở trần.
func TestEstimateClampsRogueSurgeProvider(t *testing.T) {
	for _, rogue := range []int64{2001, 9999, 1_000_000} {
		svc, _ := newSvc(Route{DistanceM: 5000}, rogue, vnTime(12, 0))
		q, err := svc.Estimate(context.Background(), EstimateInput{
			VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
		})
		if err != nil {
			t.Fatal(err)
		}
		if q.SurgePermille != MaxSurgePermille {
			t.Fatalf("SurgeProvider trả %d, Estimate phải clamp về %d, được %d",
				rogue, MaxSurgePermille, q.SurgePermille)
		}
	}
	// Sàn cũng phải chặn: không bao giờ giảm giá qua đường surge.
	svc, _ := newSvc(Route{DistanceM: 5000}, 300, vnTime(12, 0))
	q, _ := svc.Estimate(context.Background(), EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if q.SurgePermille != MinSurgePermille {
		t.Fatalf("surge dưới sàn phải clamp về %d, được %d", MinSurgePermille, q.SurgePermille)
	}
}

// ------------------------------------------------------------- bất biến tiền

// Giá cước VN làm tròn nghìn, và luôn làm tròn LÊN.
func TestTotalRoundedUpToThousand(t *testing.T) {
	for _, d := range []float64{2100, 2345, 3333, 4999, 7777, 12345, 23456} {
		svc, _ := newSvc(Route{DistanceM: d}, MinSurgePermille, vnTime(12, 0))
		q, err := svc.Estimate(context.Background(), EstimateInput{
			VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
		})
		if err != nil {
			t.Fatal(err)
		}
		if q.Total%1000 != 0 {
			t.Fatalf("cự ly %.0fm: total=%d không tròn nghìn", d, q.Total)
		}
		if q.Total < q.BaseFare {
			t.Fatalf("cự ly %.0fm: làm tròn phải LÊN, total=%d < base=%d", d, q.Total, q.BaseFare)
		}
	}
}

// MinFare là sàn: chuyến siêu ngắn không được rẻ hơn giá tối thiểu.
func TestMinFareFloor(t *testing.T) {
	svc, _ := newSvc(Route{DistanceM: 100, DurationS: 10}, MinSurgePermille, vnTime(12, 0))
	q, err := svc.Estimate(context.Background(), EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.Total < bike.MinFare {
		t.Fatalf("total=%d thấp hơn MinFare=%d", q.Total, bike.MinFare)
	}
}

// BẤT BIẾN: chiết khấu + thu nhập tài xế luôn bằng đúng tổng cước. Không đồng nào bốc hơi.
func TestFeePlusEarnAlwaysEqualsTotal(t *testing.T) {
	vts := []driver.VehicleType{driver.VehicleBike, driver.VehicleCar4, driver.VehicleCar7}
	for _, vt := range vts {
		for d := 0.0; d <= 40000; d += 137 {
			for _, surge := range []int64{1000, 1200, 1400, 1700, 2000} {
				for _, at := range []time.Time{vnTime(12, 0), vnTime(23, 0)} {
					svc, _ := newSvc(Route{DistanceM: d, DurationS: d / 6}, surge, at)
					q, err := svc.Estimate(context.Background(), EstimateInput{
						VehicleType: vt, Pickup: pickup, Dropoff: dropoff,
					})
					if err != nil {
						t.Fatal(err)
					}
					if q.PlatformFee+q.DriverEarn != q.Total {
						t.Fatalf("%s d=%.0f surge=%d: fee(%d)+earn(%d) != total(%d)",
							vt, d, surge, q.PlatformFee, q.DriverEarn, q.Total)
					}
					if q.PlatformFee < 0 || q.DriverEarn < 0 || q.Total <= 0 {
						t.Fatalf("%s d=%.0f: số tiền âm hoặc bằng 0: %+v", vt, d, q)
					}
				}
			}
		}
	}
}

// TestComputeBaseRoundsHalfUpNotTruncate: mỗi thành phần giá phải LÀM TRÒN NỬA,
// không được cắt cụt. Bản float cũ (`extra/1000 * float64(PerKm)`) cắt cụt.
//
// Ngưỡng 500 chính là "nửa đồng cuối" của phép chia cho 1000 — cắt cụt có thể
// lệch tới gần 1000, nên assert này phân biệt được hai cách làm.
func TestComputeBaseRoundsHalfUpNotTruncate(t *testing.T) {
	for dm := 2000; dm <= 30000; dm += 7 {
		r := Route{DistanceM: float64(dm)}
		got := computeBase(bike, r)
		kmPart := int64(got - bike.OpeningFare)
		exact := int64(bike.PerKm) * int64(dm-2000) // = phần km × 1000
		if diff := kmPart*1000 - exact; diff > 500 || diff < -500 {
			t.Fatalf("cự ly %dm: phần km = %d, giá trị đúng %.3f — lệch quá nửa đồng",
				dm, kmPart, float64(exact)/1000)
		}
	}
}

// TestComputeBaseKnownTruncationCases: các cự ly mà cắt cụt và làm tròn nửa cho
// kết quả KHÁC nhau. Nếu ai đó đưa float trở lại, những case này fail ngay.
func TestComputeBaseKnownTruncationCases(t *testing.T) {
	cases := []struct {
		distanceM int
		want      money.VND // làm tròn nửa (đúng); cắt cụt sẽ ra thấp hơn
	}{
		{2333, 12000 + 1432}, // 333m × 4,3đ = 1.431,9 -> 1432, cắt cụt cho 1431
		{2649, 12000 + 2791}, // 649m × 4,3đ = 2.790,7 -> 2791, cắt cụt cho 2790
		{2399, 12000 + 1716}, // 399m × 4,3đ = 1.715,7 -> 1716, cắt cụt cho 1715
	}
	for _, c := range cases {
		got := computeBase(bike, Route{DistanceM: float64(c.distanceM)})
		if got != c.want {
			t.Fatalf("cự ly %dm: computeBase=%d, muốn %d", c.distanceM, got, c.want)
		}
	}
}

// TestFloatDriftChangesFareByAThousand là hồi quy cho G-09.
//
// Sai lệch vài đồng ở computeBase KHÔNG vô hại: khi nó vắt qua ranh giới làm
// tròn nghìn, tổng cước khách phải trả lệch nguyên 1.000đ. Quét dải 2–40km với
// 5 mức surge cho thấy 138 tổ hợp rơi vào trường hợp này (chưa tính thành phần
// thời lượng; tính cả nó thì con số là 422).
func TestFloatDriftChangesFareByAThousand(t *testing.T) {
	cases := []struct {
		distanceM int
		surge     int64
		want      money.VND // bản float cũ cho ít hơn đúng 1.000đ
	}{
		{2219, 1700, 23000}, // bản float cũ: 22.000
		{2349, 2000, 28000}, // bản float cũ: 27.000
		{2903, 1700, 28000}, // bản float cũ: 27.000
		{3163, 1000, 18000}, // bản float cũ: 17.000
	}
	for _, c := range cases {
		// DurationS = 0 để cô lập đúng thành phần quãng đường.
		svc, _ := newSvc(Route{DistanceM: float64(c.distanceM)}, c.surge, vnTime(12, 0))
		q, err := svc.Estimate(context.Background(), EstimateInput{
			VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
		})
		if err != nil {
			t.Fatal(err)
		}
		if q.Total != c.want {
			t.Fatalf("cự ly %dm surge=%d: tổng cước=%d, muốn %d",
				c.distanceM, c.surge, q.Total, c.want)
		}
	}
}

// MulPermille phải khớp phép chia số nguyên có làm tròn nửa, không phải float.
func TestSurgeMultiplyIsExact(t *testing.T) {
	for _, surge := range []int64{1000, 1200, 1400, 1700, 2000} {
		for base := money.VND(10000); base <= 400000; base += 97 {
			got := base.MulPermille(surge)
			exact := int64(base) * surge
			if diff := int64(got)*1000 - exact; diff > 500 || diff < -500 {
				t.Fatalf("base=%d surge=%d: %d lệch quá nửa đồng so với %.3f",
					base, surge, got, float64(exact)/1000)
			}
		}
	}
}

// ---------------------------------------------------------------- vòng đời quote

func TestQuoteExpiresAfterTTL(t *testing.T) {
	ctx := context.Background()
	svc, clk := newSvc(Route{DistanceM: 5000}, MinSurgePermille, vnTime(12, 0))
	q, err := svc.Estimate(ctx, EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetQuote(ctx, q.ID); err != nil {
		t.Fatalf("báo giá còn hạn phải lấy được: %v", err)
	}
	clk.Advance(QuoteTTL - time.Second)
	if _, err := svc.GetQuote(ctx, q.ID); err != nil {
		t.Fatalf("sát hạn vẫn phải lấy được: %v", err)
	}
	clk.Advance(2 * time.Second)
	_, err = svc.GetQuote(ctx, q.ID)
	if got := errs.CodeOf(err); got != "quote_expired" {
		t.Fatalf("quá hạn phải trả quote_expired, được %q", got)
	}
}

func TestEstimateRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(Route{DistanceM: 5000}, MinSurgePermille, vnTime(12, 0))

	_, err := svc.Estimate(ctx, EstimateInput{VehicleType: "XE_TANG", Pickup: pickup, Dropoff: dropoff})
	if got := errs.CodeOf(err); got != "vehicle_type_invalid" {
		t.Fatalf("loại xe lạ phải trả vehicle_type_invalid, được %q", got)
	}
	_, err = svc.Estimate(ctx, EstimateInput{
		VehicleType: driver.VehicleBike,
		Pickup:      geo.Point{Lat: 999, Lng: 999}, Dropoff: dropoff,
	})
	if got := errs.CodeOf(err); got != "point_invalid" {
		t.Fatalf("toạ độ sai phải trả point_invalid, được %q", got)
	}
}

// EstimateAll trả đủ 3 loại xe, theo thứ tự ổn định, giá tăng dần.
func TestEstimateAllOrderedAndPriced(t *testing.T) {
	svc, _ := newSvc(Route{DistanceM: 6400, DurationS: 1047}, MinSurgePermille, vnTime(12, 0))
	qs, err := svc.EstimateAll(context.Background(), pickup, dropoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 3 {
		t.Fatalf("phải có 3 báo giá, có %d", len(qs))
	}
	want := []driver.VehicleType{driver.VehicleBike, driver.VehicleCar4, driver.VehicleCar7}
	for i, q := range qs {
		if q.VehicleType != want[i] {
			t.Fatalf("vị trí %d phải là %s, là %s", i, want[i], q.VehicleType)
		}
		if i > 0 && q.Total <= qs[i-1].Total {
			t.Fatalf("%s (%d) phải đắt hơn %s (%d)", q.VehicleType, q.Total, qs[i-1].VehicleType, qs[i-1].Total)
		}
	}
}
