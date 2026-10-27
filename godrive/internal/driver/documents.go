package driver

import (
	"time"

	"github.com/example/godrive/pkg/errs"
)

// Loại giấy tờ có thời hạn.
const (
	DocInsurance  = "insurance"  // bảo hiểm TNDS bắt buộc
	DocLicense    = "license"    // GPLX — hạng A1 không có hạn
	DocInspection = "inspection" // đăng kiểm — chỉ ô tô
)

// Mức cảnh báo, theo số ngày còn lại.
const (
	AlertExpired = "expired" // đã hết hạn: không nhận được chuyến
	AlertUrgent  = "urgent"  // còn ≤ 7 ngày
	AlertSoon    = "soon"    // còn ≤ 15 ngày
	AlertNotice  = "notice"  // còn ≤ 30 ngày
)

// AlertWindowDays là khoảng bắt đầu cảnh báo trước khi hết hạn.
const AlertWindowDays = 30

// vnZone là múi giờ của hạn giấy tờ. Ngày ghi trên giấy là ngày theo giờ Việt
// Nam: tính theo UTC thì từ 0 giờ tới 7 giờ sáng ngày hết hạn, tài xế vẫn bị
// coi là còn hạn thêm một ngày. Dùng múi cố định để không phụ thuộc tzdata
// của máy chủ — Việt Nam không đổi giờ theo mùa.
var vnZone = time.FixedZone("ICT", 7*3600)

// DocumentAlert là một giấy tờ sắp hoặc đã hết hạn.
type DocumentAlert struct {
	Document string `json:"document"`
	Until    string `json:"until"` // YYYY-MM-DD
	DaysLeft int    `json:"days_left"`
	Level    string `json:"level"`
}

// expiries liệt kê các hạn đã khai. Hạn trống bị bỏ qua: xem migration 0011.
func (d Documents) expiries() map[string]string {
	return map[string]string{
		DocInsurance: d.InsuranceUntil, DocLicense: d.LicenseUntil, DocInspection: d.InspectionUntil,
	}
}

// Alerts trả các giấy tờ hết hạn hoặc hết hạn trong vòng AlertWindowDays ngày,
// theo thứ tự gấp nhất trước.
//
// Tính lúc đọc chứ không lưu: cờ "đã hết hạn" lưu lại thì phải có job cập nhật
// đúng nửa đêm, và trong lúc job chưa chạy tài xế vẫn nhận chuyến bằng giấy
// tờ đã hết hạn.
func (d Documents) Alerts(now time.Time) []DocumentAlert {
	today := dateOf(now.In(vnZone))
	var out []DocumentAlert
	for _, kind := range []string{DocInsurance, DocLicense, DocInspection} {
		s := d.expiries()[kind]
		if s == "" {
			continue
		}
		until, err := time.ParseInLocation(DateLayout, s, vnZone)
		if err != nil {
			continue // Onboard và cập nhật đều đã kiểm định dạng
		}
		// Còn hạn HẾT ngày ghi trên giấy: hạn 2026-10-27 thì ngày 27 vẫn chạy.
		days := int(until.Sub(today).Hours() / 24)
		if days > AlertWindowDays {
			continue
		}
		out = append(out, DocumentAlert{Document: kind, Until: s, DaysLeft: days, Level: alertLevel(days)})
	}
	for i := 1; i < len(out); i++ { // tối đa 3 phần tử
		for j := i; j > 0 && out[j].DaysLeft < out[j-1].DaysLeft; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Expired cho biết có giấy tờ nào đã quá hạn không.
func (d Documents) Expired(now time.Time) bool {
	for _, a := range d.Alerts(now) {
		if a.Level == AlertExpired {
			return true
		}
	}
	return false
}

func alertLevel(days int) string {
	switch {
	case days < 0:
		return AlertExpired
	case days <= 7:
		return AlertUrgent
	case days <= 15:
		return AlertSoon
	default:
		return AlertNotice
	}
}

// TodayVN là ngày hiện tại theo giờ Việt Nam — mốc so hạn giấy tờ.
func TodayVN(now time.Time) time.Time { return dateOf(now.In(vnZone)) }

func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// errDocumentsExpired là mã chặn nhận chuyến khi giấy tờ quá hạn.
func errDocumentsExpired() error {
	return errs.E(errs.KindForbidden, "documents_expired",
		"Giấy tờ của bạn đã hết hạn. Vui lòng gia hạn và gửi lại cho tổng đài để tiếp tục nhận chuyến.")
}

// DocumentDates là các hạn giấy tờ vận hành cập nhật sau khi đã xem giấy thật.
// Trường nil = giữ nguyên; chuỗi rỗng = xoá hạn (giấy không có thời hạn).
type DocumentDates struct {
	InsuranceNo     *string `json:"insurance_no,omitempty"`
	InsuranceUntil  *string `json:"insurance_until,omitempty"`
	LicenseUntil    *string `json:"license_until,omitempty"`
	InspectionUntil *string `json:"inspection_until,omitempty"`
}

// validDate kiểm một hạn giấy tờ. Rỗng là hợp lệ.
func validDate(field, s string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse(DateLayout, s); err != nil {
		return errs.Invalid(field+"_invalid", "Ngày hết hạn phải theo dạng YYYY-MM-DD.")
	}
	return nil
}
