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
	"github.com/example/godrive/internal/platform/redisx"
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
	// Redis khác nil khi có REDIS_URL. Không có thì các store nóng chạy bằng
	// bộ nhớ tiến trình và hệ thống chỉ đúng với một bản sao.
	Redis   *redisx.Client
	Metrics *appMetrics

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

	var rdb *redisx.Client
	if cfg.RedisURL != "" {
		c, err := redisx.New(cfg.RedisURL)
		if err != nil {
			return nil, err
		}
		rdb = c
	}
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

	// Store NÓNG: Redis nếu có, ngược lại bộ nhớ tiến trình.
	//
	// Đây chính là ranh giới giữa "chạy được một bản sao" và "chạy được nhiều
	// bản sao": năm loại dữ liệu dưới đây phải được chia sẻ giữa các pod, nếu
	// không mỗi pod sẽ thấy một thế giới khác nhau.
	var (
		locIdx     location.Index
		quoteStore pricing.QuoteStore
		idemStore  idem.Store
		offerStore matching.Store
	)
	if rdb != nil {
		locIdx = location.NewRedisIndex(rdb.Raw(), redisx.KeyPrefix, clk)
		quoteStore = pricing.NewRedisQuoteStore(rdb.Raw(), redisx.KeyPrefix)
		idemStore = idem.NewRedisStore(rdb.Raw(), redisx.KeyPrefix)
		offerStore = matching.NewRedisStore(rdb.Raw(), redisx.KeyPrefix, clk)
	} else {
		locIdx = location.NewMemoryIndex(clk)
		quoteStore = pricing.NewMemoryQuoteStore()
		idemStore = idem.NewMemoryStore()
		offerStore = matching.NewMemoryStore(clk)
		if !cfg.InMemory() {
			log.Warn("chưa cấu hình REDIS_URL: chỉ mục vị trí, báo giá, khoá idempotency và lời mời "+
				"nằm trong bộ nhớ tiến trình — CHỈ chạy đúng với 1 bản sao",
				"in_memory", "location.index, pricing.quotes, idem.keys, matching.offers")
		}
	}

	otpSender := notification.LogOTPSender{Log: log}
	identitySvc := identity.NewService(idRepo, otpSender, issuer, clk)
	identitySvc.DevMode = cfg.DevAuth

	driverSvc := driver.NewService(driverRepo, bus, clk)
	locSvc := location.NewService(locIdx, driverSvc, clk)

	// Máy chỉ đường: OSRM nếu có, ngược lại ước lượng haversine.
	//
	// Cả hai đều có ĐƯỜNG LÙI về haversine khi OSRM lỗi: báo giá và ghép chuyến
	// nằm trên request path, thà ước lượng thô còn hơn không phục vụ được.
	var routeEngine pricing.RouteEngine = pricing.NewHaversineEngine()
	var etaEngine matching.ETAEngine = matching.NewSimpleETA()
	if cfg.OSRMURL != "" {
		routeEngine = pricing.NewOSRMEngine(cfg.OSRMURL, pricing.NewHaversineEngine())
		etaEngine = matching.NewOSRMETA(cfg.OSRMURL, matching.NewSimpleETA())
	}

	surge := pricing.NewDemandSurge(idleCounter{loc: locSvc})
	pricingSvc := pricing.NewService(routeEngine, surge, quoteStore, clk)

	tripSvc := trip.NewService(tripRepo, pricingSvc, bus, idemStore, clk)

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

	// Redis được ưu tiên cho lời mời và khoá chuyến (dữ liệu nóng, sống ngắn).
	// Không có Redis thì dùng Postgres — ở đó `offers_one_accepted_per_trip`
	// vẫn là chốt chặn cuối.
	if rdb == nil && db != nil {
		offerStore = matching.NewPostgresStore(db, clk)
	}
	matcher := matching.NewEngine(
		matching.DefaultConfig(), locSvc, driverSvc, tripSvc,
		offerStore, etaEngine, walletSvc, bus, clk,
	)

	var auditLog admin.AuditLog = admin.NewMemoryAuditLog()
	if db != nil {
		auditLog = admin.NewPostgresAuditLog(db)
	}
	adminSvc := admin.NewService(driverSvc, tripSvc, adminLocation{svc: locSvc}, walletSvc, auditLog, clk)
	adminAuth := admin.NewAuth(identitySvc, cfg.AdminPhones)
	if !adminAuth.Enabled() {
		log.Warn("chưa cấu hình ADMIN_PHONES: bảng điều khiển vận hành sẽ từ chối mọi đăng nhập")
	}

	app := &App{
		Cfg: cfg, Log: log, Clock: clk, Bus: bus, Issuer: issuer,
		Identity: identitySvc, Drivers: driverSvc, Location: locSvc,
		Pricing: pricingSvc, Trips: tripSvc, Wallet: walletSvc, Matcher: matcher,
		Admin: adminSvc, Surge: surge, Outbox: obx, adminAuth: adminAuth,
		Redis: rdb, Metrics: newAppMetrics(),
		db: db,
	}
	app.registerGauges()
	return app, nil
}

func (a *App) Close() error {
	a.Bus.Close()
	if a.Redis != nil {
		_ = a.Redis.Close()
	}
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

	// /healthz = liveness: tiến trình còn sống không ("có cần restart không").
	// /readyz  = readiness: phụ thuộc còn dùng được không ("có nên nhận việc không").
	// Gộp hai thứ này làm một sẽ khiến pod bị restart mỗi khi CSDL chậm.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "env": a.Cfg.Env})
	})
	mux.HandleFunc("GET /readyz", a.readyz)
	mux.Handle("GET /metrics", a.Metrics.reg.Handler())

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
	// Rate limit toàn cụm khi có Redis: bản in-process cho mỗi pod một hạn mức
	// riêng, nên chạy 5 pod nghĩa là kẻ tấn công được gấp 5 lần hạn mức.
	var rateLimit httpx.Middleware
	if a.Redis != nil {
		rateLimit = httpx.NewRedisRateLimit(a.Redis.Raw(), redisx.KeyPrefix, 30, 60).Middleware()
	} else {
		rateLimit = httpx.NewRateLimit(30, 60).Middleware()
	}
	return httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Logging(a.Log),
		a.metricsMiddleware(),
		httpx.Recover(),
		rateLimit,
	)
}

// idleCounter nối chỉ mục vị trí vào bộ tính surge.
//
// Đi qua location.Service chứ không qua Index trực tiếp, để bộ đếm cung dùng
// đúng ngưỡng độ tươi mà dispatcher dùng — nếu không, surge sẽ đếm cả những
// tài xế mà dispatcher đã coi là mất kết nối.
type idleCounter struct{ loc *location.Service }

func (i idleCounter) IdleCount(ctx context.Context, at pointT, radiusM float64) (int, error) {
	snaps, err := i.loc.Nearby(ctx, at, radiusM, location.Filter{
		Statuses:    []driver.Status{driver.StatusIdle},
		FreshWithin: location.StaleAfter,
	})
	return len(snaps), err
}
