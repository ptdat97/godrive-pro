// Package app lắp ráp toàn bộ ứng dụng (composition root).
//
// Kiến trúc: modular monolith. Mỗi module trong internal/ chỉ giao tiếp với
// module khác qua interface (Port) khai báo ở phía người dùng. Khi cần tách
// microservice, chỉ việc thay implementation của Port bằng gRPC client — code
// nghiệp vụ không đổi.
package app

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/config"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/identity"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/notification"
	"github.com/example/godrive/internal/outbox"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/idem"
)

type App struct {
	Cfg    config.Config
	Log    *slog.Logger
	Clock  clock.Clock
	Bus    eventbus.Bus
	Issuer *authn.Issuer

	Identity *identity.Service
	Drivers  *driver.Service
	Location *location.Service
	Pricing  *pricing.Service
	Trips    *trip.Service
	Wallet   *wallet.Service
	Matcher  *matching.Engine
	Admin    *admin.Service
	Surge    *pricing.DemandSurge
	// Outbox khác nil ở chế độ Postgres. StartWorkers tự chạy relay.
	Outbox *outbox.PostgresStore

	adminAuth *admin.Auth
	db        *sql.DB
}

// New lắp ráp app với đồng hồ thật.
func New(cfg config.Config, log *slog.Logger) (*App, error) {
	return NewWithClock(cfg, log, clock.Real())
}

// NewWithClock lắp ráp app với đồng hồ tiêm vào. Nếu cfg.InMemory() thì dùng
// repo bộ nhớ (dev/test), ngược lại dùng Postgres.
//
// Đồng hồ tiêm được là điều kiện để test những mốc thời gian nghiệp vụ mà không
// phải ngủ thật: cửa sổ huỷ miễn phí 2 phút, hạn báo giá 5 phút, độ tươi ping 45 giây.
func NewWithClock(cfg config.Config, log *slog.Logger, clk clock.Clock) (*App, error) {
	bus := eventbus.NewInMemory(log)
	issuer := authn.NewIssuer(cfg.JWTSecret, cfg.AccessTTL)

	var (
		db         *sql.DB
		driverRepo driver.Repository
		tripRepo   trip.Repository
		idRepo     identity.Repository
	)
	if cfg.InMemory() {
		driverRepo = driver.NewMemoryRepo(clk)
		tripRepo = trip.NewMemoryRepo()
		idRepo = identity.NewMemoryRepo()
		log.Warn("chạy ở chế độ IN-MEMORY: dữ liệu sẽ mất khi tắt tiến trình")
	} else {
		// Driver Postgres đăng ký ở cmd/*/main.go:
		//   import _ "github.com/jackc/pgx/v5/stdlib"
		conn, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_open_failed", "không mở được kết nối DB", err)
		}
		conn.SetMaxOpenConns(50)
		conn.SetMaxIdleConns(10)
		db = conn
		driverRepo = driver.NewPostgresRepo(conn)
		tripRepo = trip.NewPostgresRepo(conn)
		// Bỏ sót nhánh này từng làm bảng accounts không bao giờ được ghi, kéo
		// theo cả đăng ký tài xế lẫn đặt chuyến hỏng vì khoá ngoại.
		idRepo = identity.NewPostgresRepo(conn)
	}

	// Transactional Outbox: sự kiện ghi cùng transaction nghiệp vụ, relay phát
	// sau. Chuyển ngữ nghĩa từ at-most-once (publish rồi quên) sang at-least-once.
	var obx *outbox.PostgresStore
	if db != nil {
		obx = outbox.NewPostgresStore(db)
		tripRepo.(*trip.PostgresRepo).UseOutbox(obx)
	}

	otpSender := notification.LogOTPSender{Log: log}
	identitySvc := identity.NewService(idRepo, otpSender, issuer, clk)
	identitySvc.DevMode = cfg.DevAuth

	driverSvc := driver.NewService(driverRepo, bus, clk)
	locIndex := location.NewMemoryIndex(clk)
	locSvc := location.NewService(locIndex, driverSvc, clk)

	surge := pricing.NewDemandSurge(idleCounter{idx: locIndex})
	pricingSvc := pricing.NewService(pricing.NewHaversineEngine(), surge, pricing.NewMemoryQuoteStore(), clk)

	tripSvc := trip.NewService(tripRepo, pricingSvc, bus, idem.NewMemoryStore(), clk)

	var ledger wallet.Ledger = wallet.NewMemoryLedger()
	if db != nil {
		ledger = wallet.NewPostgresLedger(db)
	}
	walletSvc := wallet.NewService(ledger, bus, clk)

	// Cổng chặn công nợ đọc số dư THẬT từ sổ cái ở cả hai chỗ: khi dispatcher
	// chọn ứng viên, và một lần nữa khi tài xế bấm nhận (số dư có thể đã đổi
	// giữa hai thời điểm đó).
	driverSvc.UseBalanceReader(walletSvc)
	// Cho phép tài xế tự thoát khỏi trạng thái kẹt khi không còn chuyến nào chạy.
	driverSvc.UseTripPort(tripSvc)

	var offerStore matching.Store = matching.NewMemoryStore(clk)
	if db != nil {
		offerStore = matching.NewPostgresStore(db, clk)
	}
	matcher := matching.NewEngine(
		matching.DefaultConfig(), locSvc, driverSvc, tripSvc,
		offerStore, matching.NewSimpleETA(), walletSvc, bus, clk,
	)

	if !cfg.InMemory() {
		// Trung thực về những gì CHƯA bền: sổ cái, lời mời, chỉ mục vị trí, báo
		// giá và khoá idempotency vẫn nằm trong bộ nhớ tiến trình, kể cả khi đã
		// có DATABASE_URL. Vì vậy hiện chỉ được chạy đúng MỘT bản sao.
		log.Warn("một số store vẫn ở bộ nhớ dù đã bật Postgres — chỉ chạy 1 bản sao",
			"in_memory", "location.index, pricing.quotes, pricing.surge, idem.keys")
	}

	var auditLog admin.AuditLog = admin.NewMemoryAuditLog()
	if db != nil {
		auditLog = admin.NewPostgresAuditLog(db)
	}
	adminSvc := admin.NewService(driverSvc, tripSvc, adminLocation{svc: locSvc}, walletSvc, auditLog, clk)
	adminAuth := admin.NewAuth(identitySvc, cfg.AdminPhones)
	if !adminAuth.Enabled() {
		log.Warn("chưa cấu hình ADMIN_PHONES: bảng điều khiển vận hành sẽ từ chối mọi đăng nhập")
	}

	return &App{
		Cfg: cfg, Log: log, Clock: clk, Bus: bus, Issuer: issuer,
		Identity: identitySvc, Drivers: driverSvc, Location: locSvc,
		Pricing: pricingSvc, Trips: tripSvc, Wallet: walletSvc, Matcher: matcher,
		Admin: adminSvc, Surge: surge, Outbox: obx, adminAuth: adminAuth,
		db: db,
	}, nil
}

