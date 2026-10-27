package app

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/authn"
)

// seedDriversSQL nạp n tài xế thẳng bằng SQL — Onboard từng người cho 10.000
// tài xế thì test chậm cả phút mà không thử thêm được gì.
//
// Một nửa dùng CHUNG một created_at: chính những dòng trùng mốc thời gian là
// chỗ keyset sai nếu khoá phụ (id) không được dùng đúng.
func seedDriversSQL(t *testing.T, db *sql.DB, n int, status string) {
	t.Helper()
	_, err := db.Exec(`
        WITH s AS (
            SELECT g, lpad(g::text, 6, '0') AS k,
                   CASE WHEN g % 2 = 0 THEN timestamptz '2026-01-01 00:00:00+00'
                        ELSE timestamptz '2026-01-01 00:00:00+00' + g * interval '1 second' END AS at
            FROM generate_series(1, $1) g
        ), a AS (
            INSERT INTO accounts (id, phone, role)
            SELECT 'acc_pg' || k, '+8490' || k, 'driver' FROM s
        )
        INSERT INTO drivers (id, account_id, full_name, phone, vehicle_type, vehicle_plate,
                             national_id, driver_license, status, kyc_state, created_at, updated_at)
        SELECT 'drv_pg' || k, 'acc_pg' || k, 'Tài xế ' || k, '+8490' || k, 'BIKE', '59P' || k,
               'cccd' || k, 'gplx' || k, $2, 'APPROVED', at, at
        FROM s`, n, status)
	if err != nil {
		t.Fatalf("nạp tài xế: %v", err)
	}
}

