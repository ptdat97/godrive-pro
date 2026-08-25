package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	gohash "hash"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// MoMoProvider xác thực webhook IPN của MoMo.
//
// MoMo ký bằng HMAC-SHA256 trên một chuỗi có THỨ TỰ TRƯỜNG CỐ ĐỊNH (không phải
// thứ tự bảng chữ cái, không phải thứ tự trong JSON). Sai một trường hoặc sai
// thứ tự là chữ ký không khớp — và đó là điều tốt: nó có nghĩa là mọi thay đổi
// dù nhỏ trong dữ liệu đều làm hỏng chữ ký.
type MoMoProvider struct {
	PartnerCode string
	AccessKey   string
	SecretKey   string
}

func NewMoMo(partnerCode, accessKey, secretKey string) *MoMoProvider {
	return &MoMoProvider{PartnerCode: partnerCode, AccessKey: accessKey, SecretKey: secretKey}
}

func (m *MoMoProvider) Name() ProviderName { return MoMo }

type momoIPN struct {
	PartnerCode  string `json:"partnerCode"`
	OrderID      string `json:"orderId"`
	RequestID    string `json:"requestId"`
	Amount       int64  `json:"amount"`
	OrderInfo    string `json:"orderInfo"`
	OrderType    string `json:"orderType"`
	TransID      int64  `json:"transId"`
	ResultCode   int    `json:"resultCode"`
	Message      string `json:"message"`
	PayType      string `json:"payType"`
	ResponseTime int64  `json:"responseTime"`
	ExtraData    string `json:"extraData"`
	Signature    string `json:"signature"`
}

func (m *MoMoProvider) VerifyWebhook(body []byte, _ map[string]string) (Notification, error) {
	var p momoIPN
	if err := json.Unmarshal(body, &p); err != nil {
		return Notification{}, errs.Invalid("webhook_malformed", "Nội dung webhook không hợp lệ.")
	}
	// Kiểm partnerCode trước: thông báo của đối tác khác không phải của mình.
	if p.PartnerCode != m.PartnerCode {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_wrong_partner",
			"Thông báo không thuộc đối tác này.")
	}

	// Thứ tự trường ở đây do MoMo quy định, KHÔNG được sắp lại.
	raw := fmt.Sprintf(
		"accessKey=%s&amount=%d&extraData=%s&message=%s&orderId=%s&orderInfo=%s"+
			"&orderType=%s&partnerCode=%s&payType=%s&requestId=%s&responseTime=%d"+
			"&resultCode=%d&transId=%d",
		m.AccessKey, p.Amount, p.ExtraData, p.Message, p.OrderID, p.OrderInfo,
		p.OrderType, p.PartnerCode, p.PayType, p.RequestID, p.ResponseTime,
		p.ResultCode, p.TransID)

	if !validHMAC(sha256.New, m.SecretKey, raw, p.Signature) {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_bad_signature",
			"Chữ ký webhook không hợp lệ.")
	}
	return Notification{
		OrderID:      p.OrderID,
		ProviderTxID: fmt.Sprint(p.TransID),
		Amount:       money.VND(p.Amount),
		// MoMo: resultCode 0 là thành công. Mọi mã khác đều KHÔNG phải thành công.
		Success: p.ResultCode == 0,
		Message: p.Message,
		Raw:     body,
	}, nil
}

// AckBody: MoMo chỉ cần HTTP 204, không đọc nội dung.
func (m *MoMoProvider) AckBody(Notification) []byte { return nil }

// validHMAC so sánh chữ ký bằng hàm HẰNG THỜI GIAN.
//
// So sánh chuỗi thông thường thoát sớm ở byte đầu khác nhau, để lộ thông tin
// cho tấn công đo thời gian: kẻ tấn công dò được từng byte một của chữ ký đúng.
func validHMAC(h func() gohash.Hash, secret, raw, want string) bool {
	// Giải mã hex chấp nhận cả chữ hoa lẫn chữ thường — các cổng không thống
	// nhất về việc này.
	wantBytes, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	mac := hmac.New(h, []byte(secret))
	mac.Write([]byte(raw))
	return hmac.Equal(mac.Sum(nil), wantBytes)
}
