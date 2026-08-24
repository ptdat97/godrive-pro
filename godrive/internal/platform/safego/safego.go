// Package safego chạy goroutine nền có bắt panic.
//
// httpx.Recover() chỉ bảo vệ handler HTTP. Panic trong một goroutine nền
// (dispatcher, consumer sự kiện) sẽ giết cả tiến trình — kéo theo toàn bộ
// trạng thái đang giữ trong bộ nhớ. Mọi `go func()` chạy code nghiệp vụ đều
// phải mở đầu bằng `defer safego.Recover(...)`.
package safego

import (
	"log/slog"
	"runtime/debug"
)

// Recover bắt panic trong goroutine nền. Gọi bằng defer ở dòng đầu tiên.
//
// name là tên goroutine để đọc log ("matcher.dispatch", "eventbus.handler").
// cleanup (có thể nil) chạy SAU khi đã ghi log — dùng để đưa nghiệp vụ về
// trạng thái an toàn, ví dụ đẩy chuyến ra khỏi SEARCHING để nó không kẹt mãi.
func Recover(log *slog.Logger, name string, cleanup func()) {
	rec := recover()
	if rec == nil {
		return
	}
	log.Error("panic trong goroutine nền",
		"goroutine", name, "recover", rec, "stack", string(debug.Stack()))
	if cleanup == nil {
		return
	}
	// cleanup gọi vào chính đống code vừa hỏng nên nó cũng có thể panic.
	// Không được để lần panic thứ hai làm chết tiến trình.
	defer func() {
		if r2 := recover(); r2 != nil {
			log.Error("panic trong cleanup", "goroutine", name, "recover", r2)
		}
	}()
	cleanup()
}

// Go chạy fn trong goroutine riêng đã bọc Recover.
func Go(log *slog.Logger, name string, fn func()) {
	go func() {
		defer Recover(log, name, nil)
		fn()
	}()
}