// walkDrivers đi hết mọi trang, trả danh sách id theo thứ tự nhận được.
func walkDrivers(t *testing.T, a *App, in admin.ListDriversInput) []string {
	t.Helper()
	var ids []string
	for pages := 0; ; pages++ {
		if pages > 10_000 {
			t.Fatal("phân trang không dừng — con trỏ không tiến")
		}
		p, err := a.Admin.ListDrivers(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range p.Items {
			ids = append(ids, r.ID)
		}
		if p.NextCursor == "" {
			return ids
		}
		if len(p.Items) != in.Limit && in.Limit > 0 {
			t.Fatalf("trang giữa chừng phải đủ %d dòng, được %d", in.Limit, len(p.Items))
		}
		in.Cursor = p.NextCursor
	}
}

// T-26 — điều kiện verify trong TODO: 10.000 tài xế, trang 50 dòng, thứ tự ổn
// định, đi hết được toàn bộ, không lặp không sót.
func TestPostgresDriverPaginationCoversAll(t *testing.T) {
	a, db := newPostgresApp(t)
	const n = 10_000
	seedDriversSQL(t, db, n, "OFFLINE")

	first, err := a.Admin.ListDrivers(context.Background(), admin.ListDriversInput{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 50 || first.NextCursor == "" {
		t.Fatalf("trang đầu phải đúng 50 dòng và có trang sau: %d %q", len(first.Items), first.NextCursor)
	}
	again, _ := a.Admin.ListDrivers(context.Background(), admin.ListDriversInput{Limit: 50})
	for i := range first.Items {
		if first.Items[i].ID != again.Items[i].ID {
			t.Fatalf("hai lần tải trang đầu phải giống hệt nhau, khác ở dòng %d", i)
		}
	}

	ids := walkDrivers(t, a, admin.ListDriversInput{Limit: 50})
	if len(ids) != n {
		t.Fatalf("đi hết các trang phải được đúng %d tài xế, được %d", n, len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("tài xế %s xuất hiện hai lần", id)
		}
		seen[id] = true
	}

	// Trước T-26 trang tổng quan nạp tối đa 200 dòng mỗi trạng thái rồi đếm.
	ov, err := a.Admin.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ov.Drivers.Offline != n {
		t.Fatalf("tổng quan phải đếm đủ %d tài xế OFFLINE, được %d", n, ov.Drivers.Offline)
	}
}

// Dòng mới chen vào giữa hai lần tải trang không được làm lặp hay mất dòng cũ
// — đây là chỗ OFFSET sai mà keyset đúng.
func TestPostgresPaginationStableUnderInserts(t *testing.T) {
	a, db := newPostgresApp(t)
	seedDriversSQL(t, db, 120, "OFFLINE")

	p1, err := a.Admin.ListDrivers(context.Background(), admin.ListDriversInput{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	// Một tài xế mới đăng ký giữa lúc vận hành đang xem trang 1.
	seedDriver(t, a, "0912345679", "Mới đăng ký", "59X1-999.99")

	seen := map[string]bool{}
	for _, r := range p1.Items {
		seen[r.ID] = true
	}
	rest := walkDrivers(t, a, admin.ListDriversInput{Limit: 50, Cursor: p1.NextCursor})
	for _, id := range rest {
		if seen[id] {
			t.Fatalf("tài xế %s lặp lại ở trang sau khi có người mới đăng ký", id)
		}
		seen[id] = true
	}
	if len(seen) != 120 {
		t.Fatalf("phải thấy đủ 120 tài xế cũ, thấy %d", len(seen))
	}
}

// Lọc nằm ở CSDL: lọc sau khi cắt trang thì trang nào cũng thiếu dòng.
func TestPostgresDriverFiltersPageCorrectly(t *testing.T) {
	a, db := newPostgresApp(t)
	seedDriversSQL(t, db, 300, "OFFLINE")
	if _, err := db.Exec(`UPDATE drivers SET status='IDLE' WHERE id IN
        (SELECT id FROM drivers ORDER BY id LIMIT 130)`); err != nil {
		t.Fatal(err)
	}
	idle := walkDrivers(t, a, admin.ListDriversInput{Status: "IDLE", Limit: 50})
	if len(idle) != 130 {
		t.Fatalf("lọc IDLE phải ra 130 qua các trang, được %d", len(idle))
	}
	// Tìm theo một phần SĐT; "%" trong từ khoá là ký tự thường, không phải đại diện.
	q := walkDrivers(t, a, admin.ListDriversInput{Query: "+849000012", Limit: 5})
	if len(q) != 10 { // 000120..000129
		t.Fatalf("tìm theo SĐT phải ra 10 tài xế, được %d", len(q))
	}
	if pct := walkDrivers(t, a, admin.ListDriversInput{Query: "%", Limit: 50}); len(pct) != 0 {
		t.Fatalf("từ khoá %% phải khớp đúng ký tự %%, không phải mọi dòng: %d", len(pct))
	}
	if _, err := a.Admin.ListDrivers(context.Background(), admin.ListDriversInput{Cursor: "rác"}); err == nil {
		t.Fatal("con trỏ hỏng phải bị từ chối")
	}
}

// Danh sách chuyến: cùng bảo đảm, theo thời gian đặt mới nhất trước.
func TestTripPaginationCoversAll(t *testing.T) {
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	for i := 0; i < 23; i++ {
		seedTrip(t, a, riderID)
	}
	var ids []string
	in := admin.ListTripsInput{Limit: 5}
	for {
		p, err := a.Admin.ListTrips(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range p.Items {
			ids = append(ids, r.ID)
			if i > 0 && r.RequestedAt.After(p.Items[i-1].RequestedAt) {
				t.Fatal("chuyến mới phải đứng trước")
			}
		}
		if p.NextCursor == "" {
			break
		}
		in.Cursor = p.NextCursor
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if len(ids) != 23 || len(seen) != 23 {
		t.Fatalf("phải đi qua đúng 23 chuyến không lặp, được %d (%d khác nhau)", len(ids), len(seen))
	}
}

// Quy tắc đếm giấy tờ bằng SQL phải khớp Documents.Alerts — hai bản cài đặt
// của cùng một luật, nên phải có test buộc chúng đi cùng nhau.
func TestPostgresDocumentCountsMatchAlertRules(t *testing.T) {
	a, db := newPostgresApp(t)
	seedDriversSQL(t, db, 7, "OFFLINE")
	vn := time.FixedZone("ICT", 7*3600)
	day := func(off int) string { return time.Now().In(vn).AddDate(0, 0, off).Format(driver.DateLayout) }
	cases := []struct {
		ins, lic any
	}{
		{day(-1), nil},     // hết hạn
		{day(0), nil},      // hôm nay: còn hạn, sắp hết
		{day(7), nil},      // biên 7 ngày: sắp hết
		{day(8), nil},      // ngoài 7 ngày: không đếm
		{nil, nil},         // không khai: không đếm
		{day(30), day(-3)}, // một giấy hết hạn là đủ
		{day(3), day(20)},  // giấy gấp nhất quyết định
	}
	for i, c := range cases {
		if _, err := db.Exec(`UPDATE drivers SET insurance_until=$1, license_until=$2 WHERE id=$3`,
			c.ins, c.lic, fmt.Sprintf("drv_pg%06d", i+1)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := a.Drivers.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var wantExpired, wantSoon int
	for i := range cases {
		d, err := a.Drivers.Get(context.Background(), fmt.Sprintf("drv_pg%06d", i+1))
		if err != nil {
			t.Fatal(err)
		}
		if as := d.Documents.Alerts(time.Now()); len(as) > 0 {
			switch as[0].Level {
			case driver.AlertExpired:
				wantExpired++
			case driver.AlertUrgent:
				wantSoon++
			}
		}
	}
	if wantExpired != 2 || wantSoon != 3 {
		t.Fatalf("dữ liệu test không như dự tính: expired=%d urgent=%d", wantExpired, wantSoon)
	}
	if got.DocsExpired != wantExpired || got.DocsExpiring != wantSoon {
		t.Fatalf("SQL đếm expired=%d expiring=%d, Documents.Alerts đếm %d/%d",
			got.DocsExpired, got.DocsExpiring, wantExpired, wantSoon)
	}
}

// Số liệu chuyến xong và tiền tính TỪ 0 GIỜ HÔM NAY; chuyến đang chạy tính
// tức thời bất kể đặt từ bao giờ.
func TestPostgresOverviewTodayWindow(t *testing.T) {
	a, db := newPostgresApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	insert := func(id, status string, at time.Time) {
		if _, err := db.Exec(`INSERT INTO trips (id, rider_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
            vehicle_type, quote_id, fare, platform_fee, driver_earn, payment_method, status, requested_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,'BIKE','q',30000,6000,24000,'CASH',$7,$8,$8)`,
			id, riderID, pickup.Lat, pickup.Lng, dropoff.Lat, dropoff.Lng, status, at); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	insert("trp_hom_qua", "PAID", now.Add(-48*time.Hour))
	insert("trp_hom_nay", "PAID", now)
	insert("trp_dang_cho_tu_hom_qua", "SEARCHING", now.Add(-48*time.Hour))

	ov, err := a.Admin.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ov.Trips.Completed != 1 || ov.Money.Gross != 30000 {
		t.Fatalf("chỉ chuyến hôm nay được tính vào hoàn tất/doanh thu: %d / %d", ov.Trips.Completed, ov.Money.Gross)
	}
	if ov.Trips.Searching != 1 {
		t.Fatalf("chuyến đang chờ phải đếm tức thời dù đặt từ hôm qua: %d", ov.Trips.Searching)
	}
}
