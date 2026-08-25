// Package crypt mã hoá dữ liệu cá nhân nhạy cảm ở TẦNG ỨNG DỤNG.
//
// Vì sao mã hoá ở tầng ứng dụng chứ không chỉ bật mã hoá đĩa: mã hoá đĩa bảo vệ
// khi ai đó lấy được ổ cứng, nhưng KHÔNG bảo vệ khi ai đó đọc được cơ sở dữ
// liệu — bản sao lưu bị lộ, một câu SELECT của người có quyền quá rộng, hay một
// lỗ hổng SQL injection. Số CCCD và GPLX của tài xế thuộc loại dữ liệu mà Nghị
// định 13/2023 yêu cầu bảo vệ, và một lần lộ là không thu hồi được.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"strings"

	"github.com/example/godrive/pkg/errs"
)

// prefix đánh dấu chuỗi đã mã hoá.
//
// Có nó thì nhận ra ngay dữ liệu nào đã mã hoá, dữ liệu nào chưa — cần thiết khi
// chuyển đổi dần một bảng đang có dữ liệu cũ chưa mã hoá.
const prefix = "enc:v1:"

// KeySize là độ dài khoá AES-256.
const KeySize = 32

// Cipher mã hoá và giải mã bằng AES-256-GCM.
//
// GCM chứ không phải CBC: GCM có XÁC THỰC. Với CBC, kẻ tấn công sửa được bản mã
// để bản rõ đổi theo cách có kiểm soát mà không bị phát hiện. Với GCM, mọi sửa
// đổi đều làm giải mã thất bại.
type Cipher struct {
	aead cipher.AEAD
	// blindKey dùng cho chỉ mục mù — tách khỏi khoá mã hoá để lộ cái này không
	// làm lộ cái kia.
	blindKey []byte
}

// New dựng Cipher từ khoá 32 byte, nhận dạng hex hoặc base64.
func New(key string) (*Cipher, error) {
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	if len(raw) != KeySize {
		return nil, errs.Invalid("crypt_key_size",
			"Khoá mã hoá phải đúng 32 byte (AES-256).")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "crypt_init_failed", "crypt", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "crypt_init_failed", "crypt", err)
	}
	// Khoá chỉ mục mù suy ra từ khoá chính bằng HKDF rút gọn: một khoá cấu hình,
	// hai mục đích tách biệt.
	bk := hmac.New(sha256.New, raw)
	bk.Write([]byte("godrive-blind-index-v1"))
	return &Cipher{aead: aead, blindKey: bk.Sum(nil)}, nil
}

func decodeKey(key string) ([]byte, error) {
	if b, err := hex.DecodeString(key); err == nil && len(b) == KeySize {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == KeySize {
		return b, nil
	}
	return nil, errs.Invalid("crypt_key_invalid",
		"Khoá mã hoá phải là 32 byte dạng hex hoặc base64.")
}

// Encrypt mã hoá chuỗi. Chuỗi rỗng giữ nguyên rỗng.
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if IsEncrypted(plain) {
		return plain, nil // đã mã hoá rồi, không mã hoá chồng
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errs.Wrap(errs.KindInternal, "crypt_nonce_failed", "crypt", err)
	}
	// Nonce NGẪU NHIÊN mỗi lần: hai bản mã của cùng một số CCCD phải khác nhau,
	// nếu không kẻ đọc được CSDL vẫn biết hai tài xế dùng chung một giấy tờ.
	ct := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt giải mã. Chuỗi chưa mã hoá được trả về nguyên trạng để dữ liệu cũ
// vẫn đọc được trong lúc chuyển đổi dần.
func (c *Cipher) Decrypt(s string) (string, error) {
	if s == "" || !IsEncrypted(s) {
		return s, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil {
		return "", errs.Wrap(errs.KindInternal, "crypt_decode_failed", "crypt", err)
	}
	n := c.aead.NonceSize()
	if len(raw) < n {
		return "", errs.E(errs.KindInternal, "crypt_ciphertext_short", "Bản mã không hợp lệ.")
	}
	plain, err := c.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		// Giải mã thất bại nghĩa là SAI KHOÁ hoặc dữ liệu đã bị sửa. Cả hai đều
		// nghiêm trọng và không được nuốt lặng.
		return "", errs.Wrap(errs.KindInternal, "crypt_decrypt_failed",
			"Không giải mã được dữ liệu — sai khoá hoặc dữ liệu đã bị sửa.", err)
	}
	return string(plain), nil
}

// BlindIndex tạo chỉ mục mù để TRA CỨU bằng giá trị nhạy cảm mà không lưu nó.
//
// Bản mã GCM có nonce ngẫu nhiên nên không so khớp được; nhưng vẫn cần trả lời
// câu hỏi "số CCCD này đã đăng ký chưa". HMAC tất định giải quyết: cùng đầu vào
// cho cùng chỉ mục, mà từ chỉ mục không suy ngược ra được số gốc.
//
// Lưu ý: số CCCD có không gian nhỏ nên chỉ mục mù KHÔNG chống được tấn công dò
// toàn bộ nếu kẻ tấn công có cả CSDL lẫn khoá mù. Nó bảo vệ trước người chỉ đọc
// được CSDL — đó là mối đe doạ thực tế nhất.
func (c *Cipher) BlindIndex(plain string) string {
	if plain == "" {
		return ""
	}
	m := hmac.New(sha256.New, c.blindKey)
	m.Write([]byte(strings.TrimSpace(plain)))
	return hex.EncodeToString(m.Sum(nil))
}

// IsEncrypted cho biết chuỗi đã ở dạng mã hoá chưa.
func IsEncrypted(s string) bool { return strings.HasPrefix(s, prefix) }

// GenerateKey sinh khoá mới dạng hex, dùng cho lệnh khởi tạo môi trường.
func GenerateKey() (string, error) {
	b := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
