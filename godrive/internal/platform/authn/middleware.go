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

// Require chặn request không có token hợp lệ hoặc sai vai trò.
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
			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), c)))
		})
	}
}
