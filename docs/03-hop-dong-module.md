# 03 — Hợp đồng module

Mỗi mục: **Port phơi ra** · **Bất biến phải giữ** · **Tham số cấu hình** · **Điểm hở đã biết**.

---

## 3.1 `identity` — đăng nhập bằng số điện thoại + OTP

> **Cập nhật GĐ 0–2.** Các mục "Điểm hở" bên dưới đã được sửa phần lớn; xem
> [05 §5.3](05-doi-chieu-spec-code.md) để biết trạng thái từng gap.

| | |
|---|---|
| **Phơi ra** | `RequestOTP(phone, role) → (challengeID, devCode)`, `VerifyOTP(challengeID, code, deviceID) → TokenPair`, `GetAccount` |
| **Port cần** | `Repository`, `OTPSender`, `*authn.Issuer`, `clock.Clock` |
| **Người tiêu thụ** | HTTP `/v1/auth/*`, `admin.Auth` (qua `admin.IdentityPort`) |

**Bất biến**
- Số điện thoại **luôn** chuẩn hoá về E.164 `+84…` trước khi lưu hoặc so khớp
  (`NormalizePhone`, [service.go:26](../godrive/internal/identity/service.go#L29)).
  Regex chấp nhận đầu số VN sau quy hoạch 2018: `^(0|\+84)(3|5|7|8|9)[0-9]{8}$`.
- Mã OTP **không bao giờ lưu thô** — chỉ lưu `sha256(phone + ":" + code)`.
- So khớp bằng `subtle.ConstantTimeCompare` (chống timing attack).
- Sai quá `MaxOTPAttempts = 5` → xoá challenge, trả `otp_too_many_attempts`.
- `DevMode` trả mã OTP trong response — `config.Load()` **từ chối khởi động** nếu `APP_ENV=prod` mà còn bật.

**Tham số** — `OTPTTL = 5 phút`, `MaxOTPAttempts = 5`

**Điểm hở**
- ✅ ~~Không có `PostgresRepo`~~ — đã có từ GĐ 0 ([G-01](05-doi-chieu-spec-code.md#g-01)). Thử thách OTP tạm ở bảng `otp_challenges` + job dọn 1 phút/lần; Redis vẫn là đích đến
- 🟡 Không giới hạn tần suất gửi OTP theo **số điện thoại** (chỉ có rate limit theo IP ở tầng router) → kẻ tấn công đổi IP có thể spam tin nhắn tốn tiền thật
- 🟡 Không có refresh token, không có thu hồi token — JWT sống 24h, đăng xuất chỉ xoá cookie phía client

---

## 3.2 `driver` — hồ sơ, eKYC, trạng thái trực tuyến

| | |
|---|---|
| **Phơi ra** | `Onboard`, `ReviewKYC`, `GoOnline`, `GoOffline`, `Reserve`, `SetStatus`, `Get`, `GetByAccount`, `ListByStatus` |
| **Port cần** | `Repository`, `eventbus.Bus`, `clock.Clock` |
| **Người tiêu thụ** | `matching.DriverPort`, `location.DriverPort`, `admin.DriverPort` |

**Máy trạng thái**

```
OFFLINE ──GoOnline──► IDLE ──Reserve(CAS)──► ASSIGNED ──trip.started──► ON_TRIP
   ▲                    │                        │                          │
   └────GoOffline───────┘                        └──── trip.completed ──────┘
                                                        → IDLE

SUSPENDED  ◄── (⚠️ KHÔNG có code path nào đặt trạng thái này)
```

**Bất biến**
- `Reserve` là **chốt chặn duy nhất** chống một tài xế nhận hai chuyến:
  `UpdateStatus(id, IDLE→ASSIGNED, version)` — CAS `WHERE status='IDLE' AND version=$N`.
  `RowsAffected = 0` → `errs.Conflict("driver_state_changed")`.
- `CanAcceptTrip(debtLimit)` là **một nguồn sự thật duy nhất** cho câu hỏi "tài xế này nhận chuyến được không".
  `matching` và `admin` đều gọi chính nó — `admin.DriverRow.BlockedReason` lấy thẳng mã lỗi trả về,
  nên giao diện không tự suy luận lại điều kiện.
  Thứ tự kiểm tra: `KYC ≠ APPROVED` → `SUSPENDED` → `≠ IDLE` → `nợ > hạn mức`.
- Biển số chuẩn hoá **UPPER + bỏ khoảng trắng** rồi mới validate regex `^[0-9]{2}[A-Z]{1,2}[0-9]?-?[0-9]{3}\.?[0-9]{2}$`.
- `GoOnline`/`GoOffline` **idempotent** (đã ở trạng thái đích → trả `nil`).
- `GoOffline` bị chặn khi đang `ASSIGNED`/`ON_TRIP`.

**Tham số** — `DefaultDebtLimit = 200.000đ`, tài xế mới: `Rating = 5.0`, `AcceptanceRate = 0.8`

**Điểm hở**
- ✅ ~~`WalletBalance` không bao giờ được cập nhật~~ — GĐ 1: `Reserve` đọc số dư thật qua `driver.BalanceReader`; cột cache đồng bộ qua sự kiện `wallet.balance_changed` (**không** tăng `version`, vì đó là giá trị suy ra chứ không phải trạng thái được CAS bảo vệ)
- 🔴 `Rating`, `AcceptanceRate`, `CancelRate`, `CompletedTrips` **không bao giờ được cập nhật** →
  toàn bộ đầu vào chấm điểm bị đóng băng ở giá trị mặc định ([G-04](05-doi-chieu-spec-code.md#g-04))
- 🟡 `SUSPENDED` chỉ được **đọc**, không có API/worker nào **đặt** nó → phát hiện gian lận không dẫn tới hành động
- ✅ ~~`PostgresRepo` không lưu/đọc giấy tờ~~ — đã sửa ở GĐ 0. `insurance_until` là `DATE`, validate `YYYY-MM-DD` ở `Onboard`
- 🟡 `CanAcceptTrip` trả `driver_busy` (*"Bạn đang trong một chuyến khác"*) cho **mọi** trạng thái ≠ `IDLE`, kể cả `OFFLINE` — thông báo sai với tài xế chỉ chưa bật app ([G-27](05-doi-chieu-spec-code.md#g-27))

---

## 3.3 `location` — ping vị trí, chống gian lận, chỉ mục không gian

| | |
|---|---|
| **Phơi ra** | `Ingest(Ping)`, `Nearby(center, radius, Filter)`, `Get(driverID)`, `Fraud()` |
| **Port cần** | `Index`, `DriverPort`, `clock.Clock` |
| **Người tiêu thụ** | `matching.LocationPort`, `admin.LocationPort`, `pricing.SupplyCounter` (qua `app.idleCounter`) |

**Đường đi của một ping** ([service.go:32](../godrive/internal/location/service.go#L36)) — **thứ tự có chủ đích**:

```
1. Toạ độ hợp lệ + nằm trong hộp bao VN?     → không: point_out_of_range
2. Ping.Mocked == true?                       → gắn cờ MOCK_LOCATION, từ chối
3. AccuracyM > 200m?                          → low_accuracy
4. So với ping trước: tốc độ > 33 m/s?        → gắn cờ TELEPORT, từ chối
5. Tài xế OFFLINE / SUSPENDED?                → xoá khỏi chỉ mục
6. Upsert vào Index
```

**Bất biến**
- Ping bị từ chối **không bao giờ** lọt vào chỉ mục → dispatcher không bao giờ thấy vị trí giả.
- `Filter` lọc bằng **4 tiêu chí đồng thời**: loại xe, trạng thái, pin tối thiểu, **độ tươi của ping**
  (`StaleAfter = 45s`). Tài xế mất mạng tự động rơi khỏi tập ứng viên.

**Tham số** — `MaxPlausibleSpeedMps = 33` (~120 km/h), `MaxAccuracyM = 200`, `StaleAfter = 45s`,
`geo.CellSizeDeg = 0.005` (≈ 550m)

**Điểm hở**
- 🟡 `Snapshot.Status` được chụp **lúc ingest**. Tài xế chuyển `IDLE → ASSIGNED` mà chưa gửi ping mới thì
  chỉ mục vẫn báo `IDLE` → dispatcher gửi offer thừa. `Reserve` CAS chặn được hậu quả, nhưng lãng phí vòng dispatch.
- 🟡 `MemoryIndex.Nearby` và `FraudDetector` dùng `time.Now()` thay vì `clock.Clock` tiêm được
  ([index_memory.go:83](../godrive/internal/location/index_memory.go#L73), [fraud.go:38](../godrive/internal/location/fraud.go#L45))
  → không viết được test tất định cho lọc độ tươi
- 🟡 `ReasonSpeedOutlier` khai báo nhưng **không có code nào gán** — spec §5.2 nói có 3 loại cờ, thực tế 2
- 🟡 Cờ gian lận gom in-memory, mất khi restart; không có ngưỡng tự động khoá
- 🔵 MQTT chưa có — hiện chỉ có `POST /v1/locations/ping` (HTTP)

---

## 3.4 `pricing` — biểu giá, phụ phí đêm, surge

| | |
|---|---|
| **Phơi ra** | `Estimate(EstimateInput) → Quote`, `EstimateAll(pickup, dropoff)`, `GetQuote(quoteID)` |
| **Port cần** | `RouteEngine`, `SurgeProvider`, `QuoteStore`, `clock.Clock` |
| **Người tiêu thụ** | `trip.PricingPort`, HTTP `/v1/quotes` |

**Hai tầng, ranh giới rõ ràng**

| | `computeBase(tariff, route)` | `Service.Estimate()` |
|---|---|---|
| Loại | **pure function** | orchestration |
| I/O | không | `RouteEngine`, `SurgeProvider`, `QuoteStore` |
| Test | **phải test kỹ** — dùng để audit khi khách khiếu nại giá | test tích hợp |

**Công thức**

```
base     = OpeningFare + (distance − OpeningMeter)/1000 × PerKm + duration/60 × PerMinute
night    = base × NightSurchargePermille/1000            nếu 22:00–05:00 giờ VN (UTC+7)
subtotal = (base + night) × surge                        surge clamp vào [1.0, 2.0]
subtotal = max(subtotal, MinFare)
total    = RoundTo(subtotal, 1000)                       giá cước VN tròn nghìn
fee      = total × PlatformFeePermille/1000
earn     = total − fee
```

**Biểu giá mẫu** (`DefaultTariffs()` — TP.HCM; **giá thật phải khớp hồ sơ kê khai giá cước**)

| Loại xe | Mở cửa (2km đầu) | /km | /phút | Phụ phí đêm | Chiết khấu |
|---|---:|---:|---:|---:|---:|
| `BIKE` | 12.000đ | 4.300đ | 300đ | 10% | **20%** |
| `CAR_4` | 29.000đ | 9.500đ | 600đ | 10% | **25%** |
| `CAR_7` | 34.000đ | 11.500đ | 700đ | 10% | **25%** |

**Bậc thang surge** ([surge.go:52](../godrive/internal/pricing/surge.go#L67)) — hàm **bậc thang**, không liên tục:

```
demand = số request trong ô lưới, cửa sổ trượt 5 phút
supply = số tài xế IDLE trong bán kính 2.000m
ratio  = demand / max(supply, 1)

ratio ≥ 4.0 → 2.0     ratio ≥ 3.0 → 1.7
ratio ≥ 2.0 → 1.4     ratio ≥ 1.2 → 1.2     còn lại → 1.0
```

Bậc thang thay vì hàm liên tục: dễ giải thích cho vận hành, không nhạy với nhiễu.
Trần 2.0 clamp **hai lần** — ở bảng bậc thang và lại ở `Estimate()` (phòng khi thay `SurgeProvider` khác).

**Tham số** — `QuoteTTL = 5 phút`, `HaversineEngine{DetourFactor: 1.35, AvgSpeedKph: 22}`

**Điểm hở**
- 🔴 **Surge luôn bằng 1.0.** `DemandSurge.RecordRequest` **không có ai gọi** → `demand` vĩnh viễn = 0 ([G-07](05-doi-chieu-spec-code.md#g-07))
- ✅ ~~Ba phép float trên đường tiền~~ — GĐ 1: toàn bộ chuyển sang số nguyên, `SurgeProvider` trả permille `int64`
- ✅ ~~Không có file test nào~~ — 17 test, phủ 80,7%; `computeBase` và `isNight` 100%
- 🟡 `Quote.Discount` khai báo nhưng không bao giờ được đặt; `EstimateInput.PromoCode` nhận vào rồi bị bỏ qua
- 🟡 Biểu giá hardcode một thành phố (`City: "HCM"`); `Tariff.City` có nhưng `tariffs` map chỉ khoá theo `VehicleType`

---

## 3.5 `trip` — máy trạng thái + nhật ký bất biến

| | |
|---|---|
| **Phơi ra** | `Create`, `Assign`, `MarkArrived`, `Start`, `Complete`, `MarkPaid`, `Cancel`, `Expire`, `Get`, `ListByRider`, `ListByStatus`, `Events` |
| **Port cần** | `Repository`, `PricingPort`, `eventbus.Bus`, `idem.Store`, `clock.Clock` |
| **Người tiêu thụ** | `matching.TripPort`, `admin.TripPort`, HTTP `/v1/trips/*` |

**Đồ thị trạng thái là DỮ LIỆU, không phải if-else** ([domain.go:33](../godrive/internal/trip/domain.go#L31)):

```
CREATED ──► SEARCHING ──► ASSIGNED ──► ARRIVED ──► IN_PROGRESS ──► COMPLETED ──► PAID
   │            │             │            │
   ▼            ▼             ▼            ▼
CANCELLED   CANCELLED     CANCELLED    CANCELLED
                │
                ▼
             EXPIRED
```

`transitions map[Status][]Status` — thêm trạng thái mới = sửa **một** map.
Transition sai → `errs.Conflict("invalid_transition", …)`.

> **`IN_PROGRESS → CANCELLED` bị chặn có chủ đích.** Đang chở khách thì không có khái niệm "huỷ".
> Sự cố giữa chuyến là một luồng riêng (kết thúc sớm + hoàn tiền) — **chưa implement**.

**Bất biến**
- **Mọi** chuyển trạng thái đi qua `Service.apply()` → `repo.Save(trip, event)`.
  Trip và event ghi trong **cùng một transaction** — bắt buộc, không có ngoại lệ.
  Không bao giờ `UPDATE status` mà không ghi event tương ứng.
- `Save` có optimistic lock theo `Version`; lệch → `trip_version_conflict`.
- `Create` idempotent theo header `Idempotency-Key` (TTL 24h): retry trả về **đúng chuyến đã tạo**.
- `Complete` **chỉ publish** `trip.completed`; ghi sổ do worker làm — không giữ transaction dài trong request path.

**Tham số** — `FreeCancelWindow = 2 phút`, `CancelFeeVND = 10.000`

**Điểm hở**
- ✅ ~~Phí huỷ không ai ghi sổ~~ — GĐ 1: consumer `trip.cancelled` gọi `wallet.PostCancelFee`
- 🟡 `Repository.ActiveByDriver` đã khai báo và cài đặt ở **cả hai** repo nhưng **không ai gọi**
- 🟡 `MemoryRepo.Save` **không tăng** `t.Version` của caller, `PostgresRepo.Save` **có** → hai repo lệch hành vi
- ✅ ~~`cancelFee(t)` gọi hai lần~~ — GĐ 1: tính một lần rồi dùng lại
- ✅ ~~Khoá idempotency kẹt khi `Create` thất bại~~ — GĐ 1: `idem.Store.Release` ([G-28](05-doi-chieu-spec-code.md#g-28))

---

## 3.6 `matching` — ghép chuyến (module rủi ro nhất)

| | |
|---|---|
| **Phơi ra** | `DispatchRound(tripID, round) → int`, `Dispatch(tripID)`, `Accept(offerID, driverID)`, `Reject`, `PendingOffers` |
| **Port cần** | `LocationPort`, `DriverPort`, `TripPort`, `Store`, `ETAEngine`, `eventbus.Bus`, `clock.Clock` |

**Chiến lược: broadcast theo lô có chấm điểm** — *không* phải batch optimal assignment.
Mỗi chuyến chạy một chu trình độc lập trong goroutine riêng: đơn giản, độ trễ thấp,
không cần đợi cửa sổ gom lô. Đánh đổi: không tối ưu toàn cục khi mật độ cao.

**Hàm chấm điểm — điểm PHẠT, càng THẤP càng ưu tiên.** Đơn vị quy đổi là **giây**:

```go
điểm = WeightETA        × ETA_giây               // 1.0
     + WeightRating     × (5 − rating)           // 60.0  → chênh 1 sao ≈ 60s
     + WeightAcceptance × (1 − acceptance_rate)  // 90.0  → hay bỏ chuyến bị phạt nặng
     − WeightIdle       × idle_giây              // 0.25  → chờ lâu được ưu tiên
     + WeightHeading    × góc_lệch_hướng_độ      // 0.20  → chỉ khi khoảng cách > 100m
```

Quy đổi ra giây làm mọi trọng số **đọc được trực tiếp**: "chênh 1 sao ≈ 60 giây ETA".
Sort có **tie-break theo `DriverID`** để test tất định.

**Tham số mặc định** (`DefaultConfig()`)

| Tham số | Giá trị | Ghi chú |
|---|---:|---|
| `InitialRadiusM` | 1.500 | bán kính vòng đầu |
| `RadiusStepM` | 1.500 | nới mỗi vòng |
| `MaxRadiusM` | 5.000 | trần |
| `MaxRounds` | 3 | hết vòng → `EXPIRED` |
| `BatchSize` | 5 | offer gửi song song mỗi vòng |
| `OfferTTL` | 15s | offer hết hạn |
| `MinBatteryPc` | 15 | loại máy sắp hết pin |

### ⚠️ Bất biến quan trọng nhất của toàn hệ thống — thứ tự trong `Accept()` KHÔNG ĐƯỢC ĐỔI

```
1. Store.ClaimTrip(tripID, driverID, 30s)  ← NGUYÊN TỬ. Thua → offer=LOST, lỗi trip_taken
2. drivers.Reserve(driverID)               ← CAS. Thất bại → offer=REJECTED
3. trips.Assign()                          ← lỗi → rollback tài xế về IDLE
4. UNIQUE INDEX offers_one_accepted_per_trip  ← chốt chặn cuối ở tầng CSDL
```

**Giành khoá chuyến TRƯỚC, rồi mới giữ chỗ tài xế.** Đảo ngược thứ tự sẽ tạo deadlock chéo khi
hai tài xế nhận hai chuyến của nhau. Verify bởi `TestOnlyOneDriverWinsTrip` chạy dưới `-race`.

**Điểm hở**
- 🔴 `Candidate.IdleSeconds = now − snapshot.UpdatedAt` — đó là **độ cũ của ping**, không phải thời gian rảnh.
  Trọng số này đang **thưởng cho tài xế có ping cũ** ([G-10](05-doi-chieu-spec-code.md#g-10))
- 🔴 `Rating`/`Acceptance` đóng băng ở `5.0`/`0.8` → 3 trong 5 thành phần điểm là hằng số ([G-04](05-doi-chieu-spec-code.md#g-04))
- ✅ ~~`go Matcher.Dispatch(...)` không có `recover()`~~ — đã bọc `safego.Recover` ở GĐ 0, cleanup đẩy chuyến về `EXPIRED` thay vì để kẹt `SEARCHING`
- 🟡 `Dispatch` không giới hạn số goroutine đồng thời, không backpressure
- 🟡 `candidates()` gọi `drivers.Get()` **cho từng ứng viên** (N+1) — sẽ đau khi lên Postgres
- 🟡 `SimpleETA` gọi từng cặp; production cần OSRM `/table` một request cho cả lô
- 🔵 Không có `PostgresRepo` cho `offers`

---

## 3.7 `wallet` — sổ cái kép

| | |
|---|---|
| **Phơi ra** | `SettleTrip`, `TopUp`, `DriverBalance`, `CashOnHand`, `Statement` |
| **Port cần** | `Ledger`, `eventbus.Bus`, `clock.Clock` |
| **Người tiêu thụ** | `app.onTripCompleted`, `admin.WalletPort` |

**Bất biến**
- `Transaction.Validate()` từ chối giao dịch có **< 2 bút toán** hoặc **tổng ≠ 0**.
  `MemoryLedger.Post` gọi `Validate()` **trước mọi lần ghi** ([store_memory.go:23](../godrive/internal/wallet/store_memory.go#L23)) — không có đường vòng.
- **Idempotency:** `TxID` suy ra **tất định** từ nghiệp vụ (`"tx_trip_" + tripID`, `"tx_tax_" + tripID`, `"tx_top_" + paymentRef`).
  `SettleTrip` kiểm tra `ledger.Exists(txID)` trước khi ghi → worker retry bao nhiêu lần cũng chỉ ghi một lần.
- `ledger_entries` **chỉ INSERT** — không `UPDATE`, không `DELETE`. Sai thì ghi bút toán đảo (`ADJUSTMENT`).

**Tham số** — `TaxPermille = 0` (**tắt**). Đặt `45` = 4,5% (3% GTGT + 1,5% TNCN) cho cá nhân
kinh doanh vận tải — **cần kế toán thuế xác nhận trước khi bật**.

**Điểm hở**
- ✅ ~~Chỉ có `MemoryLedger`~~ — GĐ 1: `PostgresLedger`, một transaction ghi cả hai bảng, `ON CONFLICT (tx_id) DO NOTHING` làm chốt idempotency ở tầng CSDL
- ✅ ~~Không có handler HTTP~~ — GĐ 1: `GET /v1/drivers/me/wallet`, `/statement`, và `/topup` (**chỉ dev**)
- ✅ ~~`CancelFee` là hàm chết~~ — nay được `Service.PostCancelFee` gọi
- ✅ ~~`Validate()` không kiểm tài khoản rỗng~~ — GĐ 1: chặn cả `account_id`/`account_type`/`TxID` rỗng, kèm `CHECK` ở CSDL ([G-31](05-doi-chieu-spec-code.md#g-31))
- 🟡 `Payout` vẫn là hàm chết — chờ job chi trả ([T-22](07-todo.md#t-22))

---

## 3.8 `admin` — API vận hành

| | |
|---|---|
| **Phơi ra** | `Overview`, `ListDrivers`, `GetDriver`, `ReviewKYC`, `ListTrips`, `GetTrip`, `TripEvents`, `LiveMap` + `Auth{RequestOTP, VerifyOTP}` |
| **Port cần** | `DriverPort`, `TripPort`, `LocationPort`, `WalletPort`, `IdentityPort` — **tất cả khai báo trong package `admin`** |

**Nguyên tắc:** *nếu một câu hỏi có thể trả lời sai thì câu trả lời phải đến từ backend.*
Giao diện Next.js không tính toán, không lọc, không tự quyết định cái gì được xem.

**Chỉ MỘT hành động ghi:** `ReviewKYC`. Mọi thay đổi khác phải đi qua module sở hữu nghiệp vụ.

### Cổng đăng nhập riêng — lý do bảo mật

Luồng `/v1/auth/*` cấp token theo `role` **mà client gửi lên**. Đúng với rider/driver (ai cũng đăng ký được),
nhưng dùng chung cho admin thì **chỉ cần gửi `role=admin` là leo thang đặc quyền**.

`/v1/admin/auth/*` kiểm tra `ADMIN_PHONES` **hai lần**:
1. **Trước khi gửi OTP** — không tốn tin nhắn, và thông báo lỗi **giống hệt mọi trường hợp** nên không lộ số nào là admin.
2. **Sau khi xác thực** — challenge có thể được tạo từ luồng khác với vai trò khác.

**Mặc định đóng:** danh sách rỗng ⇒ không ai vào được. `app.New()` log cảnh báo khi chưa cấu hình.

### Dữ liệu đã gộp sẵn

- `DriverRow` gộp hồ sơ + số dư ví + công nợ tiền mặt + vị trí + cờ gian lận 24h **vào một dòng**.
  `BlockedReason` lấy **thẳng mã lỗi** từ `Driver.CanAcceptTrip` → một nguồn sự thật duy nhất.
- `LiveMapResult` trả **cung và cầu cùng lúc**, cùng bán kính, cùng thời điểm.
  Câu hỏi vận hành thật là *"chỗ nào có khách chờ mà không có tài xế"*, không phải *"tài xế ở đâu"*.

### Cảnh báo tự sinh (ngưỡng ở backend)

| Mã | Điều kiện | Mức |
|---|---|---|
| `kyc_pending` | có hồ sơ chờ duyệt | info |
| `trips_stuck` | chuyến chờ ghép **> 60 giây** → thiếu cung | warn |
| `drivers_in_debt` | tài xế bị chặn vì nợ vượt hạn mức | warn |
| `trips_expired` | chuyến không tìm được tài xế | warn |

**Tham số** — `MaxPageSize = 200`, `FraudWindow = 24h`, `DefaultMapRadiusM = 5.000`, `MaxMapRadiusM = 50.000`,
tâm bản đồ mặc định = Chợ Bến Thành `(10.7725, 106.6980)`

**Điểm hở**
- ✅ ~~Không có nhật ký thao tác admin~~ — GĐ 1: `admin_audit_log` + `GET /v1/admin/audit`. `ReviewKYC` nhận `Actor` và ghi cả trạng thái trước/sau
- 🟡 `ListDrivers`/`ListTrips` "tất cả" = **lặp qua từng trạng thái rồi hợp lại**, mỗi lần `LIMIT n`.
  Khi dữ liệu lớn, kết quả sẽ **thiếu** chứ không phải sai — cần phân trang keyset thật
- 🟡 `Overview` quét tối đa `9 × 200` chuyến + `5 × 200` tài xế **mỗi lần gọi**; giao diện tự làm mới định kỳ
- 🟡 `ReviewKYC` không xem được `Documents` (bị `json:"-"` và Postgres repo không đọc lại)

---

## 3.9 Nền tảng dùng chung

| Package | Vai trò | Lưu ý |
|---|---|---|
| `pkg/money` | `VND int64`. **`MulDiv(num, den)`** là phép nguyên thuỷ (làm tròn nửa ra xa 0); `MulPermille` = `MulDiv(rate, 1000)`; `RoundTo` làm tròn **lên** bội số | **Không dùng float cho tiền, kể cả biến tạm.** Sai một đồng ở `computeBase` có thể thành 1.000đ ở giá cuối khi vắt qua ranh giới làm tròn |
| `pkg/geo` | `Point`, `DistanceM` (haversine), `BearingDeg`, `AngleDiffDeg`, lưới ô `CellOf`/`Ring` | `CellSizeDeg = 0.005` ≈ 550m. Chỗ thay bằng H3 res 8–9 |
| `pkg/errs` | `Error{Kind, Code, Msg}` + `HTTPStatus` | App mobile switch theo **`code`**, không parse `message` |
| `pkg/id` | ID sắp xếp theo thời gian: `prefix_` + base32(ms ‖ random) | Thân thiện B-tree. `HasPrefix` chống nhầm `driverID` vào `tripID` |
| `pkg/idem` | `Reserve` / `Complete` / **`Release`** | Chỉ có bản bộ nhớ. `Reserve` trả **bản sao** (con trỏ nội bộ thoát ra ngoài khoá từng là data race — [G-29](05-doi-chieu-spec-code.md#g-29)); có quét dọn khoá quá hạn |
| `pkg/clock` | `Clock` tiêm được + `Mock` cho test | ✅ `location`, `httpx.RateLimit`, `matching.MemoryStore`, và cả `app.NewWithClock` đã tiêm được. 🟡 còn `eventbus`, `outbox`, `pkg/idem`. **Store và Engine phải dùng CHUNG một đồng hồ** — lệch nhau gây lỗi phụ thuộc giờ chạy ([G-30](05-doi-chieu-spec-code.md#g-30)) |
| `platform/authn` | JWT HS256 tự cài bằng stdlib. `Require(roles…)` middleware | Không có `jti`, không thu hồi được, không refresh token ([G-21](05-doi-chieu-spec-code.md#g-21)) |
| `platform/safego` | `Recover(log, name, cleanup)` cho goroutine nền | ✅ mới ở GĐ 0. Mọi `go func()` chạy code nghiệp vụ đều phải mở đầu bằng nó |
| `platform/httpx` | `JSON`, `Fail`, `Decode` (giới hạn 1MB + `DisallowUnknownFields`), `RequestID`/`Logging`/`Recover`/`RateLimit` | ✅ rate limit nay dọn bucket nguội (`IdleTTL` 10', `SweepEvery` 1') |
| `platform/safego` | `Recover(log, name, cleanup)` cho goroutine nền | Mới từ GĐ 0. **Mọi `go func()` chạy code nghiệp vụ phải mở đầu bằng nó** |
| `platform/eventbus` | `Bus` in-memory, publish bất đồng bộ | ✅ dùng đúng `WaitGroup`; khi đang tắt thì chạy handler **đồng bộ** để không mất sự kiện ([G-34](05-doi-chieu-spec-code.md#g-34)). Vẫn cần NATS để bảo đảm **xử lý xong**, không chỉ **phát đi** |
| `platform/logger` | `slog` + `logger.From(ctx)` | |
| `notification` | `Pusher`, `SMSSender`, `OTPSender` | Chỉ `LogOTPSender` được nối; `LogPusher` **chưa ai dùng** — chờ FCM/APNs ở GĐ 3 ([G-11](05-doi-chieu-spec-code.md#g-11)) |
| `outbox` | `Store` + `Relay` | **Chưa nối vào bất kỳ luồng nghiệp vụ nào** |
