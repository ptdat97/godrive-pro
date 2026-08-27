# 08 — Vận hành

## 8.1 Chạy tại máy

```bash
cd godrive

make run          # API tại :8080, chế độ in-memory — KHÔNG cần Postgres/Redis/NATS
make test         # toàn bộ unit + integration test (test Postgres tự bỏ qua)
make test-race    # BẮT BUỘC chạy trước khi merge thay đổi ở matching/wallet
make vet
make fmt
make build        # bin/api + bin/worker, CGO tắt, -trimpath -ldflags="-s -w"
```

**Chạy bảng điều khiển vận hành:**

```bash
cd godrive-admin
cp .env.example .env.local     # GODRIVE_API_URL=http://localhost:8080
npm install && npm run dev     # http://localhost:3000
```

> ⚠️ Bảng điều khiển **từ chối mọi đăng nhập** nếu `ADMIN_PHONES` chưa được đặt ở phía Go.
> Mặc định đóng là có chủ đích. `app.New()` log cảnh báo khi khởi động.

```bash
ADMIN_PHONES=0901234567 make run
```

---

## 8.2 Biến môi trường

Nguồn: [`internal/config/config.go`](../godrive/internal/config/config.go)

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `APP_ENV` | `dev` | `dev` / `staging` / `prod` |
| `HTTP_ADDR` | `:8080` | địa chỉ lắng nghe |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_JSON` | `false` | JSON log cho production |
| **`DATABASE_URL`** | *(rỗng)* | **rỗng ⇒ chạy toàn bộ bằng bộ nhớ.** Có giá trị ⇒ dùng Postgres cho `drivers`+`trips` |
| **`REDIS_URL`** | *(rỗng)* | Rỗng ⇒ chỉ mục vị trí, khoá giành chuyến, lời mời, báo giá, khoá idempotency và rate limit nằm trong **bộ nhớ tiến trình** ⇒ **chỉ chạy đúng 1 bản sao**. Bắt buộc ở production |
| `OSRM_URL` | *(rỗng)* | Rỗng ⇒ dùng ước lượng haversine (đường chim bay × 1.35). Sai số đi thẳng vào giá cước |
| **`NATS_URL`** | *(rỗng)* | Rỗng ⇒ sự kiện đi qua bus in-process, handler **không có ack**: giết tiến trình giữa chừng sẽ mất việc đang xử lý |
| **`MQTT_URL`** | *(rỗng)* | Rỗng ⇒ chỉ nhận ping qua HTTP. MQTT tiết kiệm pin và băng thông hơn nhiều trên máy Android giá rẻ, và có Last Will |
| `MQTT_CLIENT_ID` | `godrive-<host>-<pid>` | **Phải khác nhau giữa các pod** — hai client trùng ID sẽ liên tục đá nhau ra khỏi broker |
| **`MQTT_USERNAME`** | *(rỗng)* | Tài khoản dịch vụ của backend trên broker. Rỗng ⇒ nối mà không khai danh tính, chỉ dùng được khi broker đang mở. Xem [§8.12](#812-bảo-mật-mqtt) |
| **`MQTT_PASSWORD`** | *(rỗng)* | Mật khẩu của tài khoản dịch vụ. **Bí mật** — không đặt trong kho mã |
| `JWT_SECRET` | `dev-secret-doi-truoc-khi-len-production` | khoá ký HS256 |
| `ACCESS_TTL` | `24h` | hạn token |
| `DEV_AUTH` | `true` | trả mã OTP trong response **và mở `POST /v1/drivers/me/topup`** — chỉ dev |
| `SHUTDOWN_WAIT` | `15s` | thời gian tắt êm |
| `DATA_RESIDENCY` | `VN` | ghi chú tuân thủ NĐ 13/2023 |
| **`ADMIN_PHONES`** | *(rỗng)* | danh sách SĐT được vào bảng điều khiển, ngăn cách bằng dấu phẩy. **Rỗng = không ai vào được** |
| **`DOCUMENTS_KEY`** | *(rỗng)* | Khoá AES-256 (32 byte hex hoặc base64) mã hoá CCCD/GPLX. Rỗng ⇒ lưu **thô**. Sinh: `openssl rand -hex 32` |
| `MOMO_PARTNER_CODE` / `MOMO_ACCESS_KEY` / `MOMO_SECRET_KEY` | *(rỗng)* | Bật cổng MoMo. Thiếu `SECRET_KEY` ⇒ cổng **không** được bật |
| `ZALOPAY_APP_ID` / `ZALOPAY_KEY2` | *(rỗng)* | Bật cổng ZaloPay |
| `VNPAY_TMN_CODE` / `VNPAY_HASH_SECRET` | *(rỗng)* | Bật cổng VNPay |

### Chốt chặn ở production

`config.Load()` **từ chối khởi động** khi `APP_ENV=prod` mà:

| Điều kiện | Lỗi |
|---|---|
| `JWT_SECRET` rỗng hoặc bắt đầu bằng `dev-` | `JWT_SECRET phải được đặt ở môi trường production` |
| `DEV_AUTH` còn bật | `DEV_AUTH phải tắt ở production` |
| `DATABASE_URL` rỗng | `DATABASE_URL bắt buộc ở production` |
| `REDIS_URL` rỗng | `REDIS_URL bắt buộc ở production (nếu không sẽ chỉ chạy được 1 bản sao)` |
| `DOCUMENTS_KEY` rỗng | `DOCUMENTS_KEY bắt buộc ở production (mã hoá CCCD/GPLX)` |

> Đây là chốt chặn tốt nhưng **chưa đủ**: `ADMIN_PHONES` rỗng ở production chỉ log warning
> chứ không chặn khởi động. Cân nhắc nâng thành lỗi cứng.

---

## 8.3 Dựng cơ sở dữ liệu

### Cách 1 — Docker (khuyến nghị)

```bash
make up      # postgres+postgis, redis, nats, emqx (osrm cần profile "routing")
export DATABASE_URL="postgres://godrive:godrive@localhost:5432/godrive?sslmode=disable"
make migrate-up      # chạy TẤT CẢ migration, hiện có 0001 … 0004
```

Cần `golang-migrate`: `brew install golang-migrate`

### Cách 2 — DBngin trên macOS

```bash
./scripts/setup-db.sh      # tạo database + chạy 0001, tự dò psql của Homebrew
for f in migrations/000[234]_*.up.sql; do psql "$DATABASE_URL" -f "$f"; done
```

> `setup-db.sh` mới chỉ chạy `0001`. Migration sau đó chạy bằng `make migrate-up` (golang-migrate)
> hoặc `psql -f` như trên.

Bản Postgres của DBngin **không kèm PostGIS** → script tự chuyển sang
[`migrations-nogis/`](../godrive/migrations-nogis/) (dùng `lat`/`lng` `DOUBLE PRECISION` + b-tree
thay cho `GEOGRAPHY` + GIST).

> **Không dùng biến thể nogis ở production** — truy vấn lân cận theo bán kính cần index GIST.
> Biến thể này sinh tự động từ schema chính bằng `scripts/gen-nogis.py`; **đừng sửa tay**.

### Chạy test tích hợp Postgres

Các test này **XOÁ SẠCH bảng** trước khi chạy, nên chúng dùng biến môi trường **riêng** —
không bao giờ dùng lại `DATABASE_URL`:

```bash
createdb godrive_test
migrate -path migrations -database "postgres://postgres@localhost:5432/godrive_test?sslmode=disable" up

