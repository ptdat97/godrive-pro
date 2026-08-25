package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// ---------------------------------------------------------------- MoMo

const (
	momoPartner = "MOMOTEST"
	momoAccess  = "F8BBA842ECF85"
	momoSecret  = "K951B6PE1waDMi640xX08PD3vg6EkVlz"
)

// signMoMo ký đúng như MoMo ký. Test phải tự dựng chữ ký thật, không được gọi
// lại hàm của code — nếu không thì một công thức sai vẫn "khớp với chính nó".
func signMoMo(p map[string]any) string {
	raw := fmt.Sprintf(
		"accessKey=%s&amount=%d&extraData=%s&message=%s&orderId=%s&orderInfo=%s"+
			"&orderType=%s&partnerCode=%s&payType=%s&requestId=%s&responseTime=%d"+
			"&resultCode=%d&transId=%d",
		momoAccess, p["amount"], p["extraData"], p["message"], p["orderId"], p["orderInfo"],
		p["orderType"], p["partnerCode"], p["payType"], p["requestId"], p["responseTime"],
		p["resultCode"], p["transId"])
	m := hmac.New(sha256.New, []byte(momoSecret))
	m.Write([]byte(raw))
	return hex.EncodeToString(m.Sum(nil))
}

func momoBody(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	p := map[string]any{
		"partnerCode": momoPartner, "orderId": "ord_1", "requestId": "req_1",
		"amount": int64(500000), "orderInfo": "Nap vi GoDrive", "orderType": "momo_wallet",
		"transId": int64(2882102155), "resultCode": 0, "message": "Successful.",
		"payType": "qr", "responseTime": int64(1758000000000), "extraData": "",
	}
	if mutate != nil {
		mutate(p)
	}
	p["signature"] = signMoMo(p)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMoMoAcceptsValidSignature(t *testing.T) {
	n, err := NewMoMo(momoPartner, momoAccess, momoSecret).VerifyWebhook(momoBody(t, nil), nil)
	if err != nil {
		t.Fatalf("chữ ký hợp lệ phải được chấp nhận: %v", err)
	}
	if n.OrderID != "ord_1" || n.Amount != 500000 || !n.Success || n.ProviderTxID != "2882102155" {
		t.Fatalf("trích xuất sai: %+v", n)
	}
}

// Đây là test quan trọng nhất của cả module: webhook là endpoint CÔNG KHAI,
// chữ ký là thứ duy nhất phân biệt thông báo thật với request bất kỳ ai cũng gửi được.
func TestMoMoRejectsTamperedFields(t *testing.T) {
	p := NewMoMo(momoPartner, momoAccess, momoSecret)

	cases := map[string]func([]byte) []byte{
		"đổi số tiền sau khi ký": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), `"amount":500000`, `"amount":50000000`, 1))
		},
		"đổi mã đơn sau khi ký": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), `"orderId":"ord_1"`, `"orderId":"ord_khac"`, 1))
		},
		"đổi kết quả thành công": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), `"resultCode":1`, `"resultCode":0`, 1))
		},
		"chữ ký rỗng": func(b []byte) []byte {
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			m["signature"] = ""
			o, _ := json.Marshal(m)
			return o
		},
		"chữ ký rác": func(b []byte) []byte {
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			m["signature"] = "khong-phai-hex"
			o, _ := json.Marshal(m)
			return o
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			base := momoBody(t, nil)
			if name == "đổi kết quả thành công" {
				base = momoBody(t, func(m map[string]any) { m["resultCode"] = 1 })
			}
			if _, err := p.VerifyWebhook(tamper(base), nil); err == nil {
				t.Fatal("phải TỪ CHỐI nội dung đã bị sửa sau khi ký")
			} else if errs.CodeOf(err) != "webhook_bad_signature" {
				t.Fatalf("mã lỗi phải là webhook_bad_signature, được %q", errs.CodeOf(err))
			}
		})
	}
}

func TestMoMoRejectsOtherPartner(t *testing.T) {
	body := momoBody(t, func(m map[string]any) { m["partnerCode"] = "DOITAC_KHAC" })
	_, err := NewMoMo(momoPartner, momoAccess, momoSecret).VerifyWebhook(body, nil)
	if errs.CodeOf(err) != "webhook_wrong_partner" {
		t.Fatalf("phải trả webhook_wrong_partner, được %q", errs.CodeOf(err))
	}
}

// resultCode khác 0 là THẤT BẠI. Nhầm chỗ này nghĩa là ghi có cho giao dịch hỏng.
func TestMoMoNonZeroResultIsFailure(t *testing.T) {
	body := momoBody(t, func(m map[string]any) {
		m["resultCode"] = 1006
		m["message"] = "Giao dịch bị từ chối"
	})
	n, err := NewMoMo(momoPartner, momoAccess, momoSecret).VerifyWebhook(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n.Success {
		t.Fatal("resultCode khác 0 KHÔNG được coi là thành công")
	}
}

// ---------------------------------------------------------------- ZaloPay

const zaloKey2 = "kLtgPl8HHhfvMuDHPwKfgfsY4Ydm9eIz"

func zaloBody(t *testing.T, appID int, amount int64, appTransID string, typ int) []byte {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"app_id": appID, "app_trans_id": appTransID, "app_time": 1758000000000,
		"amount": amount, "zp_trans_id": 220101000000001, "server_time": 1758000000001,
		"embed_data": "{}", "discount_amount": 0,
	})
	m := hmac.New(sha256.New, []byte(zaloKey2))
	m.Write(data)
	b, _ := json.Marshal(map[string]any{
		"data": string(data), "mac": hex.EncodeToString(m.Sum(nil)), "type": typ,
	})
	return b
}

