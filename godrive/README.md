# GoDrive — Skeleton hệ thống gọi xe cho thị trường Việt Nam

Bộ khung Go cho ứng dụng gọi xe (xe ôm 2 bánh + ô tô), thiết kế quanh những
ràng buộc thực tế của thị trường VN: **thanh toán tiền mặt chiếm tỉ trọng lớn**,
**Nghị định 10/2020 và 13/2023**, **máy Android giá rẻ với mạng 4G chập chờn**,
và **chi phí Maps API**.

Repo chạy được ngay: `make run` khởi động API ở chế độ in-memory, **không cần
Postgres, Redis hay bất kỳ hạ tầng nào**.

```bash
make run      # API tại http://localhost:8080
make test     # toàn bộ unit + integration test
make test-race
```

### Thiết lập máy dev

Cần Go 1.22+. Trên macOS:

```bash
brew install go              # bắt buộc
brew install libpq           # psql client (tuỳ chọn, để soi DB)
```

Chế độ in-memory không cần gì thêm — `make run` là chạy được.

**Nếu muốn dùng Postgres** (DBngin: dấu `+` → PostgreSQL → cổng 5432 → Start):

```bash
./scripts/setup-db.sh        # tạo DB godrive + bật PostGIS + chạy migration
```

Script tự phát hiện instance không có PostGIS và chuyển sang schema thay thế
trong `migrations-nogis/` (sinh bằng `scripts/gen-nogis.py`, đừng sửa tay).

> **PostGIS trên DBngin:** bản đi kèm link tới `gmp` của Homebrew. Nếu
> `CREATE EXTENSION postgis` báo thiếu `libgmpxx.4.dylib`, chạy `brew install gmp`.

> ⚠️ **Chế độ Postgres hiện chưa chạy được đầu-cuối.** `identity` chưa có
> `PostgresRepo` ([app.go](internal/app/app.go) luôn dùng `NewMemoryRepo`), nên tài khoản
> chỉ nằm trong RAM còn `drivers.account_id` lại là khoá ngoại tới bảng
> `accounts` — đăng ký tài xế sẽ lỗi `driver_create_failed`. Xem mục 7.

---

## 1. Trạng thái hiện tại

| Thành phần | Mức độ |
|---|---|
| Máy trạng thái chuyến đi + nhật ký sự kiện | ✅ đầy đủ, có test |
| Bộ ghép chuyến (chấm điểm, nhiều vòng, chống ghép trùng) | ✅ đầy đủ, có test |
| Sổ cái kép + công nợ tiền mặt tài xế | ✅ đầy đủ, có test |
| Tính giá + surge theo cung/cầu | ✅ đầy đủ |
| Đăng nhập OTP + JWT (HS256) | ✅ đầy đủ |
| Chỉ mục vị trí + chống GPS giả | ✅ bản in-memory (thay bằng Redis GEO/H3) |
| Repo Postgres — `driver`, `trip` | ✅ SQL chạy được; pgx đã nối sẵn |
| Repo Postgres — `identity` | ❌ **chưa viết** → chế độ Postgres chưa dùng được đầu-cuối |
| API vận hành + bảng điều khiển | ✅ đầy đủ, có test ([godrive-admin](../godrive-admin)) |
| Cổng thanh toán, eKYC, hoá đơn điện tử, push | 🔌 chỉ có interface, chưa nối |

---

## 2. Kiến trúc

**Modular monolith** — một binary, các module tách bạch, giao tiếp **chỉ qua
interface**. Khi cần tách microservice, thay implementation của interface bằng
gRPC client; code nghiệp vụ không đổi. Đừng chia microservices ở giai đoạn đầu.

