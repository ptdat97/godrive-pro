# 01 — Kiến trúc tổng thể

## 1.1 Bối cảnh hệ thống

```
   ┌──────────────┐   ┌──────────────┐   ┌────────────────────┐
   │  App khách   │   │  App tài xế  │   │ godrive-admin      │
   │  (chưa có)   │   │  (chưa có)   │   │ Next.js 16 · React │
   └──────┬───────┘   └──────┬───────┘   └─────────┬──────────┘
          │ HTTPS/JSON       │ HTTPS/JSON          │ server-side fetch
          │                  │ (+ MQTT — kế hoạch) │ token trong cookie httpOnly
          └──────────────────┴─────────────────────┘
                             │
                  ┌──────────▼──────────┐
                  │   godrive (Go)      │  modular monolith, 1 binary
                  │   cmd/api  :8080    │
                  └──────────┬──────────┘
                             │
     ┌───────────────────────┼───────────────────────┐
     │ HIỆN TẠI              │ KẾ HOẠCH (chưa nối)   │
     │  · toàn bộ in-memory  │  · Postgres+PostGIS   │
     │  · Postgres cho       │  · Redis (GEO, claim) │
     │    drivers/trips      │  · NATS JetStream     │
     │    (đang hỏng, G-01)  │  · EMQX (MQTT)        │
     │                       │  · OSRM self-host     │
     └───────────────────────┴───────────────────────┘
```

**Đã dựng ở môi trường dev:** Postgres 18.4 + PostGIS 3.6.3 (cổng 5432, database `godrive`) và
Redis 6379 — **cả hai đều đã nối vào code và kiểm chứng đầu-cuối**.
Schema tạo được qua [`scripts/setup-db.sh`](../godrive/scripts/setup-db.sh).

### Dữ liệu nằm ở đâu, và vì sao

| | Postgres | Redis |
|---|---|---|
| Giữ gì | tài khoản, tài xế, chuyến, `trip_events`, **sổ cái**, offers, nhật ký admin, outbox | vị trí tài xế (GEO), khoá giành chuyến, lời mời, báo giá, khoá idempotency, bộ đếm rate limit |
| Đặc điểm | ghi vừa phải, sống lâu, **phải bền** | ghi rất nhiều, sống rất ngắn (15 giây – 5 phút) |
| Mất thì sao | **mất dữ liệu** | mất hiệu năng, không mất dữ liệu |

Chính Redis là thứ gỡ bỏ ràng buộc "chỉ chạy được một bản sao": trước khi có nó, sáu loại dữ liệu
trên nằm trong bộ nhớ tiến trình nên hai pod thấy hai thế giới khác nhau.

---

## 1.2 Bản đồ module

`internal/` chia theo **nghiệp vụ**, không chia theo tầng kỹ thuật. Mỗi module là một package Go.

```
                    ┌─────────────────────────────────┐
                    │  HTTP Router (app.Router)       │
                    │  RequestID → Logging → Recover  │
                    │  → RateLimit → mux              │
                    └────────────┬────────────────────┘
                                 │
  ┌────────┬─────────┬───────────┼──────────┬─────────┬────────┐
  │identity│ driver  │ location  │ pricing  │  trip   │matching│ admin
  └───┬────┴────┬────┴─────┬─────┴────┬─────┴────┬────┴───┬────┘
      │         │          │          │          │        │
      │         └──────────┴──────────┴──────────┴────────┘
      │              (giao tiếp CHỈ qua Port + eventbus)
      │
      └──────────────────┬──────────────────────────────┐
                         │                              │
              ┌──────────▼─────────┐        ┌───────────▼──────────┐
              │ platform/          │        │ pkg/                 │
              │  logger httpx      │        │  money geo errs      │
              │  authn  eventbus   │        │  id idem clock       │
              └────────────────────┘        └──────────────────────┘
                         │
              ┌──────────▼──────────┬─────────────────┐
              │  wallet (sổ cái)    │ outbox │ notification │
              └─────────────────────┴────────┴──────────────┘
                                     ▲ chưa nối  ▲ chỉ dùng LogOTPSender
```

