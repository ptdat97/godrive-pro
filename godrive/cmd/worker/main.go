// Command worker chạy các tác vụ nền: dispatcher, outbox relay, đối soát.
// Tách khỏi API để scale độc lập (dispatcher là thành phần tốn CPU nhất).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/godrive/internal/app"
	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/platform/logger"

	// Driver Postgres, giống cmd/api: app tự chọn theo DATABASE_URL.
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg, err := config.Load()
	log := logger.New(cfg.LogLevel, cfg.LogJSON)
	if err != nil {
		log.Error("cấu hình không hợp lệ", "err", err)
		os.Exit(1)
	}

	a, err := app.New(cfg, log)
	if err != nil {
		log.Error("khởi tạo ứng dụng thất bại", "err", err)
		os.Exit(1)
	}
	defer func() { _ = a.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// StartWorkers tự chạy outbox relay khi ở chế độ Postgres — không dựng
	// thêm relay riêng ở đây nữa (bản cũ dựng một MemoryStore rỗng mà không ai
	// ghi vào, nên relay chạy mỗi giây và không làm gì cả).
	a.StartWorkers(ctx)

	log.Info("worker đang chạy", "in_memory", cfg.InMemory())
	<-ctx.Done()
	log.Info("worker đã dừng")
}