TEST_DATABASE_URL="postgres://postgres@localhost:5432/godrive_test?sslmode=disable" \
  go test ./internal/app/ -race -count=1
```

Không đặt biến này thì các test Postgres tự `SKIP`, `make test` vẫn xanh.

> **Tên CSDL bắt buộc chứa `test`.** `requireTestDatabase` từ chối chạy nếu không, và báo lỗi chỉ rõ
> cách tạo CSDL đúng. Biến môi trường riêng thôi chưa đủ: chỉ cần dán nhầm một chuỗi kết nối là bộ
> test lặng lẽ `TRUNCATE` sạch cơ sở dữ liệu đó. Trước đây mặc định trong `Makefile` trỏ thẳng vào
> CSDL dev — và từ khi cấu hình vận hành nằm trong bảng `settings`, mất nó là mất luôn biểu giá cùng
> toàn bộ lịch sử thay đổi.

### Trạng thái chế độ Postgres

✅ **Dùng được từ GĐ 0.** Luồng đầu-cuối đã kiểm chứng trên Postgres 18.4 + PostGIS 3.6.3:
đăng nhập → đăng ký tài xế → duyệt KYC → online → ping → báo giá → đặt chuyến → ghép → hoàn tất → `PAID`.

✅ **Sổ cái đã bền từ GĐ 1** — restart không mất một đồng công nợ nào.

⚠️ **Nhưng vẫn chỉ được chạy MỘT bản sao.** Bốn store còn nằm trong bộ nhớ tiến trình:
`matching.offers`, `location.index`, `pricing.quotes`, `idem.keys`.
`app.New` log cảnh báo đúng danh sách này khi khởi động. Chạy 2 pod sẽ gây: hai tài xế cùng nhận một
chuyến · báo giá phát ở pod A không đặt được ở pod B · idempotency vô hiệu. Đó là GĐ 3.

---

## 8.4 Hạ tầng phát triển

[`deploy/docker-compose.yml`](../godrive/deploy/docker-compose.yml) — **ứng dụng vẫn chạy được mà không cần
bất kỳ dịch vụ nào trong này**.

| Dịch vụ | Image | Cổng | Đã nối vào code? |
|---|---|---|---|
| `postgres` | `postgis/postgis:16-3.4` | 5432 | ✅ `accounts`, `drivers`, `trips`, `trip_events`, `offers`, `trip_claims`, sổ cái, outbox, nhật ký admin |
| `redis` | `redis:7-alpine` (AOF) | 6379 | ✅ chỉ mục vị trí, khoá chuyến, lời mời, báo giá, idempotency, rate limit |
| `nats` | `nats:2.10-alpine` (`-js -m 8222`) | 4222, 8222 | ✅ bus sự kiện có ack; 8222 là cổng giám sát |
| `emqx` | `emqx/emqx:5.6` | 1883, 18083 | ✅ luồng vị trí; 18083 là bảng điều khiển |
| `osrm` | `osrm/osrm-backend` | 5000 | ✅ đặt `OSRM_URL` để bật (profile `routing`) |

Chuẩn bị dữ liệu OSRM trước khi bật profile `routing`:

```bash
wget http://download.geofabrik.de/asia/vietnam-latest.osm.pbf
docker run -t -v "$PWD/osrm:/data" osrm/osrm-backend \
  osrm-extract -p /opt/motorcycle.lua /data/vietnam-latest.osm.pbf
