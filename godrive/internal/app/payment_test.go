package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/payment"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/crypt"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

const (
	tPartner = "GODRIVETEST"
	tAccess  = "ACCESSKEY"
	tSecret  = "SECRETKEY-momo-test"
)

// signedMoMoIPN dựng một IPN có chữ ký THẬT.
func signedMoMoIPN(orderID string, amount int64, transID int64, resultCode int) []byte {
	raw := fmt.Sprintf(
		"accessKey=%s&amount=%d&extraData=&message=OK&orderId=%s&orderInfo=Nap vi"+
			"&orderType=momo_wallet&partnerCode=%s&payType=qr&requestId=req"+
			"&responseTime=1758000000000&resultCode=%d&transId=%d",
		tAccess, amount, orderID, tPartner, resultCode, transID)
	m := hmac.New(sha256.New, []byte(tSecret))
	m.Write([]byte(raw))
	b, _ := json.Marshal(map[string]any{
		"partnerCode": tPartner, "orderId": orderID, "requestId": "req",
		"amount": amount, "orderInfo": "Nap vi", "orderType": "momo_wallet",
		"transId": transID, "resultCode": resultCode, "message": "OK",
		"payType": "qr", "responseTime": int64(1758000000000), "extraData": "",
		"signature": hex.EncodeToString(m.Sum(nil)),
	})
	return b
}

