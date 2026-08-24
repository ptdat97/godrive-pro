# GoDrive — Implementation Spec cho Claude Code

> **Mục đích tài liệu:** Đặc tả kỹ thuật mô tả **codebase `godrive` hiện tại**, dùng làm nguồn tham chiếu chính khi mở rộng/sửa đổi. Mọi tên module, tên bảng, tên trạng thái và công thức trong tài liệu này **đã được đối chiếu với code thật** — nếu code và tài liệu lệch nhau, code là chuẩn và tài liệu phải được cập nhật.
>
> **Trạng thái repo:** modular monolith, 1 module Go (`github.com/example/godrive`), Go 1.22, **zero external dependency** (chỉ stdlib), ~5.650 dòng / 61 files. Chạy được ngay bằng `make run` ở chế độ in-memory — không cần Postgres/Redis/NATS.
>
> **Bối cảnh thị trường:** thanh toán tiền mặt chiếm tỉ trọng lớn, Nghị định 10/2020 (hợp đồng vận tải điện tử) và 13/2023 (dữ liệu cá nhân), máy Android giá rẻ + 4G chập chờn, chi phí Maps API cao.

---

## 0. Nguyên tắc triển khai (đọc trước khi code)

1. **Không phá vỡ module boundary.** Mỗi module trong `internal/` giao tiếp với module khác **chỉ qua interface**, hoặc qua event bus — không import struct nội bộ của module khác.
2. **Interface do bên tiêu thụ định nghĩa (consumer-defined ports).** `matching.DriverPort` nằm trong package `matching`, không nằm trong `driver`. Nhờ vậy phụ thuộc chỉ đi một chiều và mock test không cần package trung gian. Đây là quy ước bắt buộc, không phải gợi ý.
3. **Domain logic tách khỏi HTTP.** Handler (`internal/<module>/http.go`) chỉ parse request → gọi service → serialize response. Business logic nằm trong `service.go` / `domain.go`.
4. **Mọi thay đổi tiền phải qua sổ cái kép.** Không bao giờ update số dư trực tiếp. Xem Section 4.
5. **`trip_events` là append-only.** Không có `UPDATE`/`DELETE`.
6. **Tiền là `money.VND` (int64 đồng). Không dùng float cho tiền** — kể cả biến tạm. Nhân tỉ lệ bằng `MulPermille`.
7. **Mọi thao tác tạo/huỷ/thanh toán phải idempotent.** App mobile ở VN retry rất nhiều vì mạng chập chờn.
8. **Viết test trước khi merge**: tối thiểu 1 happy path + 1 edge case cho mỗi service method mới.
9. **Mỗi Phase kết thúc bằng "Acceptance Criteria" ở cuối section — phải tự verify (chạy `make test`, gọi thử API) trước khi báo hoàn thành.**

---

## 1. Kiến trúc tổng thể

```
                        ┌──────────────┐
                        │  HTTP Router │  (rate limit, JWT auth, request id, recover)
                        └──────┬───────┘
       ┌──────────┬───────────┼───────────┬──────────┬──────────┐
  ┌────▼─────┐ ┌──▼─────┐ ┌───▼──────┐ ┌──▼─────┐ ┌──▼─────┐ ┌──▼─────┐
  │ identity │ │ driver │ │ location │ │pricing │ │  trip  │ │matching│
  └────┬─────┘ └──┬─────┘ └───┬──────┘ └──┬─────┘ └──┬─────┘ └──┬─────┘
       │          │           │           │          │          │
       └──────────┴───────────┴─────┬─────┴──────────┴──────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
           ┌─────▼──────┐    ┌──────▼──────┐   ┌───────▼──────┐
           │  wallet    │    │  eventbus   │   │   outbox     │
           │ (sổ cái)   │    │ (in-memory) │   │ (relay)      │
           └────────────┘    └─────────────┘   └──────────────┘
```

**Cấu trúc thư mục thật:**

```
cmd/
  api/            HTTP API (+ worker ở chế độ dev)
  worker/         dispatcher, outbox relay, đối soát
internal/
  config/         cấu hình từ biến môi trường (12-factor)
  app/            composition root: lắp ráp, router, worker
  platform/
    logger/       slog
    httpx/        JSON, mã lỗi, middleware, rate limit
    authn/        phát hành/xác thực JWT HS256, middleware phân quyền
    eventbus/     interface Bus (in-memory; thay bằng NATS JetStream)
  identity/       OTP theo số điện thoại VN, tài khoản
  driver/         hồ sơ, eKYC, trạng thái online (CAS chống nhận 2 chuyến)
  location/       ingest ping, chỉ mục ô lưới, phát hiện GPS giả
  pricing/        biểu giá, phụ phí đêm, surge, báo giá
  trip/           máy trạng thái + nhật ký sự kiện bất biến
  matching/       chấm điểm, chào mời theo lô, giành khoá chuyến
  wallet/         sổ cái kép, công nợ tiền mặt, khấu trừ thuế
  notification/   FCM / Zalo ZNS / SMS (interface)
  outbox/         Transactional Outbox + relay
  admin/          API vận hành: tổng hợp, lọc, duyệt hồ sơ, bản đồ trực tuyến
pkg/
  money/          VND kiểu int64
  geo/            toạ độ, haversine, bearing, lưới ô (chỗ thay H3)
  errs/           lỗi nghiệp vụ có mã ổn định cho app mobile
  id/             ID sắp xếp được theo thời gian (thân thiện B-tree)
  idem/           khoá idempotency
  clock/          đồng hồ tiêm được (test tất định)
migrations/       SQL cho Postgres + PostGIS
deploy/           Dockerfile, docker-compose (Postgres, Redis, NATS, EMQX, OSRM)
```

**Composition root** là [`internal/app/app.go`](godrive/internal/app/app.go). `app.New()` chọn repo in-memory hay Postgres dựa vào `cfg.InMemory()` (tức `DATABASE_URL` rỗng hay không). Không module nào tự khởi tạo dependency của mình.