docker compose -f deploy/docker-compose.yml --profile routing up -d
```

---

## 8.5 Triển khai

### Nên chạy tiến trình nào?

**Hiện tại: chỉ `cmd/api`.** Nó đã bao gồm `StartWorkers`.

`cmd/worker` chạy **bus in-process riêng** với state riêng → ở chế độ Postgres nó **không nhận được
sự kiện nào** từ `cmd/api`, còn outbox relay thì đọc một store rỗng mà không ai ghi vào.
Tách tiến trình chỉ có ý nghĩa **sau khi** thay `eventbus` bằng NATS ([T-21](07-todo.md#t-21)).

### Số bản sao

**Hiện tại: đúng 1.** Sáu store luôn nằm trong bộ nhớ tiến trình ([01 §1.3](01-kien-truc-tong-the.md)):
sổ cái, offer + khoá chuyến, chỉ mục vị trí, kho báo giá, khoá idempotency, tài khoản.

Chạy 2 pod sẽ gây: mất tiền khi pod chết · hai tài xế cùng nhận một chuyến ·
báo giá phát ở pod A không đặt được ở pod B · idempotency vô hiệu.

→ Nhiều bản sao chỉ khả thi **sau GĐ 3**.

### Nơi đặt hạ tầng

**Trong lãnh thổ Việt Nam** — VNG Cloud / Viettel IDC / FPT Cloud, hoặc vùng có bản sao trong nước.
Căn cứ: **Nghị định 13/2023** (bảo vệ dữ liệu cá nhân) và **Nghị định 53/2022**.

### Tắt êm

`cmd/api` bắt `SIGINT`/`SIGTERM` → `srv.Shutdown(ctx)` với `SHUTDOWN_WAIT` (mặc định 15s) →
`app.Close()` → `Bus.Close()` **chờ mọi handler đang chạy xong** (`wg.Wait()`) → đóng kết nối DB.

> ⚠️ Goroutine `Matcher.Dispatch` chạy tới `MaxRounds × OfferTTL ≈ 45 giây` — **dài hơn**
> `SHUTDOWN_WAIT = 15s`. Chuyến đang dispatch lúc tắt sẽ bị cắt giữa chừng và **kẹt ở `SEARCHING`**
> (không có gì đưa nó về `EXPIRED`). Cần job dọn chuyến kẹt, hoặc tăng `SHUTDOWN_WAIT` lên ≥ 60s.

---

## 8.6 Sự cố thường gặp

| Triệu chứng | Nguyên nhân | Xử lý |
|---|---|---|
| `driver_create_failed` ở chế độ Postgres | Đã sửa ở GĐ 0. Nếu vẫn gặp: chưa chạy migration `0002` | `psql $DATABASE_URL -f migrations/0002_identity_and_documents.up.sql` |
| `insurance_until_invalid` khi đăng ký | Ngày bảo hiểm không theo dạng `YYYY-MM-DD` | Gửi `2027-03-15`, không phải `15/03/2027` |
| Admin thấy `blocked_reason: driver_busy` cho tài xế **chưa bật app** | `CanAcceptTrip` dùng chung một mã cho mọi trạng thái ≠ `IDLE` ([G-27](05-doi-chieu-spec-code.md#g-27)) | Đối chiếu thêm cột `status`; sẽ tách mã riêng cho `OFFLINE` |
| Admin đăng nhập trả `not_admin` | `ADMIN_PHONES` chưa đặt, hoặc SĐT không khớp sau chuẩn hoá E.164 | `ADMIN_PHONES=0901234567 make run`. `0901…` và `+8490…` là **cùng một người** |
| Không có `dev_code` trong response OTP | `DEV_AUTH=false` | Chỉ dùng ở dev; production dùng OTP thật |
| Chuyến kẹt ở `SEARCHING` | Không có tài xế `IDLE` ping còn tươi trong bán kính; hoặc tiến trình tắt giữa lúc dispatch | Kiểm `GET /v1/admin/live-map`; cảnh báo `trips_stuck` đã bắt được ca này |
| Tài xế không nhận được offer | KYC chưa `APPROVED` · ping cũ hơn 45s · pin < 15% · sai loại xe · `status ≠ IDLE` | `GET /v1/admin/drivers/{id}` → xem `blocked_reason` + `last_seen` |
| Số dư ví về 0 sau khi restart | Đang chạy in-memory (chưa đặt `DATABASE_URL`) | Đặt `DATABASE_URL`; ở chế độ Postgres sổ cái đã bền từ GĐ 1 |
| `POST /v1/drivers/me/topup` trả 404 | Đúng như thiết kế: endpoint này **chỉ có ở dev** | Ở production, tiền vào ví đến từ webhook cổng thanh toán ([T-22](07-todo.md#t-22)) |
| `ledger_account_missing` khi ghi sổ | Bút toán thiếu `account_id` — lỗi lập trình, không phải dữ liệu xấu | Xem bút toán nào trong `wallet/postings.go` bị gọi với ID rỗng |
| `request_in_flight` lặp lại mãi | Đã sửa ở GĐ 1 ([G-28](05-doi-chieu-spec-code.md#g-28)) — khoá idempotency nay được nhả khi thất bại | Nếu còn gặp: có request khác cùng khoá đang thật sự chạy |
| Surge luôn 1.0 | `RecordRequest` chưa được nối ([G-07](05-doi-chieu-spec-code.md#g-07)) | [T-07](07-todo.md#t-07) |
| Tài xế nợ nhiều vẫn nhận chuyến | Đã sửa ở GĐ 1 — nếu còn gặp, kiểm `SELECT SUM(amount_vnd) … account_type='DRIVER_WALLET'` so với `DefaultDebtLimit` |
| Tiến trình chết đột ngột, không log HTTP | Panic trong goroutine dispatch ([G-14](05-doi-chieu-spec-code.md#g-14)) | [T-14](07-todo.md#t-14) |
| `429 rate_limited` khi test tải | 30 req/s, burst 60 **mỗi IP** | Đặt `X-Forwarded-For` khác nhau, hoặc chỉnh `httpx.NewRateLimit` trong `app.Router` |

---

## 8.7 Câu SQL kiểm tra sức khoẻ

**Toàn bộ các câu dưới đây đã được chạy thật sau mỗi lần kiểm chứng đầu-cuối và đều sạch** —
kể cả sau khi `SIGKILL` một pod giữa chừng.
Chạy định kỳ trong CI **và** production:

```sql
-- BẤT BIẾN #1: mọi giao dịch phải cân bằng. Phải trả 0 dòng.
SELECT tx_id, SUM(amount_vnd) AS lech
FROM ledger_entries GROUP BY tx_id HAVING SUM(amount_vnd) <> 0;

