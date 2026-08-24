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
)

type Message struct {
	To    string // account id hoặc device token
	Title string
	Body  string
	Data  map[string]string
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