```
cmd/
  api/            HTTP API (+ worker ở chế độ dev)
  worker/         dispatcher, outbox relay, đối soát
internal/
  config/         cấu hình từ biến môi trường
  app/            composition root: lắp ráp, router, worker
  platform/
    logger/       slog
    httpx/        JSON, mã lỗi, middleware, rate limit
    authn/        phát hành/xác thực token, middleware phân quyền
    eventbus/     interface bus (in-memory; thay bằng NATS JetStream)
  identity/       OTP theo số điện thoại VN, tài khoản
  driver/         hồ sơ, eKYC, trạng thái online (CAS chống nhận 2 chuyến)
  location/       ingest ping, chỉ mục ô lưới, phát hiện GPS giả
  pricing/        biểu giá, phụ phí đêm, surge, báo giá
  trip/           máy trạng thái + nhật ký sự kiện bất biến
  matching/       chấm điểm, chào mời theo lô, giành khoá chuyến
  wallet/         sổ cái kép, công nợ tiền mặt, khấu trừ thuế
  notification/   FCM / Zalo ZNS / SMS (interface)
  outbox/         Transactional Outbox + relay
  admin/          API vận hành: tổng hợp, lọc, duyệt hồ sơ, bản đồ
pkg/
  money/          VND kiểu int64 (KHÔNG dùng float cho tiền)
  geo/            toạ độ, haversine, lưới ô (chỗ thay H3)
  errs/           lỗi nghiệp vụ có mã ổn định cho app mobile
  id/             ID sắp xếp được theo thời gian (thân thiện với B-tree)
  idem/, clock/
migrations/       SQL cho Postgres + PostGIS
deploy/           Dockerfile, docker-compose (Postgres, Redis, NATS, EMQX, OSRM)
```

### Nguyên tắc thiết kế
- **Interface do bên tiêu thụ định nghĩa.** `matching.DriverPort` nằm trong
  package `matching`, không phải `driver`. Nhờ vậy phụ thuộc chỉ đi một chiều.
- **Mọi thao tác tạo/huỷ/thanh toán đều idempotent.** App mobile ở VN retry rất
  nhiều vì mạng chập chờn.
- **Sự kiện đi qua Outbox**, không publish trực tiếp trong transaction.
- **Tiền là `int64`.** `money.VND` không có phép chia float.

---

## 3. Ba bài toán lõi

### 3.1 Máy trạng thái chuyến (`internal/trip`)

```
CREATED → SEARCHING → ASSIGNED → ARRIVED → IN_PROGRESS → COMPLETED → PAID
              ↓           ↓          ↓
          CANCELLED   CANCELLED  CANCELLED
              ↓
           EXPIRED (không tìm được tài xế)
```

Đồ thị chuyển trạng thái là dữ liệu, không phải if-else rải rác. Mọi lần chuyển
ghi một dòng vào `trip_events` **trong cùng transaction** với bản cập nhật
chuyến — bảng này là hợp đồng vận tải điện tử, phải lưu tối thiểu 3 năm.

Lưu ý: `IN_PROGRESS → CANCELLED` **bị chặn**. Đang chở khách thì không có khái
niệm huỷ; nếu có sự cố thì đó là một luồng riêng (kết thúc sớm + hoàn tiền).

### 3.2 Bộ ghép chuyến (`internal/matching`)

Chào mời theo lô có chấm điểm, tối đa 3 vòng, mỗi vòng nới bán kính 1,5km:

```go
điểm = 1.0×ETA_giây
     + 60×(5 − đánh_giá)          // chênh 1 sao ≈ 60 giây
     + 90×(1 − tỉ_lệ_nhận_chuyến) // tài xế hay bỏ chuyến bị phạt nặng
     − 0.25×thời_gian_chờ_giây    // chờ lâu được ưu tiên
     + 0.20×góc_lệch_hướng_xe     // xe đi ngược phải quay đầu
```

Không chỉ chọn tài xế gần nhất: tài xế gần nhưng hay bỏ chuyến làm khách chờ
lâu hơn tài xế xa mà luôn nhận. Trọng số thời gian chờ giúp phân bổ thu nhập
đều hơn — yếu tố giữ chân tài xế quan trọng ở VN.

**Chống ghép trùng** có hai lớp:
1. `Store.ClaimTrip` — nguyên tử (`SET trip:{id}:claim NX EX 30` trong Redis).
2. `driver.UpdateStatus` — CAS `WHERE status='IDLE' AND version=$N` trong Postgres.

