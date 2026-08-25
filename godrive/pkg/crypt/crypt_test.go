package crypt

import (
	"strings"
	"testing"
)

func newCipher(t *testing.T) *Cipher {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := newCipher(t)
	for _, s := range []string{"079090001234", "B2-123456789", "Nguyễn Văn Tài", ""} {
		enc, err := c.Encrypt(s)
		if err != nil {
			t.Fatal(err)
		}
		if s != "" && !strings.HasPrefix(enc, prefix) {
			t.Fatalf("thiếu tiền tố đánh dấu: %q", enc)
		}
		if s != "" && strings.Contains(enc, s) {
			t.Fatalf("bản mã KHÔNG được chứa bản rõ: %q", enc)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatal(err)
		}
		if got != s {
			t.Fatalf("giải mã ra %q, muốn %q", got, s)
		}
	}
}

// Hai lần mã hoá cùng một giá trị phải cho hai bản mã KHÁC nhau.
//
// Nếu giống nhau, người đọc được CSDL vẫn biết hai tài xế dùng chung một giấy tờ
// mà không cần giải mã gì cả.
func TestSameInputGivesDifferentCiphertext(t *testing.T) {
	c := newCipher(t)
	a, _ := c.Encrypt("079090001234")
	b, _ := c.Encrypt("079090001234")
	if a == b {
		t.Fatal("nonce phải ngẫu nhiên mỗi lần mã hoá")
	}
	// Nhưng giải ra vẫn phải bằng nhau.
	pa, _ := c.Decrypt(a)
	pb, _ := c.Decrypt(b)
	if pa != pb || pa != "079090001234" {
		t.Fatalf("giải mã sai: %q / %q", pa, pb)
	}
}

// GCM có xác thực: sửa một byte trong bản mã thì giải mã phải THẤT BẠI, không
// phải trả ra rác.
func TestTamperedCiphertextFails(t *testing.T) {
	c := newCipher(t)
	enc, _ := c.Encrypt("079090001234")
	b := []byte(enc)
	b[len(b)-2] ^= 0x01
	if _, err := c.Decrypt(string(b)); err == nil {
		t.Fatal("bản mã bị sửa phải làm giải mã thất bại")
	}
}

// Sai khoá phải thất bại, không được trả ra dữ liệu sai.
func TestWrongKeyFails(t *testing.T) {
	a, b := newCipher(t), newCipher(t)
	enc, _ := a.Encrypt("079090001234")
	if _, err := b.Decrypt(enc); err == nil {
		t.Fatal("khoá khác phải không giải mã được")
	}
}

// Dữ liệu cũ chưa mã hoá vẫn phải đọc được trong lúc chuyển đổi dần.
func TestPlaintextPassesThrough(t *testing.T) {
	c := newCipher(t)
	got, err := c.Decrypt("079090001234")
	if err != nil || got != "079090001234" {
		t.Fatalf("dữ liệu chưa mã hoá phải trả nguyên trạng: %q %v", got, err)
	}
	// Và mã hoá lại không được mã hoá chồng.
	enc, _ := c.Encrypt("079090001234")
	twice, _ := c.Encrypt(enc)
	if twice != enc {
		t.Fatal("không được mã hoá chồng lên bản đã mã hoá")
	}
}

// Chỉ mục mù phải TẤT ĐỊNH (để tra cứu được) nhưng không lộ bản gốc.
func TestBlindIndex(t *testing.T) {
	c := newCipher(t)
	a := c.BlindIndex("079090001234")
	b := c.BlindIndex("079090001234")
	if a != b {
		t.Fatal("chỉ mục mù phải tất định")
	}
	if a == c.BlindIndex("079090001235") {
		t.Fatal("giá trị khác phải cho chỉ mục khác")
	}
	if strings.Contains(a, "079090001234") {
		t.Fatal("chỉ mục không được chứa bản gốc")
	}
	if c.BlindIndex("") != "" {
		t.Fatal("chuỗi rỗng cho chỉ mục rỗng")
	}
	// Khoá khác cho chỉ mục khác: lộ CSDL mà không lộ khoá thì vẫn không dò được.
	if a == newCipher(t).BlindIndex("079090001234") {
		t.Fatal("khoá khác phải cho chỉ mục khác")
	}
}

func TestKeyValidation(t *testing.T) {
	for _, k := range []string{"", "ngan-qua", "00112233"} {
		if _, err := New(k); err == nil {
			t.Fatalf("khoá %q phải bị từ chối", k)
		}
	}
}
