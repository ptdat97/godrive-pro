# 04 — API reference

> Trích **trực tiếp** từ các hàm `Register(mux)` trong `internal/*/http.go`.
> Base URL dev: `http://localhost:8080`. Toàn bộ JSON, `Content-Type: application/json; charset=utf-8`.

## 4.1 Quy ước chung

**Xác thực** — `Authorization: Bearer <jwt>`. JWT HS256, claim `{sub, role, did, exp, iat}`, TTL mặc định **24h**.
Middleware `Issuer.Require(roles…)` chặn trước khi vào handler.

**Hình dạng lỗi** — mọi lỗi đều có dạng này:

```json
{ "code": "wallet_debt_exceeded", "message": "Ví của bạn đang âm quá hạn mức...", "trace_id": "req_01J..." }
```

> **App mobile phải switch theo `code`, không được parse `message`.**
> `message` là tiếng Việt sẵn sàng hiển thị và **có thể đổi** bất cứ lúc nào.
> Lỗi `kind = internal` **luôn** trả message chung `"Đã có lỗi xảy ra, vui lòng thử lại."` — chi tiết chỉ vào log.

**Ánh xạ `Kind` → HTTP status**

| `errs.Kind` | HTTP |
|---|---|
| `invalid` | 400 |
| `unauthorized` | 401 |
| `forbidden` | 403 |
| `not_found` | 404 |
| `conflict` | 409 |
| `rate_limited` | 429 |
| `internal` | 500 |

**Header**

| Header | Chiều | Ý nghĩa |
|---|---|---|
| `X-Request-Id` | vào (tuỳ chọn) / ra (luôn) | Truyền vào để nối log; không truyền thì server tự sinh |
| `Idempotency-Key` | vào | **Chỉ `POST /v1/trips`** đọc header này. TTL 24h |

**Giới hạn** — body tối đa **1 MB**; `DisallowUnknownFields` (trường lạ → `invalid_body`);
rate limit **30 req/s, burst 60 mỗi IP** (lấy IP từ `X-Forwarded-For` nếu có). Có `REDIS_URL` thì
hạn mức **dùng chung cho cả cụm**; không thì mỗi pod một hạn mức riêng.

`/healthz`, `/readyz` và `/metrics` **được miễn rate limit** — chặn chúng là cách tự tạo sự cố dây
chuyền: pod tải cao → health check bị chặn → orchestrator giết pod → tải dồn sang pod còn lại.

---

## 4.2 Bảng endpoint đầy đủ (36 route)

