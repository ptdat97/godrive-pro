package notification

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/godrive/pkg/errs"
)

// FCMBaseURL là gốc API FCM HTTP v1.
const FCMBaseURL = "https://fcm.googleapis.com"

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// ErrTokenGone: token không còn hợp lệ (app đã gỡ, token đã xoay). Người gọi
// xoá token khỏi CSDL — gửi tiếp vào đó chỉ tốn lượt gọi.
var ErrTokenGone = errors.New("token thiết bị không còn hợp lệ")

// ServiceAccount là phần của tệp service account JSON mà FCM cần.
type ServiceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// LoadServiceAccount đọc tệp JSON tải từ Firebase Console.
func LoadServiceAccount(path string) (ServiceAccount, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ServiceAccount{}, err
	}
	var sa ServiceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return ServiceAccount{}, fmt.Errorf("tệp service account không phải JSON hợp lệ: %w", err)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return ServiceAccount{}, errors.New("tệp service account thiếu project_id, client_email hoặc private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return sa, nil
}

// FCM gửi thông báo qua FCM HTTP v1 cho cả Android và iOS (FCM chuyển sang
// APNs khi khoá APNs đã tải lên Firebase — chốt 2026-10-27, docs/07 T-33).
//
// Tự ký JWT để lấy access token thay vì kéo thư viện Google: cả luồng chỉ là
// một chữ ký RS256 và một lời gọi HTTP, không đáng một cây phụ thuộc.
type FCM struct {
	sa      ServiceAccount
	key     *rsa.PrivateKey
	baseURL string
	http    *http.Client
	now     func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewFCM(sa ServiceAccount, baseURL string) (*FCM, error) {
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("private_key trong service account không phải PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("không đọc được private_key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private_key không phải khoá RSA")
	}
	return &FCM{
		sa: sa, key: rk, baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 10 * time.Second}, now: time.Now,
	}, nil
}

// accessToken trả token OAuth còn hạn, đổi token mới khi còn dưới 5 phút.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.token != "" && now.Before(f.expires.Add(-5*time.Minute)) {
		return f.token, nil
	}
	assertion, err := f.signJWT(now)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", errs.Wrap(errs.KindUnavailable, "fcm_auth_unreachable", "fcm", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", errs.E(errs.KindInternal, "fcm_auth_failed", fmt.Sprintf("đổi token FCM bị từ chối: %d %s", resp.StatusCode, b))
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.AccessToken == "" {
		return "", errs.E(errs.KindInternal, "fcm_auth_failed", "phản hồi đổi token FCM không hợp lệ")
	}
	f.token = body.AccessToken
	f.expires = now.Add(time.Duration(body.ExpiresIn) * time.Second)
	return f.token, nil
}

func (f *FCM) signJWT(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss": f.sa.ClientEmail, "scope": fcmScope, "aud": f.sa.TokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + enc.EncodeToString(sig), nil
}

// Send gửi một tin tới một token. Trả ErrTokenGone nếu token đã chết, lỗi
// KindUnavailable nếu nên thử lại (429, 5xx, mạng), lỗi khác nếu thử lại cũng
// vô ích (tin sai định dạng).
func (f *FCM) Send(ctx context.Context, token string, m Message) error {
	at, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	msg := map[string]any{"token": token}
	if m.Title != "" || m.Body != "" {
		msg["notification"] = map[string]string{"title": m.Title, "body": m.Body}
	}
	if len(m.Data) > 0 {
		msg["data"] = m.Data
	}
	android := map[string]any{}
	apnsHeaders := map[string]string{}
	if m.Urgent {
		android["priority"] = "HIGH"
		apnsHeaders["apns-priority"] = "10"
	}
	if m.TTL > 0 {
		android["ttl"] = strconv.FormatInt(int64(m.TTL/time.Second), 10) + "s"
		apnsHeaders["apns-expiration"] = strconv.FormatInt(f.now().Add(m.TTL).Unix(), 10)
	}
	if len(android) > 0 {
		msg["android"] = android
	}
	if len(apnsHeaders) > 0 {
		msg["apns"] = map[string]any{"headers": apnsHeaders}
	}
	raw, _ := json.Marshal(map[string]any{"message": msg})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.baseURL+"/v1/projects/"+f.sa.ProjectID+"/messages:send", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return errs.Wrap(errs.KindUnavailable, "fcm_unreachable", "fcm", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return classifyFCM(resp.StatusCode, body)
}

// classifyFCM đọc lỗi FCM v1: errorCode nằm trong error.details.
func classifyFCM(status int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	code := ""
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	switch {
	case code == "UNREGISTERED" || status == http.StatusNotFound:
		return ErrTokenGone
	case status == http.StatusTooManyRequests || status >= 500 || code == "UNAVAILABLE" || code == "INTERNAL" || code == "QUOTA_EXCEEDED":
		return errs.E(errs.KindUnavailable, "fcm_retry_later", fmt.Sprintf("FCM %d %s", status, code))
	default:
		return errs.E(errs.KindInternal, "fcm_rejected", fmt.Sprintf("FCM %d %s: %s", status, code, e.Error.Message))
	}
}
