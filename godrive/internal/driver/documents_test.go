package driver

import (
	"testing"
	"time"

	"github.com/example/godrive/pkg/errs"
)

// 2026-10-27 18:00 UTC là 01:00 sáng ngày 28 ở Việt Nam.
var lateNightUTC = time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC)

// Hạn ghi trên giấy là ngày theo giờ Việt Nam. Tính theo UTC thì từ 0 giờ tới
// 7 giờ sáng, tài xế có bảo hiểm hết hạn hôm qua vẫn được coi là còn hạn.
func TestExpiryUsesVietnamDate(t *testing.T) {
	d := Documents{InsuranceUntil: "2026-10-27"}
	if !d.Expired(lateNightUTC) {
		t.Fatal("1 giờ sáng ngày 28 giờ VN thì bảo hiểm hạn ngày 27 đã hết hạn")
	}
	// Ngày ghi trên giấy vẫn còn hạn trọn ngày.
	if d.Expired(time.Date(2026, 10, 27, 16, 59, 0, 0, time.UTC)) { // 23:59 ngày 27 giờ VN
		t.Fatal("trong ngày ghi trên giấy thì vẫn còn hạn")
	}
}

func TestAlertLevelsAndOrder(t *testing.T) {
	now := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC) // 10:00 ngày 1/10 giờ VN
	for _, c := range []struct {
		until string
		days  int
		level string // rỗng = không cảnh báo
	}{
		{"2026-09-30", -1, AlertExpired},
		{"2026-10-01", 0, AlertUrgent},
		{"2026-10-08", 7, AlertUrgent},
		{"2026-10-09", 8, AlertSoon},
		{"2026-10-16", 15, AlertSoon},
		{"2026-10-31", 30, AlertNotice},
		{"2026-11-01", 31, ""},
	} {
		as := Documents{InsuranceUntil: c.until}.Alerts(now)
		if c.level == "" {
			if len(as) != 0 {
				t.Errorf("%s: còn %d ngày thì chưa cảnh báo, được %+v", c.until, c.days, as)
			}
			continue
		}
		if len(as) != 1 || as[0].Level != c.level || as[0].DaysLeft != c.days {
			t.Errorf("%s: muốn %s/%d ngày, được %+v", c.until, c.level, c.days, as)
		}
	}

	// Gấp nhất đứng đầu — đó là thứ app và bảng điều khiển hiện ra trước.
	as := Documents{InsuranceUntil: "2026-10-20", LicenseUntil: "2026-09-29", InspectionUntil: "2026-10-05"}.Alerts(now)
	if len(as) != 3 || as[0].Document != DocLicense || as[1].Document != DocInspection || as[2].Document != DocInsurance {
		t.Fatalf("phải xếp theo số ngày còn lại tăng dần: %+v", as)
	}
}

// Hạn trống = không có hạn (GPLX A1, xe máy không đăng kiểm) — không được chặn.
func TestEmptyExpiryNeverBlocks(t *testing.T) {
	if (Documents{}).Expired(lateNightUTC) || len(Documents{}.Alerts(lateNightUTC)) != 0 {
		t.Fatal("giấy tờ không khai hạn không được bị coi là hết hạn")
	}
}

func TestExpiredDocumentsBlockTrips(t *testing.T) {
	d := &Driver{KYC: KYCApproved, Status: StatusIdle, Documents: Documents{LicenseUntil: "2026-10-27"}}
	if got := errs.CodeOf(d.CanAcceptTrip(DefaultDebtLimit, lateNightUTC)); got != "documents_expired" {
		t.Fatalf("GPLX hết hạn phải bị chặn với documents_expired, được %q", got)
	}
	d.Status = StatusOffline
	if got := errs.CodeOf(d.CanAcceptTrip(DefaultDebtLimit, lateNightUTC)); got != "documents_expired" {
		t.Fatalf("tài xế OFFLINE có giấy hết hạn phải thấy lý do THẬT documents_expired, được %s", got)
	}
}