Test `TestOnlyOneDriverWinsTrip` chạy hai tài xế bấm nhận song song dưới `-race`
và khẳng định đúng một người thắng.

### 3.3 Sổ cái & công nợ tiền mặt (`internal/wallet`)

Đây là module dễ làm sập cả doanh nghiệp nhất. Sai lệch tiền tài xế là nguyên
nhân số một khiến tài xế bỏ app.

Chuyến 50.000đ **tiền mặt**, chiết khấu 20%:

| Tài khoản | Số tiền |
|---|---|
| `DRIVER_CASH` (tài xế cầm tiền khách) | +50.000 |
| `PLATFORM_REVENUE` | −50.000 |
| `DRIVER_WALLET` (trừ chiết khấu) | −10.000 |
| `PLATFORM_REVENUE` | +10.000 |
| **Tổng** | **0** |

Ví tài xế còn **−10.000đ** — đó chính là công nợ. Khi nợ vượt
`DefaultDebtLimit` (200.000đ), `Driver.CanAcceptTrip` chặn nhận chuyến cho tới
khi tài xế nạp lại qua VietQR/MoMo/ZaloPay.

`Transaction.Validate()` từ chối mọi giao dịch không cân bằng. Bảng
`ledger_entries` **chỉ INSERT**, không bao giờ UPDATE/DELETE.

---

## 4. Quyết định riêng cho thị trường Việt Nam

| Quyết định | Lý do |
|---|---|
| **MQTT (EMQX)** cho luồng vị trí, không phải WebSocket | Tài xế dùng Android giá rẻ, 4G chập chờn. MQTT tiết kiệm pin/băng thông, có QoS và Last Will để phát hiện mất kết nối. |
| **Zalo ZNS trước, SMS brandname sau** cho OTP | ZNS rẻ hơn SMS nhiều lần và tỉ lệ đọc cao hơn ở VN. |
| **OSRM tự host + Goong/VietMap**, Google chỉ làm dự phòng | Google Maps API có thể tốn hơn cả tiền server khi lên quy mô. |
| **Ví + công nợ là module bắt buộc từ Phase 1** | Tiền mặt vẫn chiếm tỉ trọng rất lớn; không thể coi là tính năng phụ. |
| **Lưu dữ liệu trong nước** (VNG Cloud / Viettel IDC / FPT Cloud) | Nghị định 13/2023 (PDPD) và Nghị định 53/2022. |
| **`trip_events` bất biến, lưu ≥3 năm** | Hợp đồng vận tải điện tử theo Nghị định 10/2020 và Thông tư 12/2020. |
| **Tách sẵn `TAX_PAYABLE` trong sổ cái** | Khấu trừ VAT + TNCN tại nguồn trên thu nhập tài xế. |
| **Trần surge 2.0** | Giảm rủi ro truyền thông và phản ứng người dùng. |
| **Regex biển số + đầu số di động VN** | Chặn dữ liệu rác ngay tại tầng nhập liệu. |

---

## 5. API

Tất cả trả JSON. Lỗi có dạng `{"code","message","trace_id"}` với `message` là
tiếng Việt, sẵn sàng hiển thị cho người dùng cuối.

