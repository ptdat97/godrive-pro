// Package authn phát hành và xác thực access token.
// Bản này ký HMAC-SHA256 thuần stdlib (định dạng JWT-compatible HS256).
// Khi cần rotate key / multi-issuer, thay bằng thư viện JWT chuẩn.
package authn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
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
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
}

type Issuer struct {
	secret    []byte
	accessTTL time.Duration
}

func NewIssuer(secret string, accessTTL time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), accessTTL: accessTTL}
}

var b64 = base64.RawURLEncoding

func (i *Issuer) Issue(sub string, role Role, deviceID string, now time.Time) (string, time.Time) {
	exp := now.Add(i.accessTTL)
	header := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, _ := json.Marshal(Claims{Sub: sub, Role: role, DeviceID: deviceID, Exp: exp.Unix(), Iat: now.Unix()})
	payload := header + "." + b64.EncodeToString(body)
	return payload + "." + b64.EncodeToString(i.sign(payload)), exp
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
