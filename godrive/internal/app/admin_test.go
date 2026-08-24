package app

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// seedDriver tạo một tài xế đã duyệt eKYC, đang online và có vị trí.
func seedDriver(t *testing.T, a *App, phone, name, plate string) *driver.Driver {
	t.Helper()
	ctx := context.Background()
	accID := login(t, a, phone, authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: accID, FullName: name, Phone: phone, City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: plate, Model: "Wave", Color: "Đỏ"},
		Documents: driver.Documents{NationalID: "079090001234", DriverLicense: "790123456789"},
	})
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if err := a.Drivers.ReviewKYC(ctx, d.ID, true); err != nil {
		t.Fatalf("ReviewKYC: %v", err)
	}
	if err := a.Drivers.GoOnline(ctx, d.ID); err != nil {
		t.Fatalf("GoOnline: %v", err)
	}
	if err := a.Location.Ingest(ctx, location.Ping{
		DriverID: d.ID, Point: nearby, BearingDeg: 45, SpeedMps: 5,
		AccuracyM: 10, BatteryPc: 80, At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return d
}

// TestAdminOverviewCountsRealState kiểm tra số liệu tổng quan phản ánh đúng
// trạng thái thật, không phải giá trị cứng.
func TestAdminOverviewCountsRealState(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	ov, err := a.Admin.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Drivers.Online != 0 || ov.Trips.Searching != 0 {
		t.Fatalf("hệ thống rỗng phải cho số 0, được %+v", ov)
	}

	seedDriver(t, a, "0912345678", "Nguyễn Văn Tài", "59X1-123.45")

	ov, err = a.Admin.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview sau khi thêm tài xế: %v", err)
	}
	if ov.Drivers.Online != 1 {
		t.Fatalf("phải có 1 tài xế online, được %d", ov.Drivers.Online)
	}
	if ov.GeneratedAt.IsZero() {
		t.Fatal("thiếu GeneratedAt")
	}
}

// TestAdminListDriversJoinsWalletAndLocation: một dòng tài xế phải gộp sẵn ví
// và vị trí để giao diện không phải gọi thêm endpoint.
func TestAdminListDriversJoinsWalletAndLocation(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	d := seedDriver(t, a, "0912345678", "Nguyễn Văn Tài", "59X1-123.45")

	rows, err := a.Admin.ListDrivers(ctx, admin.ListDriversInput{})
	if err != nil {
		t.Fatalf("ListDrivers: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("phải có 1 tài xế, được %d", len(rows))
	}
	row := rows[0]
	if row.ID != d.ID {
		t.Fatalf("sai tài xế: %s", row.ID)
	}
	if row.Position == nil {
		t.Fatal("thiếu vị trí — dòng phải gộp dữ liệu từ module location")
	}
	if row.LastSeen == nil {
		t.Fatal("thiếu LastSeen")
	}
	if row.InDebt {
		t.Fatal("tài xế mới không nợ")
	}
	if row.BlockedReason != "" {
		t.Fatalf("tài xế đã duyệt và online phải nhận được chuyến, bị chặn vì %q", row.BlockedReason)
	}
}

// TestAdminFilterByStatusAndQuery: lọc phải chạy ở server, không ở trình duyệt.
func TestAdminFilterByStatusAndQuery(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	seedDriver(t, a, "0912345678", "Nguyễn Văn Tài", "59X1-123.45")
	seedDriver(t, a, "0987654321", "Trần Thị Bình", "59Y2-678.90")

	all, err := a.Admin.ListDrivers(ctx, admin.ListDriversInput{})
	if err != nil {
		t.Fatalf("ListDrivers: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("phải có 2 tài xế, được %d", len(all))
	}

	// Lọc theo tên.
	byName, err := a.Admin.ListDrivers(ctx, admin.ListDriversInput{Query: "bình"})
	if err != nil {
		t.Fatalf("lọc theo tên: %v", err)
	}
	if len(byName) != 1 || byName[0].FullName != "Trần Thị Bình" {
		t.Fatalf("lọc theo tên sai: %+v", byName)
	}

	// Lọc theo biển số, không phân biệt hoa thường.
	byPlate, err := a.Admin.ListDrivers(ctx, admin.ListDriversInput{Query: "59x1"})
	if err != nil {
		t.Fatalf("lọc theo biển số: %v", err)
	}
	if len(byPlate) != 1 || byPlate[0].VehiclePlate != "59X1-123.45" {
		t.Fatalf("lọc theo biển số sai: %+v", byPlate)
	}

	// Trạng thái không hợp lệ phải bị từ chối, không trả rỗng im lặng.
	if _, err := a.Admin.ListDrivers(ctx, admin.ListDriversInput{Status: "KHONG_TON_TAI"}); err == nil {
		t.Fatal("trạng thái sai phải trả lỗi")
	} else if errs.CodeOf(err) != "status_invalid" {
		t.Fatalf("mã lỗi phải là status_invalid, được %q", errs.CodeOf(err))
	}
}

// TestAdminReviewKYCChangesState: duyệt hồ sơ là hành động ghi, phải có hiệu lực.
func TestAdminReviewKYCChangesState(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	accID := login(t, a, "0912345678", authn.RoleDriver)
	d, err := a.Drivers.Onboard(ctx, driver.OnboardInput{
		AccountID: accID, FullName: "Chờ Duyệt", Phone: "0912345678", City: "HCM",
		Vehicle:   driver.Vehicle{Type: driver.VehicleBike, Plate: "59X1-123.45"},
		Documents: driver.Documents{NationalID: "079090001234", DriverLicense: "790123456789"},
	})
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}

	row, err := a.Admin.GetDriver(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDriver: %v", err)
	}
	if row.KYC != driver.KYCPending {
		t.Fatalf("hồ sơ mới phải là PENDING, được %s", row.KYC)
	}
	if row.BlockedReason != "kyc_not_approved" {
		t.Fatalf("chưa duyệt phải bị chặn với lý do kyc_not_approved, được %q", row.BlockedReason)
	}

	actor := admin.Actor{AccountID: "acc_admin_test", Phone: "+84900000001"}
	after, err := a.Admin.ReviewKYC(ctx, actor, d.ID, true)
	if err != nil {
		t.Fatalf("ReviewKYC: %v", err)
	}
	if after.KYC != driver.KYCApproved {
		t.Fatalf("sau khi duyệt phải là APPROVED, được %s", after.KYC)
	}
	if after.BlockedReason == "kyc_not_approved" {
		t.Fatal("duyệt xong thì lý do chặn phải đổi, BlockedReason là một nguồn sự thật duy nhất")
	}

	// T-13: mọi thao tác quản trị phải để lại dấu vết truy được.
	entries, err := a.Admin.Audit(ctx, admin.AuditFilter{TargetID: d.ID})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("duyệt hồ sơ phải ghi đúng 1 dòng nhật ký, có %d", len(entries))
	}
	e := entries[0]
	if e.ActorID != actor.AccountID || e.Action != admin.ActionReviewKYC ||
		e.TargetType != admin.TargetDriver || e.TargetID != d.ID {
		t.Fatalf("nhật ký thiếu thông tin truy vết: %+v", e)
	}
	if e.Payload["from"] != "PENDING" || e.Payload["to"] != "APPROVED" {
		t.Fatalf("nhật ký phải ghi cả trạng thái trước và sau: %+v", e.Payload)
	}
	if e.At.IsZero() {
		t.Fatal("nhật ký phải có thời điểm")
	}
}

