package authn

import (
	"context"
	"net/http"
	"time"

	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/errs"
)

type ctxKey struct{}

func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

func ClaimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

// MustClaims dùng trong handler đã nằm sau Require().
func MustClaims(ctx context.Context) *Claims {
	c, ok := ClaimsFrom(ctx)
	if !ok {
		panic("authn: handler thiếu middleware Require")
	}
	return c
}

// UseRevoker bật kiểm tra thu hồi token.
//
// Không gọi thì token chỉ hết hiệu lực khi hết hạn — nghĩa là một tài xế vừa bị
// khoá vẫn nhận chuyến được tới 24 giờ nữa.
func (i *Issuer) UseRevoker(r Revoker) { i.revoker = r }

// Require chặn request không có token hợp lệ, sai vai trò, hoặc đã bị thu hồi.
func (i *Issuer) Require(roles ...Role) httpx.Middleware {
	allowed := map[Role]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := BearerFrom(r)
			if tok == "" {
				httpx.Fail(w, r, errs.E(errs.KindUnauthorized, "missing_token", "Vui lòng đăng nhập."))
				return
			}
			c, err := i.Parse(tok, time.Now().UTC())
			if err != nil {
				httpx.Fail(w, r, err)
				return
			}
			if len(allowed) > 0 && !allowed[c.Role] {
				httpx.Fail(w, r, errs.E(errs.KindForbidden, "forbidden", "Bạn không có quyền thực hiện thao tác này."))
				return
			}
			if i.revoker != nil {
				revoked, err := i.revoker.IsRevoked(r.Context(), c)
				if err != nil {
					// Không kiểm tra được thì TỪ CHỐI, không cho qua.
					//
					// Đây là chỗ hiếm hoi chọn fail-closed: cho qua khi Redis
					// chết nghĩa là mọi token đã thu hồi lại có hiệu lực trở
					// lại, đúng vào lúc hệ thống đang có sự cố.
					httpx.Fail(w, r, errs.E(errs.KindUnauthorized, "revocation_check_failed",
						"Không xác minh được phiên đăng nhập, vui lòng thử lại."))
					return
				}
				if revoked {
					httpx.Fail(w, r, errs.E(errs.KindUnauthorized, "token_revoked",
						"Phiên đăng nhập đã bị thu hồi, vui lòng đăng nhập lại."))
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), c)))
		})
	}
}