| Method | Path | Vai trò | Handler |
|---|---|---|---|
| `GET` | `/healthz` | — | liveness: tiến trình còn sống không |
| `GET` | `/readyz` | — | readiness: **ping thật** Postgres + Redis, trả 503 khi hỏng |
| `GET` | `/metrics` | — | số liệu định dạng Prometheus |
| **Xác thực** ||||
| `POST` | `/v1/auth/otp` | công khai | `identity.requestOTP` |
| `POST` | `/v1/auth/verify` | công khai | `identity.verifyOTP` |
| **Tài xế** ||||
| `POST` | `/v1/drivers/register` | `driver` | `driver.register` |
| `GET` | `/v1/drivers/me` | `driver` | `driver.me` |
| `POST` | `/v1/drivers/me/online` | `driver` | `driver.online` |
| `POST` | `/v1/drivers/me/offline` | `driver` | `driver.offline` |
| **Vị trí** ||||
| `POST` | `/v1/locations/ping` | `driver` | `location.ping` |
| **Ví tài xế** ||||
| `GET` | `/v1/drivers/me/wallet` | `driver` | `wallet.wallet` |
| `GET` | `/v1/drivers/me/statement` | `driver` | `wallet.statement` |
| `POST` | `/v1/drivers/me/topup` | `driver` | `wallet.topUp` — **chỉ đăng ký khi `DEV_AUTH=true`** |
| **Báo giá** ||||
| `POST` | `/v1/quotes` | `rider` | `pricing.estimate` |
| **Chuyến đi** ||||
| `POST` | `/v1/trips` | `rider` | `trip.create` |
| `GET` | `/v1/trips` | `rider` | `trip.list` |
| `GET` | `/v1/trips/{id}` | `rider`+`driver` | `trip.get` |
| `GET` | `/v1/trips/{id}/events` | `rider`+`driver` | `trip.events` |
| `POST` | `/v1/trips/{id}/cancel` | `rider`+`driver` | `trip.cancel` |
| `POST` | `/v1/trips/{id}/rate` | `rider` | `trip.rate` |
| `POST` | `/v1/trips/{id}/arrived` | `driver` | `trip.arrived` |
| `POST` | `/v1/trips/{id}/start` | `driver` | `trip.start` |
| `POST` | `/v1/trips/{id}/complete` | `driver` | `trip.complete` |
| **Ví tài xế** ||||
| `GET` | `/v1/drivers/me/wallet` | `driver` | `wallet.wallet` |
| `GET` | `/v1/drivers/me/statement` | `driver` | `wallet.statement` |
| `POST` | `/v1/drivers/me/topup` | `driver` | `wallet.topUp` — **chỉ đăng ký khi `DEV_AUTH=true`** |
| **Ghép chuyến** ||||
| `GET` | `/v1/offers` | `driver` | `matching.pending` |
| `POST` | `/v1/offers/{id}/accept` | `driver` | `matching.accept` |
| `POST` | `/v1/offers/{id}/reject` | `driver` | `matching.reject` |
| **Quản trị** ||||
| `POST` | `/v1/admin/auth/otp` | công khai (lọc `ADMIN_PHONES`) | `admin.authOTP` |
| `POST` | `/v1/admin/auth/verify` | công khai (lọc `ADMIN_PHONES`) | `admin.authVerify` |
| `GET` | `/v1/admin/me` | `admin` | `admin.me` |
| `GET` | `/v1/admin/overview` | `admin` | `admin.overview` |
| `GET` | `/v1/admin/drivers` | `admin` | `admin.listDrivers` |
| `GET` | `/v1/admin/drivers/{id}` | `admin` | `admin.getDriver` |
| `POST` | `/v1/admin/drivers/{id}/kyc` | `admin` | `admin.reviewKYC` |
| `GET` | `/v1/admin/trips` | `admin` | `admin.listTrips` |
| `GET` | `/v1/admin/trips/{id}` | `admin` | `admin.getTrip` |
| `GET` | `/v1/admin/trips/{id}/events` | `admin` | `admin.tripEvents` |
| `GET` | `/v1/admin/live-map` | `admin` | `admin.liveMap` |
| `GET` | `/v1/admin/audit` | `admin` | `admin.audit` |
| `GET` | `/v1/admin/audit` | `admin` | `admin.audit` |

### Ví tài xế

```bash
curl -s localhost:8080/v1/drivers/me/wallet -H "Authorization: Bearer $DRV"
```
```json
{ "balance": -201600, "cash_on_hand": 1008000, "debt_limit": 200000,
  "in_debt": true, "amount_to_clear": 1600 }
```

`amount_to_clear` là số tiền cần nạp để nhận chuyến trở lại — tính ở backend để ứng dụng tài xế
không phải tự suy ra từ `balance` và `debt_limit` (cùng nguyên tắc với `blocked_reason` của bảng điều khiển).

`GET /v1/drivers/me/statement?from=&to=` nhận RFC3339, mặc định 30 ngày gần nhất, **trần 92 ngày**
(`range_too_wide`).

> ⚠️ **`POST /v1/drivers/me/topup` chỉ tồn tại khi `DEV_AUTH=true`.** Một endpoint tự ghi có vào ví mà
> không có đối ứng tiền thật chính là máy in tiền. Ở production, tiền vào ví chỉ đến từ webhook cổng
> thanh toán đã xác thực chữ ký. Nó nhận `Idempotency-Key` để retry không nạp hai lần.

### Nhật ký thao tác quản trị

```bash
curl -s "localhost:8080/v1/admin/audit?target_id=drv_01J...&limit=50" -H "Authorization: Bearer $ADMIN"
```
```json
{ "entries": [ { "id": "aud_…", "actor_id": "acc_…", "action": "review_kyc",
                 "target_type": "driver", "target_id": "drv_…",
                 "payload": {"approved": true, "from": "PENDING", "to": "APPROVED"},
                 "at": "2026-08-24T10:00:00Z" } ], "count": 1 }
```