// TestAdminTripsAndEvents: bảng chuyến và nhật ký sự kiện phục vụ đối soát.
func TestAdminTripsAndEvents(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	riderID := login(t, a, "0901234567", authn.RoleRider)

	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	tr, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup:        trip.Place{Point: pickup, Address: "Chợ Bến Thành"},
		Dropoff:       trip.Place{Point: dropoff, Address: "Quận 3"},
		PaymentMethod: trip.PayCash,
	})
	if err != nil {
		t.Fatalf("Create trip: %v", err)
	}

	rows, err := a.Admin.ListTrips(ctx, admin.ListTripsInput{})
	if err != nil {
		t.Fatalf("ListTrips: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != tr.ID {
		t.Fatalf("phải thấy đúng chuyến vừa tạo: %+v", rows)
	}
	if rows[0].Status != trip.StatusSearching {
		t.Fatalf("chuyến mới phải SEARCHING, được %s", rows[0].Status)
	}
	// WaitingSec chỉ có ý nghĩa khi đang chờ ghép.
	if rows[0].WaitingSec < 0 {
		t.Fatalf("WaitingSec không được âm: %v", rows[0].WaitingSec)
	}

	evs, err := a.Admin.TripEvents(ctx, tr.ID)
	if err != nil {
		t.Fatalf("TripEvents: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("phải có ít nhất một sự kiện chuyển trạng thái")
	}
	if evs[0].To != trip.StatusSearching {
		t.Fatalf("sự kiện đầu phải chuyển sang SEARCHING, được %s", evs[0].To)
	}

	// Chuyến không tồn tại phải trả not_found, không phải rỗng.
	if _, err := a.Admin.TripEvents(ctx, "trp_khong_ton_tai"); err == nil {
		t.Fatal("chuyến không tồn tại phải trả lỗi")
	}
}

// TestAdminLiveMapPairsSupplyAndDemand: bản đồ phải trả cung (tài xế) và cầu
// (điểm đón chờ ghép) cùng lúc, cùng bán kính — đó mới là câu hỏi vận hành.
func TestAdminLiveMapPairsSupplyAndDemand(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	seedDriver(t, a, "0912345678", "Nguyễn Văn Tài", "59X1-123.45")

	riderID := login(t, a, "0901234567", authn.RoleRider)
	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if _, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup:        trip.Place{Point: pickup, Address: "Chợ Bến Thành"},
		Dropoff:       trip.Place{Point: dropoff, Address: "Quận 3"},
		PaymentMethod: trip.PayCash,
	}); err != nil {
		t.Fatalf("Create trip: %v", err)
	}

	res, err := a.Admin.LiveMap(ctx, admin.LiveMapInput{Center: pickup})
	if err != nil {
		t.Fatalf("LiveMap: %v", err)
	}
	if len(res.Drivers) != 1 {
		t.Fatalf("phải thấy 1 tài xế, được %d", len(res.Drivers))
	}
	if len(res.Pending) != 1 {
		t.Fatalf("phải thấy 1 điểm đón chờ ghép, được %d", len(res.Pending))
	}
	if res.Pending[0].Address != "Chợ Bến Thành" {
		t.Fatalf("sai điểm đón: %q", res.Pending[0].Address)
	}
	if res.Pending[0].WaitingSec < 0 {
		t.Fatalf("thời gian chờ không được âm: %v", res.Pending[0].WaitingSec)
	}
	// Bán kính mặc định phải được điền, giao diện dùng để vẽ vòng tròn vùng.
	if res.RadiusM != admin.DefaultMapRadiusM {
		t.Fatalf("bán kính mặc định phải là %v, được %v", admin.DefaultMapRadiusM, res.RadiusM)
	}
}

