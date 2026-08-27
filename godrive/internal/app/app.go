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
	"github.com/example/godrive/internal/mqttauth"
	"github.com/example/godrive/internal/notification"
	"github.com/example/godrive/internal/outbox"
	"github.com/example/godrive/internal/payment"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/internal/platform/redisx"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/settings"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/internal/wallet"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/crypt"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
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
	// Settings là cấu hình nghiệp vụ chỉnh được từ bảng điều khiển.
	Settings *settings.Service
	Payments *payment.Service
	// Settlement khác nil ở chế độ Postgres. Đối soát và chi trả cần bảng
	// settlement_batches để chống trả tiền hai lần.
	Settlement *wallet.Settlement
	Surge      *pricing.DemandSurge
	// Outbox khác nil ở chế độ Postgres. StartWorkers tự chạy relay.
	Outbox *outbox.PostgresStore
	// Redis khác nil khi có REDIS_URL. Không có thì các store nóng chạy bằng
	// bộ nhớ tiến trình và hệ thống chỉ đúng với một bản sao.
	Redis   *redisx.Client
	Metrics *appMetrics
	// Revoker khác nil khi có Redis. Không có nó thì token chỉ hết hiệu lực
	// khi hết hạn — tài xế vừa bị khoá vẫn nhận chuyến được tới 24 giờ.
	Revoker *authn.RedisRevoker
	// MQTT khác nil khi có MQTT_URL. Luồng vị trí chính đi qua đây; endpoint
	// HTTP /v1/locations/ping vẫn giữ làm đường dự phòng.
	MQTT *location.MQTTConsumer

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
	// Bus: NATS JetStream nếu có, ngược lại in-memory.
	//
	// Khác biệt thật sự không phải "chạy được nhiều tiến trình" — outbox đã lo
	// phần đó. Khác biệt là ACK: handler chạy xong mới báo nhận, nên tiến trình
	// chết giữa chừng thì việc được giao lại thay vì biến mất.
	var bus eventbus.Bus
	if cfg.NATSURL != "" {
		b, err := eventbus.NewNATS(cfg.NATSURL, log)
		if err != nil {
			return nil, err
		}
		bus = b
	} else {
		bus = eventbus.NewInMemory(log)
		if !cfg.InMemory() {
			log.Warn("chưa cấu hình NATS_URL: sự kiện đi qua bus in-process, " +
				"handler KHÔNG có ack — giết tiến trình giữa chừng sẽ mất việc đang xử lý")
		}
	}

	var rdb *redisx.Client
	if cfg.RedisURL != "" {
		c, err := redisx.New(cfg.RedisURL)
		if err != nil {
			return nil, err
		}
		rdb = c
	}
	issuer := authn.NewIssuer(cfg.JWTSecret, cfg.AccessTTL)

	var revoker *authn.RedisRevoker
	if rdb != nil {
		revoker = authn.NewRedisRevoker(rdb.Raw(), redisx.KeyPrefix)
		issuer.UseRevoker(revoker)
	} else if !cfg.InMemory() {
		log.Warn("chưa cấu hình REDIS_URL: không thu hồi được token — " +
			"đăng xuất chỉ xoá token ở phía client, tài xế bị khoá vẫn dùng token cũ tới khi hết hạn")
	}

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

		// Mã hoá giấy tờ ở tầng ứng dụng. Không có khoá thì giấy tờ lưu thô —
		// chấp nhận được ở dev, và config.Load() đã chặn ở production.
		if cfg.DocumentsKey != "" {
			c, err := crypt.New(cfg.DocumentsKey)
			if err != nil {
				return nil, err
			}
			driverRepo.(*driver.PostgresRepo).UseCipher(c)
		} else {
			log.Warn("chưa cấu hình DOCUMENTS_KEY: số CCCD và GPLX lưu dạng THÔ trong CSDL")
		}
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

	// Cổng thanh toán: chỉ bật cổng nào có đủ khoá bí mật.
	//
	// Bật một cổng mà thiếu khoá nghĩa là mở một endpoint webhook không xác
	// thực được gì — tệ hơn hẳn việc không bật.
	var providers []payment.Provider
	if cfg.MoMoSecretKey != "" {
		providers = append(providers, payment.NewMoMo(cfg.MoMoPartnerCode, cfg.MoMoAccessKey, cfg.MoMoSecretKey))
	}
	if cfg.ZaloPayKey2 != "" {
		providers = append(providers, payment.NewZaloPay(cfg.ZaloPayAppID, cfg.ZaloPayKey2))
	}
	if cfg.VNPayHashSecret != "" {
		providers = append(providers, payment.NewVNPay(cfg.VNPayTmnCode, cfg.VNPayHashSecret))
	}

	var paySvc *payment.Service
	var settlement *wallet.Settlement
	if db != nil {
		paySvc = payment.NewService(payment.NewPostgresRepo(db), walletSvc, clk, providers...)
		settlement = wallet.NewSettlement(wallet.NewPostgresSettlementStore(db), ledger)
		if len(providers) == 0 {
			log.Warn("chưa cấu hình cổng thanh toán nào: tài xế không nạp được tiền để trả công nợ")
		} else {
			log.Info("cổng thanh toán đã bật", "providers", paySvc.Providers())
		}
	}

	// Cấu hình nghiệp vụ. Không có Postgres thì dùng kho bộ nhớ — chỉnh được
	// nhưng mất khi tắt, đủ cho môi trường phát triển.
	var setStore settings.Store = settings.NewMemoryStore()
	if db != nil {
		setStore = settings.NewPostgresStore(db)
	}
	settingsSvc := settings.NewService(setStore, clk, id.New)
	settingsSvc.UsePublisher(bus)

	var auditLog admin.AuditLog = admin.NewMemoryAuditLog()
	if db != nil {
		auditLog = admin.NewPostgresAuditLog(db)
	}
	adminSvc := admin.NewService(driverSvc, tripSvc, adminLocation{svc: locSvc}, walletSvc, auditLog, clk)
	if revoker != nil {
		adminSvc.UseRevoker(revoker)
	}
	adminAuth := admin.NewAuth(identitySvc, cfg.AdminPhones)
	if !adminAuth.Enabled() {
		log.Warn("chưa cấu hình ADMIN_PHONES: bảng điều khiển vận hành sẽ từ chối mọi đăng nhập")
	}

	app := &App{
		Cfg: cfg, Log: log, Clock: clk, Bus: bus, Issuer: issuer,
		Identity: identitySvc, Drivers: driverSvc, Location: locSvc,
		Pricing: pricingSvc, Trips: tripSvc, Wallet: walletSvc, Matcher: matcher,
		Admin: adminSvc, Settings: settingsSvc, Payments: paySvc, Settlement: settlement,
		Surge: surge, Outbox: obx, adminAuth: adminAuth,
		Redis: rdb, Revoker: revoker, Metrics: newAppMetrics(),
		db: db,
	}
	// MQTT cho luồng vị trí. Dựng SAU khi App đã sẵn sàng vì consumer bắt đầu
	// nhận ping ngay khi nối, và nó cần locSvc hoạt động được.
	if cfg.MQTTURL != "" {
		mc, err := location.NewMQTTConsumer(cfg.MQTTURL, cfg.MQTTClientID, locSvc, log,
			location.Credentials{Username: cfg.MQTTUsername, Password: cfg.MQTTPassword})
		if err != nil {
			return nil, err
		}
		app.MQTT = mc
	}

	// Nối cấu hình động vào từng module TRƯỚC khi trả app ra: nếu không, những
	// request đầu tiên sẽ chạy bằng hằng số mặc định thay vì cấu hình đã lưu.
	app.wireSettings()
	if err := settingsSvc.Reload(context.Background()); err != nil {
		log.Warn("chưa nạp được cấu hình, tạm dùng giá trị mặc định", "err", err)
	}

	app.registerGauges()
	return app, nil
}