```
POST   /v1/auth/otp                 gửi mã OTP (dev trả luôn dev_code)
POST   /v1/auth/verify              đổi OTP lấy access token

POST   /v1/drivers/register         đăng ký hồ sơ tài xế
GET    /v1/drivers/me
POST   /v1/drivers/me/online        bật nhận chuyến
POST   /v1/drivers/me/offline
POST   /v1/locations/ping           ping vị trí (chính thức đi qua MQTT)

POST   /v1/quotes                   báo giá cho mọi loại xe
POST   /v1/trips                    đặt chuyến (header Idempotency-Key)
GET    /v1/trips
GET    /v1/trips/{id}
GET    /v1/trips/{id}/events        nhật ký chuyển trạng thái
POST   /v1/trips/{id}/cancel
POST   /v1/trips/{id}/arrived       tài xế đã tới điểm đón
POST   /v1/trips/{id}/start
POST   /v1/trips/{id}/complete

GET    /v1/offers                   lời mời đang chờ của tài xế
POST   /v1/offers/{id}/accept
POST   /v1/offers/{id}/reject

# Vận hành — yêu cầu vai trò admin (số phải nằm trong ADMIN_PHONES)
POST   /v1/admin/auth/otp           gửi OTP cho quản trị viên
POST   /v1/admin/auth/verify        đổi OTP lấy token admin
GET    /v1/admin/me                 thông tin phiên hiện tại
GET    /v1/admin/overview           số liệu tổng quan + cảnh báo
GET    /v1/admin/drivers            bảng tài xế (?status=&kyc=&city=&q=&debt=1)
GET    /v1/admin/drivers/{id}
POST   /v1/admin/drivers/{id}/kyc   duyệt / từ chối hồ sơ
GET    /v1/admin/trips              bảng chuyến (?status=)
GET    /v1/admin/trips/{id}
GET    /v1/admin/trips/{id}/events  nhật ký chuyển trạng thái
GET    /v1/admin/live-map           cung (tài xế) + cầu (điểm đón chờ ghép)
                                    (?lat=&lng=&radius=&idle=1)

GET    /healthz
```

### Thử nhanh

```bash
make run

# Đăng nhập khách hàng
curl -s localhost:8080/v1/auth/otp \
  -H 'Content-Type: application/json' \
  -d '{"phone":"0901234567","role":"rider"}'
# -> {"challenge_id":"chl_...","dev_code":"123456"}

curl -s localhost:8080/v1/auth/verify \
  -H 'Content-Type: application/json' \
  -d '{"challenge_id":"chl_...","code":"123456","device_id":"dev1"}'
# -> {"access_token":"...","account":{...}}

# Báo giá Bến Thành -> Quận 3
curl -s localhost:8080/v1/quotes \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"pickup":{"lat":10.7725,"lng":106.6980},"dropoff":{"lat":10.8014,"lng":106.7109}}'
```

---

## 5b. Bảng điều khiển vận hành

Giao diện quản trị nằm ở repo riêng [`../godrive-admin`](../godrive-admin)
(Next.js). **Toàn bộ logic ở đây** — `internal/admin` lo tổng hợp, lọc, phân
quyền và ngưỡng cảnh báo; giao diện chỉ gọi API và hiển thị.

```bash
# 1. Cấp quyền admin trong .env rồi khởi động lại API
ADMIN_PHONES=0909999999

# 2. Tạo dữ liệu mẫu (chỉ dùng API công khai, đi qua đúng mọi quy tắc nghiệp vụ)
./scripts/seed-dev.sh

# 3. Chạy giao diện
cd ../godrive-admin && npm install && npm run dev
```

**Mặc định đóng:** `ADMIN_PHONES` rỗng thì không ai đăng nhập được. API ghi
cảnh báo lúc khởi động nếu chưa cấu hình.

Cổng `/v1/admin/auth/*` tách riêng khỏi `/v1/auth/*` là có chủ đích: luồng OTP
dùng chung cấp token theo `role` client gửi lên — đúng với rider/driver (ai
cũng đăng ký được) nhưng với admin thì chỉ cần gửi `role=admin` là leo thang
đặc quyền. Cổng admin kiểm tra danh sách **hai lần**: trước khi gửi mã (không
tốn tin nhắn, không lộ số nào là admin) và sau khi xác thực.

---

## 6. Chuyển sang hạ tầng thật

Repo dùng bản in-memory để chạy được ngay. Mỗi bước dưới đây là **thay một
implementation**, không sửa code nghiệp vụ:

