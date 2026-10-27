// Package page là con trỏ phân trang keyset dùng chung.
//
// Keyset chứ không OFFSET: OFFSET 9.950 bắt CSDL đọc rồi vứt 9.950 dòng, và
// khi có dòng mới chen vào giữa hai lần tải thì trang sau lặp lại hoặc bỏ sót
// dòng. Keyset nhớ "dòng cuối cùng đã thấy" nên mỗi trang chỉ đọc đúng phần
// của nó và không lặp, không sót.
package page

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
)

// Cursor là vị trí sau dòng cuối của trang trước, theo thứ tự (At, ID) giảm
// dần. Zero value = trang đầu.
type Cursor struct {
	At time.Time
	ID string
}

func (c Cursor) IsZero() bool { return c.ID == "" }

// Encode trả chuỗi mờ để giao diện gửi lại nguyên văn. Mờ là có chủ ý: giao
// diện không được tự dựng con trỏ, nếu không đổi thứ tự sắp xếp sau này sẽ
// làm hỏng mọi đường dẫn đã lưu.
func (c Cursor) Encode() string {
	if c.IsZero() {
		return ""
	}
	raw := c.At.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Decode đọc con trỏ do Encode tạo ra. Chuỗi rỗng là trang đầu.
func Decode(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	bad := errs.Invalid("cursor_invalid", "Con trỏ phân trang không hợp lệ.")
	if err != nil {
		return Cursor{}, bad
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return Cursor{}, bad
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return Cursor{}, bad
	}
	return Cursor{At: t, ID: id}, nil
}

// Before cho biết dòng (at, id) có đứng SAU con trỏ trong thứ tự giảm dần
// không — tức có thuộc các trang tiếp theo. Bản bộ nhớ của repo dùng hàm này
// để khớp đúng ngữ nghĩa của `(at, id) < ($1, $2)` trong SQL.
func (c Cursor) Before(at time.Time, id string) bool {
	if c.IsZero() {
		return true
	}
	if !at.Equal(c.At) {
		return at.Before(c.At)
	}
	return id < c.ID
}
