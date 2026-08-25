package payment

import (
	"crypto/sha512"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// VNPayProvider xác thực IPN của VNPay.
//
// Khác hai cổng kia: VNPay gửi tham số trên QUERY STRING chứ không phải JSON, và
// ký HMAC-SHA512 trên toàn bộ tham số ĐÃ SẮP THEO TÊN, bỏ chính hai trường chữ ký.
//
// Ba chỗ dễ sai:
//   - phải sắp theo tên tham số, không theo thứ tự nhận được;
//   - phải loại `vnp_SecureHash` và `vnp_SecureHashType` khỏi chuỗi ký;
//   - giá trị phải giữ nguyên dạng ĐÃ MÃ HOÁ URL như lúc nhận.
type VNPayProvider struct {
	TmnCode    string
	HashSecret string
}

func NewVNPay(tmnCode, hashSecret string) *VNPayProvider {
	return &VNPayProvider{TmnCode: tmnCode, HashSecret: hashSecret}
}

func (v *VNPayProvider) Name() ProviderName { return VNPay }

// VerifyWebhook nhận query string thô trong body.
func (v *VNPayProvider) VerifyWebhook(body []byte, header map[string]string) (Notification, error) {
	raw := string(body)
	if q, ok := header["X-Query-String"]; ok && q != "" {
		raw = q
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return Notification{}, errs.Invalid("webhook_malformed", "Tham số webhook không hợp lệ.")
	}

	got := values.Get("vnp_SecureHash")
	if got == "" {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_bad_signature",
			"Thiếu chữ ký.")
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "vnp_SecureHash" || k == "vnp_SecureHashType" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		// Giữ nguyên dạng đã mã hoá URL: VNPay ký trên chuỗi đã mã hoá.
		sb.WriteString(url.QueryEscape(values.Get(k)))
	}

	// validHMAC giải mã hex nên chấp nhận cả chữ hoa lẫn chữ thường, và so sánh
	// bằng hàm hằng thời gian.
	if !validHMAC(sha512.New, v.HashSecret, sb.String(), got) {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_bad_signature",
			"Chữ ký webhook không hợp lệ.")
	}
	if values.Get("vnp_TmnCode") != v.TmnCode {
		return Notification{}, errs.E(errs.KindForbidden, "webhook_wrong_partner",
			"Thông báo không thuộc website này.")
	}

	// VNPay tính tiền theo ĐƠN VỊ NHÂN 100 (đồng × 100).
	amount, _ := strconv.ParseInt(values.Get("vnp_Amount"), 10, 64)
	return Notification{
		OrderID:      values.Get("vnp_TxnRef"),
		ProviderTxID: values.Get("vnp_TransactionNo"),
		Amount:       money.VND(amount / 100),
		// "00" là thành công ở cả mã giao dịch lẫn mã phản hồi.
		Success: values.Get("vnp_ResponseCode") == "00" && values.Get("vnp_TransactionStatus") == "00",
		Message: values.Get("vnp_ResponseCode"),
		Raw:     body,
	}, nil
}

// AckBody: VNPay đọc JSON {RspCode, Message}. "00" nghĩa là đã nhận.
func (v *VNPayProvider) AckBody(Notification) []byte {
	return []byte(`{"RspCode":"00","Message":"Confirm Success"}`)
}