-- BẤT BIẾN #2: một tài xế tối đa một chuyến đang hoạt động. Phải trả 0 dòng.
SELECT driver_id, count(*) FROM trips
WHERE status IN ('ASSIGNED','ARRIVED','IN_PROGRESS')
GROUP BY driver_id HAVING count(*) > 1;

-- BẤT BIẾN #3: mỗi chuyến duy nhất một offer ACCEPTED. Phải trả 0 dòng.
SELECT trip_id, count(*) FROM offers
WHERE status='ACCEPTED' GROUP BY trip_id HAVING count(*) > 1;

-- BẤT BIẾN #4: mọi lần đổi trạng thái đều có event tương ứng. Phải trả 0 dòng.
SELECT t.id FROM trips t
LEFT JOIN trip_events e ON e.trip_id = t.id
GROUP BY t.id, t.status HAVING count(e.id) = 0;

-- SỨC KHOẺ: giao dịch cổng thanh toán treo ở PENDING quá lâu.
-- Bình thường job ExpireStale dọn; số này tăng đều nghĩa là job không chạy.
SELECT count(*) FROM payment_transactions
WHERE status = 'PENDING' AND expires_at < now();

-- BẤT BIẾN #5: mỗi chuyến chỉ có một offer ACCEPTED. Phải trả 0 dòng.
SELECT trip_id, count(*) FROM offers
WHERE status='ACCEPTED' GROUP BY trip_id HAVING count(*) > 1;

-- BẤT BIẾN #6: tài xế IDLE phải có mốc bắt đầu rảnh. Phải trả 0.
SELECT count(*) FROM drivers WHERE status='IDLE' AND idle_since IS NULL;

-- SỨC KHOẺ: outbox tồn đọng (cảnh báo nếu > 100 hoặc cũ hơn 60 giây).
SELECT count(*), min(created_at) FROM outbox WHERE published_at IS NULL;

-- SỨC KHOẺ: sự kiện CHẾT — đã thử quá 10 lần. Bất kỳ giá trị nào khác 0 đều cần người xem,
-- vì đó là sự kiện nghiệp vụ đã mất.
SELECT id, topic, attempts, created_at FROM outbox
WHERE published_at IS NULL AND attempts >= 10;

-- SỨC KHOẺ: bút toán mồ côi, không thuộc giao dịch nào. Phải trả 0.
SELECT count(*) FROM ledger_entries e
LEFT JOIN ledger_transactions t ON t.tx_id = e.tx_id
WHERE t.tx_id IS NULL;

-- BẤT BIẾN #7: một tài xế chỉ được CHI TRẢ một lần trong một đợt. Phải trả 0 dòng.
--
-- Trả tiền hai lần là loại sai sót không sửa được bằng cách xin lỗi.
SELECT account_id, ref_id, count(*) FROM ledger_entries
WHERE ref_type = 'PAYOUT' AND account_type = 'DRIVER_WALLET'
GROUP BY account_id, ref_id HAVING count(*) > 1;

-- BẤT BIẾN #8: giấy tờ phải nằm MÃ HOÁ. Phải trả 0 dòng.
--
-- Chạy câu này sau mỗi lần triển khai: quên đặt DOCUMENTS_KEY là một cấu hình
-- sai âm thầm — hệ thống vẫn chạy bình thường, chỉ có dữ liệu là nằm thô.
SELECT id FROM drivers
WHERE national_id <> '' AND national_id NOT LIKE 'enc:v1:%';

