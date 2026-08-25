// Package payment nhận tiền vào qua các cổng thanh toán Việt Nam.
//
// Nguyên tắc bất di bất dịch của module này:
//
//  1. XÁC THỰC CHỮ KÝ TRƯỚC MỌI THỨ. Webhook là endpoint công khai — cổng thanh
//     toán không đăng nhập được. Chữ ký là thứ DUY NHẤT phân biệt thông báo thật
//     với một request bất kỳ ai cũng gửi được.
//  2. ĐỐI CHIẾU SỐ TIỀN với ý định đã ghi trước. Chữ ký chống giả mạo nhưng
//     không chống được việc số tiền trong thông báo khác số mình yêu cầu.
//  3. IDEMPOTENT. Cổng gửi lại webhook khi không nhận được phản hồi 200 — đó là
//     hành vi bình thường, không phải sự cố.
package payment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/example/godrive/pkg/money"
)

type ProviderName string

const (
	MoMo    ProviderName = "MOMO"
	ZaloPay ProviderName = "ZALOPAY"
	VNPay   ProviderName = "VNPAY"
	VietQR  ProviderName = "VIETQR"
)

type Status string

const (
	StatusPending Status = "PENDING"
	StatusSuccess Status = "SUCCESS"
	StatusFailed  Status = "FAILED"
	StatusExpired Status = "EXPIRED"
)

type Purpose string

const (
	// PurposeTopUp: tài xế nạp tiền để trả công nợ chiết khấu.
	PurposeTopUp Purpose = "TOPUP"
	// PurposeTrip: khách trả cước chuyến qua cổng.
	PurposeTrip Purpose = "TRIP"
)

// IntentTTL là hạn của một ý định thanh toán. Quá hạn mà chưa có webhook thì
// coi như khách bỏ dở.
const IntentTTL = 30 * time.Minute

// Transaction là một giao dịch qua cổng, từ lúc tạo ý định tới lúc có kết quả.
type Transaction struct {
	ID           string          `json:"id"`
	Provider     ProviderName    `json:"provider"`
	OrderID      string          `json:"order_id"`
	ProviderTxID string          `json:"provider_tx_id,omitempty"`
	AccountID    string          `json:"account_id"`
	Purpose      Purpose         `json:"purpose"`
	Amount       money.VND       `json:"amount"`
	Status       Status          `json:"status"`
	RawCallback  json.RawMessage `json:"-"`
	CreatedAt    time.Time       `json:"created_at"`
	PaidAt       *time.Time      `json:"paid_at,omitempty"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

// Notification là kết quả đã XÁC THỰC từ webhook của cổng.
//
// Chỉ được dựng sau khi chữ ký hợp lệ. Không bao giờ dựng từ dữ liệu thô.
type Notification struct {
	OrderID      string
	ProviderTxID string
	Amount       money.VND
	Success      bool
	// Message là mô tả của cổng khi thất bại, để ghi vào nhật ký đối soát.
	Message string
	Raw     json.RawMessage
}

// Provider là một cổng thanh toán.
//
// Interface cố tình HẸP: module này chỉ cần biết cách xác thực một thông báo và
// cách trả lời cho cổng. Việc tạo link thanh toán, mã QR, hoàn tiền... thuộc về
// từng cổng và sẽ mở rộng sau, không nhét hết vào đây từ đầu.
type Provider interface {
	Name() ProviderName
	// VerifyWebhook xác thực chữ ký rồi mới trích xuất dữ liệu.
	//
	// Trả lỗi nghĩa là KHÔNG được xử lý — không log, không lưu, không ghi sổ.
	VerifyWebhook(body []byte, header map[string]string) (Notification, error)
	// AckBody là nội dung cổng mong đợi nhận lại để thôi gửi lại.
	AckBody(n Notification) []byte
}

// Repository lưu giao dịch cổng.
type Repository interface {
	Create(ctx context.Context, t *Transaction) error
	GetByOrderID(ctx context.Context, p ProviderName, orderID string) (*Transaction, error)
	// MarkResult ghi kết quả MỘT lần. Trả Conflict nếu giao dịch đã có kết quả —
	// đây là chốt chặn chống ghi sổ hai lần khi cổng gửi lại webhook.
	MarkResult(ctx context.Context, id string, n Notification, st Status, at time.Time) error
	ExpireStale(ctx context.Context, now time.Time) (int, error)
}