func (a *App) Close() error {
	a.Bus.Close()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// driverIDFromRequest map accountID trong token sang driverID.
func (a *App) driverIDFromRequest(r *http.Request) (string, error) {
	c, ok := authn.ClaimsFrom(r.Context())
	if !ok {
		return "", errs.E(errs.KindUnauthorized, "missing_token", "Vui lòng đăng nhập.")
	}
	d, err := a.Drivers.GetByAccount(r.Context(), c.Sub)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}

// Router dựng toàn bộ route công khai.
func (a *App) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "env": a.Cfg.Env})
	})

	identity.NewHandler(a.Identity).Register(mux)
	driver.NewHandler(a.Drivers, a.Issuer).Register(mux)
	location.NewHandler(a.Location, a.Issuer, a.driverIDFromRequest).Register(mux)
	pricing.NewHandler(a.Pricing, a.Issuer).Register(mux)
	trip.NewHandler(a.Trips, a.Issuer, a.driverIDFromRequest).Register(mux)
	wallet.NewHandler(a.Wallet, a.Issuer, a.driverIDFromRequest,
		a.Drivers.DebtLimit(), a.Cfg.DevAuth).Register(mux)
	matching.NewHandler(a.Matcher, a.Issuer, a.driverIDFromRequest).Register(mux)
	admin.NewHandler(a.Admin, a.Issuer, a.adminAuth).Register(mux)

	// 30 request/giây, burst 60 cho mỗi IP.
	rl := httpx.NewRateLimit(30, 60)
	return httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Logging(a.Log),
		httpx.Recover(),
		rl.Middleware(),
	)
}

// idleCounter nối chỉ mục vị trí vào bộ tính surge.
type idleCounter struct{ idx *location.MemoryIndex }

func (i idleCounter) IdleCount(ctx context.Context, at pointT, radiusM float64) (int, error) {
	snaps, err := i.idx.Nearby(ctx, at, radiusM, location.Filter{
		Statuses: []driver.Status{driver.StatusIdle},
	})
	return len(snaps), err
}
