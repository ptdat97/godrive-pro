// Package settings quản lý cấu hình nghiệp vụ chỉnh được lúc chạy.
//
// Ranh giới của package này rất rõ: nó KHÔNG biết gì về nghiệp vụ, chỉ biết
// lưu, kiểm tra và phát tán các nhóm cấu hình. Việc dịch một nhóm cấu hình
// thành kiểu dữ liệu của module nghiệp vụ nằm ở tầng lắp ráp (internal/app).
//
// Ba nguyên tắc:
//
//  1. MỌI GIÁ TRỊ ĐỀU CÓ CHẶN TRÊN CHẶN DƯỚI. Một giao diện cấu hình không có
//     ràng buộc là cách phá sập doanh nghiệp bằng một lần gõ nhầm: chiết khấu
//     90%, trần surge ×10, hạn mức nợ 0 đồng.
//  2. MỌI THAY ĐỔI ĐỀU CÓ DẤU VẾT, kèm giá trị TRƯỚC và SAU. Khi khách khiếu
//     nại giá của một chuyến ba tháng trước, phải trả lời được biểu giá lúc đó.
//  3. BÁO GIÁ ĐÃ PHÁT KHÔNG ĐỔI. Quote lưu sẵn tổng tiền, và Trip.Create đọc
//     quote chứ không tính lại — nên đổi biểu giá không hồi tố lên chuyến đang đặt.
package settings

import (
	"context"
	"encoding/json"
	"time"
)

// Key là tên một nhóm cấu hình.
type Key string

const (
	KeyPricing  Key = "pricing"
	KeySurge    Key = "surge"
	KeyMatching Key = "matching"
	KeyWallet   Key = "wallet"
	KeyLocation Key = "location"
)

// AllKeys theo thứ tự hiển thị trên giao diện.
var AllKeys = []Key{KeyPricing, KeySurge, KeyMatching, KeyWallet, KeyLocation}

func (k Key) Valid() bool {
	for _, x := range AllKeys {
		if x == k {
			return true
		}
	}
	return false
}

// Record là một nhóm cấu hình đã lưu.
type Record struct {
	Key       Key             `json:"key"`
	Value     json.RawMessage `json:"value"`
	Version   int             `json:"version"`
	UpdatedBy string          `json:"updated_by,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// HistoryEntry là một lần thay đổi.
type HistoryEntry struct {
	ID        string          `json:"id"`
	Key       Key             `json:"key"`
	Version   int             `json:"version"`
	OldValue  json.RawMessage `json:"old_value,omitempty"`
	NewValue  json.RawMessage `json:"new_value"`
	ChangedBy string          `json:"changed_by"`
	Reason    string          `json:"reason,omitempty"`
	At        time.Time       `json:"at"`
}

// Store lưu cấu hình.
type Store interface {
	GetAll(ctx context.Context) (map[Key]Record, error)
	// Put ghi đè nhóm cấu hình với khoá lạc quan theo expectVersion.
	//
	// expectVersion = 0 nghĩa là "chưa từng lưu, đang tạo lần đầu". Trả Conflict
	// nếu phiên bản không khớp — hai quản trị viên cùng sửa thì người sau phải
	// đọc lại thay vì ghi đè âm thầm lên thay đổi của người trước.
	Put(ctx context.Context, k Key, value json.RawMessage, expectVersion int,
		by, reason string, now time.Time, newID func(string) string) (Record, error)
	History(ctx context.Context, k Key, limit int) ([]HistoryEntry, error)
}

// Validator là thứ mọi nhóm cấu hình phải cài đặt.
type Validator interface {
	// Validate trả lỗi Invalid nếu giá trị nằm ngoài ngưỡng an toàn.
	Validate() error
}
