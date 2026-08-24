package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
)

const (
	OTPTTL         = 5 * time.Minute
	MaxOTPAttempts = 5
)

// Đầu số di động Việt Nam sau quy hoạch 2018 (3x, 5x, 7x, 8x, 9x).
var vnPhoneRe = regexp.MustCompile(`^(0|\+84)(3|5|7|8|9)[0-9]{8}$`)

// NormalizePhone đưa mọi định dạng về E.164 (+84xxxxxxxxx).
func NormalizePhone(raw string) (string, error) {
	p := strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
	p = strings.ReplaceAll(p, "-", "")
	if !vnPhoneRe.MatchString(p) {
		return "", errs.Invalid("phone_invalid", "Số điện thoại không hợp lệ.")
	}
	if strings.HasPrefix(p, "0") {
		return "+84" + p[1:], nil
	}
	return p, nil
}

type Service struct {
	repo   Repository
	sender OTPSender
	issuer *authn.Issuer
	clk    clock.Clock
	// DevMode trả mã OTP thẳng trong response để test — PHẢI tắt ở production.
	DevMode bool
}

func NewService(repo Repository, sender OTPSender, issuer *authn.Issuer, clk clock.Clock) *Service {
	return &Service{repo: repo, sender: sender, issuer: issuer, clk: clk}
}

// RequestOTP tạo thử thách đăng nhập. Trả về (challengeID, code-nếu-DevMode).
func (s *Service) RequestOTP(ctx context.Context, rawPhone string, role authn.Role) (string, string, error) {
	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return "", "", err
	}
	code, err := randomCode(6)
	if err != nil {
		return "", "", err
	}
	now := s.clk.Now()
	c := Challenge{
		ID:        id.New("chl"),
		Phone:     phone,
		Role:      role,
		CodeHash:  hashCode(phone, code),
		ExpiresAt: now.Add(OTPTTL),
		CreatedAt: now,
	}
	if err := s.repo.SaveChallenge(ctx, c); err != nil {
		return "", "", err
	}
	if err := s.sender.Send(ctx, phone, code); err != nil {
		return "", "", errs.Wrap(errs.KindInternal, "otp_send_failed", "Không gửi được mã xác thực.", err)
	}
	if s.DevMode {
		return c.ID, code, nil
	}
	return c.ID, "", nil
}

func (s *Service) VerifyOTP(ctx context.Context, challengeID, code, deviceID string) (*TokenPair, error) {
	c, err := s.repo.GetChallenge(ctx, challengeID)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	if now.After(c.ExpiresAt) {
		_ = s.repo.DeleteChallenge(ctx, challengeID)
		return nil, errs.Invalid("otp_expired", "Mã xác thực đã hết hạn.")
	}
	if c.Attempts >= MaxOTPAttempts {
		_ = s.repo.DeleteChallenge(ctx, challengeID)
		return nil, errs.E(errs.KindRateLimited, "otp_too_many_attempts", "Bạn đã nhập sai quá nhiều lần.")
	}
	// So sánh hằng thời gian, tránh timing attack.
	if subtle.ConstantTimeCompare([]byte(hashCode(c.Phone, code)), []byte(c.CodeHash)) != 1 {
		c.Attempts++
		_ = s.repo.SaveChallenge(ctx, c)
		return nil, errs.Invalid("otp_invalid", "Mã xác thực không đúng.")
	}
	_ = s.repo.DeleteChallenge(ctx, challengeID)

	acc, err := s.repo.UpsertAccount(ctx, c.Phone, c.Role, now)
	if err != nil {
		return nil, err
	}
	if acc.Blocked {
		return nil, errs.E(errs.KindForbidden, "account_blocked", "Tài khoản đã bị khoá.")
	}
	tok, exp := s.issuer.Issue(acc.ID, acc.Role, deviceID, now)
	return &TokenPair{AccessToken: tok, ExpiresAt: exp, Account: acc}, nil
}

func (s *Service) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	return s.repo.GetAccount(ctx, accountID)
}

// SweepExpiredChallenges dọn thử thách OTP quá hạn, trả về số dòng đã xoá.
// Worker nền gọi định kỳ. VerifyOTP đã xoá thử thách nó chạm tới; hàm này lo
// những cái không ai quay lại xác thực.
func (s *Service) SweepExpiredChallenges(ctx context.Context) (int, error) {
	return s.repo.DeleteExpiredChallenges(ctx, s.clk.Now())
}

func hashCode(phone, code string) string {
	sum := sha256.Sum256([]byte(phone + ":" + code))
	return hex.EncodeToString(sum[:])
}

func randomCode(n int) (string, error) {
	const digits = "0123456789"
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", err
		}
		b[i] = digits[v.Int64()]
	}
	return string(b), nil
}