func (a *App) Close() error {
	// Thứ tự có ý nghĩa: ngừng nhận ping mới trước, rồi mới đóng bus (bus tự
	// chờ handler đang chạy xong), cuối cùng mới cắt Redis và Postgres — handler
	// đang chạy dở vẫn cần chúng.
	if a.MQTT != nil {
		a.MQTT.Close()
	}
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

	// Cửa xác thực cho broker MQTT. Đăng ký cả khi chưa cấu hình MQTT_URL:
	// broker có thể chạy ở nơi khác và trỏ về đây.
	mqttSvc := mqttauth.NewService(a.Issuer, driverRefLookup{a.Drivers}, a.Clock,
		mqttauth.ServiceAccount{Username: a.Cfg.MQTTUsername, Password: a.Cfg.MQTTPassword})
	if a.Revoker != nil {
		mqttSvc.UseRevoker(a.Revoker)
	}
	mqttauth.NewHandler(mqttSvc, a.Log).Register(mux)

	identity.NewHandler(a.Identity, a.Issuer, a.Revoker).Register(mux)
	driver.NewHandler(a.Drivers, a.Issuer).Register(mux)
	location.NewHandler(a.Location, a.Issuer, a.driverIDFromRequest).Register(mux)
	pricing.NewHandler(a.Pricing, a.Issuer).Register(mux)
	trip.NewHandler(a.Trips, a.Issuer, a.driverIDFromRequest).Register(mux)
	// Endpoint nạp ví thủ công CHỈ còn ở chế độ dev và CHỈ khi chưa có cổng
	// thanh toán nào — có cổng thật rồi thì không có lý do gì để giữ một đường
	// tự ghi có vào ví.
	devTopUp := a.Cfg.DevAuth && (a.Payments == nil || !a.Payments.Enabled())
	wallet.NewHandler(a.Wallet, a.Issuer, a.driverIDFromRequest,
		a.Drivers.DebtLimit, devTopUp).Register(mux)
	if a.Payments != nil {
		payment.NewHandler(a.Payments, a.Issuer, a.driverIDFromRequest).Register(mux)
	}
	matching.NewHandler(a.Matcher, a.Issuer, a.driverIDFromRequest).Register(mux)
	admin.NewHandler(a.Admin, a.Issuer, a.adminAuth).Register(mux)
	settings.NewHandler(a.Settings, a.Issuer, a.Admin).Register(mux)

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

// driverRefLookup thu hẹp driver.Service xuống đúng hai trường mà việc phân
// quyền MQTT cần. Đặt ở gốc lắp ráp để internal/mqttauth không phải import
// internal/driver, và để nó kiểm thử được bằng một struct dựng tay.
type driverRefLookup struct{ svc *driver.Service }

func (l driverRefLookup) GetByAccount(ctx context.Context, accountID string) (*mqttauth.DriverRef, error) {
	d, err := l.svc.GetByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return &mqttauth.DriverRef{ID: d.ID, Suspended: d.Status == driver.StatusSuspended}, nil
}
