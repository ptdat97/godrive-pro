package app

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/errs"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Test tích hợp Postgres bật bằng biến môi trường RIÊNG, không dùng lại
// DATABASE_URL: các test này XOÁ SẠCH bảng trước khi chạy, nên không bao giờ
// được phép trỏ nhầm vào cơ sở dữ liệu thật.
//
//	TEST_DATABASE_URL="postgres://postgres@localhost:5432/godrive?sslmode=disable" go test ./internal/app/
const testDBEnv = "TEST_DATABASE_URL"

// appTables theo thứ tự xoá an toàn (con trước, cha sau).
var appTables = []string{
	"trip_events", "offers", "trips", "driver_locations", "drivers",
	"otp_challenges", "accounts", "ledger_entries", "ledger_transactions",
	"idempotency_keys", "outbox", "admin_audit_log", "trip_claims",
}

// mustDB mở một kết nối phụ tới CSDL test.
func mustDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", os.Getenv(testDBEnv))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newPostgresApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	dsn := os.Getenv(testDBEnv)
	if dsn == "" {
		t.Skipf("bỏ qua: đặt %s để chạy test tích hợp Postgres", testDBEnv)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("mở DB: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping DB: %v", err)
	}
	for _, tb := range appTables {
		if _, err := db.Exec("TRUNCATE TABLE " + tb + " CASCADE"); err != nil {
			t.Fatalf("dọn bảng %s: %v", tb, err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = "test"
	cfg.DatabaseURL = dsn
	cfg.DevAuth = true

	a, err := New(cfg, logger.New("error", false))
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = db.Close() })
	return a, db
}