Chỉ đọc — không có endpoint nào sửa hay xoá nhật ký.

### ❌ Endpoint chưa có nhưng nghiệp vụ đã sẵn sàng

| Endpoint cần | Service đã có | Mã gap |
|---|---|---|
| `GET /v1/drivers/me/trips` | `trip.Repository.ActiveByDriver` (đã cài, không ai gọi) | [G-24](05-doi-chieu-spec-code.md#g-24) |
| `POST /v1/payments/webhook/{provider}` | — | [T-22](07-todo.md#t-22) |

---

## 4.3 Luồng chính — ví dụ đầy đủ

### Đăng nhập (dev, `DEV_AUTH=true`)

```bash
# 1. Xin mã. role ∈ {rider, driver} — role=admin bị TỪ CHỐI ở đây.
curl -sX POST localhost:8080/v1/auth/otp \
  -d '{"phone":"0901234567","role":"rider"}'
# → {"challenge_id":"chl_01J...","dev_code":"483920"}
#   dev_code CHỈ xuất hiện khi DEV_AUTH=true.

# 2. Đổi mã lấy token.
curl -sX POST localhost:8080/v1/auth/verify \
  -d '{"challenge_id":"chl_01J...","code":"483920","device_id":"iphone-15"}'
# → {"access_token":"eyJ...","expires_at":"...","account":{"id":"acc_01J...","role":"rider",...}}
```

Lỗi: `phone_invalid` · `otp_expired` (>5 phút) · `otp_invalid` · `otp_too_many_attempts` (>5 lần) · `account_blocked`

### Báo giá → đặt chuyến

```bash
curl -sX POST localhost:8080/v1/quotes -H "Authorization: Bearer $RIDER" \
  -d '{"pickup":{"lat":10.7725,"lng":106.6980},"dropoff":{"lat":10.8014,"lng":106.7109}}'
```

> ⚠️ `vehicle_type` **được nhận nhưng bị bỏ qua** — handler luôn gọi `EstimateAll` và trả về
> báo giá cho **cả ba** loại xe. Client tự chọn `quote_id` phù hợp.

`surge_permille` là **nguồn sự thật để tính tiền** (1000 = ×1.0). `surge_multiplier` chỉ để hiển thị —
mọi phép nhân trên đường tiền đi qua permille bằng số nguyên.
>
> **`surge_permille` là nguồn sự thật để tính tiền** (1000 = ×1.0). `surge_multiplier` chỉ để hiển thị:
> mọi phép nhân trên đường tiền đi qua số nguyên, không qua float.

```json
{ "quotes": [
    { "id": "qte_01J...", "vehicle_type": "BIKE",  "distance_m": 4740, "duration_s": 776,
      "base_fare": 26000, "night_fee": 0, "surge_permille": 1000, "surge_multiplier": 1, "total": 28000,
      "platform_fee": 5600, "driver_earn": 22400, "expires_at": "…+5m" },
    { "…": "CAR_4 → 63.000đ" }, { "…": "CAR_7 → 75.000đ" } ] }
```

```bash
curl -sX POST localhost:8080/v1/trips -H "Authorization: Bearer $RIDER" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"quote_id":"qte_01J...","payment_method":"CASH",
       "pickup":{"point":{"lat":10.7725,"lng":106.6980},"address":"Chợ Bến Thành","note":"cổng Tây"},
       "dropoff":{"point":{"lat":10.8014,"lng":106.7109},"address":"Thảo Cầm Viên"}}'
```

Trả `201` + trip ở trạng thái **`SEARCHING`** (đã tự động chuyển từ `CREATED`).

Lỗi: `payment_method_invalid` · `quote_expired` (>5 phút) · `request_in_flight` (retry khi lần đầu chưa xong)

**Idempotency:** cùng `Idempotency-Key` gọi lại ⇒ trả **đúng chuyến đã tạo**, không tạo chuyến thứ hai.

### Tài xế: online → nhận chuyến

```bash
curl -sX POST localhost:8080/v1/drivers/register -H "Authorization: Bearer $DRV" \
  -d '{"full_name":"Nguyễn Văn A","phone":"+84907654321","city":"HCM",
       "vehicle":{"type":"BIKE","plate":"59X1-123.45","model":"Wave Alpha","color":"đen"},
       "documents":{"national_id":"079...","driver_license":"B2-...","vehicle_reg_no":"..."}}'
# → 201, kyc: "PENDING"

# ⚠️ Phải được admin duyệt KYC trước — GoOnline từ chối nếu kyc ≠ APPROVED.
curl -sX POST localhost:8080/v1/drivers/me/online -H "Authorization: Bearer $DRV"

curl -sX POST localhost:8080/v1/locations/ping -H "Authorization: Bearer $DRV" \
  -d '{"point":{"lat":10.7740,"lng":106.6995},"bearing_deg":45,"speed_mps":8,
       "accuracy_m":12,"mocked":false,"battery_pc":85,"at":"2026-08-24T10:00:00Z"}'
# → 202 {"status":"ok"}   (driver_id lấy từ token, KHÔNG lấy từ body)

curl -s localhost:8080/v1/offers -H "Authorization: Bearer $DRV"
curl -sX POST localhost:8080/v1/offers/ofr_01J.../accept -H "Authorization: Bearer $DRV"
```

Lỗi ping: `point_out_of_range` · `mock_location` (403, gắn cờ gian lận) · `low_accuracy` · `implausible_jump`
Lỗi accept: `not_your_offer` · `offer_not_pending` · `offer_expired` · **`trip_taken`** (thua cuộc giành khoá) ·
`driver_state_changed` · `kyc_not_approved` · `wallet_debt_exceeded`

### Vòng đời chuyến (phía tài xế)

```bash
POST /v1/trips/{id}/arrived    # ASSIGNED    → ARRIVED
POST /v1/trips/{id}/start      # ARRIVED     → IN_PROGRESS
POST /v1/trips/{id}/complete   # IN_PROGRESS → COMPLETED  (→ worker tự ghi sổ → PAID)
```

Sai thứ tự → `409 invalid_transition`. Không phải chuyến của mình → `403 not_your_trip`.

---

## 4.4 API quản trị

**Đăng nhập** — giống `/v1/auth/*` nhưng **không nhận trường `role`** (luôn là `admin`)
và số điện thoại phải nằm trong `ADMIN_PHONES`. Số ngoài danh sách → `403 not_admin`
**trước khi** OTP được gửi.

**Tham số truy vấn**

| Endpoint | Tham số |
|---|---|
| `GET /v1/admin/drivers` | `status` · `kyc` · `city` · `q` (tên/SĐT/biển số) · `debt=1` · `limit` (≤200, mặc định 50) |
| `GET /v1/admin/trips` | `status` · `limit` |
| `GET /v1/admin/live-map` | `lat` · `lng` (mặc định Chợ Bến Thành) · `radius` (m, mặc định 5.000, trần 50.000) · `idle=1` |

> Trạng thái không hợp lệ trả **`400 status_invalid`** — *không* im lặng trả danh sách rỗng.
> Đây là lựa chọn có chủ đích: lỗi gõ nhầm ở giao diện phải lộ ra ngay, không được giả trang thành "không có dữ liệu".

**Duyệt hồ sơ**

```bash
curl -sX POST localhost:8080/v1/admin/drivers/drv_01J.../kyc \
  -H "Authorization: Bearer $ADMIN" -d '{"approved":true}'
# → DriverRow đã cập nhật, blocked_reason đổi từ "kyc_not_approved" thành ""
```

**Bản đồ trực tuyến** trả cung + cầu **cùng bán kính, cùng thời điểm**:

```json
{ "center": {...}, "radius_m": 5000,
  "drivers": [ { "driver_id": "drv_…", "point": {...}, "bearing_deg": 45, "status": "IDLE", … } ],
  "pending": [ { "trip_id": "trp_…", "point": {...}, "waiting_sec": 83.4, … } ],
  "generated_at": "2026-08-24T10:00:00Z" }
```

`pending` sắp xếp **chờ lâu nhất lên đầu** — đó là chuyến cần can thiệp trước.

---

## 4.5 Bảng mã lỗi

| `code` | Kind | Nguồn |
|---|---|---|
| `invalid_body` | invalid | `httpx.Decode` — JSON sai / trường lạ / >1MB |
| `rate_limited` | rate_limited | `httpx.RateLimit` |
| `missing_token` / `bad_token` / `token_expired` / `forbidden` | unauthorized/forbidden | `authn` |
| `phone_invalid` | invalid | `identity.NormalizePhone` |
| `otp_expired` / `otp_invalid` / `otp_too_many_attempts` / `challenge_not_found` | invalid/rate_limited/not_found | `identity` |
| `account_blocked` / `account_not_found` | forbidden/not_found | `identity` |
| `not_admin` | forbidden | `admin.Auth` — **thông báo giống hệt mọi trường hợp** |
| `full_name_required` / `vehicle_type_invalid` / `plate_invalid` / `documents_required` / `insurance_until_invalid` | invalid | `driver.Onboard` |
| `driver_not_found` / `driver_create_failed` | not_found/conflict | `driver` repo |
| `kyc_not_approved` / `driver_suspended` / `driver_busy` / `wallet_debt_exceeded` | forbidden/conflict | **`Driver.CanAcceptTrip`** |
| `driver_on_trip` / `driver_state_changed` | conflict | `driver.GoOffline` / CAS |
| `point_out_of_range` / `mock_location` / `low_accuracy` / `implausible_jump` | invalid/forbidden | `location.Ingest` |
| `point_invalid` / `vehicle_type_invalid` / `quote_expired` | invalid | `pricing` |
| `payment_method_invalid` / `request_in_flight` | invalid/conflict | `trip.Create` |
| `rating_invalid` / `trip_not_finished` / `trip_already_rated` / `trip_has_no_driver` | invalid/conflict | `trip.Rate` |
| `insurance_until_invalid` | invalid | `driver.Onboard` — hạn bảo hiểm phải là `YYYY-MM-DD` |
| `amount_invalid` / `from_invalid` / `to_invalid` / `range_invalid` / `range_too_wide` | invalid | API ví |
| `target_type_invalid` | invalid | `admin.Audit` |
| `trip_not_found` / `trip_exists` / `not_your_trip` | not_found/conflict/forbidden | `trip` |
| **`invalid_transition`** | conflict | `trip.transition` — máy trạng thái |
| `trip_not_searching` / `trip_already_final` / `trip_version_conflict` | conflict | `trip` |
| `offer_not_found` / `not_your_offer` / `offer_not_pending` / `offer_expired` | not_found/forbidden/conflict | `matching` |
| **`trip_taken`** | conflict | `matching.Accept` — **thua cuộc giành khoá chuyến** |
| `ledger_incomplete` / `ledger_unbalanced` / `ledger_tx_id_missing` / `ledger_account_missing` / `ledger_account_type_missing` | invalid | **`wallet.Transaction.Validate`** — chốt chặn duy nhất, mọi `Post` đều qua đây |
| `amount_invalid` / `from_invalid` / `to_invalid` / `range_invalid` / `range_too_wide` | invalid | `wallet` HTTP |
| `target_type_invalid` | invalid | `admin.Audit` |
| `status_invalid` | invalid | `admin` — bộ lọc sai |
| `db_error` / `db_open_failed` / `internal_error` | internal | hạ tầng |

---

## 4.6 Giao diện quản trị tiêu thụ API thế nào

[`godrive-admin`](../godrive-admin) — Next.js 16 + React 19, **App Router, toàn bộ fetch phía máy chủ**.

```
Trình duyệt ──► Next.js server (:3000) ──► godrive API (:8080)
               token trong cookie httpOnly
```

- **Không có CORS**, vì trình duyệt **không bao giờ** gọi thẳng cổng 8080.
- Token nằm trong cookie `godrive_admin_token`: `httpOnly` + `sameSite=lax` + `secure` khi production.
  Kể cả có lỗ hổng XSS, JavaScript vẫn không đọc được token.
- `cache: "no-store"` trên mọi lời gọi — dữ liệu vận hành phải luôn tươi.
- `GoDriveError` giữ nguyên `code` của API → giao diện xử lý **theo mã**, không theo chuỗi.
- `currentAdmin()` xác thực phiên bằng cách **hỏi `/v1/admin/me`**, không tự giải mã JWT.

`src/lib/types.ts` là bản sao TypeScript của `internal/admin/domain.go`.
**Sửa struct Go ⇒ phải sửa file này** — hiện chưa có sinh mã tự động, xem [07 — T-23](07-todo.md).
