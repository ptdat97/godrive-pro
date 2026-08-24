// Package httpx chứa helper JSON, mã lỗi và middleware dùng chung.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/example/godrive/internal/platform/logger"
	"github.com/example/godrive/pkg/errs"
)

const MaxBodyBytes = 1 << 20 // 1MB

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// Fail ghi lỗi ra response theo đúng HTTP status của Kind.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	status := errs.HTTPStatus(err)
	msg := "Đã có lỗi xảy ra, vui lòng thử lại."
	var de *errs.Error
	if errors.As(err, &de) && de.Kind != errs.KindInternal {
		msg = de.Msg
	}
	if status >= 500 {
		logger.From(r.Context()).Error("request failed", "err", err.Error(), "path", r.URL.Path)
	}
	JSON(w, status, ErrorBody{Code: errs.CodeOf(err), Message: msg, TraceID: RequestIDFrom(r.Context())})
}

func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errs.Wrap(errs.KindInvalid, "invalid_body", "Dữ liệu gửi lên không hợp lệ.", err)
	}
	return nil
}

type reqIDKey struct{}

func WithRequestID(ctx context.Context, v string) context.Context {
	return context.WithValue(ctx, reqIDKey{}, v)
}

func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(reqIDKey{}).(string); ok {
		return v
	}
	return ""
}
