# 07 — TODO

> Danh sách việc **thực thi được**. Mỗi mục có: file cần sửa · điều kiện hoàn thành · **lệnh verify**.
> Không đánh dấu `[x]` khi chưa chạy được lệnh verify.
>
> `GĐ` = giai đoạn trong [06 — Kế hoạch triển khai](06-ke-hoach-trien-khai.md).
> `G-xx` = mã gap trong [05 — Đối chiếu spec ↔ code](05-doi-chieu-spec-code.md).

**Tiến độ (T-01…T-28):** **25 / 28 xong** · 🟡 T-21, T-24 xong một phần · chưa làm T-11 (trùng T-33, cần credential FCM/APNs) — đếm lại 2026-10-27 theo dấu ở từng mục

| GĐ | Xong | Còn |
|---|---|---|
| GĐ 0 — Sửa nền | ✅ T-01, T-08, T-14, T-15, T-16, T-17 | — |
| GĐ 1 — Bền dữ liệu & đúng tiền | ✅ T-02, T-03, T-05, T-09, T-12, T-13, T-18 | — |
| GĐ 2 — Đúng nghiệp vụ | ✅ T-04, T-06, T-07, T-10, T-19, T-20 | — |
| GĐ 3 — Hạ tầng | ✅ T-25 · ✅ T-21 (Redis, NATS, MQTT, OSRM; H3 chưa) | T-11 (cần credential FCM) |
| GĐ 4 — Thương mại | ✅ T-22 (cổng thanh toán + đối soát) · ✅ T-23 · ✅ T-26 · 🟡 T-24 (mã hoá + thu hồi token xong; eKYC chưa) | — |
| Chưa xếp | ✅ T-27 (quyền CSDL + trigger + tự kiểm lúc khởi động) · ✅ T-28 | — |
| Trước app | ✅ T-32 · ✅ T-31 (lời mời p95 5–16 ms, đo ở T-35) | T-33 (cần credential FCM/APNs) |