**Lộ trình tách microservice:** `matching` và `location` là 2 module cần tách sớm nhất khi tải tăng. Vì các Port đã sạch, việc tách là thay implementation bằng gRPC client — code nghiệp vụ không đổi.

---

## 2. Data Model (Postgres — `migrations/0001_init.up.sql`)

> **Quy ước PK:** dùng `TEXT` chứa ID sắp xếp được theo thời gian (`pkg/id`, dạng `trp_...`, `drv_...`, `ofr_...`), **không dùng UUID v4**. Lý do: UUID ngẫu nhiên làm phân mảnh B-tree khi insert nhiều; ID theo thời gian giữ locality và dễ đọc log.
>
> **Quy ước thời gian:** bảng tài chính (`ledger_entries`) và nhật ký (`trip_events`) chỉ có `created_at`/`at`, không có `updated_at`/`deleted_at`. Bảng trạng thái động (`drivers`, `trips`) có `updated_at` + cột `version` cho optimistic lock.

### 2.1 Tài khoản & tài xế

```sql
CREATE TABLE accounts (
    id          TEXT PRIMARY KEY,
    phone       TEXT        NOT NULL,
    full_name   TEXT        NOT NULL DEFAULT '',
    role        TEXT        NOT NULL CHECK (role IN ('rider','driver','admin')),
    blocked     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (phone, role)
);
```

`drivers` gộp cả thông tin xe (không có bảng `vehicles` riêng — một tài xế một xe ở giai đoạn này):

```sql
CREATE TABLE drivers (
    id              TEXT PRIMARY KEY,
    account_id      TEXT        NOT NULL UNIQUE REFERENCES accounts(id),
    full_name       TEXT        NOT NULL,
    phone           TEXT        NOT NULL,
    city            TEXT        NOT NULL DEFAULT 'HCM',

    vehicle_type    TEXT        NOT NULL CHECK (vehicle_type IN ('BIKE','CAR_4','CAR_7')),
    vehicle_plate   TEXT        NOT NULL,
    vehicle_model   TEXT        NOT NULL DEFAULT '',
    vehicle_color   TEXT        NOT NULL DEFAULT '',

    -- Giấy tờ bắt buộc theo Nghị định 10/2020. Cân nhắc mã hoá ở tầng ứng dụng.
    national_id     TEXT        NOT NULL,
    driver_license  TEXT        NOT NULL,
    vehicle_reg_no  TEXT        NOT NULL DEFAULT '',
    kyc_state       TEXT        NOT NULL DEFAULT 'PENDING'
                                CHECK (kyc_state IN ('PENDING','APPROVED','REJECTED')),

    status          TEXT        NOT NULL DEFAULT 'OFFLINE'
                                CHECK (status IN ('OFFLINE','IDLE','ASSIGNED','ON_TRIP','SUSPENDED')),
    rating          NUMERIC(3,2) NOT NULL DEFAULT 5.00,
    completed_trips INTEGER     NOT NULL DEFAULT 0,
    acceptance_rate NUMERIC(4,3) NOT NULL DEFAULT 0.800,
    cancel_rate     NUMERIC(4,3) NOT NULL DEFAULT 0.000,

    -- Số dư ví tính bằng ĐỒNG (BIGINT). Âm = tài xế đang nợ chiết khấu.
    wallet_balance  BIGINT      NOT NULL DEFAULT 0,
    version         INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX drivers_plate_uidx ON drivers (vehicle_plate);
-- Index một phần: dispatcher chỉ quan tâm tài xế đang rảnh.
CREATE INDEX drivers_idle_idx ON drivers (city, vehicle_type) WHERE status = 'IDLE';
```

Vị trí nóng ghi rất nhiều — nguồn nhanh thực tế là Redis GEO, Postgres chỉ giữ ảnh chụp mới nhất cho phân tích và khôi phục sự cố:

```sql
CREATE TABLE driver_locations (
    driver_id   TEXT PRIMARY KEY REFERENCES drivers(id),
    geom        GEOGRAPHY(POINT, 4326) NOT NULL,
    bearing_deg REAL        NOT NULL DEFAULT 0,
    speed_mps   REAL        NOT NULL DEFAULT 0,
    battery_pc  SMALLINT    NOT NULL DEFAULT 100,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX driver_locations_geom_idx ON driver_locations USING GIST (geom);
```

### 2.2 Chuyến đi (nhật ký append-only — Nghị định 10/2020)

```sql
CREATE TABLE trips (
    id              TEXT PRIMARY KEY,
    rider_id        TEXT        NOT NULL REFERENCES accounts(id),
    driver_id       TEXT        REFERENCES drivers(id),

    pickup_lat      DOUBLE PRECISION NOT NULL,
    pickup_lng      DOUBLE PRECISION NOT NULL,
    pickup_address  TEXT        NOT NULL DEFAULT '',
    pickup_note     TEXT        NOT NULL DEFAULT '',   -- "hẻm 123, cổng màu xanh"
    dropoff_lat     DOUBLE PRECISION NOT NULL,
    dropoff_lng     DOUBLE PRECISION NOT NULL,
    dropoff_address TEXT        NOT NULL DEFAULT '',
    dropoff_note    TEXT        NOT NULL DEFAULT '',

    vehicle_type    TEXT        NOT NULL,
    quote_id        TEXT        NOT NULL,
    fare            BIGINT      NOT NULL,
    platform_fee    BIGINT      NOT NULL,
    driver_earn     BIGINT      NOT NULL,
    payment_method  TEXT        NOT NULL
                                CHECK (payment_method IN ('CASH','MOMO','ZALOPAY','VNPAY','WALLET')),

    status          TEXT        NOT NULL
                                CHECK (status IN ('CREATED','SEARCHING','ASSIGNED','ARRIVED',
                                                  'IN_PROGRESS','COMPLETED','PAID','CANCELLED','EXPIRED')),
    cancel_by       TEXT,
    cancel_reason   TEXT        NOT NULL DEFAULT '',

    requested_at    TIMESTAMPTZ NOT NULL,
    assigned_at     TIMESTAMPTZ,
    arrived_at      TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    version         INTEGER     NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX trips_searching_idx ON trips (requested_at) WHERE status = 'SEARCHING';
-- Chốt chặn ở tầng DB: một tài xế chỉ được có tối đa một chuyến đang hoạt động.
CREATE UNIQUE INDEX trips_one_active_per_driver
    ON trips (driver_id)
    WHERE status IN ('ASSIGNED','ARRIVED','IN_PROGRESS');
```

