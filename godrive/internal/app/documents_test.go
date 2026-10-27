package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/errs"
)

// vnDate trả ngày theo giờ Việt Nam, lệch offset ngày so với hôm nay.
func vnDate(offset int) string {
	return time.Now().In(time.FixedZone("ICT", 7*3600)).AddDate(0, 0, offset).Format(driver.DateLayout)
}

func ptr(s string) *string { return &s }

// T-37: giấy tờ hết hạn chặn nhận chuyến NGAY, không chờ ai xử lý tay; vận
// hành gia hạn xong thì tự mở lại, và việc gia hạn để lại dấu vết.
func TestExpiredDocumentsBlockUntilRenewed(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45") // đang IDLE, có vị trí
	actor := admin.Actor{AccountID: "acc_ops", Phone: "+84900000001"}

	// Bảo hiểm hết hạn hôm qua, giữa lúc tài xế đang online.
	if _, err := a.Admin.UpdateDocuments(ctx, actor, d.ID,
		driver.DocumentDates{InsuranceUntil: ptr(vnDate(-1))}, "nhập hạn từ giấy cũ"); err != nil {
		t.Fatal(err)
	}

	// 1. Dispatcher không chào tài xế này nữa.
	tr := seedTrip(t, a, riderID)
	m := fastMatcher(a, time.Minute, 1)
	if sent, err := m.DispatchRound(ctx, tr.ID, 0); err != nil || sent != 0 {
		t.Fatalf("tài xế có bảo hiểm hết hạn không được nhận lời mời: sent=%d err=%v", sent, err)
	}
	// 2. Bảng điều khiển nêu đúng lý do.
	row, err := a.Admin.GetDriver(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.BlockedReason != "documents_expired" || len(row.DocumentAlerts) != 1 ||
		row.DocumentAlerts[0].Level != driver.AlertExpired {
		t.Fatalf("admin phải thấy lý do documents_expired kèm cảnh báo: %q %+v", row.BlockedReason, row.DocumentAlerts)
	}
	ov, err := a.Admin.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAlert(ov.Alerts, "documents_expired") {
		t.Fatalf("tổng quan phải cảnh báo tài xế bị chặn vì giấy tờ: %+v", ov.Alerts)
	}
	// 3. Tắt rồi bật lại app cũng không lách được.
	if err := a.Drivers.GoOffline(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.GoOnline(ctx, d.ID); errs.CodeOf(err) != "documents_expired" {
		t.Fatalf("bật app với giấy tờ hết hạn phải bị từ chối documents_expired, được %v", err)
	}

	// 4. Gia hạn: thiếu lý do thì không được.
	if _, err := a.Admin.UpdateDocuments(ctx, actor, d.ID,
		driver.DocumentDates{InsuranceUntil: ptr(vnDate(365))}, ""); errs.CodeOf(err) != "reason_required" {
		t.Fatalf("gia hạn không ghi lý do phải bị từ chối, được %v", err)
	}
	if _, err := a.Admin.UpdateDocuments(ctx, actor, d.ID,
		driver.DocumentDates{InsuranceUntil: ptr("27/10/2027")}, "sai định dạng"); err == nil {
		t.Fatal("ngày sai định dạng phải bị từ chối")
	}
	if _, err := a.Admin.UpdateDocuments(ctx, actor, d.ID,
		driver.DocumentDates{InsuranceUntil: ptr(vnDate(365))}, "đã xem hợp đồng bảo hiểm mới"); err != nil {
		t.Fatal(err)
	}
	if err := a.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatalf("gia hạn xong phải bật app được ngay: %v", err)
	}
	row, _ = a.Admin.GetDriver(ctx, d.ID)
	if row.BlockedReason != "" || len(row.DocumentAlerts) != 0 {
		t.Fatalf("gia hạn 1 năm thì hết bị chặn và hết cảnh báo: %q %+v", row.BlockedReason, row.DocumentAlerts)
	}

	// 5. Nhật ký: hai lần đổi hạn thành công, có ngày trước/sau.
	entries, err := a.Admin.Audit(ctx, admin.AuditFilter{TargetID: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Action == admin.ActionUpdateDocuments {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("phải có đúng 2 bản ghi đổi hạn giấy tờ (lần bị từ chối không tính), được %d", n)
	}
}

// Sắp hết hạn: chưa chặn, nhưng tài xế thấy cảnh báo ở hồ sơ của mình.
func TestExpiringDocumentsWarnButDoNotBlock(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Tài", "59X1-123.45")
	if _, err := a.Drivers.UpdateDocumentDates(ctx, d.ID, driver.DocumentDates{LicenseUntil: ptr(vnDate(5))}); err != nil {
		t.Fatal(err)
	}
	cur, _ := a.Drivers.Get(ctx, d.ID)
	as := a.Drivers.DocumentAlerts(cur)
	if len(as) != 1 || as[0].Document != driver.DocLicense || as[0].Level != driver.AlertUrgent || as[0].DaysLeft != 5 {
		t.Fatalf("còn 5 ngày phải là cảnh báo urgent: %+v", as)
	}
	if err := cur.CanAcceptTrip(driver.DefaultDebtLimit, time.Now()); err != nil {
		t.Fatalf("chưa hết hạn thì vẫn nhận chuyến được: %v", err)
	}
}

func hasAlert(as []admin.Alert, code string) bool {
	for _, x := range as {
		if x.Code == code {
			return true
		}
	}
	return false
}

// Ba hạn giấy tờ phải đi trọn đường xuống CSDL và quay về — bỏ sót một cột
// trong scan là lặp lại đúng lỗi T-08 (admin duyệt hồ sơ mà không thấy giấy tờ).
func TestPostgresDocumentDatesRoundTrip(t *testing.T) {
	ctx := context.Background()
	a, _ := newPostgresApp(t)
	accID := login(t, a, "0912345678", authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: accID, FullName: "Tài Ô Tô", Phone: "+84912345678", City: "HCM",
		Vehicle: driver.Vehicle{Type: driver.VehicleCar4, Plate: "51A-123.45"},
		Documents: driver.Documents{
			NationalID: "079090001234", DriverLicense: "790123456789",
			InsuranceUntil: "2027-01-31", LicenseUntil: "2030-06-15", InspectionUntil: "2027-03-01",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Drivers.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Documents.InsuranceUntil != "2027-01-31" || got.Documents.LicenseUntil != "2030-06-15" ||
		got.Documents.InspectionUntil != "2027-03-01" {
		t.Fatalf("hạn giấy tờ không khớp sau khi lưu: %+v", got.Documents)
	}
	// Xoá hạn (giấy không thời hạn) phải lưu thành NULL, không phải lỗi định dạng.
	if _, err := a.Drivers.UpdateDocumentDates(ctx, d.ID, driver.DocumentDates{
		LicenseUntil: ptr(""), InspectionUntil: ptr("2028-03-01"),
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = a.Drivers.Get(ctx, d.ID)
	if got.Documents.LicenseUntil != "" || got.Documents.InspectionUntil != "2028-03-01" ||
		got.Documents.InsuranceUntil != "2027-01-31" || got.Documents.NationalID != "079090001234" {
		t.Fatalf("cập nhật một phần phải giữ nguyên phần còn lại: %+v", got.Documents)
	}
}