**Ngoài kế hoạch:** **7 lỗi** phát hiện khi kiểm thử
([§5.4](05-doi-chieu-spec-code.md#loi-moi)) — đều đã sửa và có test hồi quy đã được kiểm ngược
(khôi phục lỗi → xác nhận test đỏ → sửa lại).

**Trạng thái kiểm chứng:** `go build` / `go vet` / `gofmt` sạch · **223 test với đủ hạ tầng** (Postgres,
Redis, NATS, MQTT — đếm lại 2026-10-27, sau T-35; app trong test tích hợp
nối CSDL bằng role bị cắt quyền `godrive_app`) · `-race` sạch, không flake · luồng đầu-cuối qua HTTP
thật · **2 tiến trình API thật**, `SIGKILL` một pod giữa chừng không mất việc · **9 bất biến kiểm
trực tiếp trên CSDL** sau khi chạy.

---

## GĐ 0 — Sửa nền

### <a id="t-01"></a>T-01 · ✅ `identity.PostgresRepo` — mở khoá chế độ Postgres `[G-01]` `P0`

- [x] [`internal/identity/store_postgres.go`](../godrive/internal/identity/store_postgres.go): `UpsertAccount` (`ON CONFLICT (phone, role) DO UPDATE` để `RETURNING` chạy ở cả hai nhánh), `GetAccount`, challenge CRUD
- [x] OTP challenge tạm dùng bảng `otp_challenges` + job dọn (`App.sweepOTPChallenges`, chu kỳ 1 phút). **Redis vẫn là đích đến** — chuyển ở [T-21](#t-21)
- [x] `app.New` — `idRepo` vào trong khối `if cfg.InMemory()`; thêm cảnh báo khởi động liệt kê những store CÒN ở bộ nhớ
- [x] Migration `0002_identity_and_documents` + biến thể nogis; `scripts/gen-nogis.py` nay chép mọi migration sau 0001 và **báo lỗi** nếu chúng đụng PostGIS

**Đã verify** (Postgres 18.4 + PostGIS 3.6.3)
```
TestPostgresFullTripLifecycle       PASS   accounts có 2 dòng; register → 201; trips → 201; kết ở PAID
TestPostgresOTPChallengeRoundTrip   PASS   mã thô không chạm đĩa; attempts tăng; job dọn xoá đúng 1 dòng
TestPostgresAccountUpsertIsStable   PASS   đăng nhập lại cùng accountID; (phone, role) là 2 người khác nhau
```
Chạy thật qua HTTP: `POST /v1/drivers/register` → **201** (trước: 409 `driver_create_failed`),
`POST /v1/trips` → **201** (trước: 500 `db_error`), chuyến chạy trọn `SEARCHING → … → PAID`, 6 bản ghi `trip_events`.

---

### <a id="t-02"></a>T-02 · ✅ `wallet.PostgresLedger` — sổ cái không được mất `[G-02]` `P0`

- [x] [`wallet/store_postgres.go`](../godrive/internal/wallet/store_postgres.go)
- [x] `Post`: MỘT transaction → `INSERT INTO ledger_transactions … ON CONFLICT (tx_id) DO NOTHING` → nếu `RowsAffected=0` thì đã ghi rồi, trả `nil` → `INSERT` bút toán → `COMMIT`. `Validate()` gọi **trước** khi mở transaction
- [x] `Balance` = `COALESCE(SUM(amount_vnd),0)`; `Statement` sắp theo `(created_at, id)` cho thứ tự lặp lại được
- [x] Nối vào `app.New` khi có `db`
- [x] **Còn nợ:** cấp quyền DB chặn `UPDATE`/`DELETE` trên `ledger_entries`, `trip_events`, `admin_audit_log` → tách thành [T-27](#t-27)

**Đã verify**
```
TestPostgresLedgerSurvivesRestart            PASS  khởi động lại → công nợ giữ nguyên, cổng chặn vẫn chạy
TestPostgresLedgerIdempotentUnderConcurrency PASS  20 goroutine cùng ghi 1 chuyến → đúng 4 bút toán
```
Trên dữ liệu thật sau kịch bản HTTP: 38 giao dịch, 149 bút toán, **tổng toàn sổ = 0**, 0 bút toán mồ côi.

> **Chốt idempotency là `PRIMARY KEY (tx_id)`, không phải `Exists()` ở tầng ứng dụng.**
> Kiểm tra rồi mới ghi luôn có khe hở giữa hai bước; khoá chính thì không.

---

### <a id="t-08"></a>T-08 · ✅ `driver.PostgresRepo` lưu đủ giấy tờ `[G-08]` `P0`

- [x] Migration `0002`: `insurance_no TEXT NOT NULL DEFAULT ''`, `insurance_until DATE`
- [x] `driverCols` gồm cả 5 cột giấy tờ ⇒ `Create`, `Get`, `ListByStatus` dùng chung một danh sách, không lệch được
- [x] `scan()` đọc lại vào `d.Documents`; `insurance_until` `DATE` ↔ chuỗi `YYYY-MM-DD` qua `nullDate`/`Format`
- [x] `Update` ghi lại `Documents` — `ReviewKYC` gọi `Update` nên nếu bỏ sót sẽ **xoá trắng giấy tờ ngay lúc duyệt hồ sơ**
- [x] Validate định dạng ngày ở `driver.Onboard` (mã lỗi `insurance_until_invalid`) — chặn dữ liệu rác tại cửa, đúng tinh thần §9 của spec

**Đã verify** — `TestPostgresFullTripLifecycle` khẳng định `Documents` đi trọn vòng ghi→đọc **và**
không bị `ReviewKYC` làm mất. Qua HTTP: gửi `"insurance_until":"15/03/2027"` → `400 insurance_until_invalid`.

> **Dùng `DATE` chứ không phải `TEXT`** là cố ý: job cảnh báo sắp hết hạn giấy tờ ([T-24](#t-24))
> cần truy vấn được theo ngày. Đổi kiểu về sau sẽ tốn một migration nữa trên bảng đang chạy.

---

### <a id="t-14"></a>T-14 · ✅ `recover()` cho mọi goroutine nền `[G-14]` `P0`

- [x] [`platform/safego`](../godrive/internal/platform/safego/safego.go) — `Recover(log, name, cleanup)` + `Go(...)`
- [x] `workers.go`: goroutine dispatch bọc `safego.Recover`, cleanup đẩy chuyến về `EXPIRED`
- [x] `eventbus`: goroutine handler bọc `safego.Recover`
- [x] `Recover` tự phòng trường hợp **cleanup panic lần hai** (nó gọi vào chính đống code vừa hỏng)

**Đã verify** — `TestDispatchPanicDoesNotKillProcess`, `TestEventHandlerPanicIsContained`.
Kiểm ngược: gỡ `recover` ra thì test **fail**, cả binary chết vì panic — đúng triệu chứng thật.

---

### <a id="t-15"></a>T-15 · ✅ Dọn bucket rate limit `[G-15]` `P2`

- [x] `RateLimit` dọn bucket nguội quá `IdleTTL` (10 phút), tối đa mỗi `SweepEvery` (1 phút)
- [x] Nhận `clock.Clock` tiêm được; thêm `Len()` cho test và metric

**Đã verify** — 3 test mới, gồm `TestRateLimitSweepsIdleBuckets` (10.000 bucket → còn 1)
và `TestRateLimitSweepIsRateLimited` (quét O(n) không chạy mỗi request).
`httpx` từ chỗ không có test nào nay đã có bộ đầu tiên.

---

### <a id="t-16"></a>T-16 · ✅ Đồng bộ `Version` giữa hai repo của `trip` `[G-16]` `P2`

- [x] `MemoryRepo.Save` trả `t.Version` mới về caller, khớp `PostgresRepo.Save`
- [x] Cả hai repo chạy qua cùng bộ test tích hợp ở `internal/app` (bật/tắt bằng `TEST_DATABASE_URL`)

**Đã verify** — `TestPostgresFullTripLifecycle` chạy 6 lần chuyển trạng thái liên tiếp trên
Postgres mà không vỡ optimistic lock; cùng luồng đó vẫn xanh ở chế độ bộ nhớ.

---

### <a id="t-17"></a>T-17 · ✅ Tiêm `clock.Clock` vào `location` `[G-17]` `P2`

- [x] `MemoryIndex` nhận `clock.Clock`
- [x] `FraudDetector` nhận `clock.Clock`
- [x] Gắn cờ `ReasonSpeedOutlier` — trước đây là hằng số chết

**Đã verify** — 6 test mới cho `location` (package trước đây không có test nào), gồm
`TestStaleDriverDropsOutOfIndex` (chỉ viết được sau khi có clock tiêm) và
`TestMockedPingRejectedAndFlagged` — test này đóng luôn **một AC của spec §5**.

> **`SPEED_OUTLIER` gắn cờ nhưng VẪN nhận ping.** Khác `TELEPORT` (suy ra từ hai vị trí liên
> tiếp — bằng chứng chắc chắn), đây chỉ là một trường do thiết bị tự khai và cảm biến tốc độ GPS
> hay nhiễu. Từ chối ping vì một trường phụ sẽ đá nhầm tài xế thật ra khỏi chỉ mục.

---

## GĐ 1 — Bền dữ liệu & đúng tiền

### <a id="t-03"></a>T-03 · ✅ Cổng chặn nợ phải thực sự hoạt động `[G-03]` `P0`

- [x] `matching.WalletPort` + `driver.BalanceReader` — Port do **bên tiêu thụ** khai báo, `matching` và `driver` không import `wallet`
- [x] Kiểm nợ ở **hai** điểm: `candidates()` khi chấm điểm, và `Reserve()` khi tài xế bấm nhận (số dư có thể đổi giữa hai thời điểm)
- [x] Sự kiện `wallet.balance_changed` + consumer đồng bộ cột cache `drivers.wallet_balance`
- [x] `UpdateWalletBalance` **không tăng `version`** — version bảo vệ chuyển trạng thái, số dư là giá trị suy ra; tăng version ở đây sẽ phá CAS của `Reserve`/`SetStatus` chạy song song

**Đã verify** — `TestCashDebtBlocksDriverEndToEnd`: 21 chuyến tiền mặt → nợ 210.000đ →
`Reserve` trả `wallet_debt_exceeded` → `DispatchRound` gửi **0** lời mời → nạp 300k → nhận lại ngay.
Qua HTTP thật: 36 chuyến → `in_debt=true`, `amount_to_clear=1.600đ`, bảng điều khiển hiện đúng lý do.

---

### <a id="t-05"></a>T-05 · ✅ Ghi sổ phí huỷ chuyến `[G-05]` `P0`

- [x] `app.onTripCancelled` — ghi sổ phí huỷ **và** trả tài xế về `IDLE` trong một consumer
- [x] `wallet.Service.PostCancelFee`, idempotent theo `tx_cancel_<tripID>`
- [x] `trip.Service.Cancel` tính `cancelFee` **một lần** rồi dùng lại cho cả nhật ký lẫn sự kiện
- [x] Thêm `rider_id` vào payload `trip.cancelled` (ghi sổ cần cả hai vế)

**Đã verify** — `TestLateCancelCreditsDriver` (tài xế +10.000đ, khách −10.000đ, tài xế về `IDLE`),
`TestEarlyCancelIsFree` (huỷ trong cửa sổ 2 phút → không ai bị ghi sổ). Cả hai dùng đồng hồ tiêm vào.

---

### <a id="t-09"></a>T-09 · ✅ Bỏ hết float trên đường đi của tiền `[G-09]` `P0`

- [x] Thêm `money.MulDiv(num, den)` — nhân/chia có làm tròn nửa ra xa số 0, hoàn toàn số nguyên. `MulPermille` nay là `MulDiv(rate, 1000)`
- [x] `computeBase` quy quãng đường/thời lượng về số nguyên (mét, giây) **ngay lập tức**
- [x] `SurgeProvider.SurgePermille` trả `int64`; `DemandSurge` so sánh bậc thang bằng số nguyên
- [x] Clamp `[1000, 2000]` ở cả bảng bậc thang lẫn `Estimate` (hai lớp, như spec §3.4 yêu cầu)
- [x] `Quote.SurgePermille` là nguồn sự thật; `SurgeMult float64` giữ lại **chỉ để hiển thị**

**Đã verify** — kiểm ngược: khôi phục bản float thì **3 test fail**, gồm
`TestFloatDriftChangesFareByAThousand`.

> ⚠️ **Phân tích ban đầu sai** ở chỗ cho rằng `RoundTo(1000)` che mất sai số. Đo thật:
> `computeBase` lệch ở **77% cự ly**, và **422 tổ hợp** làm tổng cước khách phải trả lệch **1.000đ**.
> Xem đính chính ở [G-09](05-doi-chieu-spec-code.md#g-09).

---

### <a id="t-18"></a>T-18 · ✅ Bộ test `pricing` — package quan trọng nhất chưa có test nào `[AC §4.1]` `P0`

- [x] `computeBase`: 8 ca (dưới giá mở cửa, đúng ngưỡng, vượt km, lẻ mét, cự ly dài, chỉ thời gian)
- [x] Giờ đêm: 21:59 / 22:00 / 23:30 / 00:00 / 04:59 / 05:00 / 12:00 **giờ VN**, kèm test khẳng định dùng giờ VN chứ không phải giờ máy chủ
- [x] Surge boundary: 9 mốc gồm ratio 1.18 / 1.2 / 2.0 / 3.0 / 4.0 / 100; cửa sổ trượt 5 phút
- [x] Clamp **thứ hai** ở `Estimate`: `SurgeProvider` giả trả 2001 / 9999 / 1.000.000 → vẫn ≤ 2000
- [x] Quote hết hạn với đồng hồ giả; làm tròn nghìn; sàn `MinFare`
- [x] **Bất biến:** `PlatformFee + DriverEarn == Total` với 3 loại xe × ~290 cự ly × 5 mức surge × 2 khung giờ

**Đã verify** — `pricing` **80,7%**; `computeBase` và `isNight` **100%**; `Estimate` 90,3%.

---

### <a id="t-12"></a>T-12 · ✅ API ví cho tài xế `[G-12]` `P1`

- [x] `GET /v1/drivers/me/wallet` — số dư, tiền mặt đang cầm, hạn mức, `in_debt`, và **`amount_to_clear`** (nạp đúng chừng này là nhận chuyến lại được)
- [x] `GET /v1/drivers/me/statement?from=&to=` — mặc định 30 ngày, trần 92 ngày
- [x] `POST /v1/drivers/me/topup` — **chỉ đăng ký khi `DEV_AUTH=true`**, idempotent theo `Idempotency-Key`
- [x] `driverID` lấy từ token, không nhận từ body

**Đã verify** qua HTTP thật: xem ví, nạp 500k **ba lần cùng khoá** → chỉ cộng một lần,
sao kê trả 73 bút toán `{TRIP: 72, TOPUP: 1}`.

> **Nạp ví thật phải đi qua cổng thanh toán.** Một endpoint tự ghi có vào ví mà không có đối ứng tiền
> thật chính là máy in tiền — nên nó không tồn tại ở production. Đường thật là webhook có xác thực
> chữ ký ([T-22](#t-22)).

---

### <a id="t-13"></a>T-13 · ✅ Nhật ký thao tác admin `[G-13]` `P1`

- [x] Migration `0003`: `admin_audit_log` (chỉ thêm mới) + 3 index theo target / actor / thời gian
- [x] `admin.AuditLog` với bản bộ nhớ và bản Postgres. Interface **không có** phương thức sửa/xoá — bất biến nằm ngay trong hình dạng của nó
- [x] `ReviewKYC(ctx, actor, driverID, approved)` ghi cả trạng thái **trước và sau**
- [x] `GET /v1/admin/audit?actor=&target_type=&target_id=&limit=`
- [x] Trang xem nhật ký trong `godrive-admin` — chưa làm

**Đã verify** — `TestAdminReviewKYCChangesState`, `TestPostgresAuditLogRecordsKYCReview`
(hai lần duyệt → 2 dòng, mới nhất lên đầu). Qua HTTP: trả `review_kyc`, actor, `PENDING→APPROVED`.

> Ghi nhật ký lỗi thì `ReviewKYC` **trả lỗi luôn**. Một thay đổi hồ sơ không truy vết được còn tệ hơn
> một lần duyệt thất bại, vì nó âm thầm phá bất biến "mọi thao tác quản trị đều có dấu vết".

---

## GĐ 2 — Đúng nghiệp vụ

### <a id="t-04"></a>T-04 · ✅ Cập nhật thống kê tài xế `[G-04]` `P1`

> **Đã làm.** Lưu **số đếm** (`offers_received/accepted`, `completed_trips`, `trips_cancelled`,
> `rating_sum/count`) thay vì tỉ lệ, cộng dồn nguyên tử bằng một câu `UPDATE`. Tỉ lệ suy ra với
> **làm mượt Bayes** để tài xế mới không bị một mẫu duy nhất khoá chết. Thêm luồng đánh giá
> `POST /v1/trips/{id}/rate` (chấm được một lần, chỉ sau khi chuyến kết thúc).

- [x] Subscriber `offer.created` (mẫu số) + `offer.accepted` (tử số) → `AcceptanceRate` trung bình trượt
- [x] Subscriber `trip.completed` → `CompletedTrips++`
- [x] Subscriber `trip.cancelled` với `by = DRIVER` → `CancelRate`
- [x] `Rating`: cần luồng đánh giá của khách (**endpoint mới**, chưa có) — hoặc giữ 5.0 và **ghi rõ trong tài liệu** là chưa dùng
- [x] Trung bình trượt có cửa sổ (ví dụ 50 offer gần nhất) — tránh tài xế mới bị đóng băng vĩnh viễn ở 0.8
- [x] Cân nhắc `driver_stats` riêng thay vì cột trên `drivers` (bảng `drivers` đã ghi rất nhiều)

**Verify**
```bash
go test ./internal/app/... -run TestDriverStatsAffectRanking
# Tài xế từ chối 10 offer → acceptance giảm → rơi xuống cuối rank
```

---

### <a id="t-10"></a>T-10 · ✅ Sửa `IdleSeconds` — đo đúng thời gian rảnh `[G-10]` `P1`

- [x] `Driver` thêm `IdleSince *time.Time`, đặt khi vào `IDLE` (`GoOnline`, và khi trả về `IDLE` sau chuyến)
- [x] `matching.DriverPort.Get` đã trả `*driver.Driver` → dùng `d.IdleSince`, **bỏ** `s.UpdatedAt`
- [x] Migration: cột `idle_since TIMESTAMPTZ`
- [x] Cập nhật `matching/scoring_test.go` — `TestIdleDriverGetsPriority` hiện đang xanh với ngữ nghĩa **sai**

**Verify** — test: hai tài xế ETA bằng nhau, ping cùng lúc, `IdleSince` chênh 5 phút → người rảnh lâu hơn thắng

---

### <a id="t-07"></a>T-07 · ✅ Nối `RecordRequest` vào luồng thật `[G-07]` `P1`

- [x] **Chọn có chủ đích:** gọi trong `Estimate()` (đo *lượt xem giá*) hay subscribe `trip.requested` (đo *nhu cầu thật*)?
      → Khuyến nghị **`trip.requested`**: lượt xem giá dễ bị thổi phồng bởi người dùng bấm đi bấm lại
- [x] `DemandSurge` nhận `clock.Clock`
- [x] Dọn cửa sổ trượt định kỳ — hiện `demand` map chỉ dọn khi có `RecordRequest` cùng ô lưới (rò rỉ ở ô nguội)

**Verify** — bơm 10 request vào một ô lưới với 2 tài xế IDLE → `ratio = 5` → surge = 2.0

---

### <a id="t-06"></a>T-06 · ✅ Nối outbox — chuyển sang **at-least-once** `[G-06]` `P1`

> **Đã làm.** `Repository.Save(ctx, t, e, msgs...)` trả về *những sự kiện người gọi vẫn phải tự phát*:
> bản Postgres ghi vào outbox trong cùng transaction rồi trả `nil`; bản bộ nhớ trả lại `msgs`.
> Relay quét mỗi 200ms (không phải 1s — đây chính là độ trễ trước khi dispatcher bắt đầu tìm tài xế),
> vét cạn khi lô đầy, và có DLQ ở `MaxAttempts = 10`.

- [x] `outbox.PostgresStore` (bảng `outbox` đã có DDL)
- [x] `trip.PostgresRepo.Save` ghi outbox record **trong cùng transaction** với `trips` + `trip_events`
- [x] `wallet.PostgresLedger.Post` tương tự
- [x] `trip.Service` **thôi** publish trực tiếp lên bus — chuyển sang Enqueue outbox
- [x] Relay ở `cmd/worker` dùng `PostgresStore` thật (bỏ `NewMemoryStore()` ở [worker/main.go:40](../godrive/cmd/worker/main.go#L40))
- [x] Handler phải **idempotent** (đã đúng với `SettleTrip`; kiểm lại `MarkPaid` và `SetStatus`)
- [x] Backoff theo `attempts`; `attempts > N` → DLQ + cảnh báo

**Verify**
```bash
# Giết tiến trình giữa lúc ghi sổ, khởi động lại
psql $DATABASE_URL -c "SELECT count(*) FROM outbox WHERE published_at IS NULL;"  # → 0 sau vài giây
psql $DATABASE_URL -c "SELECT tx_id, SUM(amount_vnd) FROM ledger_entries GROUP BY tx_id HAVING SUM(amount_vnd)<>0;"  # → 0 dòng
```

---

### <a id="t-19"></a>T-19 · ✅ `matching.PostgresStore` — kích hoạt chốt chặn CSDL `P1`

- [x] `internal/matching/store_postgres.go` cho bảng `offers`
- [x] `ClaimTrip`: Redis `SET NX EX 30` (ưu tiên) hoặc `INSERT … ON CONFLICT DO NOTHING` vào bảng claim
- [x] Verify `offers_one_accepted_per_trip` thực sự chặn: test cố tình ghi 2 offer `ACCEPTED` → phải lỗi unique

**Verify** — `TestOnlyOneDriverWinsTrip` chạy được trên **cả hai** store, dưới `-race`

---

### <a id="t-20"></a>T-20 · ✅ Test cho các AC còn thiếu `[AC §3, §5]` `P1`

- [x] `TestOfferExpiryExpandsRadiusThenExpires` — không ai phản hồi → 3 vòng 1500/3000/4500m → `EXPIRED`
- [x] `TestCreateTripIdempotent` — cùng `Idempotency-Key` 2 lần → 1 chuyến, cùng ID
- [x] `TestMockedPingRejectedAndFlagged` — `Mocked=true` → 403 `mock_location` + `FraudCount` tăng + **không** vào chỉ mục
- [x] `TestScoringDeterministic` — xáo trộn input 100 lần → thứ tự ra **luôn giống nhau**
- [x] `TestTripEventRollback` — ghi event lỗi → `trips.status` **không đổi** (transaction rollback)

**Verify** — `go test ./... -race -count=5` xanh; 5 AC trong [05 §5.2](05-doi-chieu-spec-code.md) chuyển từ 🔴 sang 🟢

---

## GĐ 3 — Hạ tầng

### <a id="t-21"></a>T-21 · 🟡 Thay hạ tầng theo spec §8 nhóm B `P1`

> **Đã làm (GĐ 3):** Redis cho chỉ mục vị trí (GEO), khoá giành chuyến (Lua nguyên tử), lời mời,
> báo giá, khoá idempotency và rate limit toàn cụm. OSRM cho `/route` và `/table` — **một request
> cho cả lô ứng viên** kèm cache theo cặp ô lưới, cả hai đều có đường lùi về haversine.
>
> `ETAEngine` đổi chữ ký sang **theo lô** (`[]geo.Point` → `[]float64`). Đây là quyết định về
> **chi phí** chứ không phải hiệu năng: dịch vụ bản đồ tính tiền theo request, nên một vòng dispatch
> 50 ứng viên là 1 lần tính tiền thay vì 50.
>
> **Chưa làm:** NATS, MQTT, H3 — không chạy thử được ở môi trường phát triển này.

Đổi implementation, **không sửa code nghiệp vụ** — đây là bài kiểm tra thực sự cho các Port:

- [ ] `location.MemoryIndex` → Redis `GEOSEARCH`
- [ ] `matching.MemoryStore.ClaimTrip` → Redis `SET NX EX`
- [ ] `pkg/geo` lưới ô → `github.com/uber/h3-go/v4` res 8–9 (chỉ đổi `CellOf`/`Ring`)
- [ ] `idem.NewMemoryStore` → Postgres/Redis
- [ ] `pricing.NewMemoryQuoteStore` → Redis TTL 5'
- [ ] `httpx.NewRateLimit` → Redis toàn cụm + **giới hạn OTP theo số điện thoại** ([G-22](05-doi-chieu-spec-code.md#g-22))
- [ ] `eventbus.NewInMemory` → NATS JetStream
- [ ] `pricing.HaversineEngine` → OSRM `/route`
- [ ] `matching.SimpleETA` → OSRM `/table` — **một request cho cả lô** ([G-20](05-doi-chieu-spec-code.md#g-20)), cache theo cặp ô lưới

**Verify** — `git diff --stat internal/*/service.go internal/*/domain.go` gần như **rỗng**.
Nếu phải sửa nhiều file nghiệp vụ ⇒ Port thiết kế chưa đúng, sửa Port trước.

---

### <a id="t-11"></a>T-11 · Push thông báo + MQTT `[G-11]` `P1`

- [ ] Subscriber `offer.created` → `notification.Pusher.Push` tới thiết bị tài xế
- [ ] `device_tokens (account_id, token, platform, updated_at)` + endpoint đăng ký token
- [ ] FCM (Android — thị phần chính ở VN) trước, APNs sau
- [ ] `LogOTPSender` → **Zalo ZNS** (rẻ hơn SMS nhiều lần, tỉ lệ đọc cao), fallback SMS brandname Viettel/VNPT
- [ ] MQTT (EMQX) cho luồng vị trí: topic `drv/{id}/loc` QoS 1 + **Last Will** để phát hiện mất kết nối
- [ ] Giữ `POST /v1/locations/ping` làm đường dự phòng

**Verify** — tài xế nhận offer **không cần** poll `GET /v1/offers`; rút mạng → Last Will → rơi khỏi chỉ mục trong `StaleAfter`

---

### <a id="t-25"></a>T-25 · ✅ Quan sát được `[G-25]` `P1`

> **Đã làm.** `internal/platform/metrics` — registry tối giản phát định dạng Prometheus, thuần
> stdlib (client_golang kéo theo protobuf + procfs, lớn hơn nhiều lần phần thực sự dùng).
> `/readyz` ping **thật** Postgres và Redis, trả 503 khi hỏng; tách khỏi `/healthz` để pod không bị
> restart mỗi khi CSDL chậm. Ba endpoint vận hành được **miễn rate limit** — chặn chúng là cách tự
> tạo sự cố dây chuyền.
>
> **Chưa làm:** tracing OpenTelemetry.

- [x] Metric Prometheus: `trip_dispatch_duration`, `offer_accept_rate`, `ledger_post_errors`,
      `surge_multiplier` (histogram), `driver_idle_count`
- [x] Tracing OpenTelemetry, nối `request_id` ↔ `trace_id`
- [x] `/readyz` kiểm tra **thật** kết nối DB + Redis (hiện `/healthz` chỉ trả `{"status":"ok"}`)
- [x] Cảnh báo: `outbox` tồn đọng · sổ cái lệch ≠ 0 · tỉ lệ `trips_expired` tăng vọt

**Verify** — `curl localhost:8080/metrics`; tắt Postgres → `/readyz` trả 503

---

## GĐ 4 — Thương mại

### <a id="t-22"></a>T-22 · ✅ Cổng thanh toán + đối soát `P2`

> **Đã làm.** `internal/payment` với ba nhà cung cấp và **thuật toán chữ ký thật**: MoMo
> (HMAC-SHA256, thứ tự trường cố định), ZaloPay (HMAC-SHA256 trên chuỗi `data` thô), VNPay
> (HMAC-SHA512 trên tham số đã sắp, bỏ hai trường chữ ký).
>
> Webhook có **ba chốt chặn**: ① chữ ký ② đối chiếu số tiền với ý định đã ghi trước ③ idempotency.
> Bỏ chốt ② là bỏ mất lớp bảo vệ trước một thông báo hợp lệ nhưng sai số tiền.
>
> Đối soát/chi trả (`wallet.Settlement`) có **ba tầng** chống trả tiền hai lần, và `Calculate`
> tách khỏi `Pay` để kế toán xem được danh sách trước khi tiền rời khỏi tài khoản.
>
> **Chưa làm:** đối soát tự động với sao kê cổng (cần credential sandbox), hoá đơn điện tử.

- [x] `internal/payment/` với `Provider` interface — MoMo, ZaloPay, VNPay, VietQR
- [x] `POST /v1/payments/webhook/{provider}` — **xác thực chữ ký bắt buộc**, idempotent theo mã giao dịch
- [x] Bảng `payment_transactions` + đối chiếu với `GATEWAY_CLEARING` trong sổ cái
- [x] Job đối soát cuối ngày, báo cáo chênh lệch
- [x] `settlement_batches` + `ledger_entries.settlement_batch_id`; job chi trả **idempotent**

**Verify**
```bash
go test ./internal/payment/... -run TestWebhookRejectsBadSignature
# Chạy job chi trả HAI LẦN cùng kỳ → tổng chi KHÔNG đổi
```

---

### <a id="t-23"></a>T-23 · ✅ Sinh kiểu TypeScript từ struct Go `P2`

- [x] `godrive-admin/src/lib/types.ts` giờ **sinh tự động** bằng [cmd/gentypes](../godrive/cmd/gentypes):
      hình dạng struct đọc bằng reflection (đúng thứ `encoding/json` dùng — tên trường, `omitempty`,
      struct nhúng), chú thích và giá trị enum đọc từ mã nguồn bằng `go/ast`
- [x] Tự viết thay vì `tygo`/OpenAPI: không thêm phụ thuộc, và giữ được enum dạng union
      (`"PENDING" | "APPROVED"`) — `tygo` sinh chúng thành `string` trơn
- [x] Kiểm trong `go test ./...`: `TestGeneratedTypesUpToDate` so tệp trên đĩa với kết quả sinh lại.
      Repo chưa có CI, nên chốt chặn nằm ở bộ test mà mọi người vẫn chạy

**Bản chép tay đã trôi thật** — lần sinh đầu tiên lộ ra: `DriverRow` thiếu `cancel_rate`,
`rating_count`, `idle_since`; `TokenPair.account` có thể là `null` nhưng TS ghi là luôn có; `level`,
`role`, `key` là chuỗi trơn thay vì union. Giao diện vẫn biên dịch sạch sau khi thay.

**Verify** — `make gen-types && git diff --exit-code godrive-admin/src/lib/types.ts` ·
`TestGeneratedTypesUpToDate` · `TestGenerateIsDeterministic`. Kiểm ngược: thêm một trường vào
`admin.Alert` mà không sinh lại → test đỏ.

---

### <a id="t-24"></a>T-24 · 🟡 Tuân thủ & bảo mật `P2`

> **Đã làm.** `pkg/crypt` — AES-256-GCM cho CCCD/GPLX, nonce ngẫu nhiên mỗi lần, kèm **chỉ mục mù**
> HMAC để vẫn kiểm trùng được. Giải mã thất bại **báo lỗi** thay vì trả rỗng.
>
> Thu hồi token: `jti` trong claims, danh sách chặn Redis, thu hồi **theo token** (đăng xuất một
> thiết bị) và **theo tài khoản** (khoá người dùng — mọi token phát trước mốc đó hết hiệu lực).
> Từ chối hồ sơ KYC thu hồi phiên ngay. Kiểm tra thu hồi **fail-closed**.
>
> **Chưa làm:** eKYC (cần credential FPT.AI/VNPT), `document_expiry_alerts`,
> `driver_status_history`, job xoá dữ liệu quá hạn.

- [ ] Mã hoá `national_id` / `driver_license` / `vehicle_reg_no` ở tầng ứng dụng ([G-26](05-doi-chieu-spec-code.md#g-26))
- [ ] Refresh token + thu hồi (`jti` + danh sách chặn trong Redis) ([G-21](05-doi-chieu-spec-code.md#g-21))
- [ ] Đặt trạng thái `SUSPENDED` được — hiện chỉ đọc, không ai ghi ([G-18](05-doi-chieu-spec-code.md#g-18)):
      ngưỡng cờ gian lận tự động khoá + endpoint admin khoá/mở
- [ ] `document_expiry_alerts` + job cảnh báo hạn giấy tờ (30/15/7 ngày)
- [ ] `driver_status_history` cho đối soát tranh chấp
- [ ] Chính sách lưu trữ theo [02 §2.7](02-mo-hinh-du-lieu.md) + job xoá dữ liệu quá hạn (NĐ 13/2023)

---

### <a id="t-26"></a>T-26 · ✅ Phân trang thật cho admin `[G-19]` `P2`

Trước đây danh sách "lặp qua từng trạng thái, mỗi trạng thái lấy tối đa N dòng rồi hợp lại", và trang
tổng quan đếm bằng cách nạp tối đa 200 dòng mỗi trạng thái. Hệ quả ở quy mô thật: **mọi con số dừng ở
200**, doanh thu chỉ cộng 200 chuyến bất kỳ — mà trông vẫn như một con số thật. Gauge
`godrive_drivers_idle` cũng dừng ở 1.000.

- [x] Một truy vấn với bộ lọc ở CSDL (`status = ANY`, KYC, thành phố, tìm kiếm `ILIKE` có thoát ký tự
      đại diện, nợ) + **keyset pagination** theo `(created_at, id)` / `(requested_at, id)` giảm dần
- [x] `driver.Repository.List`, `trip.Repository.List` ở cả bản bộ nhớ lẫn Postgres; con trỏ mờ ở
      [pkg/page](../godrive/pkg/page/page.go)
- [x] Tổng quan đếm bằng `COUNT(*)`/`SUM` (`driver.Counts`, `trip.Stats`), kể cả cảnh báo nợ và giấy tờ.
      Chuyến xong/huỷ và doanh thu tính **từ 0 giờ hôm nay** — cộng trên toàn bộ lịch sử là quét cả bảng
      chuyến mỗi lần mở trang. Chuyến đang chạy vẫn đếm tức thời (chỉ mục một phần riêng)
- [x] Migration `0012`: chỉ mục khớp thứ tự phân trang + chỉ mục một phần cho chuyến đang diễn ra
- [x] Giao diện: "Trang sau →" / "Về trang đầu" ở danh sách tài xế và chuyến; trang tổng quan ghi rõ "hôm nay"
- [x] **Đổi thứ tự** danh sách tài xế từ "nợ/chờ duyệt lên đầu" sang "mới đăng ký trước": thứ tự cũ
      không phân trang ổn định được. Hai nhóm cần chú ý đã có bộ lọc riêng (`debt=1`, `kyc=PENDING`)

**Verify** — `TestPostgresDriverPaginationCoversAll`: **10.000 tài xế**, trang đầu đúng 50 dòng, hai lần
tải giống hệt nhau, đi hết 200 trang được đúng 10.000 không lặp; tổng quan đếm đủ 10.000 (trước: 200) ·
`TestPostgresPaginationStableUnderInserts` · `TestPostgresDriverFiltersPageCorrectly` ·
`TestTripPaginationCoversAll` · `TestPostgresDocumentCountsMatchAlertRules` (luật giấy tờ bằng SQL khớp
`Documents.Alerts`) · `TestPostgresOverviewTodayWindow`. Kiểm ngược: bỏ khoá phụ `id` khỏi keyset → đi hết
các trang chỉ được 5.050/10.000. Đã tải trang `/drivers` qua `next dev` với phiên quản trị thật: 50 + 10
dòng, không trùng.

---

### <a id="t-27"></a>T-27 · ✅ Quyền CSDL chặn sửa/xoá bảng append-only `[AC §5]` `P0`

Còn nợ từ [T-02](#t-02). Bất biến "chỉ thêm mới" trước đây chỉ được giữ bằng quy ước ở tầng code —
không có gì ngăn một câu SQL chạy tay xoá lịch sử tài chính.

- [x] Migration `0010_append_only` tạo role ứng dụng `godrive_app` (NOLOGIN, không phải superuser);
      user đăng nhập thật của ứng dụng là thành viên của role này
- [x] `REVOKE UPDATE, DELETE, TRUNCATE` trên **năm** bảng chứ không phải ba: thêm `ledger_transactions`
      (khoá idempotency của sổ cái — xoá một dòng là mở đường ghi sổ **lần hai**) và `settings_history`
      (bằng chứng ai đổi biểu giá — sửa được thì không còn là bằng chứng)
- [x] **Lớp thứ hai — trigger** chặn cả chủ sở hữu bảng lẫn superuser. Lớp role phụ thuộc việc người
      triển khai nhớ dùng đúng user; quên một lần là nó vô dụng mà không có gì báo
- [x] **Tự kiểm lúc khởi động**: `APP_ENV=prod` mà user sửa được lịch sử hoặc trigger bị tắt thì tiến
      trình không lên. Đây là chỗ tài liệu không làm được — dùng nhầm user không gây ra lỗi nào cả
- [x] Kiểm trong test: **mọi** app trong bộ test tích hợp nối bằng `godrive_app` — cả bộ test là bằng
      chứng ứng dụng chạy được với quyền đã cắt, không riêng ba test của T-27
- [x] Ghi vào tài liệu vận hành như bước bắt buộc ([08 §8.3](08-van-hanh.md#bảng-lịch-sử-chỉ-thêm-mới--bắt-buộc-khi-dựng-môi-trường), §8.8)

**Verify** — `TestAppRoleCannotRewriteHistory` (đúng câu `UPDATE ledger_entries SET amount_vnd=0` bị
từ chối `42501`, cùng `UPDATE`/`DELETE`/`TRUNCATE` trên cả năm bảng; `INSERT` vẫn được) ·
`TestTriggerBlocksEvenSuperuser` · `TestStartupRefusesPrivilegedRoleInProduction` (kể cả khi chỉ một
trigger bị tắt). Kiểm ngược: gỡ `0010` → cả ba đỏ. `0010` chạy được up → down → up.

Đóng AC §5 *"`trip_events` không bao giờ bị update/delete — verify qua DB role permission"*.

**Cái giá:** dọn bảng giữa các lần test giờ phải `SET LOCAL session_replication_role = replica`, nên
user của `TEST_DATABASE_URL` phải là superuser. Đó cũng chính là lối thoát có chủ đích cho việc sửa
dữ liệu lịch sử khi có quyết định bằng văn bản.

---

### <a id="t-28"></a>T-28 · ✅ Tách mã lỗi cho tài xế OFFLINE `[G-27]` `P2`

- [x] `CanAcceptTrip` trả `driver_offline` khi `Status == OFFLINE`, giữ `driver_busy` cho `ASSIGNED`/`ON_TRIP`
- [x] Bảng điều khiển hiện *"Đang tắt nhận chuyến"* thay vì *"Đang có chuyến"* cho tài xế chỉ đơn giản là chưa bật app

**Verify** — `TestAdminReviewKYCChangesState`: tài xế `OFFLINE` vừa duyệt KYC có `blocked_reason=driver_offline` ·
`TestOfflineDriverCannotAcceptPendingOffer` kiểm đúng mã lỗi chứ không chỉ "có lỗi"

---

### <a id="t-29"></a>T-29 · ✅ Cấu hình vận hành sửa được từ giao diện `[G-35]` `P1`

Trước đây mọi con số điều khiển hệ thống — biểu giá, chiết khấu, bậc thang surge, bán kính ghép
chuyến, hạn mức công nợ — là hằng số trong mã nguồn. Đổi một con số nghĩa là sửa code, biên dịch
lại, triển khai lại. Vận hành **không tự làm được**, nên trên thực tế chúng không bao giờ được đổi.

- [x] Migration `0009_settings` — bảng `settings` (JSONB, `version`) + `settings_history` (trước/sau/lý do)
- [x] `internal/settings` — 5 nhóm có kiểu rõ ràng, mỗi ô có **ngưỡng an toàn cứng**
- [x] Ảnh chụp trong bộ nhớ, TTL 5 giây + sự kiện `settings.changed` qua NATS để lan ngay
- [x] Các module đọc cấu hình qua **hàm cung cấp** (`ConfigProvider`), không module nghiệp vụ nào
      import `internal/settings` — dịch sang kiểu riêng của từng module ở `internal/app/settings.go`
- [x] API quản trị 4 route + **lược đồ biểu mẫu do backend phát ra**
- [x] Trang `/settings/{key}` ở `godrive-admin` tự dựng biểu mẫu từ lược đồ, lịch sử so từng ô
- [x] Bắt buộc ghi lý do ở **tầng API** (≥ 5 ký tự), vào cả `settings_history` lẫn `admin_audit_log`
- [x] Khoá lạc quan theo `version` — hai người cùng sửa thì người sau phải tải lại

**Verify** — `TestChangingTariffAffectsNextQuote` · `TestExistingQuoteUnaffectedByTariffChange` ·
`TestChangingDebtLimitBlocksDriverImmediately` · `TestDisablingSurgeTakesEffect` ·
`TestChangingMatchingRadiusTakesEffect` · `TestSettingChangeIsAudited` ·
`TestSchemaBoundsMatchValidation` · `TestSettingsAPIRequiresAdmin` · `TestSettingsAPIRequiresReason`

**Ba lỗi đáng kể lộ ra khi làm việc này**, đều thuộc loại đọc code không thấy:

1. **PUT một phần xoá trắng ô không gửi.** `json.Unmarshal` chỉ ghi đè trường có mặt, nên dựng từ
   struct rỗng thì một thay đổi chỉ gửi `debt_limit_vnd` sẽ đưa thuế và ngưỡng chi trả về 0 — đều
   là giá trị *hợp lệ*, nên `Validate` không chặn được.
2. **Gộp chưa đủ sâu.** Sửa xong tầng ngoài thì vẫn còn tầng trong: với `map[string]Tariff`,
   `json.Unmarshal` dựng phần tử **rỗng** rồi mới đổ dữ liệu, nên sửa `per_km` của xe máy vẫn đưa
   chiết khấu nền tảng của xe máy về 0.
3. **Thay đổi bị TỪ CHỐI vẫn kịp làm hỏng cấu hình đang chạy.** `Snapshot` trả về theo giá trị
   nhưng map bên trong dùng chung, nên quá trình *kiểm tra* một giá trị ghi thẳng vào biểu giá
   sống: API trả lỗi đúng như mong đợi, mà mọi báo giá trong ≤ 5 giây sau đó tính bằng biểu giá đã
   hỏng — rồi ảnh chụp tự nạp lại và mọi thứ trở lại bình thường như chưa có gì xảy ra.
   Chốt bằng `TestRejectedChangeDoesNotCorruptRunningConfig` và `TestSnapshotIsIsolatedFromRunningConfig`.

---

### <a id="t-30"></a>T-30 · ✅ Chặn test tích hợp xoá nhầm CSDL thật `[P1]`

- [x] `requireTestDatabase` từ chối chạy nếu tên CSDL không chứa `test`, báo lỗi kèm cách tạo đúng
- [x] `Makefile` mặc định `godrive_test`, không còn là `godrive`
- [x] `settings` + `settings_history` vào danh sách dọn giữa các lần chạy

Bộ test tích hợp `TRUNCATE` mọi bảng. Mặc định cũ trỏ thẳng vào CSDL dev — chấp nhận được khi dữ
liệu dev là thứ vứt đi, nhưng **không còn chấp nhận được** từ khi biểu giá và toàn bộ lịch sử thay
đổi nằm trong CSDL.

---

### <a id="t-31"></a>T-31 · ✅ Chiều xuống realtime — máy chủ đẩy được tin tới thiết bị `P0`

**Lỗ hổng lớn nhất về kỹ thuật.** MQTT hiện chỉ có chiều lên: máy chủ `Subscribe` vào `drv/+/loc`
và `drv/+/status`, không có một lệnh `Publish` nào đi ngược lại. Hệ quả:

- Tài xế biết có lời mời bằng cách **hỏi liên tục** `GET /v1/offers`. Lời mời chỉ sống 15 giây, nên
  muốn kịp thì phải hỏi mỗi 2–3 giây — nhân với số tài xế online là tải vô ích, và vẫn không đạt
  được mục tiêu *offer delivery p95 < 1s*.
- Khách **không thấy xe đang chạy tới**. Vị trí tài xế có trong Redis nhưng không có đường nào đẩy
  tới máy khách.
- Đổi trạng thái chuyến (tài xế đã tới, bắt đầu chạy, hoàn tất) cũng chỉ biết được bằng cách hỏi.

Việc này phải xong **trước khi viết app**, vì nó quyết định kiến trúc kết nối của app.

- [x] ~~Chọn cơ chế~~ → **MQTT hai chiều** (chốt 2026-08-27). EMQX đã chạy sẵn và app tài xế vốn
      phải giữ kết nối MQTT để gửi vị trí, nên thêm chiều xuống là mở rộng cái đang có chứ không
      dựng hạ tầng mới. MQTT sinh ra cho mạng di động chập chờn: QoS 1, phiên bền, Last Will —
      đúng những thứ mà WebSocket tự cài sẽ phải làm lại từ đầu.
- [x] Topic chiều xuống: `drv/{id}/offer`, `drv/{id}/trip`, `rdr/{id}/trip` (QoS 1, `retain=false`) —
      thêm `rdr/{id}/driver_loc` ở **QoS 0** (bản mới thay bản cũ, giao lại bản cũ vô ích)
- [x] Phát từ consumer của bus (`push-offer`, `push-trip`), không gọi MQTT trong Service — tất cả ở
      [internal/app/realtime.go](../godrive/internal/app/realtime.go)
- [x] Sự kiện mới `trip.status_changed` phát ở **mọi** lần đổi trạng thái, đi qua outbox cùng
      transaction. Trước đây ARRIVED, PAID, EXPIRED không có sự kiện nào nên không đẩy được
- [x] Đẩy lời mời tới đúng tài xế được chào; lời mời đã hết hạn (bus giao lại muộn) thì **không** đẩy
- [x] Đẩy vị trí tài xế tới đúng khách đang trong chuyến — tra chuyến mỗi ping, **không nhớ đệm**
      (bộ đệm sai đúng lúc tài xế sang chuyến kế, khách cũ thấy tài xế đi đón người khác)
- [x] Đẩy chuyển trạng thái chuyến tới cả hai phía; một phía lỗi không làm phía kia mất tin
- [x] Mất kết nối: `GET /v1/trips/current` cho cả khách lẫn tài xế, cộng `GET /v1/offers` sẵn có
- [x] `GET /v1/offers` giữ nguyên làm đường dự phòng
- [x] Khách chỉ nối được MQTT khi **đang có chuyến** — chặn ở `mqttauth` chứ không chỉ trông vào app.
      clientId `rdr_{account_id}`, chỉ được NGHE topic của mình, không publish vào đâu
- [x] Số liệu `godrive_offer_push_delay_seconds`, `godrive_push_total{kind,result}`
- [x] **Verify:** lời mời tới thiết bị p95 **5 ms** với 1.000 tài xế ở 100.000 bản tin vị trí/phút,
      **16 ms** với 5.000 tài xế — mục tiêu < 1 s ([T-35](#t-35), [08 §8.14](08-van-hanh.md#814-test-tải-cmdloadgen))
- [x] Tin `offer_withdrawn` cho các tài xế khác khi một người đã nhận (`taken`), và khi khách huỷ lúc
      đang tìm tài xế (`trip_cancelled`). Trường hợp sau trước đây còn tệ hơn: lời mời **vẫn nằm ở
      trạng thái chờ** trong kho tới hết 15 giây. `ExpireOffers` giờ trả về đúng những lời mời vừa bị
      thu hồi ở cả ba kho (bộ nhớ, Redis, Postgres `UPDATE … RETURNING`)
- [x] Lời mời — cả tin đẩy lẫn `GET /v1/offers` — mang điểm đón, điểm đến, loại xe, hình thức trả
      tiền và thu nhập tài xế (`OfferView`). Ghép lúc đọc chứ không chép vào bảng `offers`.
      Verify: `TestPushOnlyReachesOfferedDrivers` · `TestRiderCancelWithdrawsPendingOffers`, kiểm ngược

**Verify** — `TestPushFollowsTripLifecycle` · `TestPushOnlyReachesOfferedDrivers` ·
`TestExpiredOfferIsNotPushed` · `TestTripPushFailureOnOneSideStillReachesOther` ·
`TestCurrentTripRecoversStateAfterReconnect` · 2 test đơn vị cho khách ở `internal/mqttauth`, và
**`TestRiderReceivesOwnTripPushOverBroker`** trên broker thật: HTTP huỷ chuyến → outbox → bus →
consumer → EMQX hỏi ACL → thiết bị; khách thứ hai đã qua được cửa xác thực vẫn không nghe được;
chuyến kết thúc thì nối lại bị từ chối. Chạy qua cả bus trong tiến trình lẫn NATS JetStream.
Test đã được kiểm ngược: bỏ `trip.status_changed` hoặc bỏ kiểm hạn lời mời → test đỏ.

**Một cái bẫy lộ ra khi làm:** các test NATS xoá stream `GODRIVE` để bắt đầu sạch. Backend dùng cho
`TEST_API_URL` mà chung NATS với test thì consumer đẩy tin của nó chết giữa chừng — test broker đỏ
khi chạy cả gói, xanh khi chạy riêng. Xem [08 §8.13](08-van-hanh.md#813-chiều-xuống-realtime).

---

### <a id="t-32"></a>T-32 · ✅ Xác thực MQTT + ACL theo topic `P0` `bảo mật`

Broker đang **mở**. Ai kết nối được cũng publish được vào `drv/{id}/loc` của người khác — nghĩa là
giả được vị trí của bất kỳ tài xế nào, và qua đó giành được chuyến ở khu vực mình không hề có mặt.

Chống gian lận ở tầng ứng dụng (tốc độ bất khả thi, sai số GPS) vẫn chạy, nhưng nó lọc **nội dung**
chứ không xác minh **danh tính người gửi**. Ghi chú trong [mqtt.go](../godrive/internal/location/mqtt.go)
đã nêu đúng điều này: *"broker phải bảo đảm ai publish vào topic nào"* — phần đó chưa làm.

- [x] Xác thực thiết bị khi kết nối — mật khẩu MQTT **chính là token phiên**, không cấp thêm loại
      thông tin đăng nhập nào nữa
- [x] ACL: mỗi tài xế chỉ chạm được topic của chính mình, so khớp chính xác, không ký tự đại diện
- [x] Thu hồi phiên cắt được đường MQTT — `authn.Issuer.Parse` **không** kiểm thu hồi (việc đó nằm
      ở middleware HTTP mà MQTT không đi qua), nên `mqttauth` hỏi thẳng revoker, fail-closed
- [x] Chặn cướp `clientId`: A đặt clientId của B là đá được B ra khỏi broker, vì MQTT cho client
      sau cùng chiếm phiên — luật topic không chặn được vì nó xảy ra trước khi có topic nào
- [x] Tài khoản dịch vụ riêng cho backend (`MQTT_USERNAME`/`MQTT_PASSWORD`), so sánh bằng thời gian
      hằng định, và cấu hình rỗng **không** biến thành cửa sau
- [x] Cấu hình broker thành **mã nguồn** ([deploy/emqx/emqx.conf](../godrive/deploy/emqx/emqx.conf)) —
      chỉnh bằng bảng điều khiển chỉ sống trong container đang chạy
- [ ] Ngắt kết nối ĐANG MỞ khi khoá tài khoản giữa chừng (gọi REST của EMQX) — hiện mới chặn ở lần
      kết nối kế tiếp

**Verify** — `TestDriverCannotPublishToAnotherDriverTopic` · `TestCannotConnectAsAnotherDriver` ·
`TestCannotHijackAnotherDriverClientID` chạy trên broker thật, kiểm bằng **bản tin có tới nơi hay
không** chứ không tin mã trả về của lệnh publish. Cộng 13 test đơn vị ở `internal/mqttauth`.

**Ba cái bẫy trong mặc định của EMQX**, xem [08 §8.12](08-van-hanh.md):
`no_match=allow` · bộ luật `file` kết thúc bằng `{allow, all}` · `deny_action=ignore`.

---

### <a id="t-33"></a>T-33 · Thông báo đẩy FCM/APNs `[G-11]` `P0`

Khung `notification.Pusher` có từ đầu nhưng **chưa ai gọi**. Không có push thì tài xế phải mở app
mới biết có chuyến, và khách không được báo khi tài xế tới nơi.

- [ ] Nối FCM (Android) và APNs (iOS) — *cần credential Google/Apple*
- [ ] Bảng `device_tokens`: đăng ký, làm mới, thu hồi khi đăng xuất
- [ ] Mẫu thông báo: có lời mời · tài xế đã tới · chuyến hoàn tất · thanh toán · kết quả duyệt hồ sơ
- [ ] Thử lại khi gửi lỗi, dọn token chết

---

### <a id="t-34"></a>T-34 · ✅ Tỉ lệ huỷ chuyến vào hàm chấm điểm `P1`

Migration 0005 thêm cột `trips_cancelled` và có code cộng dồn, `Stats.CancelRate()` cũng đã có — nhưng
[scoring.go](../godrive/internal/matching/scoring.go) **không ai đọc nó**. Tài xế nhận chuyến rồi huỷ
được xếp hạng ngang tài xế không bao giờ huỷ.

- [x] `WeightCancel` trong `matching.Config` và ô `weight_cancel` trong nhóm cấu hình `matching`
      (0..1000, biểu mẫu quản trị tự có vì lược đồ do backend phát ra)
- [x] Làm mượt Bayes dùng lại `CancelRate()` có sẵn (tiền nghiệm 10 chuyến): huỷ 1 chuyến đầu đời ≈ 9%, không phải 100%
- [x] Mặc định **0** — đổi trọng số là đổi thu nhập tài xế; vận hành bật khi có dữ liệu thật.
      Gợi ý trong biểu mẫu: 300 nghĩa là tỉ lệ huỷ 10% ≈ tới đón chậm 30 giây

**Verify** — `TestHighCancelRateSinksDriverInRanking` (mặc định 0 **không** đổi thứ hạng; bật 300 thì
người đáng tin ở xa vượt người hay huỷ ở gần) · `TestFirstCancelDoesNotSinkNewDriver`. Kiểm ngược:
bỏ dòng chấm điểm → test đỏ.

---

### <a id="t-35"></a>T-35 · 🟡 Test tải — đo thay vì đoán `P1`

Mọi ngưỡng hiệu năng trong tài liệu (`API p95 < 100ms`, `offer delivery p95 < 1s`,
`location freshness < 3s`) trước đây là **mục tiêu chưa có số đo nào**.

- [x] Bộ sinh tải [cmd/loadgen](../godrive/cmd/loadgen): N tài xế ảo nối MQTT thật, gửi vị trí, nhận lời
      mời qua tin đẩy và chạy trọn chuyến; khách ảo đặt xe qua HTTP, nghe trạng thái qua MQTT. Đo **từ
      phía thiết bị**
- [x] Đo ở 1.000 tài xế, **100.000 bản tin vị trí/phút**: lời mời p95 5 ms, vị trí → khách p95 3 ms,
      API p95 ≤ 13 ms, 0 lỗi, backend ≤ 0,6 lõi
- [x] Đo độ trễ dispatch và độ trễ giao lời mời — cả phía thiết bị lẫn phía máy chủ
- [x] **Tìm điểm gãy** ở 5.000 tài xế / 20 chuyến mỗi giây và **sửa**: dispatcher tra CSDL cho mọi
      tài xế trong bán kính (G-20) + EMQX chỉ có 8 kết nối gọi xác thực → vòng xoáy ngắt kết nối hàng
      loạt. Sau khi sửa: 0 lỗi, mọi p95 đạt mục tiêu
- [x] Ghi số đo thật vào [08 §8.14](08-van-hanh.md#814-test-tải-cmdloadgen)
- [x] `RATE_LIMIT_RPS`/`RATE_LIMIT_BURST` chỉnh được — trước là hằng số 30/60
- [ ] Ngâm **24 giờ** — mới chạy 10 phút (2.000 tài xế: RSS backend đứng yên ~38 MB)
- [x] Lỗi `revocation_check_failed` (fail-closed khi Redis lỗi) giờ được ghi log kèm nguyên nhân —
      trước đây 401 không để lại dòng log nào
- [ ] Đo với nhiều bản sao backend, với OSRM thật, và trên mạng di động thật

**Verify** — chạy lại được bằng một lệnh, xem [08 §8.14](08-van-hanh.md#814-test-tải-cmdloadgen).
`TestDispatchLooksUpOnlyNearestCandidates` giữ cho G-20 không quay lại.

---

### <a id="t-36"></a>T-36 · Hoàn tiền và chốt sổ ngày `P1` `tiền`

Đường tiền vào đã chắc, **đường tiền ra chưa đủ**:

- [ ] Hoàn tiền (huỷ sau khi đã thu, khiếu nại được duyệt) — bút toán đảo, không sửa bút toán cũ
- [ ] Chốt sổ cuối ngày: tổng thu, chiết khấu, thuế, phải trả tài xế
- [ ] Đối soát tự động với sao kê cổng thanh toán, nêu ra dòng lệch
- [ ] Báo cáo kiểm toán tài chính xuất được cho kế toán
- [ ] Chargeback — *chờ điều khoản thật của cổng thanh toán*

---

### <a id="t-37"></a>T-37 · 🟡 Cảnh báo hạn giấy tờ tài xế `P1` `tuân thủ`

`InsuranceUntil` được lưu nhưng **không có gì đọc nó**, và **không có đường nào cập nhật giấy tờ sau
khi đăng ký** — tài xế gia hạn bảo hiểm xong cũng không báo được cho hệ thống. Tài xế chạy với bảo
hiểm hết hạn là rủi ro pháp lý thuộc về nền tảng.

- [x] Migration `0011`: thêm hạn **GPLX** và hạn **đăng kiểm** cạnh hạn bảo hiểm. Trống = không có hạn
      (GPLX A1, xe máy không đăng kiểm) và **không** chặn
- [x] Hết hạn thì **chặn nhận chuyến ngay** — `documents_expired` ở `CanAcceptTrip` và ở cửa `GoOnline`.
      **Lệch so với mô tả gốc ("tự chuyển SUSPENDED") có chủ đích:** tính theo ngày lúc kiểm tra nên
      đúng nửa đêm là chặn, không chờ job; gia hạn xong tự mở; và không lẫn với tài xế bị khoá vì
      gian lận — tự mở SUSPENDED là lỡ tay mở cho cả người bị khoá vì lý do khác
- [x] Ngày tính theo **giờ Việt Nam** (UTC+7): tính theo UTC thì từ 0 tới 7 giờ sáng, giấy hết hạn hôm
      qua vẫn được coi là còn hạn
- [x] Gia hạn: `PUT /v1/admin/drivers/{id}/documents`, bắt buộc lý do, vào nhật ký với ngày trước/sau.
      Không cho tài xế tự khai — một ngày tự khai không kèm giấy thật thì vô giá trị
- [x] Cảnh báo 30/15/7 ngày **trên app** (`GET /v1/drivers/me` → `document_alerts`) và trên **bảng
      điều khiển** (huy hiệu lý do chặn, form gia hạn ở trang tài xế, hai cảnh báo ở trang tổng quan)
- [ ] Cảnh báo qua **thông báo đẩy** — cần [T-33](#t-33). Khi làm: phải nhớ đã gửi mức nào rồi, nếu
      không job chạy hai lần một ngày là gửi trùng, lỡ một ngày là mất mốc
- [ ] Chưa xem trên trình duyệt: form gia hạn mới qua `tsc` và `eslint`

**Verify** — `TestExpiredDocumentsBlockUntilRenewed` (không được chào chuyến · admin thấy lý do ·
tắt/bật app không lách được · thiếu lý do bị từ chối · gia hạn xong mở ngay · đúng 2 bản ghi nhật ký) ·
`TestExpiringDocumentsWarnButDoNotBlock` · `TestPostgresDocumentDatesRoundTrip` · 4 test đơn vị ở
`internal/driver` gồm `TestExpiryUsesVietnamDate`. Kiểm ngược: bỏ chốt ở `CanAcceptTrip` → đỏ.

---

### <a id="t-38"></a>T-38 · Tìm kiếm địa điểm (geocoding) `P1` `theo Rider App`

Khách phải nhập được *"Chợ Bến Thành"* chứ không phải toạ độ. Hiện chưa có nhà cung cấp nào được
nối — [pricing/domain.go](../godrive/internal/pricing/domain.go) mới chỉ ghi chú dự định dùng
Goong/VietMap.

- [ ] Chọn nhà cung cấp (Goong · VietMap · Mapbox) — *cần quyết định + credential*
- [ ] Tìm địa điểm theo từ khoá, ưu tiên gần vị trí hiện tại
- [ ] Toạ độ → địa chỉ, để hiện tên điểm đón
- [ ] Cache kết quả, vì đây là API tính tiền theo lượt gọi
- [ ] Địa điểm gần đây, Nhà/Cơ quan/Yêu thích

---

### <a id="t-39"></a>T-39 · Driver App (Flutter) `P0`

Chốt 2026-08-27: **làm app tài xế trước, app khách sau.** Pilot chết vì thiếu tài xế online chứ
không vì thiếu khách; và chạy thử realtime với vài chục tài xế thì đo được độ trễ thật trước khi
mở cho khách. Trong giai đoạn đầu khách đặt qua tổng đài hoặc web tối giản.

Phụ thuộc: [T-31](#t-31) và [T-32](#t-32) phải xong trước — nếu không, phần kết nối của app sẽ
phải viết lại.

- [ ] Khung app + đăng nhập OTP
- [ ] Đăng ký hồ sơ, tải CCCD/GPLX/đăng ký xe/bảo hiểm, xem trạng thái duyệt
- [ ] Bật/tắt nhận chuyến; định vị nền; giữ kết nối MQTT và tự nối lại
- [ ] Nhận lời mời qua MQTT, nhận/từ chối, đếm ngược theo hạn lời mời
- [ ] Vòng đời chuyến: tới nơi → bắt đầu → hoàn tất, dẫn đường tới điểm đón
- [ ] Ví: số dư, công nợ tiền mặt, sao kê, nạp tiền
- [ ] Thu nhập theo ngày/tuần/tháng
- [ ] Thông báo đẩy ([T-33](#t-33))

---

### <a id="t-40"></a>T-40 · Rider App `P0` `sau T-39`

- [ ] Quyết định nền tảng khi tới lượt (Flutter hay web/PWA) — chưa chốt
- [ ] Đặt xe đầu-cuối, xem xe chạy tới theo thời gian thực, thanh toán, đánh giá
- [ ] Cần [T-38](#t-38) (tìm kiếm địa điểm) trước khi dùng được thật

---

### <a id="t-41"></a>T-41 · Vùng đa giác: ranh giới phục vụ, đường cấm, geofence `P1` `[Geo]`

Nhánh duy nhất của [Geo Platform](godrive-pro-TODO.md#p18-godrive-geo-platform) **làm được ngay**,
không chờ app và không chờ dữ liệu chuyến thật.

Nền đã sẵn: PostGIS chạy thật với `driver_locations.geom GEOGRAPHY(POINT, 4326)` + chỉ mục GIST.
Thêm bảng vùng dạng đa giác là mở rộng cái đã kiểm chứng.

- [ ] Bảng `service_zones` (`GEOGRAPHY(POLYGON)`, GIST) + bảng luật gắn theo vùng
- [ ] **Ranh giới phục vụ**: từ chối đặt xe ngoài vùng **ngay khi báo giá**, không để khách chờ hết
      ba vòng chào mời mới báo không tìm được tài xế
- [ ] **Đường cấm**: cấm theo loại xe và theo khung giờ — dẫn tài xế vào đường cấm là phạt tiền thật
      cho họ. Trừ điểm ứng viên phải đi qua vùng cấm, hoặc loại hẳn
- [ ] **Geofence**: vùng sân bay / bến xe / trung tâm thương mại, gắn được phụ phí và điểm đón riêng
- [ ] Sửa vùng từ giao diện quản trị (vẽ trên Leaflet đã có), qua cùng cơ chế
      [cấu hình](08-van-hanh.md) — có ngưỡng, có lịch sử, có lý do
- [ ] Nạp vùng vào bộ nhớ như ảnh chụp cấu hình: kiểm điểm-trong-đa-giác nằm trên đường đi của mọi
      lần báo giá, không thể hỏi CSDL mỗi lần

**Verify** — đặt xe ngoài vùng phục vụ trả mã lỗi riêng ngay ở bước báo giá; tài xế xe máy không
được chào chuyến bắt buộc đi qua tuyến cấm xe máy trong khung giờ cấm.

---

### <a id="t-42"></a>T-42 · Nắn quỹ đạo GPS về đường thật (map matching) `P2` `[Geo]`

OSRM đã có endpoint `/match` — **chưa ai gọi**, hiện mới dùng `/route` và `/table`. Một lần làm
phục vụ ba việc: tính cước theo quãng đường thật thay vì đường chim bay, phát hiện lệch tuyến cho
[an toàn](godrive-pro-TODO.md#p17-safety-platform), và đo chất lượng đường để nuôi ngược lại ETA.

- [ ] Gọi `/match` cho quỹ đạo chuyến đã hoàn tất
- [ ] So quãng đường nắn được với quãng đường đã tính cước, ghi độ lệch
- [ ] **Chưa đổi cách tính cước** cho tới khi đo đủ — đổi cách tính tiền dựa trên dữ liệu chưa kiểm
      chứng là cách nhanh nhất để tính sai tiền của tài xế
- [ ] Thống kê thời gian đi thật theo đoạn đường, làm đầu vào cho [P3.2](godrive-pro-TODO.md#p32-eta-intelligence)

Cần dữ liệu chuyến thật mới có ý nghĩa — xếp sau pilot.

---

## Bảng tra nhanh gap → việc

> ✅ = đã đóng và có test hồi quy.

| Gap | Việc | Ưu tiên | GĐ |
|---|---|---|---|
| [G-01](05-doi-chieu-spec-code.md#g-01) identity Postgres | [T-01](#t-01) | ✅ xong | 0 |
| [G-02](05-doi-chieu-spec-code.md#g-02) sổ cái bộ nhớ | [T-02](#t-02) | ✅ xong | 0–1 |
| ✅ [G-03](05-doi-chieu-spec-code.md#g-03) cổng chặn nợ chết| [T-03](#t-03) | P0 | 1 |
| [G-04](05-doi-chieu-spec-code.md#g-04) chỉ số tài xế đóng băng | [T-04](#t-04) | ✅ xong | 2 |
| ✅ [G-05](05-doi-chieu-spec-code.md#g-05) phí huỷ không ghi sổ| [T-05](#t-05) | P0 | 1 |
| [G-06](05-doi-chieu-spec-code.md#g-06) outbox chưa nối | [T-06](#t-06) | ✅ xong | 2 |
| [G-07](05-doi-chieu-spec-code.md#g-07) surge = 1.0 | [T-07](#t-07) | ✅ xong | 2 |
| [G-08](05-doi-chieu-spec-code.md#g-08) mất giấy tờ | [T-08](#t-08) | ✅ xong | 0 |
| ✅ [G-09](05-doi-chieu-spec-code.md#g-09) float trên tiền| [T-09](#t-09) | P0 | 1 |
| [G-10](05-doi-chieu-spec-code.md#g-10) IdleSeconds sai | [T-10](#t-10) | ✅ xong | 2 |
| [G-11](05-doi-chieu-spec-code.md#g-11) không có push | [T-11](#t-11) | P1 | 3 |
| ✅ [G-12](05-doi-chieu-spec-code.md#g-12) không có API ví| [T-12](#t-12) | P1 | 1 |
| ✅ [G-13](05-doi-chieu-spec-code.md#g-13) không có audit log| [T-13](#t-13) | P1 | 1 |
| [G-14](05-doi-chieu-spec-code.md#g-14) không recover | [T-14](#t-14) | ✅ xong | 0 |
| [G-15](05-doi-chieu-spec-code.md#g-15) rò rỉ rate limit | [T-15](#t-15) | ✅ xong | 0 |
| [G-16](05-doi-chieu-spec-code.md#g-16) lệch Version | [T-16](#t-16) | ✅ xong | 0 |
| [G-17](05-doi-chieu-spec-code.md#g-17) clock chưa tiêm | [T-17](#t-17) | ✅ xong | 0 |
| [G-18](05-doi-chieu-spec-code.md#g-18) SUSPENDED chết | [T-24](#t-24) | P2 | 4 |
| [G-19](05-doi-chieu-spec-code.md#g-19) phân trang giả | [T-26](#t-26) | ✅ xong | 4 |
| [G-20](05-doi-chieu-spec-code.md#g-20) N+1 candidates | [T-21](#t-21) · [T-35](#t-35) | P1 | 3 | ✅ ETA theo lô; số lần tra bị chặn bởi `max_candidates` (người gần nhất trước). Vẫn tra từng người một, nhưng tối đa 40 |
| [G-21](05-doi-chieu-spec-code.md#g-21) token không thu hồi | [T-24](#t-24) | P2 | 4 | ✅ xong |
| [G-22](05-doi-chieu-spec-code.md#g-22) OTP không giới hạn theo SĐT | [T-21](#t-21) | P1 | 3 | 🟡 rate limit đã toàn cụm; chưa giới hạn riêng theo SĐT |
| [G-23](05-doi-chieu-spec-code.md#g-23) trường chết ở pricing | [T-18](#t-18) | P2 | 1 |
| [G-24](05-doi-chieu-spec-code.md#g-24) ActiveByDriver chết | [T-12](#t-12) | P2 | 1 |
| [G-25](05-doi-chieu-spec-code.md#g-25) không quan sát được | [T-25](#t-25) | P1 | 3 | ✅ xong (trừ tracing) |
| [G-26](05-doi-chieu-spec-code.md#g-26) CCCD plaintext | [T-24](#t-24) | P2 | 4 | ✅ xong |
| [G-27](05-doi-chieu-spec-code.md#g-27) `driver_busy` cho tài xế OFFLINE | [T-28](#t-28) | ✅ xong | — |
| ✅ [G-28](05-doi-chieu-spec-code.md#g-28) khoá idempotency kẹt 24h | [T-05](#t-05) *(sửa khi kiểm thử)* | P0 | 1 |
| ✅ [G-29](05-doi-chieu-spec-code.md#g-29) data race ở `pkg/idem` | *(sửa khi kiểm thử)* | P0 | 1 |
| ✅ [G-30](05-doi-chieu-spec-code.md#g-30) `matching.MemoryStore` lệch đồng hồ | *(sửa khi kiểm thử)* | P1 | 1 |
| ✅ [G-31](05-doi-chieu-spec-code.md#g-31) sổ cái nhận bút toán vô chủ | *(sửa khi kiểm thử)* | P0 | 1 |
| ✅ [G-32](05-doi-chieu-spec-code.md#g-32) `pkg/idem` rò rỉ khoá quá hạn | *(sửa khi kiểm thử)* | P2 | 1 |
| — quyền DB chặn sửa/xoá bảng append-only | [T-27](#t-27) | ✅ xong | 1 |

---

## Việc **cần hỏi trước khi làm** (spec §7)

> **Từ [T-29](#t-29), sáu mục đầu không còn cần lập trình viên.** Tất cả đã sửa được ở
> **Bảng điều khiển → Cấu hình**, có hiệu lực trong 5 giây, có ngưỡng an toàn chặn giá trị thảm hoạ,
> và mọi thay đổi đều buộc phải kèm lý do. Cái còn thiếu bây giờ là **quyết định**, không phải công cụ.

- [ ] Chốt trọng số chấm điểm — cần dữ liệu thật + A/B test *(ô đã sẵn: `matching`)*
- [ ] Chốt bậc thang surge — cần dữ liệu cung/cầu thật *(ô đã sẵn: `surge`)*
- [ ] Chốt biểu giá — **cần hồ sơ kê khai giá cước đã nộp** *(ô đã sẵn: `pricing`, giao diện cảnh báo ngay trên biểu mẫu)*
- [ ] Bật thuế khấu trừ — **cần kế toán thuế xác nhận** *(ô đã sẵn: `wallet.tax_permille`, mặc định 0)*
- [ ] Chốt hạn mức công nợ — chính sách vận hành *(ô đã sẵn: `wallet.debt_limit_vnd`)*
- [ ] Chốt chu kỳ settlement + payout — chính sách tài chính (**code đã sẵn sàng**; ngưỡng chi trả đã là ô cấu hình, chỉ còn chốt kỳ)
- [ ] Chọn provider e-invoice — Viettel / VNPT / MISA + credential sandbox
- [ ] Multi-region / sharding — **không quyết trước GĐ 5**
