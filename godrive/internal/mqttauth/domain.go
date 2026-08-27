// Package mqttauth trả lời câu hỏi "thiết bị này là ai, và được đụng vào topic
// nào" cho broker MQTT.
//
// Vì sao broker phải hỏi ngược lại backend thay vì tự giữ danh sách người dùng:
// danh tính tài xế đã sống ở Postgres, phiên đã có cơ chế thu hồi ở Redis, và
// trạng thái khoá tài khoản đổi liên tục. Nhân bản những thứ đó sang broker là
// tạo ra bản sao thứ hai chắc chắn sẽ lệch — tài xế bị khoá lúc 9 giờ mà 10 giờ
// vẫn đẩy được vị trí.
//
// Cái giá phải trả: EMQX không nối được nếu backend chết. Nhưng backend chết thì
// cũng không còn ai tiêu thụ ping, nên đây không phải mất mát thật.
package mqttauth

import (
	"context"
	"time"

	"github.com/example/godrive/internal/platform/authn"
)

// TokenParser kiểm token phiên. Port khai báo ở đây (bên tiêu thụ).
type TokenParser interface {
	Parse(token string, now time.Time) (*authn.Claims, error)
}

// Revoker cho biết một phiên đã bị thu hồi chưa.
//
// Bắt buộc phải hỏi: authn.Issuer.Parse chỉ kiểm chữ ký và hạn dùng, còn việc
// thu hồi nằm ở middleware HTTP — mà kết nối MQTT không đi qua middleware nào.
// Bỏ qua bước này thì đăng xuất và khoá tài khoản không cắt được luồng vị trí,
// và token lộ ra ngoài vẫn dùng được tới lúc hết hạn.
type Revoker interface {
	IsRevoked(ctx context.Context, c *authn.Claims) (bool, error)
}

// DriverLookup tra hồ sơ tài xế từ tài khoản đăng nhập.
type DriverLookup interface {
	GetByAccount(ctx context.Context, accountID string) (*DriverRef, error)
}

// DriverRef là phần thông tin tài xế mà việc phân quyền cần tới — không hơn.
type DriverRef struct {
	ID        string
	Suspended bool
}

// Decision là câu trả lời gửi lại cho broker.
type Decision struct {
	// Allow=false nghĩa là từ chối kết nối.
	Allow bool
	// Superuser bỏ qua mọi kiểm tra topic. Chỉ dành cho chính backend.
	Superuser bool
	// Rules là danh sách quyền theo topic, rỗng nếu Superuser.
	Rules []Rule
	// Deny giải thích vì sao từ chối — chỉ để ghi log phía máy chủ, KHÔNG gửi
	// cho thiết bị: nói rõ "sai mật khẩu" hay "tài khoản bị khoá" là chỉ đường
	// cho người đang dò.
	Deny string
}

// Rule là một dòng quyền: cho phép hay cấm, hành động nào, trên topic nào.
type Rule struct {
	Allow  bool
	Action Action
	Topic  string
}

type Action string

const (
	ActionPublish   Action = "publish"
	ActionSubscribe Action = "subscribe"
	ActionAll       Action = "all"
)

// Topic của một tài xế. Chiều lên là thiết bị báo về, chiều xuống là máy chủ
// đẩy tới thiết bị.
const (
	TopicDriverLoc    = "drv/%s/loc"    // lên: ping vị trí
	TopicDriverStatus = "drv/%s/status" // lên: Last Will
	TopicDriverOffer  = "drv/%s/offer"  // xuống: lời mời chuyến
	TopicDriverTrip   = "drv/%s/trip"   // xuống: chuyển trạng thái chuyến
)