**`trip_events` ghi chuyển trạng thái, không ghi location ping.** Location ping đi vào `driver_locations` / Redis, không làm phình bảng nhật ký pháp lý:

```sql
CREATE TABLE trip_events (
    id          TEXT PRIMARY KEY,
    trip_id     TEXT        NOT NULL REFERENCES trips(id),
    from_status TEXT        NOT NULL,
    to_status   TEXT        NOT NULL,
    actor       TEXT        NOT NULL,   -- account id hoặc "system"
    meta        JSONB       NOT NULL DEFAULT '{}',
    at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX trip_events_trip_idx ON trip_events (trip_id, at);
```

Lưu tối thiểu **3 năm** — đây là hợp đồng vận tải điện tử theo Nghị định 10/2020 và Thông tư 12/2020.

### 2.3 Ghép chuyến

```sql
CREATE TABLE offers (
    id                TEXT PRIMARY KEY,
    trip_id           TEXT        NOT NULL REFERENCES trips(id),
    driver_id         TEXT        NOT NULL REFERENCES drivers(id),
    round             SMALLINT    NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL DEFAULT 'PENDING'
                                  CHECK (status IN ('PENDING','ACCEPTED','REJECTED','EXPIRED','LOST')),
    eta_sec           REAL        NOT NULL DEFAULT 0,
    pickup_distance_m REAL        NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX offers_driver_pending_idx ON offers (driver_id) WHERE status = 'PENDING';
-- Chốt chặn cuối cùng: mỗi chuyến chỉ có duy nhất một lời mời được chấp nhận.
CREATE UNIQUE INDEX offers_one_accepted_per_trip ON offers (trip_id) WHERE status = 'ACCEPTED';
```

### 2.4 Sổ cái kép

**Khác biệt quan trọng so với mô hình debit/credit cổ điển:** không có cột `direction`, không có bảng `ledger_accounts`. Một bút toán là `amount_vnd BIGINT` **có dấu** — dương = ghi nợ vào tài khoản, âm = ghi có. Bất biến: **tổng `amount_vnd` của mọi bút toán cùng `tx_id` phải bằng 0**.

