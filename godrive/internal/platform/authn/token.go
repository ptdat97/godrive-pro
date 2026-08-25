// Package authn phát hành và xác thực access token.
// Bản này ký HMAC-SHA256 thuần stdlib (định dạng JWT-compatible HS256).
// Khi cần rotate key / multi-issuer, thay bằng thư viện JWT chuẩn.
package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
)

type Role string

const (
	RoleRider  Role = "rider"
	RoleDriver Role = "driver"
	RoleAdmin  Role = "admin"
)

type Claims struct {
	Sub      string `json:"sub"` // account id
	Role     Role   `json:"role"`
	DeviceID string `json:"did"`
	// JTI là mã định danh duy nhất của token này.
	//
	// Không có nó thì không thu hồi được token nào cả: JWT tự chứng minh tính
	// hợp lệ bằng chữ ký, nên máy chủ không có cách nào "quên" một token đã
	// phát. Đăng xuất chỉ xoá token ở phía client, còn bản sao mà kẻ tấn công
	// lấy được vẫn dùng tốt tới lúc hết hạn.
	JTI string `json:"jti"`
	Exp int64  `json:"exp"`
	Iat int64  `json:"iat"`
}

// IssuedAt trả thời điểm phát hành.
func (c Claims) IssuedAt() time.Time { return time.Unix(c.Iat, 0).UTC() }

// ExpiresAt trả thời điểm hết hạn.
func (c Claims) ExpiresAt() time.Time { return time.Unix(c.Exp, 0).UTC() }

// Revoker cho biết một token đã bị thu hồi chưa.
//
// Port khai báo ở đây (bên tiêu thụ) nên authn không phải biết gì về Redis.
type Revoker interface {
	// IsRevoked kiểm tra cả hai đường thu hồi: theo TOKEN (đăng xuất một thiết
	// bị) và theo TÀI KHOẢN (khoá tài xế, đăng xuất mọi thiết bị).
	IsRevoked(ctx context.Context, c *Claims) (bool, error)
}

type Issuer struct {
	secret    []byte
	accessTTL time.Duration
	revoker   Revoker
}

func NewIssuer(secret string, accessTTL time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), accessTTL: accessTTL}
}

var b64 = base64.RawURLEncoding

func (i *Issuer) Issue(sub string, role Role, deviceID string, now time.Time) (string, time.Time) {
	tok, exp, _ := i.IssueWithID(sub, role, deviceID, now)
	return tok, exp
}

// IssueWithID phát hành token và trả về cả JTI để nơi gọi lưu lại nếu cần.
func (i *Issuer) IssueWithID(sub string, role Role, deviceID string, now time.Time) (string, time.Time, string) {
	exp := now.Add(i.accessTTL)
	jti := id.New("jti")
	header := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, _ := json.Marshal(Claims{
		Sub: sub, Role: role, DeviceID: deviceID, JTI: jti,
		Exp: exp.Unix(), Iat: now.Unix(),
	})
	payload := header + "." + b64.EncodeToString(body)
	return payload + "." + b64.EncodeToString(i.sign(payload)), exp, jti
}

func (i *Issuer) sign(payload string) []byte {
	m := hmac.New(sha256.New, i.secret)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

func (i *Issuer) Parse(token string, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errs.E(errs.KindUnauthorized, "bad_token", "Phiên đăng nhập không hợp lệ.")
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, i.sign(parts[0]+"."+parts[1])) {
		return nil, errs.E(errs.KindUnauthorized, "bad_token", "Phiên đăng nhập không hợp lệ.")
	}
	raw, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, errs.E(errs.KindUnauthorized, "bad_token", "Phiên đăng nhập không hợp lệ.")
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, errs.E(errs.KindUnauthorized, "bad_token", "Phiên đăng nhập không hợp lệ.")
	}
	if now.Unix() >= c.Exp {
		return nil, errs.E(errs.KindUnauthorized, "token_expired", "Phiên đăng nhập đã hết hạn.")
	}
	return &c, nil
}

// BearerFrom lấy token từ header Authorization.
func BearerFrom(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return h[7:]
	}
	return ""
}