func TestZaloPayVerifiesAndRejects(t *testing.T) {
	p := NewZaloPay(2553, zaloKey2)

	n, err := p.VerifyWebhook(zaloBody(t, 2553, 300000, "ord_z", 1), nil)
	if err != nil {
		t.Fatalf("chữ ký hợp lệ phải được chấp nhận: %v", err)
	}
	if n.OrderID != "ord_z" || n.Amount != 300000 || !n.Success {
		t.Fatalf("trích xuất sai: %+v", n)
	}

	// Sửa `data` sau khi ký -> MAC không khớp.
	bad := zaloBody(t, 2553, 300000, "ord_z", 1)
	bad = []byte(strings.Replace(string(bad), `\"amount\":300000`, `\"amount\":30000000`, 1))
	if _, err := p.VerifyWebhook(bad, nil); err == nil {
		t.Fatal("sửa data sau khi ký phải bị từ chối")
	}

	// Ứng dụng khác.
	if _, err := p.VerifyWebhook(zaloBody(t, 9999, 300000, "ord_z", 1), nil); errs.CodeOf(err) != "webhook_wrong_partner" {
		t.Fatalf("app_id khác phải bị từ chối, được %q", errs.CodeOf(err))
	}
}

// ---------------------------------------------------------------- VNPay

const (
	vnpTmn    = "GODRIVE1"
	vnpSecret = "SECRETKEYVNPAY123456789"
)

func vnpayQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k + "=" + url.QueryEscape(params[k]))
	}
	m := hmac.New(sha512.New, []byte(vnpSecret))
	m.Write([]byte(sb.String()))
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("vnp_SecureHash", hex.EncodeToString(m.Sum(nil)))
	return q.Encode()
}

func TestVNPayVerifiesAndRejects(t *testing.T) {
	p := NewVNPay(vnpTmn, vnpSecret)
	base := map[string]string{
		"vnp_TmnCode": vnpTmn, "vnp_TxnRef": "ord_v", "vnp_Amount": "70000000",
		"vnp_ResponseCode": "00", "vnp_TransactionStatus": "00",
		"vnp_TransactionNo": "14203373", "vnp_BankCode": "NCB",
		"vnp_OrderInfo": "Nap vi GoDrive",
	}
	n, err := p.VerifyWebhook([]byte(vnpayQuery(base)), nil)
	if err != nil {
		t.Fatalf("chữ ký hợp lệ phải được chấp nhận: %v", err)
	}
	// VNPay tính tiền × 100.
	if n.Amount != 700000 {
		t.Fatalf("phải chia 100: được %d, muốn 700000", n.Amount)
	}
	if n.OrderID != "ord_v" || !n.Success {
		t.Fatalf("trích xuất sai: %+v", n)
	}

	// Sửa số tiền sau khi ký.
	q := vnpayQuery(base)
	if _, err := p.VerifyWebhook([]byte(strings.Replace(q, "vnp_Amount=70000000", "vnp_Amount=7000000000", 1)), nil); err == nil {
		t.Fatal("sửa số tiền sau khi ký phải bị từ chối")
	}
	// Thiếu chữ ký.
	if _, err := p.VerifyWebhook([]byte("vnp_TxnRef=ord_v&vnp_Amount=70000000"), nil); err == nil {
		t.Fatal("thiếu chữ ký phải bị từ chối")
	}

	// Chữ ký CHỮ HOA vẫn phải chấp nhận: các cổng không thống nhất về việc này.
	//
	// Phải viết hoa ĐÚNG giá trị chữ ký, không phải cả phần đuôi query string —
	// url.Values.Encode() sắp theo tên nên vnp_SecureHash không nằm cuối.
	parsed, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Set("vnp_SecureHash", strings.ToUpper(parsed.Get("vnp_SecureHash")))
	if _, err := p.VerifyWebhook([]byte(parsed.Encode()), nil); err != nil {
		t.Fatalf("chữ ký viết hoa vẫn phải hợp lệ: %v", err)
	}

	// Giao dịch thất bại.
	fail := map[string]string{}
	for k, v := range base {
		fail[k] = v
	}
	fail["vnp_ResponseCode"] = "24"
	fail["vnp_TransactionStatus"] = "02"
	n, err = p.VerifyWebhook([]byte(vnpayQuery(fail)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n.Success {
		t.Fatal("mã phản hồi 24 KHÔNG được coi là thành công")
	}
}

func TestMoneyRoundTrip(t *testing.T) {
	if got := money.VND(70000000 / 100); got != 700000 {
		t.Fatalf("quy đổi đơn vị VNPay sai: %d", got)
	}
}