// TestPostgresFullTripLifecycle là điều kiện hoàn thành của Giai đoạn 0:
// toàn bộ luồng đầu-cuối phải chạy được với Postgres.
//
// Trước T-01, luồng này dừng ngay ở bước đăng ký tài xế với driver_create_failed,
// vì identity luôn dùng repo bộ nhớ nên bảng accounts không bao giờ có dòng nào
// mà drivers.account_id lại là khoá ngoại trỏ tới đó.
func TestPostgresFullTripLifecycle(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)
	a.StartWorkers(ctx)

	riderID := login(t, a, "0901234567", authn.RoleRider)
	drvAccID := login(t, a, "0912345678", authn.RoleDriver)

	// accounts phải thật sự có dòng — đây chính là mắt xích từng đứt.
	var accounts int
	if err := db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts != 2 {
		t.Fatalf("phải có 2 tài khoản trong bảng accounts, có %d", accounts)
	}

	docs := driver.Documents{
		NationalID: "079090001234", DriverLicense: "790123456789",
		VehicleRegNo: "59X1-12345", InsuranceNo: "BH-2026-001",
		InsuranceUntil: "2027-03-15",
	}
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: drvAccID, FullName: "Nguyễn Văn Tài", Phone: "+84912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45", Model: "Air Blade", Color: "Đen"},
		Documents: docs,
	})
	if err != nil {
		t.Fatalf("Onboard trên Postgres: %v", err)
	}

	// T-08: giấy tờ phải đi trọn vòng ghi -> đọc. Trước đây scan() bỏ qua hết
	// nhóm cột này nên admin duyệt KYC mà không xem được gì.
	got, err := a.Drivers.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Documents != docs {
		t.Fatalf("giấy tờ phải đọc lại đúng như đã ghi\n  muốn: %+v\n  được: %+v", docs, got.Documents)
	}

	if err := a.Drivers.ReviewKYC(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	// ReviewKYC gọi Update: giấy tờ không được bị xoá mất trong lúc đó.
	if got, err = a.Drivers.Get(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if got.Documents != docs {
		t.Fatalf("Update không được làm mất giấy tờ, còn: %+v", got.Documents)
	}
	if got.KYC != driver.KYCApproved {
		t.Fatalf("KYC phải là APPROVED, là %s", got.KYC)
	}

	if err := a.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Location.Ingest(ctx, location.Ping{
		DriverID: d.ID, Point: nearby, BearingDeg: 45, SpeedMps: 5,
		AccuracyM: 10, BatteryPc: 80, At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}

	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup:        trip.Place{Point: pickup, Address: "Chợ Bến Thành", Note: "cổng Tây"},
		Dropoff:       trip.Place{Point: dropoff, Address: "Thảo Cầm Viên"},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatalf("Create trip trên Postgres: %v", err)
	}

	offers, err := waitForOffers(t, a, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 {
		t.Fatalf("phải nhận đúng 1 lời mời, được %d", len(offers))
	}
	if _, err := a.Matcher.Accept(ctx, offers[0].ID, d.ID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	for _, step := range []func(context.Context, string, string) (*trip.Trip, error){
		a.Trips.MarkArrived, a.Trips.Start, a.Trips.Complete,
	} {
		if _, err := step(ctx, tr.ID, d.ID); err != nil {
			t.Fatalf("chuyển trạng thái: %v", err)
		}
	}

	// trip_events là nhật ký append-only: 6 lần chuyển trạng thái
	// CREATED->SEARCHING->ASSIGNED->ARRIVED->IN_PROGRESS->COMPLETED->PAID.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM trip_events WHERE trip_id=$1`, tr.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("phải có 6 bản ghi trip_events, có %d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM trips WHERE id=$1`, tr.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(trip.StatusPaid) {
		t.Fatalf("chuyến phải kết thúc ở PAID, đang ở %s", status)
	}
}

// TestPostgresOTPChallengeRoundTrip: thử thách OTP đi qua Postgres và job dọn
// thật sự xoá được cái quá hạn.
func TestPostgresOTPChallengeRoundTrip(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)

	cid, code, err := a.Identity.RequestOTP(ctx, "0987654321", authn.RoleRider)
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("DevMode phải trả mã OTP")
	}

	var hash string
	if err := db.QueryRow(`SELECT code_hash FROM otp_challenges WHERE id=$1`, cid).Scan(&hash); err != nil {
		t.Fatalf("thử thách phải nằm trong Postgres: %v", err)
	}
	if hash == code {
		t.Fatal("mã OTP thô không bao giờ được chạm đĩa")
	}

	// Nhập sai: attempts phải tăng và được lưu lại (SaveChallenge là upsert).
	if _, err := a.Identity.VerifyOTP(ctx, cid, "000000", "dev"); err == nil {
		t.Fatal("mã sai phải bị từ chối")
	}
	var attempts int
	if err := db.QueryRow(`SELECT attempts FROM otp_challenges WHERE id=$1`, cid).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("attempts phải là 1, là %d", attempts)
	}

	if _, err := a.Identity.VerifyOTP(ctx, cid, code, "dev"); err != nil {
		t.Fatalf("mã đúng phải qua: %v", err)
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM otp_challenges WHERE id=$1`, cid).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatal("xác thực xong phải xoá thử thách")
	}

	// Thử thách không ai quay lại xác thực: job dọn phải quét được.
	if _, _, err := a.Identity.RequestOTP(ctx, "0977777777", authn.RoleRider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE otp_challenges SET expires_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	n, err := a.Identity.SweepExpiredChallenges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("phải dọn 1 thử thách quá hạn, dọn %d", n)
	}
}

// TestPostgresAccountUpsertIsStable: đăng nhập lại cùng số + vai trò phải trả
// về ĐÚNG tài khoản cũ, không tạo tài khoản thứ hai.
func TestPostgresAccountUpsertIsStable(t *testing.T) {
	a, db := newPostgresApp(t)

	first := login(t, a, "0901112223", authn.RoleRider)
	second := login(t, a, "0901112223", authn.RoleRider)
	if first != second {
		t.Fatalf("đăng nhập lại phải trả cùng accountID: %s vs %s", first, second)
	}
	// Cùng số nhưng vai trò khác là người dùng khác — UNIQUE (phone, role).
	asDriver := login(t, a, "0901112223", authn.RoleDriver)
	if asDriver == first {
		t.Fatal("cùng số điện thoại nhưng khác vai trò phải là tài khoản riêng")
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM accounts WHERE phone='+84901112223'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("phải có đúng 2 tài khoản (rider + driver), có %d", n)
	}
}

// TestPostgresLedgerSurvivesRestart là điều kiện hoàn thành của Giai đoạn 1.
//
// Trước T-02, sổ cái nằm hoàn toàn trong RAM: một lần `kubectl rollout restart`
// xoá sạch công nợ của mọi tài xế, và không có cách nào khôi phục.
func TestPostgresLedgerSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)

	drvAccID := login(t, a, "0912345678", authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: drvAccID, FullName: "Tài", Phone: "+84912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45"},
		Documents: driver.Documents{NationalID: "079", DriverLicense: "790"},
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 21; i++ {
		if err := a.Wallet.SettleTrip(ctx, "trp_p_"+string(rune('a'+i)), d.ID, 50000, 10000, true); err != nil {
			t.Fatal(err)
		}
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, d.ID); bal != -210000 {
		t.Fatalf("ví phải là -210.000đ, là %d", bal)
	}

	// "Khởi động lại": đóng app, dựng app mới trên cùng cơ sở dữ liệu.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env, cfg.DatabaseURL = "test", os.Getenv(testDBEnv)
	restarted, err := New(cfg, logger.New("error", false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()

	bal, err := restarted.Wallet.DriverBalance(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bal != -210000 {
		t.Fatalf("sau khi khởi động lại, công nợ phải giữ nguyên -210.000đ, là %d", bal)
	}
	cash, err := restarted.Wallet.CashOnHand(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cash != 21*50000 {
		t.Fatalf("tiền mặt đang cầm phải giữ nguyên, là %d", cash)
	}
	// Cổng chặn nợ vẫn phải hoạt động sau khi khởi động lại.
	if err := restarted.Drivers.ReviewKYC(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Drivers.Reserve(ctx, d.ID); errs.CodeOf(err) != "wallet_debt_exceeded" {
		t.Fatalf("sau khởi động lại vẫn phải chặn vì nợ, được %v", err)
	}

	assertLedgerInvariants(t, db)
}

// TestPostgresLedgerIdempotentUnderConcurrency: hai tiến trình cùng ghi sổ một
// chuyến (worker retry chồng lên nhau) chỉ được tạo một bộ bút toán.
//
// Chốt chặn là PRIMARY KEY (tx_id) của ledger_transactions, không phải kiểm tra
// Exists() ở tầng ứng dụng — kiểm tra đó luôn có khe hở giữa lúc đọc và lúc ghi.
func TestPostgresLedgerIdempotentUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)

	const n = 20
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			errCh <- a.Wallet.SettleTrip(ctx, "trp_race", "drv_race", 50000, 10000, true)
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("ghi sổ song song không được lỗi: %v", err)
		}
	}

	var entries int
	if err := db.QueryRow(`SELECT count(*) FROM ledger_entries WHERE tx_id='tx_trip_trp_race'`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 4 {
		t.Fatalf("SettleCashTrip có 4 bút toán; %d lần gọi song song tạo ra %d", n, entries)
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, "drv_race"); bal != -10000 {
		t.Fatalf("ví phải là -10.000đ dù gọi %d lần, là %d", n, bal)
	}
	assertLedgerInvariants(t, db)
}

// TestPostgresAuditLogRecordsKYCReview: mọi thao tác quản trị phải truy vết được.
func TestPostgresAuditLogRecordsKYCReview(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)

	drvAccID := login(t, a, "0912345678", authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: drvAccID, FullName: "Tài", Phone: "+84912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45"},
		Documents: driver.Documents{NationalID: "079", DriverLicense: "790"},
	})
	if err != nil {
		t.Fatal(err)
	}

	actor := admin.Actor{AccountID: "acc_admin_1", Phone: "+84900000001"}
	if _, err := a.Admin.ReviewKYC(ctx, actor, d.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Admin.ReviewKYC(ctx, actor, d.ID, false); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM admin_audit_log WHERE target_id=$1`, d.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("hai lần duyệt phải ghi 2 dòng nhật ký, có %d", n)
	}

	entries, err := a.Admin.Audit(ctx, admin.AuditFilter{TargetID: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("Audit trả %d dòng", len(entries))
	}
	// Mới nhất lên đầu.
	if entries[0].Payload["to"] != "REJECTED" || entries[1].Payload["to"] != "APPROVED" {
		t.Fatalf("thứ tự hoặc nội dung nhật ký sai: %+v", entries)
	}
	if entries[0].ActorID != actor.AccountID {
		t.Fatalf("nhật ký phải ghi đúng người thực hiện, ghi %q", entries[0].ActorID)
	}
}

