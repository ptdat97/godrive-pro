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
| `REDIS_URL` | *(rỗng)* | **đọc vào config nhưng chưa dùng ở đâu** |
| `NATS_URL` | *(rỗng)* | **chưa dùng** |
| `MQTT_URL` | *(rỗng)* | **chưa dùng** |
| `JWT_SECRET` | `dev-secret-doi-truoc-khi-len-production` | khoá ký HS256 |
| `ACCESS_TTL` | `24h` | hạn token |
| `DEV_AUTH` | `true` | trả mã OTP trong response **và mở `POST /v1/drivers/me/topup`** — chỉ dev |
| `SHUTDOWN_WAIT` | `15s` | thời gian tắt êm |
| `DATA_RESIDENCY` | `VN` | ghi chú tuân thủ NĐ 13/2023 |
| **`ADMIN_PHONES`** | *(rỗng)* | danh sách SĐT được vào bảng điều khiển, ngăn cách bằng dấu phẩy. **Rỗng = không ai vào được** |

### Chốt chặn ở production

`config.Load()` **từ chối khởi động** khi `APP_ENV=prod` mà:

| Điều kiện | Lỗi |
|---|---|
| `JWT_SECRET` rỗng hoặc bắt đầu bằng `dev-` | `JWT_SECRET phải được đặt ở môi trường production` |
| `DEV_AUTH` còn bật | `DEV_AUTH phải tắt ở production` |
| `DATABASE_URL` rỗng | `DATABASE_URL bắt buộc ở production` |

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
không bao giờ dùng lại `DATABASE_URL` để không có cách nào trỏ nhầm vào cơ sở dữ liệu thật:

```bash
TEST_DATABASE_URL="postgres://postgres@localhost:5432/godrive?sslmode=disable" \
  go test ./internal/app/ -race -count=1
```

Không đặt biến này thì 3 test Postgres tự `SKIP`, `make test` vẫn xanh.

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
| `redis` | `redis:7-alpine` (AOF) | 6379 | ❌ chưa |
| `nats` | `nats:2.10-alpine` (`-js`) | 4222, 8222 | ❌ chưa |
| `emqx` | `emqx/emqx:5.6` | 1883, 18083 | ❌ chưa |
| `osrm` | `osrm/osrm-backend` | 5000 | ❌ chưa (profile `routing`) |

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

**9 câu dưới đây đã được chạy thật sau mỗi lần kiểm chứng đầu-cuối và đều sạch.**
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
- [ ] Cả 9 câu SQL bất biến ở §8.7 trả về kết quả sạch trên dữ liệu staging
- [x] `go test ./... -race -count=6` xanh ở **cả** in-memory lẫn Postgres ✅
- [ ] `DEV_AUTH=false` ⇒ endpoint `POST /v1/drivers/me/topup` **không được đăng ký** (kiểm bằng curl, phải trả 404)
- [ ] Sao lưu Postgres + đã **thử khôi phục thật**, không chỉ cấu hình sao lưu

### Pháp lý (spec §10 rủi ro #2)
- [ ] Đã đăng ký phần mềm kết nối vận tải với **Sở GTVT**
- [ ] Biểu giá khớp **hồ sơ kê khai giá cước đã nộp** — không phải bảng mẫu TP.HCM trong code
- [ ] Chính sách lưu trữ theo [02 §2.7](02-mo-hinh-du-lieu.md): `trip_events` ≥ 3 năm, `ledger_entries` ≥ 10 năm
- [ ] Dữ liệu cá nhân lưu trong lãnh thổ VN (NĐ 13/2023, NĐ 53/2022)
- [ ] CCCD / GPLX đã mã hoá ở tầng ứng dụng ([T-24](07-todo.md#t-24))
- [ ] `TaxPermille` chỉ bật **sau khi kế toán thuế xác nhận**

### Vận hành
- [ ] Metric + cảnh báo đã chạy ([T-25](07-todo.md#t-25)): outbox tồn đọng · sổ cái lệch · `trips_expired` tăng vọt
- [ ] `SHUTDOWN_WAIT` ≥ 60s **hoặc** đã có job dọn chuyến kẹt `SEARCHING` (xem §8.5)
- [ ] Runbook cho: sổ cái lệch · cổng thanh toán chết · OSRM chết · Redis chết
- [ ] Load test riêng module `matching` — spec §8 nhóm C #13 gọi đây là điểm nghẽn đầu tiên