-- BẤT BIẾN #9: một chuyến chỉ được ghi sổ MỘT lần, kể cả khi sự kiện bị giao
-- lại sau khi pod xử lý nó chết. Phải trả 0 dòng.
SELECT ref_id, count(*) FROM ledger_transactions
WHERE ref_type = 'TRIP' GROUP BY ref_id HAVING count(*) > 1;

-- BẤT BIẾN #5: mọi bút toán thuộc một giao dịch đã đăng ký. Phải trả 0.
SELECT count(*) FROM ledger_entries e
LEFT JOIN ledger_transactions t ON t.tx_id = e.tx_id
WHERE t.tx_id IS NULL;

-- SỨC KHOẺ: cache lệch sổ cái. Phải trả 0 dòng.
SELECT d.id, d.wallet_balance, COALESCE(SUM(l.amount_vnd), 0) AS so_cai
FROM drivers d
LEFT JOIN ledger_entries l ON l.account_id = d.id AND l.account_type = 'DRIVER_WALLET'
GROUP BY d.id, d.wallet_balance
HAVING d.wallet_balance <> COALESCE(SUM(l.amount_vnd), 0);

-- VẬN HÀNH: tài xế đang nợ vượt hạn mức 200.000đ.
SELECT account_id, SUM(amount_vnd) AS so_du FROM ledger_entries
WHERE account_type = 'DRIVER_WALLET'
GROUP BY account_id HAVING SUM(amount_vnd) < -200000
ORDER BY 2;
```

---

## 8.8 Danh sách kiểm tra trước khi phát hành

**Không tick bừa.** Mỗi dòng đều là một cách mất tiền hoặc mất giấy phép.

### Chặn phát hành
- [ ] `APP_ENV=prod`, `JWT_SECRET` là giá trị ngẫu nhiên ≥ 32 byte, `DEV_AUTH=false`
- [ ] `ADMIN_PHONES` đặt tường minh, chỉ gồm người thực sự cần quyền
- [x] Sổ cái đã nằm ở Postgres ([T-02](07-todo.md#t-02)) — **tuyệt đối không phát hành với `MemoryLedger`**
- [ ] **`DEV_AUTH=false`** ⇒ `POST /v1/drivers/me/topup` không được đăng ký. Kiểm bằng cách gọi thử: phải trả 404
- [ ] Role ứng dụng **không có** `UPDATE`/`DELETE` trên `ledger_entries`, `trip_events`, `admin_audit_log` ([T-27](07-todo.md#t-27))
- [ ] Không còn dòng log `"một số store vẫn ở bộ nhớ dù đã bật Postgres"` lúc khởi động
- [ ] Toàn bộ câu SQL bất biến ở §8.7 trả về kết quả sạch trên dữ liệu staging
- [ ] `NATS_URL` đã đặt — không có nó thì giết pod giữa chừng sẽ mất việc đang xử lý
- [ ] Mỗi pod có `MQTT_CLIENT_ID` **khác nhau**
- [ ] Broker MQTT **đã bật xác thực**: `mosquitto_pub` nặc danh phải thất bại ([§8.12](#812-bảo-mật-mqtt))
- [ ] `authorization.no_match = deny` và **không có** nguồn luật `file` của EMQX
- [ ] `/internal/mqtt/auth` và `/internal/mqtt/authz` **không** tiếp cận được từ Internet
- [x] `go test ./... -race -count=6` xanh ở **cả** in-memory lẫn Postgres ✅
- [ ] `DEV_AUTH=false` ⇒ endpoint `POST /v1/drivers/me/topup` **không được đăng ký** (kiểm bằng curl, phải trả 404)
- [ ] Sao lưu Postgres + đã **thử khôi phục thật**, không chỉ cấu hình sao lưu

### Pháp lý (spec §10 rủi ro #2)
- [ ] Đã đăng ký phần mềm kết nối vận tải với **Sở GTVT**
- [ ] Biểu giá khớp **hồ sơ kê khai giá cước đã nộp** — không phải bảng mẫu TP.HCM trong code
- [ ] Chính sách lưu trữ theo [02 §2.7](02-mo-hinh-du-lieu.md): `trip_events` ≥ 3 năm, `ledger_entries` ≥ 10 năm
- [ ] Dữ liệu cá nhân lưu trong lãnh thổ VN (NĐ 13/2023, NĐ 53/2022)
- [x] CCCD / GPLX đã mã hoá ở tầng ứng dụng ✅ GĐ 4 — kiểm bằng bất biến #8 ở §8.7
- [ ] `DOCUMENTS_KEY` đã **sao lưu riêng**, không để chung với bản sao lưu CSDL
- [ ] Khoá bí mật của cổng thanh toán lấy từ kho bí mật, **không** nằm trong biến môi trường thô
- [ ] Đã thử một webhook **giả mạo chữ ký** trên staging và xác nhận bị từ chối + có log
- [ ] `TaxPermille` chỉ bật **sau khi kế toán thuế xác nhận**

### Vận hành
- [x] `/metrics` phát số liệu Prometheus; `/readyz` kiểm thật DB + Redis ✅ GĐ 3
- [ ] Đã cấu hình cảnh báo trên các số liệu sau (xem §8.9)
- [ ] `SHUTDOWN_WAIT` ≥ 60s **hoặc** đã có job dọn chuyến kẹt `SEARCHING` (xem §8.5)
- [ ] Runbook cho: sổ cái lệch · cổng thanh toán chết · OSRM chết · Redis chết
- [ ] Load test riêng module `matching` — spec §8 nhóm C #13 gọi đây là điểm nghẽn đầu tiên


---

## 8.9 Số liệu và ngưỡng cảnh báo

`GET /metrics` phát định dạng Prometheus. Những số liệu đáng đặt cảnh báo nhất:

| Số liệu | Loại | Cảnh báo khi |
|---|---|---|
| `godrive_outbox_dead` | gauge | **> 0** — sự kiện nghiệp vụ đã mất. Đây là cảnh báo quan trọng nhất trong cả hệ thống |
| `num_ack_pending` (từ `/jsz` của NATS) | — | cao kéo dài — có handler treo, việc không được ack |
| `num_redelivered` (từ `/jsz`) | — | tăng đều — có handler lỗi lặp lại |
| `godrive_outbox_pending` | gauge | > 100 hoặc tăng đều — relay chết hoặc bus hỏng |
| `godrive_trip_dispatch_seconds` | histogram | p95 > 30s — khách chờ quá lâu để được ghép |
| `godrive_trips_searching` | gauge | tăng đều mà `godrive_drivers_idle` không tăng — thiếu cung |
| `godrive_offers_total{outcome}` | counter | tỉ lệ `accepted/created` giảm — chất lượng ghép chuyến đi xuống |
| `godrive_settle_errors_total` | counter | **> 0** — có chuyến không ghi sổ được |
| `godrive_surge_permille` | histogram | p99 chạm 2000 kéo dài — thiếu cung nghiêm trọng |
| `godrive_http_request_duration_seconds` | histogram | p99 > 1s |
| Bất kỳ gauge nào **= −1** | — | Phép đo lỗi (không đọc được DB/Redis), khác hẳn với "thật sự bằng 0" |

> **Nhãn có số giá trị hữu hạn.** `route` được đưa về khuôn mẫu (`/v1/trips/{id}`) trước khi làm
> nhãn. Đưa thẳng `URL.Path` vào nhãn là cách chắc chắn làm nổ Prometheus: mỗi chuyến đi sẽ tạo một
> chuỗi số liệu mới và không bao giờ được thu hồi.


---

## 8.10 Vận hành đối soát và chi trả

Hai bước tách rời, **cố ý**: kế toán phải xem được danh sách trước khi tiền rời khỏi tài khoản.

```
1. Calculate(kỳ)   chốt danh sách → trạng thái CALCULATED
                   (kỳ phải đã KẾT THÚC — số dư của kỳ đang chạy còn thay đổi)
