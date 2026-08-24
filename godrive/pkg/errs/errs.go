// Package errs định nghĩa lỗi nghiệp vụ có mã ổn định để client mobile xử lý.
package errs

import (
	"errors"
	"fmt"
	"net/http"
)

type Kind string

const (
	KindInvalid      Kind = "invalid"
	KindUnauthorized Kind = "unauthorized"
	KindForbidden    Kind = "forbidden"
	KindNotFound     Kind = "not_found"
	KindConflict     Kind = "conflict"
	KindRateLimited  Kind = "rate_limited"
	KindInternal     Kind = "internal"
)

type Error struct {
	Kind Kind
	// Code là mã ổn định, app mobile map sang thông báo tiếng Việt.
	Code string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s/%s: %s: %v", e.Kind, e.Code, e.Msg, e.Err)
	}
	return fmt.Sprintf("%s/%s: %s", e.Kind, e.Code, e.Msg)
}

func (e *Error) Unwrap() error { return e.Err }

func E(kind Kind, code, msg string) *Error {
	return &Error{Kind: kind, Code: code, Msg: msg}
}

func Wrap(kind Kind, code, msg string, err error) *Error {
	return &Error{Kind: kind, Code: code, Msg: msg, Err: err}
}

func Invalid(code, msg string) *Error  { return E(KindInvalid, code, msg) }
func NotFound(code, msg string) *Error { return E(KindNotFound, code, msg) }
func Conflict(code, msg string) *Error { return E(KindConflict, code, msg) }

// KindOf lấy Kind của lỗi bất kỳ, mặc định internal.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "internal_error"
}

func HTTPStatus(err error) int {
	switch KindOf(err) {
	case KindInvalid:
		return http.StatusBadRequest
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
