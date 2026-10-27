// Package notification gửi thông báo tới người dùng.
//
// Chiến lược kênh ở VN:
//   - Push realtime: FCM (Android chiếm ưu thế), APNs cho iOS.
//   - OTP: Zalo ZNS trước (chi phí ~1/3 SMS, tỉ lệ đọc cao), fallback SMS
//     brandname Viettel/VNPT khi người dùng không có Zalo.
//   - Thông báo giao dịch: ZNS template đã duyệt.
package notification

import (
	"context"
	"log/slog"
	"time"
)

type Message struct {
	To    string // account id hoặc device token
	Title string
	Body  string
	Data  map[string]string
	// TTL > 0: quá hạn mà máy chưa nhận thì FCM bỏ luôn. Lời mời sống 15
	// giây — tới tay tài xế sau 2 phút là bắt họ bấm vào thứ đã chết.
	TTL time.Duration
	// Urgent: đánh thức máy ngay (Android priority HIGH, APNs priority 10).
	// Chỉ dùng cho tin cần hành động tức thì; lạm dụng thì hệ điều hành hạ
	// mức ưu tiên của cả app.
	Urgent bool
}

type Pusher interface {
	Push(ctx context.Context, m Message) error
}

type SMSSender interface {
	SendSMS(ctx context.Context, phone, text string) error
}

// LogPusher dùng cho dev: in ra log thay vì gọi FCM.
type LogPusher struct{ Log *slog.Logger }

func (l LogPusher) Push(_ context.Context, m Message) error {
	l.Log.Info("push", "to", m.To, "title", m.Title, "body", m.Body, "data", m.Data)
	return nil
}

// LogOTPSender dùng cho dev/staging.
type LogOTPSender struct{ Log *slog.Logger }

func (l LogOTPSender) Send(_ context.Context, phone, code string) error {
	l.Log.Info("otp", "phone", phone, "code", code)
	return nil
}
