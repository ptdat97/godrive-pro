package admin

import (
	"context"
	"strings"

	"github.com/example/godrive/internal/identity"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/errs"
)

// Vì sao admin có cổng đăng nhập riêng:
//
// Luồng OTP dùng chung sẽ cấp token cho bất kỳ số điện thoại nào yêu cầu vai
// trò được gửi lên — với rider/driver thì đúng (ai cũng đăng ký được), nhưng
// với admin thì đó là lỗ hổng leo thang đặc quyền: chỉ cần gửi `role=admin`.
//
// Ở đây danh sách số điện thoại được phép nằm trong cấu hình máy chủ. Số không
// có trong danh sách bị từ chối TRƯỚC khi gửi OTP, nên không lộ việc số nào là
// admin và cũng không tốn tin nhắn.
//
// Giai đoạn sau nên thay bằng nhà cung cấp danh tính nội bộ (SSO/LDAP) + 2FA;
// interface AdminAuth giữ nguyên khi đổi.

type IdentityPort interface {
	RequestOTP(ctx context.Context, rawPhone string, role authn.Role) (string, string, error)
	VerifyOTP(ctx context.Context, challengeID, code, deviceID string) (*identity.TokenPair, error)
}

type Auth struct {
	identity IdentityPort
	// allowed là tập số điện thoại đã chuẩn hoá E.164 được phép làm admin.
	allowed map[string]bool
}

// NewAuth dựng cổng đăng nhập admin. Danh sách rỗng nghĩa là KHÔNG ai đăng nhập
// được — mặc định đóng, phải cấu hình tường minh mới mở.
func NewAuth(idsvc IdentityPort, phones []string) *Auth {
	allowed := make(map[string]bool, len(phones))
	for _, p := range phones {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Chuẩn hoá để "0901234567" và "+84901234567" là cùng một người.
		if norm, err := identity.NormalizePhone(p); err == nil {
			allowed[norm] = true
		}
	}
	return &Auth{identity: idsvc, allowed: allowed}
}

// Enabled cho biết có cấu hình admin nào không (dùng để cảnh báo lúc khởi động).
func (a *Auth) Enabled() bool { return len(a.allowed) > 0 }

func (a *Auth) check(rawPhone string) error {
	phone, err := identity.NormalizePhone(rawPhone)
	if err != nil {
		return err
	}
	if !a.allowed[phone] {
		// Thông báo giống hệt mọi trường hợp để không lộ số nào là admin.
		return errs.E(errs.KindForbidden, "not_admin", "Số điện thoại này không có quyền quản trị.")
	}
	return nil
}

// RequestOTP gửi mã cho quản trị viên đã được cấp quyền.
func (a *Auth) RequestOTP(ctx context.Context, rawPhone string) (string, string, error) {
	if err := a.check(rawPhone); err != nil {
		return "", "", err
	}
	return a.identity.RequestOTP(ctx, rawPhone, authn.RoleAdmin)
}

// VerifyOTP đổi mã lấy token vai trò admin.
func (a *Auth) VerifyOTP(ctx context.Context, challengeID, code, deviceID string) (*identity.TokenPair, error) {
	tp, err := a.identity.VerifyOTP(ctx, challengeID, code, deviceID)
	if err != nil {
		return nil, err
	}
	// Chốt chặn thứ hai: challenge có thể được tạo từ luồng khác với vai trò
	// khác. Chỉ chấp nhận token thực sự mang vai trò admin.
	if tp.Account == nil || tp.Account.Role != authn.RoleAdmin {
		return nil, errs.E(errs.KindForbidden, "not_admin", "Phiên đăng nhập không có quyền quản trị.")
	}
	if !a.allowed[tp.Account.Phone] {
		return nil, errs.E(errs.KindForbidden, "not_admin", "Số điện thoại này không có quyền quản trị.")
	}
	return tp, nil
}
