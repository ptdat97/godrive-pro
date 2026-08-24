// Command api chạy HTTP API và (ở chế độ dev) cả worker nền.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/godrive/internal/app"
	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/platform/logger"

	// Driver Postgres. Chỉ đăng ký vào database/sql; app tự chọn Postgres hay
	// in-memory tuỳ DATABASE_URL có được đặt hay không.
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

	a.StartWorkers(ctx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		log.Info("API đang lắng nghe", "addr", cfg.HTTPAddr, "env", cfg.Env, "in_memory", cfg.InMemory())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server dừng bất thường", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("nhận tín hiệu dừng, đang tắt êm...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("tắt không êm", "err", err)
	}
	log.Info("đã dừng")
}
