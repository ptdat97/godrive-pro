package payment

import (
	"crypto/sha256"
	"encoding/json"
	"strconv"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// ZaloPayProvider xác thực callback của ZaloPay.
//
// ZaloPay bọc dữ liệu thật trong một chuỗi JSON ở trường `data`, và ký HMAC-SHA256
// trên CHÍNH CHUỖI ĐÓ bằng key2. Điểm dễ sai: phải ký trên chuỗi thô nhận được,
// không phải trên kết quả sau khi giải mã rồi mã hoá lại — giải rồi mã lại có
// thể đổi thứ tự trường hoặc khoảng trắng, và chữ ký sẽ không khớp.
type ZaloPayProvider struct {
	AppID int
	Key2  string
}

func NewZaloPay(appID int, key2 string) *ZaloPayProvider {
	return &ZaloPayProvider{AppID: appID, Key2: key2}
}

func (z *ZaloPayProvider) Name() ProviderName { return ZaloPay }

type zaloCallback struct {
	Data string `json:"data"`
	MAC  string `json:"mac"`
	Type int    `json:"type"`
}

type zaloData struct {
	AppID          int    `json:"app_id"`
	AppTransID     string `json:"app_trans_id"`
	AppTime        int64  `json:"app_time"`
	Amount         int64  `json:"amount"`
	ZpTransID      int64  `json:"zp_trans_id"`
	ServerTime     int64  `json:"server_time"`
	EmbedData      string `json:"embed_data"`
	DiscountAmount int64  `json:"discount_amount"`
}

func (z *ZaloPayProvider) VerifyWebhook(body []byte, _ map[string]string) (Notification, error) {
	var cb zaloCallback
	if err := json.Unmarshal(body, &cb); err != nil {
		return Notification{}, errs.Invalid("webhook_malformed", "Nội dung webhook không hợp lệ.")
	}
	// Ký trên CHUỖI THÔ trong trường data, không phải trên struct đã giải mã.
	if !validHMAC(sha256.New, z.Key2, cb.Data, cb.MAC) {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_bad_signature",
			"Chữ ký webhook không hợp lệ.")
	}

	var d zaloData
	if err := json.Unmarshal([]byte(cb.Data), &d); err != nil {
		return Notification{}, errs.Invalid("webhook_malformed", "Trường data không hợp lệ.")
	}
	if d.AppID != z.AppID {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_wrong_partner",
			"Thông báo không thuộc ứng dụng này.")
	}
	return Notification{
		OrderID:      d.AppTransID,
		ProviderTxID: strconv.FormatInt(d.ZpTransID, 10),
		Amount:       money.VND(d.Amount),
		// ZaloPay chỉ gọi callback khi giao dịch THÀNH CÔNG (type = 1).
		Success: cb.Type == 1,
		Raw:     body,
	}, nil
}

// AckBody: ZaloPay đọc JSON {return_code, return_message}. return_code = 1 nghĩa
// là đã nhận, thôi gửi lại.
func (z *ZaloPayProvider) AckBody(Notification) []byte {
	return []byte(`{"return_code":1,"return_message":"success"}`)
}