2. người xem lại   SELECT * FROM settlement_items WHERE batch_id = ...
3. Pay(đợt)        ghi bút toán chi trả → trạng thái PAID
```

Chạy lại bước nào cũng an toàn: `Calculate` cho cùng kỳ trả về đợt cũ, `Pay` cho đợt đã trả
không chi thêm đồng nào.

```sql
-- Đợt gần nhất và tình hình từng dòng
SELECT b.id, b.period_start, b.period_end, b.status, b.driver_count, b.total_vnd,
       count(*) FILTER (WHERE i.status = 'PAID')    AS da_tra,
       count(*) FILTER (WHERE i.status = 'SKIPPED') AS duoi_nguong,
       count(*) FILTER (WHERE i.status = 'FAILED')  AS that_bai
FROM settlement_batches b LEFT JOIN settlement_items i ON i.batch_id = b.id
GROUP BY b.id ORDER BY b.created_at DESC LIMIT 5;

-- Dòng THẤT BẠI cần xử lý tay
SELECT driver_id, amount_vnd, reason FROM settlement_items
WHERE status = 'FAILED' ORDER BY created_at DESC;

-- Đối soát với sao kê cổng thanh toán cho một ngày
SELECT provider, status, count(*), sum(amount_vnd)
FROM payment_transactions
WHERE created_at >= date_trunc('day', now()) GROUP BY provider, status;
```

> **`FAILED` khác `SKIPPED`.** `SKIPPED` là dưới ngưỡng chi trả — bình thường, số dư giữ lại cho
> đợt sau. `FAILED` là ghi sổ hỏng sau khi đã giành quyền chi — cần người xem, vì dòng đó đã bị
> đánh dấu rồi trả về thất bại.

---

## 8.11 Cấu hình vận hành (sửa từ giao diện quản trị)

Từ **2026-08-25**, những con số điều khiển hệ thống không còn nằm trong mã nguồn. Chúng nằm trong
bảng `settings`, sửa được ở **Bảng điều khiển → Cấu hình** (`/settings`), và **có hiệu lực trong
vòng 5 giây mà không cần triển khai lại**.

| Nhóm | Ô | Điều khiển cái gì |
|---|---|---|
| `pricing` | 24 | Biểu giá 3 loại xe (mở cửa, km, phút, giá sàn, phụ phí đêm, chiết khấu), hạn báo giá, khung giờ đêm |
| `surge` | 5 | Bật/tắt, trần, cửa sổ đếm cầu, bán kính đếm cung, bậc thang |
| `matching` | 13 | Bán kính và số vòng chào mời, số tài xế mỗi vòng, hạn lời mời, pin tối thiểu, 5 trọng số chấm điểm |
| `wallet` | 5 | Hạn mức công nợ, thuế khấu trừ, ngưỡng chi trả, phí huỷ, cửa sổ huỷ miễn phí |
| `location` | 3 | Ngưỡng ping quá hạn, tốc độ tối đa hợp lý, sai số GPS chấp nhận được |

### Ba lớp bảo vệ

1. **Ngưỡng cứng trong code** (`internal/settings/groups.go`). Mỗi ô có khoảng hợp lệ trả lời câu
   hỏi *"giá trị nào, nếu ai đó gõ nhầm, sẽ gây thiệt hại không sửa được bằng cách sửa lại cấu
   hình?"* — ví dụ chiết khấu tối đa 40%, trần tăng giá tuyệt đối 3,0 lần, thuế tối đa 20%.
   Ngưỡng công bố cho giao diện và ngưỡng thực thi là **cùng một nguồn**, và
   `TestSchemaBoundsMatchValidation` chốt chúng không trôi khỏi nhau.
2. **Khoá lạc quan theo `version`.** Hai người cùng sửa thì người sau nhận `setting_version_conflict`
   và phải tải lại — hệ thống **không tự gộp hai bản sửa**.
3. **Bắt buộc ghi lý do** (≥ 5 ký tự), kiểm ở **tầng API** chứ không chỉ trên giao diện, nên gọi
   thẳng bằng script cũng không lách được. Lý do vào `settings_history` và `admin_audit_log`.

### Điều cần biết trước khi đổi

> **Biểu giá là hồ sơ pháp lý.** Giá cước phải khớp hồ sơ kê khai giá cước đã nộp cho Sở GTVT. Đổi
> trên giao diện mà chưa nộp hồ sơ mới là vi phạm — giao diện có cảnh báo này ngay trên biểu mẫu,
> nhưng nó không thay được quy trình nộp hồ sơ.

> **Thay đổi không hồi tố.** Báo giá đã phát cho khách và chuyến đang chạy giữ nguyên giá cũ; chỉ
> báo giá và chuyến phát sinh *sau đó* mới dùng giá mới. Đây là hành vi cố ý, có test chốt
> (`TestExistingQuoteUnaffectedByTariffChange`).

> **Trọng số ghép chuyến đổi là thu nhập tài xế đổi.** Chỉ nên chạm khi có số liệu thật hoặc kết
> quả A/B test. Đổi mò sẽ làm thu nhập biến động mà không ai giải thích được vì sao.

> **Thuế khấu trừ cần kế toán thuế xác nhận.** Mức hiện hành cho cá nhân kinh doanh vận tải là
> 45 phần nghìn (4,5%). Mặc định để **0** — bật lên là bắt đầu giữ lại tiền của tài xế.

### Truy vết một thay đổi

```sql
-- Ai đổi gì, khi nào, vì sao — mới nhất trước
SELECT version, changed_by, reason, at FROM settings_history
WHERE key = 'pricing' ORDER BY at DESC LIMIT 20;