// TestAdminLiveMapFiltersByRadius: điểm đón ngoài bán kính không được lọt vào.
func TestAdminLiveMapFiltersByRadius(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	riderID := login(t, a, "0901234567", authn.RoleRider)
	q, err := a.Pricing.Estimate(ctx, pricing.EstimateInput{
		VehicleType: driver.VehicleBike, Pickup: pickup, Dropoff: dropoff,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if _, err := a.Trips.Create(ctx, trip.CreateInput{
		RiderID: riderID, QuoteID: q.ID,
		Pickup:        trip.Place{Point: pickup, Address: "Chợ Bến Thành"},
		Dropoff:       trip.Place{Point: dropoff, Address: "Quận 3"},
		PaymentMethod: trip.PayCash,
	}); err != nil {
		t.Fatalf("Create trip: %v", err)
	}

	// Tâm đặt ở Hà Nội — điểm đón TP.HCM phải bị loại.
	hanoi := geo.Point{Lat: 21.0278, Lng: 105.8342}
	res, err := a.Admin.LiveMap(ctx, admin.LiveMapInput{Center: hanoi, RadiusM: 5000})
	if err != nil {
		t.Fatalf("LiveMap: %v", err)
	}
	if len(res.Pending) != 0 {
		t.Fatalf("điểm đón ngoài bán kính phải bị loại, còn %d", len(res.Pending))
	}

	// Toạ độ không hợp lệ phải bị từ chối rõ ràng.
	if _, err := a.Admin.LiveMap(ctx, admin.LiveMapInput{
		Center: geo.Point{Lat: 999, Lng: 999},
	}); err == nil {
		t.Fatal("toạ độ sai phải trả lỗi")
	} else if errs.CodeOf(err) != "point_invalid" {
		t.Fatalf("mã lỗi phải là point_invalid, được %q", errs.CodeOf(err))
	}
}

// TestAdminAuthRejectsNonAllowlistedPhone là kiểm thử bảo mật quan trọng nhất
// của module: không được phép leo thang đặc quyền bằng cách tự khai role=admin.
func TestAdminAuthRejectsNonAllowlistedPhone(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)

	auth := admin.NewAuth(a.Identity, []string{"0900000001"})

	// Số không nằm trong danh sách bị chặn ngay, trước khi gửi OTP.
	if _, _, err := auth.RequestOTP(ctx, "0912345678"); err == nil {
		t.Fatal("số ngoài danh sách phải bị từ chối")
	} else if errs.CodeOf(err) != "not_admin" {
		t.Fatalf("mã lỗi phải là not_admin, được %q", errs.CodeOf(err))
	}

	// Số trong danh sách thì đăng nhập được và nhận đúng vai trò admin.
	cid, code, err := auth.RequestOTP(ctx, "0900000001")
	if err != nil {
		t.Fatalf("RequestOTP cho admin hợp lệ: %v", err)
	}
	tp, err := auth.VerifyOTP(ctx, cid, code, "dev")
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if tp.Account.Role != authn.RoleAdmin {
		t.Fatalf("phải cấp vai trò admin, được %s", tp.Account.Role)
	}
}

// TestAdminAuthClosedByDefault: không cấu hình thì không ai vào được.
func TestAdminAuthClosedByDefault(t *testing.T) {
	a := newTestApp(t)
	auth := admin.NewAuth(a.Identity, nil)
	if auth.Enabled() {
		t.Fatal("danh sách rỗng thì phải coi là chưa bật")
	}
	if _, _, err := auth.RequestOTP(context.Background(), "0912345678"); err == nil {
		t.Fatal("chưa cấu hình thì mọi số đều phải bị từ chối")
	}
}

// TestAdminAuthNormalizesPhoneFormat: 0901... và +8490... là cùng một người.
func TestAdminAuthNormalizesPhoneFormat(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	auth := admin.NewAuth(a.Identity, []string{"+84900000001"})

	if _, _, err := auth.RequestOTP(ctx, "0900000001"); err != nil {
		t.Fatalf("dạng 0xxx phải khớp với cấu hình +84xxx: %v", err)
	}
}
