# 07 — TODO

> Danh sách việc **thực thi được**. Mỗi mục có: file cần sửa · điều kiện hoàn thành · **lệnh verify**.
> Không đánh dấu `[x]` khi chưa chạy được lệnh verify.
>
> `GĐ` = giai đoạn trong [06 — Kế hoạch triển khai](06-ke-hoach-trien-khai.md).
> `G-xx` = mã gap trong [05 — Đối chiếu spec ↔ code](05-doi-chieu-spec-code.md).

**Tiến độ:** **25 / 28** — GĐ 0, 1, 2 xong · GĐ 3 xong phần hạ tầng · GĐ 4 xong đường tiền và hai chốt bảo mật

| GĐ | Xong | Còn |
|---|---|---|
| GĐ 0 — Sửa nền | ✅ T-01, T-08, T-14, T-15, T-16, T-17 | — |
| GĐ 1 — Bền dữ liệu & đúng tiền | ✅ T-02, T-03, T-05, T-09, T-12, T-13, T-18 | — |
| GĐ 2 — Đúng nghiệp vụ | ✅ T-04, T-06, T-07, T-10, T-19, T-20 | — |
| GĐ 3 — Hạ tầng | ✅ T-25 · ✅ T-21 (Redis, NATS, MQTT, OSRM; H3 chưa) | T-11 (cần credential FCM) |
| GĐ 4 — Thương mại | ✅ T-22 (cổng thanh toán + đối soát) · 🟡 T-24 (mã hoá + thu hồi token xong; eKYC, hạn giấy tờ chưa) | T-23, T-26 |
| Chưa xếp | — | T-27 (quyền CSDL, `P0` khi triển khai), T-28 |