### Chiều phụ thuộc thật (đã kiểm bằng `go list`/import)

| Module | Import module nghiệp vụ khác | Ghi chú |
|---|---|---|
| `identity` | — (chỉ `platform/authn`) | lá |
| `driver` | — | lá |
| `location` | `driver` (kiểu `Status`, `VehicleType`) | đọc kiểu, có `DriverPort` riêng |
| `pricing` | `driver` (kiểu `VehicleType`) | đọc kiểu |
| `trip` | `driver`, `pricing` (`PricingPort`) | |
| `matching` | `driver`, `location`, `trip` — qua `DriverPort`/`LocationPort`/`TripPort` | |
| `wallet` | — | lá, chỉ phụ thuộc `pkg/money` |
| `admin` | `driver`, `location`, `trip`, `wallet`, `identity` — **toàn bộ qua Port của chính nó** | |
| `app` | tất cả | composition root duy nhất |

> **Quy ước bắt buộc:** Port do **bên tiêu thụ** khai báo.
> `matching.DriverPort` ([engine.go:22](../godrive/internal/matching/engine.go#L22)) nằm trong `matching`.
> `admin.DriverPort` ([domain.go:27](../godrive/internal/admin/domain.go#L27)) nằm trong `admin`.
> Adapter nối chúng lại nằm ở tầng lắp ráp: [`internal/app/admin.go`](../godrive/internal/app/admin.go).
> Nhờ vậy thêm `admin` **không phải sửa một dòng nào** trong `driver`/`trip`/`wallet`.

---

## 1.3 Composition root

Chỉ **một** chỗ được phép `new` dependency: [`internal/app/app.go`](../godrive/internal/app/app.go) → `app.New()`.

```go
if cfg.InMemory() {                       // DATABASE_URL rỗng
    driverRepo = driver.NewMemoryRepo()
    tripRepo   = trip.NewMemoryRepo()
} else {
    conn, _  := sql.Open("pgx", cfg.DatabaseURL)
    driverRepo = driver.NewPostgresRepo(conn)
    tripRepo   = trip.NewPostgresRepo(conn)
}
```

**Điểm cần biết ngay:** *chỉ* `driver` và `trip` có nhánh Postgres. Sáu store còn lại **luôn** là bộ nhớ,
kể cả khi `DATABASE_URL` đã đặt:

| Store | Hệ quả khi chạy Postgres |
|---|---|
| ~~`identity.NewMemoryRepo()`~~ | ✅ **đã có nhánh Postgres** từ GĐ 0 |
| ~~`wallet.NewMemoryLedger()`~~ | ✅ **đã có `PostgresLedger`** từ GĐ 1 |
| `matching.NewMemoryStore(clk)` | offer + khoá chuyến không chia sẻ được giữa các pod |
| `location.NewMemoryIndex(clk)` | chỉ mục vị trí cục bộ theo tiến trình |
| `pricing.NewMemoryQuoteStore()` | báo giá phát ở pod A, đặt chuyến ở pod B → `quote_expired` |
| `idem.NewMemoryStore()` | idempotency không hiệu lực khi >1 pod |

→ **Hệ thống hiện tại chỉ đúng khi chạy đúng 1 tiến trình.** `app.New` log cảnh báo liệt kê
đúng 4 store này khi khởi động ở chế độ Postgres. Chi tiết: [05](05-doi-chieu-spec-code.md).

---

## 1.4 Luồng chạy: vòng đời một chuyến đi

```
 KHÁCH                       API                     WORKER (in-process)          TÀI XẾ
   │                          │                              │                      │
   │ POST /v1/auth/otp        │                              │                      │
   │ POST /v1/auth/verify ───►│ identity: OTP → JWT          │                      │
   │                          │                              │                      │
   │ POST /v1/quotes ────────►│ pricing.Estimate             │                      │
   │◄─── quote (TTL 5 phút)   │  RouteEngine → surge → tariff│                      │
   │                          │                              │                      │
   │ POST /v1/trips ─────────►│ trip.Create                  │                      │
   │  Idempotency-Key: ...    │  CREATED → SEARCHING         │                      │
   │◄─── trip                 │  publish trip.requested ────►│ onTripRequested      │
   │                          │                              │ go Matcher.Dispatch  │
   │                          │                              │                      │
   │                          │            vòng 0..2:        │                      │
   │                          │   bán kính 1500/3000/4500m   │                      │
   │                          │   lọc → chấm điểm → top 5    │                      │
   │                          │   SaveOffers ────────────────┼──► offer.created ───►│ GET /v1/offers
   │                          │   chờ OfferTTL = 15s         │                      │
   │                          │                              │                      │
   │                          │◄─────────────────────────────┼──────────────────────│ POST /v1/offers/{id}/accept
   │                          │ matching.Accept:             │                      │
   │                          │  1. ClaimTrip (nguyên tử)    │                      │
   │                          │  2. drivers.Reserve (CAS)    │                      │
   │                          │  3. trips.Assign             │                      │
   │                          │  4. ExpireOffers (còn lại)   │                      │
   │◄─── trip ASSIGNED        │  publish trip.assigned       │                      │
   │                          │                              │                      │
   │                          │◄─────────────────────────────┼──────────────────────│ POST .../arrived
   │                          │◄─────────────────────────────┼──────────────────────│ POST .../start
   │                          │  publish trip.started ──────►│ driver → ON_TRIP     │
   │                          │◄─────────────────────────────┼──────────────────────│ POST .../complete
   │                          │  publish trip.completed ────►│ onTripCompleted:     │
   │                          │                              │  wallet.SettleTrip   │
   │                          │                              │  trips.MarkPaid      │
   │                          │                              │  driver → IDLE       │
   │                          │                              │  publish payment.settled
```

Không tài xế nào nhận sau 3 vòng → `Matcher.Dispatch` gọi `trips.Expire` → `EXPIRED`.

### Nơi luồng này còn hở

| Bước | Vấn đề | Mã |
|---|---|---|
| `offer.created` | **không ai subscribe** — tài xế phải poll `GET /v1/offers` | [G-11](05-doi-chieu-spec-code.md#g-11) |
| ~~`trip.cancelled`~~ | ✅ GĐ 1: `onTripCancelled` ghi sổ phí huỷ **và** trả tài xế về `IDLE` | [G-05](05-doi-chieu-spec-code.md#g-05) |
| ~~`drivers.wallet_balance` không đồng bộ~~ | ✅ GĐ 1: sự kiện mới `wallet.balance_changed` | [G-03](05-doi-chieu-spec-code.md#g-03) |
| ~~`Dispatch` goroutine~~ | ✅ đã bọc `safego.Recover`, panic không còn giết tiến trình | [G-14](05-doi-chieu-spec-code.md#g-14) |

---

## 1.5 Sự kiện

`internal/platform/eventbus` — interface `Bus`, bản hiện tại in-memory ([bus.go:63](../godrive/internal/platform/eventbus/bus.go#L61)).

> **Cập nhật GĐ 2:** sự kiện nay đi qua **Transactional Outbox** ở chế độ Postgres — `trip.Save`
> ghi chúng vào bảng `outbox` trong cùng transaction với thay đổi nghiệp vụ, relay quét mỗi 200ms
> và phát lên bus. Ngữ nghĩa chuyển từ **at-most-once** sang **at-least-once**.

| Topic | Publisher | Subscriber hiện có |
|---|---|---|
| `trip.requested` | `trip.Create` | `app.onTripRequested` → spawn dispatch · `app.onTripRequestedSurge` → đếm cầu |
| `trip.assigned` | `trip.Assign` | `app.syncDriverStatus` |
| `trip.started` | `trip.Start` | `app.syncDriverStatus` |
| `trip.completed` | `trip.Complete` | `app.onTripCompleted` → ghi sổ + MarkPaid + thống kê + đồng bộ trạng thái |
| `trip.cancelled` | `trip.Cancel` | `app.onTripCancelled` → ghi sổ phí huỷ + trả tài xế về IDLE |
| `driver.online` / `driver.offline` | `driver.GoOnline/GoOffline` | **(không)** |
| `offer.created` | `matching.DispatchRound` | `app.onOfferStat` → mẫu số tỉ lệ nhận |
| `offer.accepted` | `matching.Accept` | `app.onOfferStat` → tử số tỉ lệ nhận |
| `payment.settled` | `wallet.SettleTrip` | **(không)** |
| `trip.rated` | `trip.Rate` | `app.onTripRated` → cộng điểm đánh giá |
| `wallet.balance_changed` | `wallet.SettleTrip` / `TopUp` / `PostCancelFee` | `app.onWalletBalanceChanged` → đồng bộ cột cache `drivers.wallet_balance` |

### Ngữ nghĩa giao hàng hiện tại: **at-most-once**

`inMemory.Publish` spawn một goroutine cho mỗi handler; handler lỗi **chỉ được log rồi bỏ qua** —
không retry, không DLQ, không backoff ([bus.go:81](../godrive/internal/platform/eventbus/bus.go#L84)).

> Với `trip.completed` điều này nghĩa là: **một lần `SettleTrip` lỗi = chuyến đó không bao giờ được ghi sổ.**
> Đây là lý do `outbox` tồn tại trong repo — nhưng nó **chưa được nối** ([G-06](05-doi-chieu-spec-code.md#g-06)).

---

## 1.6 Tiến trình và triển khai

Hai binary, nhưng **cả hai đều gọi `app.New()` đầy đủ**:

| Binary | Chạy gì | Vấn đề |
|---|---|---|
| [`cmd/api`](../godrive/cmd/api/main.go) | HTTP server **+ `StartWorkers`** | ổn ở chế độ 1 tiến trình |
| [`cmd/worker`](../godrive/cmd/worker/main.go) | `StartWorkers` + `outbox.Relay` | **bus riêng, state riêng** |

> **Hệ quả:** chạy `cmd/api` và `cmd/worker` song song ở chế độ Postgres thì worker **không nhận được
> sự kiện nào** từ API (bus là in-process), còn relay thì đọc `outbox.NewMemoryStore()` mới toanh mà
> không ai ghi vào ([worker/main.go:40](../godrive/cmd/worker/main.go#L40)).
> Tách tiến trình chỉ có ý nghĩa sau khi thay `eventbus` bằng NATS. Xem [06 — Giai đoạn 2](06-ke-hoach-trien-khai.md).

**Hiện tại chỉ nên chạy `cmd/api` (đã bao gồm worker).**

---

## 1.7 Lộ trình tách microservice

Vì Port đã sạch, tách service = thay implementation, không sửa nghiệp vụ:

| Thứ tự tách | Module | Đổi cái gì |
|---|---|---|
| 1 | `location` | `location.Index` → Redis GEO client; `Nearby` thành gRPC |
| 2 | `matching` | `matching.Store` → Redis; `Engine` thành service riêng, đọc `trip.requested` từ NATS |
| 3 | `pricing` | `RouteEngine` → OSRM client; quote store → Redis |
| 4 | `wallet` | tách sau cùng — cần transaction chung với `trip` cho tới khi có outbox hoàn chỉnh |

`identity`, `driver`, `trip` nên **ở lại monolith lâu nhất có thể**: chúng chia sẻ transaction Postgres.
