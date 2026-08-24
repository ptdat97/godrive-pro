// Package config đọc cấu hình từ biến môi trường (12-factor).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string // dev | staging | prod
	HTTPAddr string
	LogLevel string
	LogJSON  bool

	// DatabaseURL rỗng => chạy toàn bộ bằng bộ nhớ (chế độ dev).
	DatabaseURL string
	RedisURL    string
	NATSURL     string
	MQTTURL     string

	JWTSecret    string
	AccessTTL    time.Duration
	DevAuth      bool // trả mã OTP trong response, chỉ dùng ở dev
	ShutdownWait time.Duration

	// Tuân thủ Nghị định 13/2023: dữ liệu cá nhân phải lưu trong lãnh thổ VN.
	DataResidency string

	// AdminPhones là danh sách số điện thoại được phép đăng nhập bảng điều
	// khiển vận hành (phân tách bằng dấu phẩy). Rỗng = không ai vào được.
	// Mặc định đóng: quyền quản trị phải cấp tường minh.
	AdminPhones []string
}

func Load() (Config, error) {
	c := Config{
		Env:           get("APP_ENV", "dev"),
		HTTPAddr:      get("HTTP_ADDR", ":8080"),
		LogLevel:      get("LOG_LEVEL", "info"),
		LogJSON:       getBool("LOG_JSON", false),
		DatabaseURL:   get("DATABASE_URL", ""),
		RedisURL:      get("REDIS_URL", ""),
		NATSURL:       get("NATS_URL", ""),
		MQTTURL:       get("MQTT_URL", ""),
		JWTSecret:     get("JWT_SECRET", "dev-secret-doi-truoc-khi-len-production"),
		AccessTTL:     getDur("ACCESS_TTL", 24*time.Hour),
		DevAuth:       getBool("DEV_AUTH", true),
		ShutdownWait:  getDur("SHUTDOWN_WAIT", 15*time.Second),
		DataResidency: get("DATA_RESIDENCY", "VN"),
		AdminPhones:   getList("ADMIN_PHONES"),
	}
	if c.Env == "prod" {
		if c.JWTSecret == "" || strings.HasPrefix(c.JWTSecret, "dev-") {
			return c, fmt.Errorf("config: JWT_SECRET phải được đặt ở môi trường production")
		}
		if c.DevAuth {
			return c, fmt.Errorf("config: DEV_AUTH phải tắt ở production")
		}
		if c.DatabaseURL == "" {
			return c, fmt.Errorf("config: DATABASE_URL bắt buộc ở production")
		}
	}
	return c, nil
}

func (c Config) InMemory() bool { return c.DatabaseURL == "" }

// getList đọc danh sách phân tách bằng dấu phẩy, bỏ khoảng trắng và phần tử rỗng.
func getList(k string) []string {
	raw := os.Getenv(k)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func get(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func getDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