```sql
CREATE TABLE ledger_entries (
    id           TEXT PRIMARY KEY,
    tx_id        TEXT        NOT NULL,
    account_id   TEXT        NOT NULL,   -- driverID / riderID / "platform"
    account_type TEXT        NOT NULL
                             CHECK (account_type IN ('DRIVER_WALLET','DRIVER_CASH','RIDER_WALLET',
                                                     'PLATFORM_REVENUE','PROMO_EXPENSE','TAX_PAYABLE',
                                                     'GATEWAY_CLEARING')),
    amount_vnd   BIGINT      NOT NULL,
    ref_type     TEXT        NOT NULL,   -- TRIP|TOPUP|PAYOUT|ADJUSTMENT|CANCEL_FEE
    ref_id       TEXT        NOT NULL,
    memo         TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ledger_tx_idx      ON ledger_entries (tx_id);
CREATE INDEX ledger_account_idx ON ledger_entries (account_id, account_type, created_at);

-- Khoá idempotency cho giao dịch: chống ghi sổ hai lần khi worker retry.
CREATE TABLE ledger_transactions (
    tx_id      TEXT PRIMARY KEY,
    ref_type   TEXT        NOT NULL,
    ref_id     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Số dư **luôn tính bằng `SUM(amount_vnd)`** theo `(account_id, account_type)`, không có bảng balance cập nhật trực tiếp. Cột `drivers.wallet_balance` chỉ là bản cache để đọc nhanh khi chặn nhận chuyến — nguồn sự thật là `ledger_entries`.

### 2.5 Hạ tầng ứng dụng

```sql
CREATE TABLE idempotency_keys (
    key        TEXT PRIMARY KEY,
    response   BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

-- Transactional Outbox: sự kiện ghi cùng transaction nghiệp vụ, relay publish sau.
CREATE TABLE outbox (
    id           TEXT PRIMARY KEY,
    topic        TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    attempts     SMALLINT    NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
```

**Redis keys** (chưa implement, ghi chú cho giai đoạn thay hạ tầng):
- `driver:location:{driver_id}` → GEO entry, TTL 60s, cập nhật mỗi ping MQTT
- `trip:{id}:claim` → `SET NX EX 30`, dùng cho `matching.Store.ClaimTrip`
- `driver:online:{driver_id}` → hash trạng thái/last_ping, detect mất kết nối

---

## 3. Module: Matching (ghép chuyến)

### 3.1 Chiến lược: broadcast theo lô có chấm điểm

**Không phải batch optimal assignment.** Mỗi chuyến chạy một chu trình dispatch độc lập trong goroutine riêng. Lựa chọn này có chủ đích: đơn giản, độ trễ thấp, không cần đợi cửa sổ gom lô — đánh đổi là không tối ưu toàn cục khi mật độ cao.

```
Rider tạo chuyến (CREATED → SEARCHING)
   → publish trip.requested
   → worker onTripRequested spawn goroutine Matcher.Dispatch(tripID)
   → mỗi vòng (tối đa MaxRounds = 3):
       - bán kính = InitialRadiusM + round × RadiusStepM, chặn trên MaxRadiusM
       - lọc ứng viên: đúng loại xe, status IDLE, pin ≥ MinBatteryPc, ping còn tươi
       - loại tài xế không đủ điều kiện (CanAcceptTrip: KYC, suspended, nợ ví)
       - chấm điểm + sort, gửi offer cho BatchSize tài xế điểm tốt nhất
       - chờ OfferTTL (15s); nếu không có ứng viên thì chờ 2s rồi thử vòng sau
       - nếu trip.status ≠ SEARCHING → dừng (đã ghép hoặc đã huỷ)
   → hết MaxRounds vẫn SEARCHING → trip.Expire() → EXPIRED
```

Tham số mặc định (`matching.DefaultConfig()`, [domain.go](godrive/internal/matching/domain.go)):

| Tham số | Giá trị | Ghi chú |
|---|---|---|
| `InitialRadiusM` | 1500 | bán kính vòng đầu |
| `RadiusStepM` | 1500 | nới mỗi vòng |
| `MaxRadiusM` | 5000 | trần |
| `MaxRounds` | 3 | hết vòng → EXPIRED |
| `BatchSize` | 5 | số offer gửi song song mỗi vòng |
| `OfferTTL` | 15s | offer hết hạn |
| `MinBatteryPc` | 15 | loại máy sắp hết pin |

### 3.2 Hàm chấm điểm

**Điểm là điểm PHẠT — càng THẤP càng được ưu tiên.** Đơn vị quy đổi là giây, nên mọi trọng số đọc được trực tiếp ("chênh 1 sao ≈ 60 giây ETA").

```go
// internal/matching/scoring.go
điểm = WeightETA        × ETA_giây              // 1.0
     + WeightRating     × (5 − rating)          // 60.0  → chênh 1 sao ≈ 60s
     + WeightAcceptance × (1 − acceptance_rate) // 90.0  → hay bỏ chuyến bị phạt nặng
     − WeightIdle       × idle_giây             // 0.25  → chờ lâu được ưu tiên
     + WeightHeading    × góc_lệch_hướng_độ     // 0.20  → chỉ áp dụng khi cách >100m
```

- **Không chỉ chọn tài xế gần nhất.** Tài xế gần nhưng hay bỏ chuyến làm khách chờ lâu hơn tài xế xa mà luôn nhận.
- **Trọng số thời gian chờ** phân bổ thu nhập đều hơn — yếu tố giữ chân tài xế quan trọng ở VN.
- **Trọng số hướng xe** phạt xe đang chạy ngược hướng điểm đón (phải quay đầu). Bỏ qua khi khoảng cách ≤ 100m vì bearing lúc đó nhiễu.
- Sort có **tie-break theo `DriverID`** để test tất định.
- Toàn bộ trọng số nằm trong `matching.Config`, **không hardcode** — xem Section 7.

`ETASec` lấy từ `ETAEngine`. Bản hiện tại `SimpleETA` = haversine × 1.35 ÷ 18km/h (tốc độ xe máy giờ cao điểm TP.HCM). Production: OSRM `/table` — **một request cho cả lô ứng viên**, cache theo cặp ô lưới.

### 3.3 Chống ghép trùng — hai lớp

Đây là bất biến quan trọng nhất của module. Thứ tự trong `Engine.Accept()` không được đổi:

1. **`Store.ClaimTrip(tripID, driverID, 30s)`** — nguyên tử. In-memory dùng mutex; production dùng Redis `SET trip:{id}:claim NX EX 30`. Thua → offer chuyển `LOST`, trả lỗi `trip_taken`.
2. **`drivers.Reserve(driverID)`** — CAS `WHERE status='IDLE' AND version=$N` trong Postgres. Thất bại → offer chuyển `REJECTED`.
3. **`trips.Assign()`** — nếu lỗi thì rollback tài xế về `IDLE`.
4. **Unique partial index** `offers_one_accepted_per_trip` — chốt chặn cuối ở tầng DB.

Giành khoá chuyến **TRƯỚC**, rồi mới giữ chỗ tài xế. Test `TestOnlyOneDriverWinsTrip` chạy hai tài xế bấm nhận song song dưới `-race`.

### 3.4 Surge Pricing

Tính **theo yêu cầu (on-demand) lúc báo giá**, không phải worker định kỳ, và không có bảng `surge_zones` — trạng thái nằm in-memory trong `pricing.DemandSurge`.

```
demand = số request trong ô lưới, cửa sổ trượt 5 phút
supply = số tài xế IDLE trong bán kính 2000m (từ chỉ mục vị trí)
ratio  = demand / max(supply, 1)

ratio ≥ 4.0 → 2.0
ratio ≥ 3.0 → 1.7
ratio ≥ 2.0 → 1.4
ratio ≥ 1.2 → 1.2
còn lại     → 1.0
```

Hàm bậc thang, **không phải hàm liên tục** — dễ giải thích cho vận hành và không nhạy với nhiễu. Trần 2.0 được clamp **hai lần**: ở bảng bậc thang và lại ở `pricing.Service.Estimate()` (phòng khi thay `SurgeProvider` khác).

**Acceptance Criteria — Module Matching:**
- [ ] Không tài xế nào bị ghép 2 chuyến cùng lúc — test hai tài xế accept song song dưới `-race` (`TestOnlyOneDriverWinsTrip`)
- [ ] Offer hết hạn và nới bán kính hoạt động đúng — test simulate không ai phản hồi, verify chuyến về `EXPIRED` sau `MaxRounds`
- [ ] Tài xế nợ quá `DefaultDebtLimit` hoặc KYC chưa duyệt không lọt vào candidate list
- [ ] Surge multiplier không bao giờ vượt 2.0 — **cần test boundary tại ratio ≥ 4 và test clamp ở `Estimate`**
- [ ] Chấm điểm tất định — cùng input cho ra cùng thứ tự

---

## 4. Module: Pricing & Wallet

### 4.1 Tính giá

Chia làm hai tầng, ranh giới rõ ràng:

- **`computeBase(tariff, route)` — pure function** ([pricing/service.go](godrive/internal/pricing/service.go)), không I/O, không side-effect. Đây là phần phải test kỹ vì dùng để audit khi khách khiếu nại giá.
- **`Service.Estimate()`** — orchestration: gọi `RouteEngine`, `SurgeProvider`, lưu quote vào store. Có I/O, không phải pure.

```
base     = OpeningFare + (distance − OpeningMeter)/1000 × PerKm + duration/60 × PerMinute
night    = base × NightSurchargePermille/1000     nếu 22:00–05:00 giờ VN (UTC+7)
subtotal = (base + night) × surge_multiplier      clamp surge vào [1.0, 2.0]
subtotal = max(subtotal, MinFare)
total    = RoundTo(subtotal, 1000)                giá cước VN tròn nghìn
fee      = total × PlatformFeePermille/1000
earn     = total − fee
```

Biểu giá mẫu (`DefaultTariffs()`, TP.HCM — **giá thật phải khớp hồ sơ kê khai giá cước**):

| Loại xe | Mở cửa (2km đầu) | /km | /phút | Chiết khấu |
|---|---|---|---|---|
| `BIKE` | 12.000đ | 4.300đ | 300đ | 20% |
| `CAR_4` | 29.000đ | 9.500đ | 600đ | 25% |
| `CAR_7` | 34.000đ | 11.500đ | 700đ | 25% |

Quote có TTL 5 phút (`QuoteTTL`); `Trip.Create` từ chối quote hết hạn.

### 4.2 Sổ cái kép khi chuyến hoàn tất

Ghi sổ **không nằm trong request path**. `Trip.Complete()` chỉ publish `trip.completed`; worker `onTripCompleted` mới ghi sổ — tránh giữ transaction dài.

Chuyến 50.000đ **tiền mặt**, chiết khấu 20% (`SettleCashTrip`):

| Tài khoản | Số tiền |
|---|---|
| `DRIVER_CASH` (tài xế cầm tiền khách) | +50.000 |
| `PLATFORM_REVENUE` | −50.000 |
| `DRIVER_WALLET` (trừ chiết khấu) | −10.000 |
| `PLATFORM_REVENUE` | +10.000 |
| **Tổng** | **0** |

Ví tài xế còn **−10.000đ** — đó chính là công nợ. Khi nợ vượt `driver.DefaultDebtLimit`, `Driver.CanAcceptTrip` chặn nhận chuyến cho tới khi nạp lại qua VietQR/MoMo/ZaloPay. **Đây là mô hình bắt buộc từ Phase 1, không phải tính năng phụ** — tiền mặt vẫn chiếm tỉ trọng rất lớn ở VN.

Chuyến trả **online** (`SettleOnlineTrip`): `GATEWAY_CLEARING −fare`, `DRIVER_WALLET +earn`, `PLATFORM_REVENUE +fee`.

Các giao dịch khác đã có: `TopUp`, `Payout`, `CancelFee`, `WithholdTax`.

**Bất biến:** `Transaction.Validate()` từ chối giao dịch có < 2 bút toán hoặc tổng ≠ 0. Gọi trước mọi lần `Post`.

**Idempotency:** `TxID` suy ra tất định từ nghiệp vụ (`"tx_trip_" + tripID`), `Service.SettleTrip` kiểm tra `ledger.Exists(txID)` trước khi ghi. Worker retry bao nhiêu lần cũng chỉ ghi một lần.

### 4.3 Khấu trừ thuế

`WithholdTax` tách VAT + TNCN tại nguồn trên thu nhập tài xế: `DRIVER_WALLET −tax`, `TAX_PAYABLE +tax`. `Service.TaxPermille` **mặc định 0 (tắt)**; đặt 45 = 4,5% (3% GTGT + 1,5% TNCN) cho cá nhân kinh doanh vận tải — **cần đối chiếu lại với kế toán thuế trước khi bật.**

### 4.4 Huỷ chuyến

Khách huỷ trong `FreeCancelWindow` (2 phút sau khi ghép) → miễn phí. Quá cửa sổ → `CancelFeeVND` (10.000đ) ghi có cho tài xế qua `wallet.CancelFee`.

**Acceptance Criteria — Pricing & Wallet:**
- [ ] `computeBase` có test cho: cự ly ngắn dưới giá mở cửa, cự ly dài, surge = 2.0, giờ đêm, làm tròn nghìn
- [ ] Không test case nào tạo được `Transaction` lệch mà `Validate()` bỏ qua — test phải fail nếu bug này xảy ra
- [ ] `SettleTrip` gọi 2 lần chỉ ghi sổ 1 lần (test idempotent)
- [ ] Tài xế nợ vượt hạn mức bị `CanAcceptTrip` chặn (`TestCashSettlementBlocksIndebtedDriver`)
- [ ] Không có phép chia float nào trên đường đi của tiền

---

## 5. Module: Trip & Location

### 5.1 Máy trạng thái

```
CREATED → SEARCHING → ASSIGNED → ARRIVED → IN_PROGRESS → COMPLETED → PAID
   ↓          ↓           ↓          ↓
CANCELLED  CANCELLED  CANCELLED  CANCELLED
              ↓
           EXPIRED (không tìm được tài xế)
```

Đồ thị là **dữ liệu** (`transitions map[Status][]Status`), không phải if-else rải rác. Transition không hợp lệ trả `errs.Conflict("invalid_transition", ...)`.

**`IN_PROGRESS → CANCELLED` bị chặn có chủ đích.** Đang chở khách thì không có khái niệm huỷ; sự cố giữa chuyến là một luồng riêng (kết thúc sớm + hoàn tiền) chưa implement.

Mọi lần chuyển gọi qua `Service.apply()` → `repo.Save(trip, event)` — **trip và event ghi trong CÙNG một transaction, bắt buộc**. Không bao giờ update `status` mà không ghi event tương ứng.

`Trip.Create` idempotent theo header `Idempotency-Key` (TTL 24h): retry trả về đúng chuyến đã tạo thay vì tạo chuyến thứ hai.

### 5.2 Location

Luồng production: app tài xế → MQTT (EMQX) topic `drv/{id}/loc` QoS 1 → consumer gọi `Service.Ingest` → `Index` (Redis GEO / H3) + publish sự kiện. **MQTT thay vì WebSocket** vì tiết kiệm pin và băng thông trên Android giá rẻ, mạng 4G chập chờn — có QoS và Last Will để phát hiện mất kết nối.

`Filter` loại ứng viên theo loại xe, status, pin tối thiểu, và **độ tươi của ping** (`FreshWithin` / `location.StaleAfter`) — tài xế mất mạng không được nhận offer.

**Phát hiện gian lận** ([location/fraud.go](godrive/internal/location/fraud.go)) — 3 loại cờ:
- `MOCK_LOCATION` — `Ping.Mocked` từ `Location.isFromMockProvider` (Android). Đây là hình thức gian lận phổ biến nhất: tài xế "dịch chuyển" tới điểm đón để nhận chuyến.
- `TELEPORT` — nhảy vị trí bất khả thi
- `SPEED_OUTLIER` — tốc độ vượt ngưỡng

Hiện gom cờ in-memory. Production: đẩy sang stream riêng (Kafka → hệ thống risk), tự động khoá khi vượt ngưỡng.

**Acceptance Criteria — Trip & Location:**
- [ ] Transition không hợp lệ (vd `COMPLETED → ASSIGNED`, `IN_PROGRESS → CANCELLED`) bị reject với mã lỗi rõ ràng (`statemachine_test.go`)
- [ ] `trip_events` không bao giờ bị update/delete — verify qua DB role permission
- [ ] Trip + event luôn cùng transaction — test rollback khi ghi event lỗi
- [ ] `Create` với cùng `Idempotency-Key` hai lần chỉ tạo một chuyến
- [ ] Ping `Mocked=true` bị gắn cờ và không lọt vào chỉ mục

---

## 5c. Module: Admin (API vận hành)

Phục vụ bảng điều khiển nội bộ ([godrive-admin](godrive-admin), Next.js). **Toàn bộ logic nằm ở Go** — giao diện chỉ gọi endpoint và hiển thị. Quy tắc: nếu một câu hỏi có thể trả lời sai thì câu trả lời phải đến từ backend.

**Consumer-defined ports.** `admin` khai báo `DriverPort`/`TripPort`/`LocationPort`/`WalletPort` trong package của chính nó; adapter nối ở tầng lắp ráp ([app/admin.go](godrive/internal/app/admin.go)). Module khác không phải sửa gì vì admin.

**Chỉ một hành động ghi:** `ReviewKYC`. Mọi thay đổi khác phải đi qua module sở hữu nghiệp vụ.

### Cổng đăng nhập riêng — lý do bảo mật

Luồng `/v1/auth/*` cấp token theo `role` mà client gửi lên. Đúng với rider/driver (ai cũng đăng ký được), nhưng nếu dùng chung cho admin thì **chỉ cần gửi `role=admin` là leo thang đặc quyền**.

`/v1/admin/auth/*` kiểm tra danh sách `ADMIN_PHONES` **hai lần**: trước khi gửi OTP (không tốn tin nhắn, không lộ số nào là admin — thông báo lỗi giống hệt mọi trường hợp) và sau khi xác thực (challenge có thể tạo từ luồng khác). Mặc định **đóng**: danh sách rỗng thì không ai vào được.

### Dữ liệu đã gộp sẵn

`DriverRow` gộp hồ sơ + số dư ví + công nợ tiền mặt + vị trí + cờ gian lận 24h vào một dòng — giao diện không phải gọi thêm endpoint. `BlockedReason` lấy thẳng mã lỗi từ `Driver.CanAcceptTrip`, nên UI không tự suy luận lại điều kiện chặn (một nguồn sự thật duy nhất).

`LiveMapResult` trả **cung và cầu cùng lúc**: `Drivers` (tài xế có ping còn tươi) và `Pending` (điểm đón đang chờ ghép, đã lọc theo cùng bán kính, sắp xếp chờ lâu nhất lên đầu). Ghép ở backend bảo đảm hai tập cùng một thời điểm — câu hỏi vận hành thật là "chỗ nào có khách chờ mà không có tài xế", không phải "tài xế ở đâu".

### Cảnh báo tự sinh (ngưỡng ở backend, không ở giao diện)

| Mã | Điều kiện |
|---|---|
| `kyc_pending` | có hồ sơ chờ duyệt |
| `trips_stuck` | chuyến chờ ghép quá 60 giây → thiếu cung tài xế |
| `drivers_in_debt` | tài xế bị chặn nhận chuyến vì nợ vượt hạn mức |
| `trips_expired` | chuyến không tìm được tài xế |

**Acceptance Criteria — Module Admin:**
- [x] Số ngoài `ADMIN_PHONES` không lấy được token admin (`TestAdminAuthRejectsNonAllowlistedPhone`)
- [x] Danh sách rỗng = không ai đăng nhập được (`TestAdminAuthClosedByDefault`)
- [x] `0901…` và `+8490…` coi là cùng một người (`TestAdminAuthNormalizesPhoneFormat`)
- [x] Số liệu tổng quan phản ánh trạng thái thật, không hardcode (`TestAdminOverviewCountsRealState`)
- [x] Dòng tài xế gộp sẵn ví + vị trí (`TestAdminListDriversJoinsWalletAndLocation`)
- [x] Lọc chạy ở server; trạng thái sai trả `status_invalid` chứ không im lặng trả rỗng (`TestAdminFilterByStatusAndQuery`)
- [x] Duyệt hồ sơ có hiệu lực và cập nhật `BlockedReason` (`TestAdminReviewKYCChangesState`)
- [x] Bản đồ trả cung + cầu cùng bán kính, cùng thời điểm (`TestAdminLiveMapPairsSupplyAndDemand`)
- [x] Điểm đón ngoài bán kính bị loại; toạ độ sai trả `point_invalid` (`TestAdminLiveMapFiltersByRadius`)
- [ ] Nhật ký thao tác admin (ai duyệt hồ sơ nào, lúc nào) — **chưa làm**, cần cho đối soát nội bộ

---

## 6. Nền tảng dùng chung

**Event bus** ([platform/eventbus](godrive/internal/platform/eventbus/bus.go)) — in-memory, thay bằng NATS JetStream (Kafka khi >200k msg/s). Topic đặt tên `<domain>.<sự kiện quá khứ>`:

```
trip.requested  trip.assigned   trip.started    trip.completed  trip.cancelled
driver.online   driver.offline
offer.created   offer.accepted
payment.settled
```

**Worker** ([app/workers.go](godrive/internal/app/workers.go)) đăng ký consumer: `trip.requested` → spawn dispatch; `trip.completed` → ghi sổ + `MarkPaid` + trả tài xế về `IDLE`; `trip.started`/`trip.cancelled` → đồng bộ trạng thái tài xế. Production: mỗi consumer một tiến trình, đọc từ NATS/Kafka với consumer group để scale ngang.

**Outbox** — sự kiện ghi cùng transaction nghiệp vụ, relay publish sau. **Không publish trực tiếp trong transaction.**

**Lỗi** (`pkg/errs`) — mã ổn định + message tiếng Việt sẵn sàng hiển thị: `{"code","message","trace_id"}`. App mobile switch theo `code`, không parse `message`.

**Auth** — JWT HS256, `Issuer` phát hành, middleware phân quyền theo role. `DevAuth=true` trả mã OTP trong response (chỉ dev; `config.Load()` **từ chối khởi động** nếu `APP_ENV=prod` mà `DEV_AUTH` còn bật hoặc `JWT_SECRET` còn giá trị dev).

**Rate limit** — 30 req/s, burst 60 mỗi IP (in-memory; production dùng Redis để giới hạn toàn cụm).

**Clock** — `pkg/clock` tiêm được, test không dùng `time.Now()` thật.

---

## 7. Việc KHÔNG được tự quyết định

Các giá trị sau **đã có trong code nhưng chỉ là giả định khởi điểm**. Phải hỏi user trước khi coi là chốt, và tuyệt đối không tune ngầm:

| Hạng mục | Giá trị hiện tại | Cần gì để chốt |
|---|---|---|
| Trọng số chấm điểm (§3.2) | `1.0 / 60 / 90 / 0.25 / 0.20` | dữ liệu thật + A/B test |
| Bậc thang surge (§3.4) | `4→2.0, 3→1.7, 2→1.4, 1.2→1.2` | dữ liệu cung/cầu thật |
| Biểu giá (§4.1) | bảng TP.HCM mẫu | **hồ sơ kê khai giá cước đã nộp** |
| `TaxPermille` | 0 (tắt) | xác nhận của kế toán thuế |
| `DefaultDebtLimit` | 200.000đ | chính sách vận hành |
| Chu kỳ settlement + payout | chưa có | chính sách tài chính |
| Provider e-invoice | chưa chọn | Viettel/VNPT/MISA + credential sandbox |
| Multi-region / sharding | chưa | quyết định ở Phase 5, không sớm hơn |

---

## 8. Việc chưa làm (theo thứ tự ưu tiên)

Đây là backlog thật, đã đối chiếu với code:

**Nhóm A — thiếu hẳn, ảnh hưởng vận hành:**

0. **`identity.PostgresRepo` — chặn toàn bộ chế độ Postgres.** `app.New()` luôn gọi `identity.NewMemoryRepo()`, không có nhánh Postgres như `driver`/`trip`. Bảng `accounts` vì thế không bao giờ được ghi, trong khi `drivers.account_id` là khoá ngoại trỏ tới nó → `POST /v1/drivers/register` trả `driver_create_failed`. **Đã kiểm chứng bằng cách chạy thật với Postgres 18.4 + PostGIS 3.6.3.** Cần: repo Postgres cho `accounts`, chỗ lưu OTP challenge (Redis hợp hơn Postgres vì TTL ngắn), và nhánh chọn repo trong `app.New`.
1. **Driver settlement job** — chưa có worker định kỳ, chưa có `settlement_batch_id` để đánh dấu bút toán đã đối soát. Yêu cầu: idempotent, chạy 2 lần không double-pay.
2. **Theo dõi hạn giấy tờ** — `Documents.InsuranceUntil` hiện là string không ai đọc. Cần bảng `document_expiry_alerts` + job cảnh báo trước hạn (đăng kiểm, bảo hiểm TNDS, GPLX).
3. **`driver_status_history`** — hiện chỉ có `drivers.status` tại thời điểm hiện tại, không có lịch sử để đối soát tranh chấp.
4. **Cổng thanh toán** — MoMo, ZaloPay, VNPay, VietQR: webhook có xác thực chữ ký + đối soát tự động cuối ngày.
5. **Hoá đơn điện tử** — Viettel/VNPT/MISA meInvoice theo Nghị định 123/2020. Tách `internal/wallet/einvoice/`, adapter pattern:
   ```go
   type EInvoiceProvider interface {
       IssueInvoice(ctx context.Context, trip TripInvoiceData) (InvoiceResult, error)
       GetInvoiceStatus(ctx context.Context, invoiceID string) (InvoiceStatus, error)
   }
   ```
   Retry exponential backoff, **không block hoàn tất chuyến** — issue invoice là async job. Cần bảng `invoices`.
6. **eKYC** — FPT.AI hoặc VNPT eKYC, đối chiếu CCCD gắn chip với GPLX.

**Nhóm B — thay hạ tầng (đổi implementation, không sửa nghiệp vụ):**

| Hiện tại | Thay bằng |
|---|---|
| `driver.MemoryRepo` / `trip.MemoryRepo` | `NewPostgresRepo(db)` — SQL đã viết sẵn |
| `location.MemoryIndex` | Redis `GEOSEARCH`, hoặc H3 cell → Redis Set |
| `pkg/geo` lưới ô vuông | `github.com/uber/h3-go/v4` res 8–9 (đổi `CellOf`/`Ring`) |
| `matching.MemoryStore` | Redis `SET NX` cho `ClaimTrip` |
| `eventbus.NewInMemory` | NATS JetStream |
| `pricing.HaversineEngine` | OSRM `/table` — một request cho cả lô |
| `matching.SimpleETA` | OSRM + cache theo cặp ô lưới |
| `notification.LogPusher` | FCM / APNs; `LogOTPSender` → Zalo ZNS |
| `identity.MemoryRepo` | Postgres + Redis cho OTP challenge |
| `httpx.NewRateLimit` | Redis (giới hạn toàn cụm) |

Đổi `DATABASE_URL` trong `.env`, bỏ comment `_ "github.com/jackc/pgx/v5/stdlib"` trong `cmd/api/main.go`, chạy `make migrate-up`.

**Nhóm C — tính năng:**
7. An toàn — nút SOS, chia sẻ hành trình, ghi âm chuyến
8. Khuyến mãi — voucher, campaign, ngân sách, chống lạm dụng (`PROMO_EXPENSE` đã có sẵn trong sổ cái)
9. Chống gian lận nâng cao — phân tích đồ thị phát hiện chuyến ảo giữa cặp rider–driver quen nhau
10. Admin dashboard API (read-only)
11. Đặt trước, ghép chuyến chung, giao hàng/đồ ăn
12. Kho dữ liệu — ClickHouse + BI; ML cho ETA và surge
13. Load test riêng module `matching` — đây là điểm nghẽn đầu tiên khi scale

---

## 9. Quyết định riêng cho thị trường Việt Nam

| Quyết định | Lý do |
|---|---|
| **MQTT (EMQX)** cho luồng vị trí, không phải WebSocket | Android giá rẻ, 4G chập chờn. MQTT tiết kiệm pin/băng thông, có QoS và Last Will. |
| **Zalo ZNS trước, SMS brandname sau** cho OTP | ZNS rẻ hơn SMS nhiều lần, tỉ lệ đọc cao hơn. |
| **OSRM tự host + Goong/VietMap**, Google chỉ dự phòng | Google Maps API có thể tốn hơn cả tiền server khi lên quy mô. |
| **Ví + công nợ bắt buộc từ Phase 1** | Tiền mặt chiếm tỉ trọng rất lớn; không thể coi là tính năng phụ. |
| **Lưu dữ liệu trong nước** (VNG Cloud / Viettel IDC / FPT Cloud) | Nghị định 13/2023 (PDPD) và 53/2022. |
| **`trip_events` bất biến, lưu ≥3 năm** | Hợp đồng vận tải điện tử — Nghị định 10/2020, Thông tư 12/2020. |
| **Tách sẵn `TAX_PAYABLE`** | Khấu trừ VAT + TNCN tại nguồn. |
| **Trần surge 2.0** | Giảm rủi ro truyền thông và phản ứng người dùng. |
| **Regex biển số + đầu số di động VN** | Chặn dữ liệu rác tại tầng nhập liệu. |
| **ID sắp xếp theo thời gian thay UUID v4** | Giữ locality B-tree khi insert nhiều. |

---

## 10. Rủi ro lớn nhất

1. **Đối soát tiền mặt.** Sai lệch một đồng cũng làm mất niềm tin của tài xế. Đây là lý do sổ cái kép + idempotency là bắt buộc, không phải tuỳ chọn.
2. **Pháp lý.** Phải đăng ký phần mềm kết nối vận tải với Sở GTVT trước khi chạy thương mại. Nên có luật sư từ Phase 0.
3. **Cung tài xế.** Kỹ thuật hoàn hảo mà không có tài xế thì matching vô nghĩa. Ngân sách incentive thường lớn hơn ngân sách kỹ thuật.
4. **Chi phí Maps.** Cache thật mạnh, nếu không hoá đơn API sẽ vượt tiền server.

---

## 11. Chạy và kiểm thử

```bash
make run        # API tại http://localhost:8080, chế độ in-memory
make test       # toàn bộ unit + integration test
make test-race  # bắt buộc chạy trước khi merge thay đổi ở matching/wallet
```

Test hiện có: `internal/app/e2e_test.go` (luồng đầu-cuối), `internal/trip/statemachine_test.go`, `internal/matching/scoring_test.go`, `internal/wallet/ledger_test.go`, `pkg/money/money_test.go`, `pkg/geo/geo_test.go`.

**Kết quả chạy thật** (Go 1.26.5, 2026-08-09): `go build`, `go vet`, `gofmt` sạch; `go test ./...` và `go test -race ./...` **pass toàn bộ 24 test**, gồm `TestOnlyOneDriverWinsTrip` (AC quan trọng nhất của §3.3), `TestUnbalancedRejected` + `TestPostIsIdempotent` (§4.2), `TestTransitionGraph` (§5.1), và 10 test của module admin (§5c).

> **Đã sửa một test bấp bênh:** `TestFullTripLifecycle` vừa gọi `StartWorkers` (đăng ký dispatcher nền trên `trip.requested`) vừa gọi `DispatchRound` thủ công — hai nguồn cùng tạo lời mời cho một chuyến, thỉnh thoảng ra 2 offer thay vì 1. Nay chờ worker nền bằng `waitForOffers` (bỏ phiếu, không ngủ cố định). Chạy 15 lần liên tiếp dưới `-race`: 0 lần fail.

**Chưa có test** cho: `pricing` (không có file test nào — kể cả `computeBase`), `location`, `identity`, `outbox`, `httpx`, `authn`. Các Acceptance Criteria liên quan tới những package này vẫn chưa được verify.

**Môi trường dev đã dựng** (macOS, DBngin): Postgres 18.4 + PostGIS 3.6.3 ở cổng 5432, database `godrive`, 10 bảng ứng dụng + 6 partial index tạo thành công qua `scripts/setup-db.sh`. Redis 6379 sẵn sàng (chưa nối vào code). pgx v5.7.4 đã import ở `cmd/api`/`cmd/worker` — ghim bản này cùng `x/{sync,crypto,text}` cũ để giữ `go 1.22`; pgx ≥5.10 tự nâng lên Go 1.25.

**Chạy đầu-cuối đã kiểm chứng ở chế độ in-memory**: OTP → JWT → báo giá 3 loại xe (BIKE 28.000đ, CAR_4 63.000đ, CAR_7 75.000đ cho 4,74km) — làm tròn nghìn và chiết khấu 20%/25% đúng biểu giá. **Chế độ Postgres dừng ở bước đăng ký tài xế** vì lý do ở §8 mục 0.