// assertLedgerInvariants chạy các câu kiểm tra bất biến trực tiếp trên CSDL.
// Đây chính là những câu nên chạy định kỳ ở production (xem docs/08 §8.7).
func assertLedgerInvariants(t *testing.T, db *sql.DB) {
	t.Helper()

	// BẤT BIẾN #1: mọi giao dịch phải cân bằng về 0.
	rows, err := db.Query(`
        SELECT tx_id, SUM(amount_vnd) FROM ledger_entries
        GROUP BY tx_id HAVING SUM(amount_vnd) <> 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var txID string
		var sum int64
		_ = rows.Scan(&txID, &sum)
		t.Errorf("giao dịch %s lệch %d đồng — sổ cái kép bị phá", txID, sum)
	}

	// BẤT BIẾN #2: mỗi bút toán phải thuộc một giao dịch đã đăng ký.
	var orphans int
	if err := db.QueryRow(`
        SELECT count(*) FROM ledger_entries e
        LEFT JOIN ledger_transactions t ON t.tx_id = e.tx_id
        WHERE t.tx_id IS NULL`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d bút toán mồ côi, không thuộc giao dịch nào", orphans)
	}
}

// TestPostgresOutboxDeliversEventsAtLeastOnce là điều kiện hoàn thành của T-06.
//
// Trước GĐ 2, sự kiện được publish thẳng lên bus in-memory và handler lỗi chỉ
// được ghi log rồi bỏ qua — một lần SettleTrip lỗi là chuyến đó vĩnh viễn không
// được ghi sổ, và không có gì phát hiện ra. Giờ sự kiện ghi vào outbox TRONG
// CÙNG transaction với thay đổi nghiệp vụ, relay phát lại cho tới khi thành công.
func TestPostgresOutboxDeliversEventsAtLeastOnce(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)

	riderID := login(t, a, "0901234567", authn.RoleRider)
	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatal(err)
	}

	// CHƯA chạy worker: sự kiện phải nằm lại trong outbox, không bốc hơi.
	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup: trip.Place{Point: pickup}, Dropoff: trip.Place{Point: dropoff},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatal(err)
	}

	var pending int
	if err := db.QueryRow(
		`SELECT count(*) FROM outbox WHERE published_at IS NULL AND topic='trip.requested'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("trip.requested phải nằm trong outbox chờ phát, có %d bản ghi", pending)
	}

	// Sự kiện và thay đổi nghiệp vụ nằm cùng một transaction: chuyến đã SEARCHING
	// thì sự kiện chắc chắn có mặt, không thể lệch nhau.
	var status string
	if err := db.QueryRow(`SELECT status FROM trips WHERE id=$1`, tr.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(trip.StatusSearching) {
		t.Fatalf("chuyến phải ở SEARCHING, đang %s", status)
	}

	// Bật worker -> relay phát nốt những gì còn tồn đọng.
	a.StartWorkers(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := db.QueryRow(
			`SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay phải phát hết outbox, còn tồn %d", pending)
		}
		time.Sleep(50 * time.Millisecond)
	}

	var dead int
	if err := db.QueryRow(
		`SELECT count(*) FROM outbox WHERE published_at IS NULL AND attempts >= 10`).Scan(&dead); err != nil {
		t.Fatal(err)
	}
	if dead != 0 {
		t.Fatalf("không được có sự kiện chết, có %d", dead)
	}
}

// TestPostgresOfferUniqueIndexBlocksDoubleAccept: chốt chặn CUỐI ở tầng CSDL.
//
// Hai lớp bảo vệ ở tầng ứng dụng (ClaimTrip và CAS Reserve) có thể bị một bug
// tương lai đi vòng qua. Unique partial index thì không.
func TestPostgresOfferUniqueIndexBlocksDoubleAccept(t *testing.T) {
	_, db := newPostgresApp(t)

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO accounts (id, phone, role) VALUES ('acc_x','+84900000009','driver')`)
	mustExec(`INSERT INTO drivers (id, account_id, full_name, phone, vehicle_type, vehicle_plate,
	          national_id, driver_license) VALUES ('drv_x','acc_x','X','+84900000009','BIKE','59X1-000.00','n','d')`)
	mustExec(`INSERT INTO trips (id, rider_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
	          vehicle_type, quote_id, fare, platform_fee, driver_earn, payment_method, status, requested_at)
	          VALUES ('trp_x','acc_x',10.7,106.7,10.8,106.8,'BIKE','q',1000,200,800,'CASH','SEARCHING',now())`)

	ins := `INSERT INTO offers (id, trip_id, driver_id, status, expires_at)
	        VALUES ($1,'trp_x','drv_x','ACCEPTED', now() + interval '1 hour')`
	mustExec(ins, "ofr_1")

	// Lời mời ACCEPTED thứ hai cho CÙNG chuyến phải bị chặn ở tầng CSDL.
	if _, err := db.Exec(ins, "ofr_2"); err == nil {
		t.Fatal("offers_one_accepted_per_trip phải chặn lời mời ACCEPTED thứ hai")
	}

	// Nhưng nhiều lời mời PENDING cho cùng chuyến thì hợp lệ (chào mời theo lô).
	if _, err := db.Exec(`INSERT INTO offers (id, trip_id, driver_id, status, expires_at)
	    VALUES ('ofr_3','trp_x','drv_x','PENDING', now() + interval '1 hour')`); err != nil {
		t.Fatalf("nhiều lời mời PENDING cho một chuyến phải hợp lệ: %v", err)
	}
}

// TestPostgresClaimTripIsAtomic: hai tài xế giành cùng một chuyến qua Postgres,
// đúng một người được.
func TestPostgresClaimTripIsAtomic(t *testing.T) {
	ctx := context.Background()
	a, _ := newPostgresApp(t)

	store := matching.NewPostgresStore(mustDB(t), a.Clock)
	const n = 16
	res := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			won, err := store.ClaimTrip(ctx, "trp_claim", "drv_"+string(rune('a'+i)), 30*time.Second)
			if err != nil {
				t.Error(err)
			}
			res <- won
		}(i)
	}
	wins := 0
	for i := 0; i < n; i++ {
		if <-res {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("đúng MỘT tài xế được giành chuyến, có %d người thắng", wins)
	}

	// Cùng tài xế gọi lại vẫn thắng: app mobile retry không được biến thành
	// "chuyến đã có người khác nhận".
	won, err := store.ClaimTrip(ctx, "trp_claim2", "drv_same", 30*time.Second)
	if err != nil || !won {
		t.Fatalf("lần giành đầu phải thắng: won=%v err=%v", won, err)
	}
	won, err = store.ClaimTrip(ctx, "trp_claim2", "drv_same", 30*time.Second)
	if err != nil || !won {
		t.Fatalf("chính chủ gọi lại vẫn phải thắng (idempotent): won=%v err=%v", won, err)
	}
}