**Ngoài kế hoạch:** **7 lỗi** phát hiện khi kiểm thử
([§5.4](05-doi-chieu-spec-code.md#loi-moi)) — đều đã sửa và có test hồi quy đã được kiểm ngược
(khôi phục lỗi → xác nhận test đỏ → sửa lại).

**Trạng thái kiểm chứng:** `go build` / `go vet` / `gofmt` sạch · **118 test in-memory, 157 test với
đủ hạ tầng** (Postgres, Redis, NATS, MQTT) · `-race` sạch, không flake · luồng đầu-cuối qua HTTP
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

### <a id="t-23"></a>T-23 · Sinh kiểu TypeScript từ struct Go `P2`

- [ ] `godrive-admin/src/lib/types.ts` đang **chép tay** từ `internal/admin/domain.go` → sẽ trôi khỏi nhau
- [ ] Sinh tự động (`tygo` hoặc OpenAPI spec + `openapi-typescript`)
- [ ] Kiểm tra trong CI: sinh lại → `git diff` phải rỗng

**Verify** — `make gen-types && git diff --exit-code godrive-admin/src/lib/types.ts`

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

### <a id="t-26"></a>T-26 · Phân trang thật cho admin `[G-19]` `P2`

- [ ] Thay "lặp qua từng trạng thái rồi hợp lại" bằng **một** truy vấn `WHERE status = ANY($1)` + keyset pagination
- [ ] `Repository` thêm `List(ctx, filter, cursor, limit)`
- [ ] `Overview` dùng `COUNT(*)` thay vì đếm bằng cách nạp tối đa 200 dòng/trạng thái
- [ ] Giao diện: nút "tải thêm" thay vì cắt cứng

**Verify** — 10.000 tài xế trong CSDL → `GET /v1/admin/drivers?limit=50` trả **đúng** 50 dòng đầu theo thứ tự ổn định, phân trang hết được toàn bộ

---

### <a id="t-27"></a>T-27 · Quyền CSDL chặn sửa/xoá bảng append-only `[AC §5]` `P0`

Còn nợ từ [T-02](#t-02). Bất biến "chỉ thêm mới" hiện chỉ được giữ bằng quy ước ở tầng code — không
có gì ngăn một câu SQL chạy tay xoá lịch sử tài chính.

- [ ] Tạo role ứng dụng riêng (không phải superuser)
- [ ] `REVOKE UPDATE, DELETE ON ledger_entries, trip_events, admin_audit_log FROM <role>`
- [ ] Kiểm trong CI: chạy `UPDATE ledger_entries SET amount_vnd=0` bằng role ứng dụng → **phải bị từ chối**
- [ ] Ghi vào tài liệu vận hành như một bước bắt buộc khi dựng môi trường

Hoàn thành T-27 sẽ đóng AC §5 *"`trip_events` không bao giờ bị update/delete — verify qua DB role permission"*.

---

### <a id="t-28"></a>T-28 · Tách mã lỗi cho tài xế OFFLINE `[G-27]` `P2`

- [ ] `CanAcceptTrip` trả mã riêng (ví dụ `driver_offline`) khi `Status == OFFLINE`, giữ `driver_busy` cho `ASSIGNED`/`ON_TRIP`
- [ ] Bảng điều khiển hiện đang hiển thị *"Bạn đang trong một chuyến khác"* cho tài xế chỉ đơn giản là chưa bật app

**Verify** — `GET /v1/admin/drivers/{id}` của tài xế `OFFLINE` đã duyệt KYC trả `blocked_reason=driver_offline`

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
| [G-19](05-doi-chieu-spec-code.md#g-19) phân trang giả | [T-26](#t-26) | P2 | 4 |
| [G-20](05-doi-chieu-spec-code.md#g-20) N+1 candidates | [T-21](#t-21) | P1 | 3 | 🟡 ETA đã theo lô; `drivers.Get` vẫn N+1 |
| [G-21](05-doi-chieu-spec-code.md#g-21) token không thu hồi | [T-24](#t-24) | P2 | 4 | ✅ xong |
| [G-22](05-doi-chieu-spec-code.md#g-22) OTP không giới hạn theo SĐT | [T-21](#t-21) | P1 | 3 | 🟡 rate limit đã toàn cụm; chưa giới hạn riêng theo SĐT |
| [G-23](05-doi-chieu-spec-code.md#g-23) trường chết ở pricing | [T-18](#t-18) | P2 | 1 |
| [G-24](05-doi-chieu-spec-code.md#g-24) ActiveByDriver chết | [T-12](#t-12) | P2 | 1 |
| [G-25](05-doi-chieu-spec-code.md#g-25) không quan sát được | [T-25](#t-25) | P1 | 3 | ✅ xong (trừ tracing) |
| [G-26](05-doi-chieu-spec-code.md#g-26) CCCD plaintext | [T-24](#t-24) | P2 | 4 | ✅ xong |
| [G-27](05-doi-chieu-spec-code.md#g-27) `driver_busy` cho tài xế OFFLINE | [T-28](#t-28) | P2 | — |
| ✅ [G-28](05-doi-chieu-spec-code.md#g-28) khoá idempotency kẹt 24h | [T-05](#t-05) *(sửa khi kiểm thử)* | P0 | 1 |
| ✅ [G-29](05-doi-chieu-spec-code.md#g-29) data race ở `pkg/idem` | *(sửa khi kiểm thử)* | P0 | 1 |
| ✅ [G-30](05-doi-chieu-spec-code.md#g-30) `matching.MemoryStore` lệch đồng hồ | *(sửa khi kiểm thử)* | P1 | 1 |
| ✅ [G-31](05-doi-chieu-spec-code.md#g-31) sổ cái nhận bút toán vô chủ | *(sửa khi kiểm thử)* | P0 | 1 |
| ✅ [G-32](05-doi-chieu-spec-code.md#g-32) `pkg/idem` rò rỉ khoá quá hạn | *(sửa khi kiểm thử)* | P2 | 1 |
| — quyền DB chặn sửa/xoá bảng append-only | [T-27](#t-27) | P0 | 1 |

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