```bash
# pgx ĐÃ được cài và nối sẵn vào cmd/api + cmd/worker.
# Ghim pgx v5.7.4 + x/{sync,crypto,text} bản cũ để giữ `go 1.22` trong go.mod;
# pgx ≥5.10 yêu cầu Go 1.25, sẽ tự nâng go directive nếu nâng cấp.
go get github.com/redis/go-redis/v9     # Redis GEO + khoá phân tán
go get github.com/nats-io/nats.go       # NATS JetStream
go get github.com/eclipse/paho.mqtt.golang
go get github.com/uber/h3-go/v4         # thay lưới ô bằng H3 res 8-9
```

| Hiện tại | Thay bằng |
|---|---|
| `driver.MemoryRepo` | `driver.NewPostgresRepo(db)` — đã nối, chạy được |
| `trip.MemoryRepo` | `trip.NewPostgresRepo(db)` — đã nối, có transaction + optimistic lock |
| `identity.MemoryRepo` | **cần viết `identity.PostgresRepo`** — chặn chế độ Postgres |
| `location.MemoryIndex` | Redis `GEOSEARCH`, hoặc H3 cell → Redis Set |
| `matching.MemoryStore` | Redis (`SET NX` cho `ClaimTrip`) |
| `eventbus.NewInMemory` | NATS JetStream (Kafka khi >200k msg/s) |
| `pricing.HaversineEngine` | OSRM `/table` — một request cho cả lô ứng viên |
| `matching.SimpleETA` | OSRM + cache theo cặp ô lưới |
| `notification.LogPusher` | FCM / APNs |
| `identity` OTP challenge | Redis (hiện in-memory, mất khi restart) |
| `httpx.NewRateLimit` | Redis để giới hạn toàn cụm |

Đặt `DATABASE_URL` trong `.env` rồi chạy `./scripts/setup-db.sh`. Driver pgx đã
được import sẵn ở `cmd/api` và `cmd/worker`; bỏ trống `DATABASE_URL` là quay lại
in-memory.

---

## 7. Việc chưa làm (theo thứ tự ưu tiên)

0. **`identity.PostgresRepo`** — chặn toàn bộ chế độ Postgres. `app.New` luôn
   gọi `identity.NewMemoryRepo()`, nên `accounts` không bao giờ được ghi xuống
   DB, trong khi `drivers.account_id` là khoá ngoại tới bảng đó. Hệ quả: đăng ký
   tài xế trả `driver_create_failed`. Cần repo Postgres cho `accounts` +
   `otp_challenges` (hoặc Redis cho challenge), rồi thêm nhánh chọn repo trong
   `app.New` giống `driver`/`trip`.
1. **Tích hợp cổng thanh toán** — MoMo, ZaloPay, VNPay, VietQR: cần webhook có
   xác thực chữ ký + đối soát tự động cuối ngày.
2. **eKYC** — FPT.AI hoặc VNPT eKYC, đối chiếu CCCD gắn chip với GPLX.
3. **Hoá đơn điện tử** — Viettel/VNPT/MISA meInvoice theo Nghị định 123/2020.
4. **An toàn** — nút SOS, chia sẻ hành trình, ghi âm chuyến đi.
5. **Khuyến mãi** — voucher, campaign, ngân sách và chống lạm dụng.
6. **Chống gian lận nâng cao** — phân tích đồ thị phát hiện chuyến ảo giữa các
   cặp rider–driver quen nhau (hình thức "cày cuốc" phổ biến).
7. **Đặt trước, ghép chuyến chung, giao hàng/đồ ăn.**
8. **Kho dữ liệu** — ClickHouse + BI; ML cho ETA và surge.

---

## 8. Rủi ro lớn nhất

1. **Đối soát tiền mặt.** Sai lệch một đồng cũng làm mất niềm tin của tài xế.
2. **Pháp lý.** Phải đăng ký phần mềm kết nối vận tải với Sở GTVT trước khi
   chạy thương mại. Nên có luật sư tham gia từ Phase 0.
3. **Cung tài xế.** Kỹ thuật hoàn hảo mà không có tài xế thì matching vô nghĩa.
   Ngân sách incentive thường lớn hơn ngân sách kỹ thuật.
4. **Chi phí Maps.** Cache thật mạnh, nếu không hoá đơn API sẽ vượt tiền server.