-- Xem đúng những ô đã đổi giữa hai phiên bản liền nhau
SELECT h.version, h.reason,
       jsonb_pretty(h.old_value::jsonb) AS truoc,
       jsonb_pretty(h.new_value::jsonb) AS sau
FROM settings_history h WHERE h.key = 'pricing' AND h.version = 3;

-- Nhóm nào đang chạy bằng mặc định trong code (chưa từng ai sửa)
SELECT k FROM unnest(ARRAY['pricing','surge','matching','wallet','location']) k
WHERE k NOT IN (SELECT key FROM settings);
```

Giao diện lịch sử tự so hai phiên bản và chỉ hiện những ô thật sự đổi, kèm nhãn tiếng Việt:
*"Đơn giá mỗi km — Xe máy: 5.000 → 4.300"*. Bản ghi **đầu tiên** của mỗi nhóm so với **giá trị mặc
định trong mã nguồn**, vì đó mới là thứ hệ thống đang chạy trước lần sửa đó.

### Khôi phục khi cấu hình sai

Cấu hình sai **không cần triển khai lại để sửa** — vào lại giao diện và sửa tiếp. Ba đường lùi:

- **Sửa tiếp trên giao diện.** Nhanh nhất, và để lại dấu vết đúng.
- **Lấy lại giá trị cũ từ lịch sử.** `SELECT old_value FROM settings_history WHERE key=$1 AND version=$2`
  rồi `PUT` lại qua API với lý do rõ ràng.
- **Xoá dòng để về mặc định.** `DELETE FROM settings WHERE key='pricing'` — hệ thống lùi về giá trị
  trong mã nguồn ngay lần nạp lại kế tiếp. Dùng khi giá trị trong CSDL hỏng tới mức không sửa được.

> **Giá trị hỏng không làm sập hệ thống.** Nếu ai đó sửa tay vào CSDL và ghi vào một giá trị không
> hợp lệ, nhóm đó lùi về mặc định còn các nhóm khác vẫn nạp bình thường
> (`TestCorruptStoredValueFallsBackToDefault`). Tương tự, CSDL lỗi thì hệ thống dùng ảnh chụp cũ
> và **chạy tiếp** — dừng phục vụ vì không đọc được cấu hình là biến sự cố nhỏ thành sự cố lớn.

---

## 8.12 Bảo mật MQTT

Trước **2026-08-27** broker **mở toang**: ai kết nối được cũng publish được vào `drv/{id}/loc` của
bất kỳ tài xế nào — tức giả được vị trí người khác và qua đó giành chuyến ở khu vực mình không hề
có mặt. Chống gian lận ở tầng ứng dụng (tốc độ bất khả thi, sai số GPS) vẫn chạy, nhưng nó lọc
**nội dung** chứ không xác minh **người gửi**.

### Cách hoạt động

Broker không giữ danh sách người dùng. Nó **hỏi ngược lại backend** ở hai thời điểm:

| Khi nào | Broker gọi | Backend trả lời |
|---|---|---|
| Thiết bị kết nối | `POST /internal/mqtt/auth` | cho vào hay không, có phải tài khoản dịch vụ không |
| Thiết bị pub/sub một topic | `POST /internal/mqtt/authz` | được hay không |

Danh tính tài xế, trạng thái khoá tài khoản và việc thu hồi phiên đã sống sẵn ở backend. Nhân bản
chúng sang broker là tạo ra bản sao thứ hai chắc chắn sẽ lệch — tài xế bị khoá lúc 9 giờ mà 10 giờ
vẫn đẩy được vị trí.

**Mật khẩu MQTT của thiết bị chính là token phiên.** Không cấp thêm loại thông tin đăng nhập nào
nữa: thêm một loại là thêm một thứ phải cấp, phải xoay vòng và phải thu hồi — trong khi token đã có
đủ cả ba. Luật vào cửa nằm ở [internal/mqttauth](../godrive/internal/mqttauth), có test riêng.

### Quyền của một tài xế

```
drv/{id}/loc      publish     ping vị trí
drv/{id}/status   publish     Last Will
drv/{id}/offer    subscribe   lời mời chuyến (T-31)
drv/{id}/trip     subscribe   chuyển trạng thái chuyến (T-31)
```

Topic so khớp **chính xác**, không nhận ký tự đại diện. Backend dùng tài khoản dịch vụ riêng
(`MQTT_USERNAME` / `MQTT_PASSWORD`) với cờ superuser để đọc topic của mọi tài xế.

### Ba cái bẫy trong cấu hình mặc định của EMQX

Cấu hình nằm ở [deploy/emqx/emqx.conf](../godrive/deploy/emqx/emqx.conf) và **phải là mã nguồn**.
Chỉnh bằng bảng điều khiển hay REST API chỉ có tác dụng trên container đang chạy; dựng lại là quay
về mặc định mở toang mà không có gì báo.

1. **`authorization.no_match` mặc định là `allow`.** Thao tác không khớp luật nào thì *được phép* —
   quên một luật không gây lỗi, nó chỉ lặng lẽ mở một cánh cửa. Phải đặt `deny`.
2. **Bộ luật `file` mặc định kết thúc bằng `{allow, all}`**, và còn một dòng
   `{allow, {ipaddr, "127.0.0.1"}, all, ["#"]}` mở toang cho mọi tiến trình chạy cùng máy với
   broker. Cấu hình của GoDrive **bỏ hẳn nguồn `file`**.
3. **`deny_action` mặc định là `ignore`** — im lặng bỏ gói, thiết bị tưởng đã gửi thành công. Đặt
   `disconnect` để lỗi lộ ra ngay lần đầu.

> **Hệ quả của `disconnect` khi viết test:** một vi phạm là mất phiên. Dùng chung một kết nối cho cả
> phép thử hợp lệ lẫn phép thử vi phạm thì lệnh sau sẽ không bao giờ được gửi, và test đổ lỗi nhầm
> cho luật ACL. Mỗi phép thử một kết nối riêng.

> **EMQX 5.6 bỏ qua danh sách quyền gửi kèm phản hồi xác thực.** Chỗ đó có trong giao thức, nhưng
> thử trên 5.6.1 thì nó không đọc — kết nối vào được mà không topic nào dùng được. `is_superuser`
> thì lại có tác dụng, nên phản hồi rõ ràng *có* được đọc. Vì vậy phân quyền đi qua endpoint riêng,
> nơi luật nằm trong Go và có test.

### Kiểm tra sau khi triển khai

```bash
# Nặc danh PHẢI bị từ chối
mosquitto_pub -h <broker> -t 'drv/bat-ky/loc' -m '{}' ; echo "mã thoát: $?"   # khác 0 là đúng

# Ba bất biến, chạy trên hạ tầng thật
TEST_MQTT_URL=tcp://<broker>:1883 TEST_API_URL=http://<api> \
TEST_MQTT_USERNAME=... TEST_MQTT_PASSWORD=... \
  go test ./internal/app/ -run 'TestDriverCannotPublish|TestCannotConnect|TestCannotHijack' -v
```

Test tự dừng với thông báo rõ ràng nếu broker vẫn đang mở — không có chuyện nó xanh một cách vô
nghĩa vì chẳng có gì được bật.

> **`MQTT_USERNAME`/`MQTT_PASSWORD` rỗng** thì backend nối vào broker mà không khai danh tính, và
> log ghi một cảnh báo. Chỉ chấp nhận được ở máy phát triển.

> **Hai endpoint `/internal/mqtt/*` không có middleware xác thực** — chính chúng là cửa xác thực,
> và người gọi là broker chứ không phải người dùng. Phải chặn ở tầng mạng: chỉ broker mới gọi tới
> được. Mở chúng ra Internet là biến chúng thành công cụ dò tài khoản.