func paymentApp(t *testing.T) (*App, *payment.Service, string) {
	t.Helper()
	a, _ := newPostgresApp(t)
	svc := payment.NewService(
		payment.NewPostgresRepo(mustDB(t)), a.Wallet, a.Clock,
		payment.NewMoMo(tPartner, tAccess, tSecret),
	)
	ctx := context.Background()
	accID := login(t, a, "0912345678", authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: accID, FullName: "Tài", Phone: "+84912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45"},
		Documents: driver.Documents{NationalID: "079", DriverLicense: "790"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, svc, d.ID
}

// Luồng đúng: tạo ý định → webhook có chữ ký thật → tiền vào ví.
func TestPaymentTopUpCreditsWallet(t *testing.T) {
	ctx := context.Background()
	a, svc, driverID := paymentApp(t)

	// Tài xế đang nợ.
	if err := a.Wallet.SettleTrip(ctx, "trp_1", driverID, 50000, 10000, true); err != nil {
		t.Fatal(err)
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, driverID); bal != -10000 {
		t.Fatalf("ví phải là -10.000đ, là %d", bal)
	}

	intent, err := svc.CreateTopUpIntent(ctx, payment.MoMo, driverID, 500000)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != payment.StatusPending {
		t.Fatalf("ý định mới phải là PENDING, là %s", intent.Status)
	}

	if _, err := svc.HandleWebhook(ctx, payment.MoMo,
		signedMoMoIPN(intent.OrderID, 500000, 111, 0), nil); err != nil {
		t.Fatalf("webhook hợp lệ phải được xử lý: %v", err)
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, driverID); bal != 490000 {
		t.Fatalf("sau khi nạp 500k, ví phải là 490.000đ, là %d", bal)
	}
}

// CHỐT CHẶN 1: chữ ký. Webhook là endpoint công khai.
func TestPaymentRejectsForgedWebhook(t *testing.T) {
	ctx := context.Background()
	_, svc, driverID := paymentApp(t)
	intent, err := svc.CreateTopUpIntent(ctx, payment.MoMo, driverID, 500000)
	if err != nil {
		t.Fatal(err)
	}

	// Thông báo tự chế, không có chữ ký hợp lệ — đúng thứ kẻ tấn công gửi.
	forged, _ := json.Marshal(map[string]any{
		"partnerCode": tPartner, "orderId": intent.OrderID, "amount": int64(500000),
		"resultCode": 0, "transId": int64(999), "signature": "deadbeef",
	})
	_, err = svc.HandleWebhook(ctx, payment.MoMo, forged, nil)
	if errs.CodeOf(err) != "webhook_bad_signature" {
		t.Fatalf("thông báo giả phải bị từ chối, được %q", errs.CodeOf(err))
	}
}

// CHỐT CHẶN 2: số tiền phải khớp ý định.
//
// Chữ ký chỉ chứng minh thông báo đến TỪ cổng. Nếu cổng bị cấu hình sai, hoặc
// ai đó ở phía cổng đổi số tiền, chữ ký vẫn hợp lệ mà số tiền thì không.
func TestPaymentRejectsAmountMismatch(t *testing.T) {
	ctx := context.Background()
	a, svc, driverID := paymentApp(t)
	intent, err := svc.CreateTopUpIntent(ctx, payment.MoMo, driverID, 500000)
	if err != nil {
		t.Fatal(err)
	}

	// Chữ ký THẬT, nhưng số tiền gấp 100 lần số đã yêu cầu.
	_, err = svc.HandleWebhook(ctx, payment.MoMo,
		signedMoMoIPN(intent.OrderID, 50000000, 222, 0), nil)
	if errs.CodeOf(err) != "webhook_amount_mismatch" {
		t.Fatalf("số tiền lệch phải bị từ chối, được %q", errs.CodeOf(err))
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, driverID); bal != 0 {
		t.Fatalf("KHÔNG được ghi có đồng nào, ví = %d", bal)
	}
}

// CHỐT CHẶN 3: idempotency. Cổng gửi lại webhook là chuyện thường ngày.
func TestPaymentWebhookIsIdempotent(t *testing.T) {
	ctx := context.Background()
	a, svc, driverID := paymentApp(t)
	intent, err := svc.CreateTopUpIntent(ctx, payment.MoMo, driverID, 300000)
	if err != nil {
		t.Fatal(err)
	}
	body := signedMoMoIPN(intent.OrderID, 300000, 333, 0)

	for i := 0; i < 5; i++ {
		if _, err := svc.HandleWebhook(ctx, payment.MoMo, body, nil); err != nil {
			t.Fatalf("lần %d: %v", i, err)
		}
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, driverID); bal != 300000 {
		t.Fatalf("gửi lại 5 lần chỉ được cộng 1 lần: ví = %d", bal)
	}
}

// Giao dịch thất bại ở cổng thì KHÔNG được ghi có.
func TestPaymentFailedTransactionCreditsNothing(t *testing.T) {
	ctx := context.Background()
	a, svc, driverID := paymentApp(t)
	intent, err := svc.CreateTopUpIntent(ctx, payment.MoMo, driverID, 200000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HandleWebhook(ctx, payment.MoMo,
		signedMoMoIPN(intent.OrderID, 200000, 444, 1006), nil); err != nil {
		t.Fatal(err)
	}
	if bal, _ := a.Wallet.DriverBalance(ctx, driverID); bal != 0 {
		t.Fatalf("giao dịch thất bại không được ghi có, ví = %d", bal)
	}
}

// Webhook cho đơn không tồn tại: có thể là dò tìm, phải từ chối.
func TestPaymentRejectsUnknownOrder(t *testing.T) {
	ctx := context.Background()
	_, svc, _ := paymentApp(t)
	_, err := svc.HandleWebhook(ctx, payment.MoMo, signedMoMoIPN("ord_khong_ton_tai", 1000, 555, 0), nil)
	if errs.CodeOf(err) != "payment_not_found" {
		t.Fatalf("đơn không tồn tại phải trả payment_not_found, được %q", errs.CodeOf(err))
	}
}

func TestPaymentIntentValidation(t *testing.T) {
	ctx := context.Background()
	_, svc, driverID := paymentApp(t)
	for _, amount := range []money.VND{0, -100, 5000, 100000000} {
		if _, err := svc.CreateTopUpIntent(ctx, payment.MoMo, driverID, amount); err == nil {
			t.Fatalf("số tiền %d phải bị từ chối", amount)
		}
	}
	if _, err := svc.CreateTopUpIntent(ctx, payment.ProviderName("PAYPAL"), driverID, 100000); err == nil {
		t.Fatal("cổng chưa cấu hình phải bị từ chối")
	}
}

// ---------------------------------------------------------------- đối soát

// BẤT BIẾN QUAN TRỌNG NHẤT của GĐ 4: chạy job chi trả HAI LẦN không trả tiền
// hai lần. Đây là thứ mà sai một lần là mất tiền thật và mất niềm tin tài xế.
func TestSettlementPayIsIdempotent(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)
	st := wallet.NewPostgresSettlementStore(db)
	s := wallet.NewSettlement(st, wallet.NewPostgresLedger(db))

	// Ba tài xế: hai có thu nhập, một dưới ngưỡng chi trả.
	drivers := make([]string, 3)
	earns := []money.VND{500000, 250000, 20000}
	for i := range drivers {
		accID := login(t, a, fmt.Sprintf("091234567%d", i), authn.RoleDriver)
		d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
			AccountID: accID, FullName: "Tài", Phone: fmt.Sprintf("+8491234567%d", i), City: "HCM",
			Vehicle: driver.Vehicle{Type: driver.VehicleBike, Plate: fmt.Sprintf("59X%d-111.1%d", i+1, i)},
			// CCCD/GPLX phải KHÁC nhau từng người: chỉ số duy nhất dựng trên
			// blind index sẽ chặn người thứ hai dùng lại giấy tờ của người đầu.
			Documents: driver.Documents{
				NationalID:    fmt.Sprintf("07930000000%d", i),
				DriverLicense: fmt.Sprintf("79012345678%d", i),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		drivers[i] = d.ID
		// Chuyến trả ONLINE: tiền vào ví tài xế.
		if err := a.Wallet.SettleTrip(ctx, "trp_s"+d.ID, d.ID, earns[i]*4/3, earns[i]/3, false); err != nil {
			t.Fatal(err)
		}
	}

	now := a.Clock.Now()
	from, to := now.Add(-24*time.Hour), now.Add(-time.Hour)

	b, err := s.Calculate(ctx, from, to, now, id.New)
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	if b.DriverCount != 2 {
		t.Fatalf("phải chốt 2 tài xế đủ ngưỡng, chốt %d", b.DriverCount)
	}

	// Chạy Calculate LẠI cho cùng kỳ: không được tạo đợt thứ hai.
	b2, err := s.Calculate(ctx, from, to, now, id.New)
	if err != nil {
		t.Fatal(err)
	}
	if b2.ID != b.ID {
		t.Fatalf("chạy lại cùng kỳ phải trả đợt cũ: %s vs %s", b.ID, b2.ID)
	}
	var batches int
	if err := db.QueryRow(`SELECT count(*) FROM settlement_batches`).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if batches != 1 {
		t.Fatalf("chỉ được có 1 đợt, có %d", batches)
	}

	// Chi trả, rồi chi trả LẠI ba lần nữa.
	res, err := s.Pay(ctx, b.ID, now)
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if res.Paid != 2 {
		t.Fatalf("phải chi cho 2 tài xế, chi %d", res.Paid)
	}
	for i := 0; i < 3; i++ {
		again, err := s.Pay(ctx, b.ID, now)
		if err != nil {
			t.Fatalf("chạy lại lần %d: %v", i, err)
		}
		if again.Paid != 0 {
			t.Fatalf("chạy lại KHÔNG được chi thêm đồng nào, chi cho %d tài xế", again.Paid)
		}
	}

	// Số dư phải về 0 với hai người được chi, giữ nguyên với người dưới ngưỡng.
	for i, did := range drivers {
		bal, err := a.Wallet.DriverBalance(ctx, did)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && bal != 0 {
			t.Fatalf("tài xế %d đã được chi thì ví phải về 0, là %d", i, bal)
		}
		if i == 2 && bal == 0 {
			t.Fatalf("tài xế dưới ngưỡng KHÔNG được chi, ví phải giữ nguyên")
		}
	}

	// Sổ cái vẫn cân bằng và mỗi tài xế chỉ có MỘT bút toán chi trả.
	rows, err := db.Query(`
        SELECT account_id, count(*) FROM ledger_entries
        WHERE ref_type='PAYOUT' AND account_type='DRIVER_WALLET'
        GROUP BY account_id HAVING count(*) > 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var acc string
		var n int
		_ = rows.Scan(&acc, &n)
		t.Errorf("tài xế %s có %d bút toán chi trả — đã trả tiền hai lần", acc, n)
	}
	assertLedgerInvariants(t, db)

	// Bút toán phải nối được về đợt đã sinh ra nó.
	var linked int
	if err := db.QueryRow(`SELECT count(*) FROM ledger_entries WHERE settlement_batch_id=$1`, b.ID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 4 { // 2 tài xế × 2 vế
		t.Fatalf("phải có 4 bút toán mang mã đợt, có %d", linked)
	}
}

// Không được chốt một kỳ chưa kết thúc — số dư còn đang thay đổi.
func TestSettlementRejectsUnfinishedPeriod(t *testing.T) {
	ctx := context.Background()
	a, db := newPostgresApp(t)
	s := wallet.NewSettlement(wallet.NewPostgresSettlementStore(db), wallet.NewPostgresLedger(db))
	now := a.Clock.Now()
	if _, err := s.Calculate(ctx, now.Add(-time.Hour), now.Add(time.Hour), now, id.New); err == nil {
		t.Fatal("kỳ chưa kết thúc phải bị từ chối")
	}
	if _, err := s.Calculate(ctx, now, now.Add(-time.Hour), now, id.New); err == nil {
		t.Fatal("kỳ ngược phải bị từ chối")
	}
}

// ---------------------------------------------------------------- mã hoá giấy tờ

// Giấy tờ phải NẰM MÃ HOÁ trong cơ sở dữ liệu, nhưng đọc ra vẫn đúng.
//
// Đây là điều mã hoá đĩa không làm được: nó bảo vệ khi ai đó lấy ổ cứng, chứ
// không bảo vệ khi bản sao lưu bị lộ hay ai đó chạy được một câu SELECT.
func TestDriverDocumentsAreEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	dsn := requireDB(t)
	a, db := newPostgresApp(t)

	key, err := crypt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := crypt.New(key)
	if err != nil {
		t.Fatal(err)
	}
	sepDB := mustDB(t)
	repo := driver.NewPostgresRepo(sepDB)
	repo.UseCipher(c)
	_ = dsn

	accID := login(t, a, "0912345678", authn.RoleDriver)
	now := a.Clock.Now()
	docs := driver.Documents{
		NationalID: "079090001234", DriverLicense: "B2-987654321",
		VehicleRegNo: "59X1-12345", InsuranceNo: "BH-2026-001",
		InsuranceUntil: "2027-03-15",
	}
	d := &driver.Driver{
		ID: "drv_enc_1", AccountID: accID, FullName: "Tài", Phone: "+84912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-777.77"},
		Documents: docs, KYC: driver.KYCPending, Status: driver.StatusOffline,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}

	// Trong CSDL: KHÔNG được thấy số gốc.
	var rawNID, rawGPLX, blind string
	if err := db.QueryRow(
		`SELECT national_id, driver_license, national_id_idx FROM drivers WHERE id=$1`,
		d.ID).Scan(&rawNID, &rawGPLX, &blind); err != nil {
		t.Fatal(err)
	}
	if rawNID == docs.NationalID || rawGPLX == docs.DriverLicense {
		t.Fatalf("giấy tờ đang nằm THÔ trong CSDL: %q / %q", rawNID, rawGPLX)
	}
	if !crypt.IsEncrypted(rawNID) || !crypt.IsEncrypted(rawGPLX) {
		t.Fatalf("thiếu dấu hiệu đã mã hoá: %q / %q", rawNID, rawGPLX)
	}
	if blind == "" || blind == docs.NationalID {
		t.Fatalf("chỉ mục mù sai: %q", blind)
	}

	// Đọc qua repo: phải ra đúng bản gốc.
	got, err := repo.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Documents != docs {
		t.Fatalf("giấy tờ không đi trọn vòng:\n  muốn %+v\n  được %+v", docs, got.Documents)
	}

	// Cùng số CCCD ở hồ sơ thứ hai phải bị chặn bởi chỉ mục mù.
	acc2 := login(t, a, "0987654321", authn.RoleDriver)
	dup := *d
	dup.ID, dup.AccountID = "drv_enc_2", acc2
	dup.Vehicle.Plate = "59X1-888.88"
	if err := repo.Create(ctx, &dup); err == nil {
		t.Fatal("hai hồ sơ dùng chung số CCCD phải bị chặn")
	}
}

// Sai khoá phải BÁO LỖI, không được trả ra hồ sơ thiếu giấy tờ.
//
// Trả chuỗi rỗng sẽ biến một sự cố bảo mật thành "hồ sơ chưa có giấy tờ" — một
// triệu chứng dẫn người tìm lỗi đi hoàn toàn sai hướng.
func TestWrongDocumentKeyFailsLoudly(t *testing.T) {
	ctx := context.Background()
	a, _ := newPostgresApp(t)

	k1, _ := crypt.GenerateKey()
	c1, _ := crypt.New(k1)
	repo1 := driver.NewPostgresRepo(mustDB(t))
	repo1.UseCipher(c1)

	accID := login(t, a, "0912345678", authn.RoleDriver)
	now := a.Clock.Now()
	d := &driver.Driver{
		ID: "drv_key_1", AccountID: accID, FullName: "Tài", Phone: "+84912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-555.55"},
		Documents: driver.Documents{NationalID: "079090001234", DriverLicense: "B2-1"},
		KYC:       driver.KYCPending, Status: driver.StatusOffline,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo1.Create(ctx, d); err != nil {
		t.Fatal(err)
	}

	k2, _ := crypt.GenerateKey()
	c2, _ := crypt.New(k2)
	repo2 := driver.NewPostgresRepo(mustDB(t))
	repo2.UseCipher(c2)

	if _, err := repo2.Get(ctx, d.ID); err == nil {
		t.Fatal("sai khoá phải trả LỖI, không được trả hồ sơ với giấy tờ rỗng")
	} else if errs.CodeOf(err) != "crypt_decrypt_failed" {
		t.Fatalf("mã lỗi phải là crypt_decrypt_failed, được %q", errs.CodeOf(err))
	}
}

func requireDB(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(testDBEnv)
	if dsn == "" {
		t.Skipf("cần %s", testDBEnv)
	}
	return dsn
}
