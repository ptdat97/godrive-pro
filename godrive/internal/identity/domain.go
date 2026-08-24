// Package identity xử lý đăng nhập bằng số điện thoại + OTP.
// Ở Việt Nam gần như không ai đăng nhập bằng email, nên số điện thoại là
// định danh chính. OTP gửi qua Zalo ZNS (rẻ, tỉ lệ đọc cao) và fallback SMS
// brandname của Viettel/VNPT.
package identity

import (
	"context"
	"time"

	"github.com/example/godrive/internal/platform/authn"
)

type Account struct {
	ID        string     `json:"id"`
	Phone     string     `json:"phone"` // chuẩn hoá E.164: +84...
	FullName  string     `json:"full_name"`
	Role      authn.Role `json:"role"`
	Blocked   bool       `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
}

type Challenge struct {
	ID        string
	Phone     string
	Role      authn.Role
	CodeHash  string
	Attempts  int
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Repository interface {
	UpsertAccount(ctx context.Context, phone string, role authn.Role, now time.Time) (*Account, error)
	GetAccount(ctx context.Context, id string) (*Account, error)
	SaveChallenge(ctx context.Context, c Challenge) error
	GetChallenge(ctx context.Context, id string) (Challenge, error)
	DeleteChallenge(ctx context.Context, id string) error
	// DeleteExpiredChallenges dọn thử thách quá hạn, trả về số dòng đã xoá.
	// VerifyOTP chỉ xoá cái nó chạm tới; thử thách không ai xác thực thì nằm lại.
	DeleteExpiredChallenges(ctx context.Context, now time.Time) (int, error)
}

// OTPSender trừu tượng hoá kênh gửi mã.
type OTPSender interface {
	Send(ctx context.Context, phone, code string) error
}

type TokenPair struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	Account     *Account  `json:"account"`
}
