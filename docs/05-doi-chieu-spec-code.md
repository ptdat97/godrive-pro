# 05 — Đối chiếu spec ↔ code

> Đối chiếu ngày **2026-08-24**. Mọi kết luận dưới đây **đã được xác minh bằng cách đọc code
> và chạy `go build` / `go vet` / `go test ./...` / `go test -race ./...`** — không có mục nào suy đoán.
>
> **Cập nhật sau Giai đoạn 0 và 1** (2026-08-24): **12 gap đã đóng** —
> G-01, G-02, G-03, G-05, G-08, G-09, G-12, G-13, G-14, G-15, G-16, G-17.
> Thêm **5 lỗi mới phát hiện trong lúc kiểm thử** (§5.6), đều đã sửa.
> Kiểm chứng trên Postgres 18.4 + PostGIS 3.6.3: 71 test in-memory + 6 test tích hợp, `-race -count=3` sạch.
>
> Ký hiệu: 🟢 đã làm & đã verify · 🟡 đã làm nhưng chưa verify hoặc còn hở · 🔴 chưa làm / hỏng · ✅ **đã đóng ở GĐ 0**

---

## 5.1 Điểm số tổng quan

| Vùng | Đã có | Còn hở | Đánh giá |
|---|---|---|---|
| Kiến trúc & ranh giới module | Port một chiều sạch, composition root duy nhất, không có import chéo | — | 🟢 **Rất tốt.** Đây là phần mạnh nhất của repo |
| Máy trạng thái chuyến | Đồ thị là dữ liệu, trip+event cùng transaction, optimistic lock | phí huỷ chưa ghi sổ | 🟢 |
| Chống ghép trùng | 2 lớp (`ClaimTrip` + CAS `Reserve`), test dưới `-race` | lớp CSDL chưa kích hoạt | 🟢 |
| Sổ cái kép | Tổng = 0 cưỡng chế (ứng dụng **+ CHECK ở CSDL**), idempotency chốt bằng PK, đã bền ở Postgres | — | 🟢 |
| Tính giá | `computeBase` thuần, **toàn số nguyên**, 17 test, phủ 82% | surge vẫn là bậc thang giả định | 🟢 |
| Ghép chuyến — chấm điểm | Công thức đúng spec, tie-break tất định, đã lọc theo nợ thật | **3/5 đầu vào vẫn là hằng số** | 🔴 |
| Chế độ Postgres | **5/9 store** có repo (thêm `identity`, `wallet`, `admin_audit`) | 4 store còn ở bộ nhớ ⇒ chỉ chạy được 1 bản sao | 🟡 |
| Độ bền dữ liệu | tài khoản, tài xế, chuyến, **sổ cái**, nhật ký admin đã bền | offer + idempotency vẫn mất khi restart | 🟡 |
| Vận hành / quan sát | log có `request_id`, `admin` có cảnh báo | không metric, không tracing, không audit log | 🔴 |
| Kiểm thử | **77 test**, `-race -count=3` sạch ở cả hai chế độ, phủ 57,2% | `outbox` 0%; chưa có test tầng HTTP | 🟡 |

**Một câu tóm tắt:** *bộ khung kiến trúc đã đúng và đáng giữ; phần chưa xong là **nối dây** —
nhiều thành phần đã viết xong nhưng chưa được cắm vào luồng chạy.*

**Sau GĐ 0 + 1:** hệ thống chạy trọn vẹn trên Postgres và **phần tiền đã đúng** — sổ cái bền, cân
bằng được cưỡng chế ở hai tầng, cổng chặn công nợ hoạt động thật, phí huỷ được ghi sổ, đường tính
giá hoàn toàn bằng số nguyên.

Việc còn lại nặng nhất chuyển sang **chất lượng ghép chuyến**: 3 trong 5 thành phần chấm điểm vẫn là
hằng số vì chỉ số tài xế không bao giờ được cập nhật, và surge vẫn luôn bằng 1.0. Đó là GĐ 2.

---

## 5.2 Đối chiếu Acceptance Criteria của spec

### §3 — Matching (spec ghi 5 AC)

| AC | Trạng thái | Bằng chứng |
|---|:---:|---|
| Không tài xế nào bị ghép 2 chuyến, test song song dưới `-race` | 🟢 | `TestOnlyOneDriverWinsTrip` — pass với `-race` |
| Offer hết hạn + nới bán kính → `EXPIRED` sau `MaxRounds` | 🔴 | **Không có test nào.** Đường đi này chưa bao giờ được thực thi trong kiểm thử |
| Tài xế nợ / KYC chưa duyệt không lọt candidate list | 🟢 | `TestCashDebtBlocksDriverEndToEnd` khẳng định `DispatchRound` gửi **0** lời mời cho tài xế nợ quá hạn |
| Surge không bao giờ vượt 2.0, test boundary `ratio ≥ 4` + clamp ở `Estimate` | 🟢 | `TestSurgeStaircase` (9 mốc, gồm ratio 4.0 và 100) + `TestEstimateClampsRogueSurgeProvider` |
| Chấm điểm tất định | 🟡 | Tie-break `DriverID` đã cài; 4 test scoring có, nhưng **không có test riêng cho tính tất định** |

### §4 — Pricing & Wallet (5 AC)

| AC | Trạng thái | Bằng chứng |
|---|:---:|---|
| `computeBase` test: cự ly ngắn, dài, surge 2.0, giờ đêm, làm tròn nghìn | 🟢 | 17 test; `computeBase` phủ **100%**, package 80,7% |
| Không tạo được `Transaction` lệch mà `Validate()` bỏ qua | 🟢 | `TestUnbalancedRejected`; `MemoryLedger.Post` gọi `Validate()` trước mọi lần ghi |
| `SettleTrip` gọi 2 lần chỉ ghi 1 lần | 🟢 | `TestPostIsIdempotent` + `Exists(txID)` guard |
| Tài xế nợ vượt hạn mức bị `CanAcceptTrip` chặn | 🟢 | `TestCashDebtBlocksDriverEndToEnd` + kiểm chứng HTTP thật: 36 chuyến → nợ → bị chặn → nạp → nhận lại |
| **Không có phép chia float nào trên đường đi của tiền** | 🟢 | Đã bỏ cả 3; thêm `money.MulDiv`; `SurgeProvider` trả permille `int64` |

### §5 — Trip & Location (5 AC)

| AC | Trạng thái | Bằng chứng |
|---|:---:|---|
| Transition không hợp lệ bị reject với mã lỗi rõ ràng | 🟢 | `TestTransitionGraph`, `TestTerminalStates` |
| `trip_events` không bao giờ bị update/delete | 🟡 | Đúng ở tầng code; `TestPostgresFullTripLifecycle` xác nhận 6 bản ghi đủ và đúng thứ tự. **Chưa verify bằng DB role permission** như spec yêu cầu → [T-27](07-todo.md#t-27) |
| Trip + event luôn cùng transaction — test rollback khi ghi event lỗi | 🟡 | `PostgresRepo.Save` dùng `BeginTx` + `defer Rollback` đúng; **không có test rollback** |
| `Create` cùng `Idempotency-Key` 2 lần chỉ tạo 1 chuyến | 🟢 | `TestIdempotentCreateReturnsSameTrip` + `TestConcurrentCreateSameKeyCreatesOneTrip` (10 goroutine) |
| Ping `Mocked=true` bị gắn cờ và không lọt chỉ mục | 🔴 | Code có (`Ingest` từ chối trước khi `Upsert`); **không có test** |

### §5c — Admin (10 AC)

| AC | Trạng thái |
|---|:---:|
| Số ngoài `ADMIN_PHONES` không lấy được token | 🟢 `TestAdminAuthRejectsNonAllowlistedPhone` |
| Danh sách rỗng = không ai đăng nhập được | 🟢 `TestAdminAuthClosedByDefault` |
| `0901…` và `+8490…` là cùng một người | 🟢 `TestAdminAuthNormalizesPhoneFormat` |
| Số liệu tổng quan phản ánh trạng thái thật | 🟢 `TestAdminOverviewCountsRealState` |
| Dòng tài xế gộp sẵn ví + vị trí | 🟢 `TestAdminListDriversJoinsWalletAndLocation` |
| Lọc chạy ở server; trạng thái sai trả `status_invalid` | 🟢 `TestAdminFilterByStatusAndQuery` |
| Duyệt hồ sơ có hiệu lực, cập nhật `BlockedReason` | 🟢 `TestAdminReviewKYCChangesState` |
| Bản đồ trả cung + cầu cùng bán kính, cùng thời điểm | 🟢 `TestAdminLiveMapPairsSupplyAndDemand` |
| Điểm đón ngoài bán kính bị loại; toạ độ sai trả `point_invalid` | 🟢 `TestAdminLiveMapFiltersByRadius` |
| **Nhật ký thao tác admin** | 🔴 chưa làm ([G-13](#g-13)) |

**Tổng: 16/25 AC đạt (64%)** — tăng 1 sau GĐ 0. Module `admin` là phần hoàn thiện nhất (9/10);
`pricing` vẫn là phần yếu nhất (0/2 AC riêng, và kéo theo 1 AC của matching) — đó là [T-18](07-todo.md#t-18) ở GĐ 1.

---

## 5.3 Danh mục gap

Sắp theo mức độ chặn. **P0 = chặn phát hành**, **P1 = chặn vận hành thật**, **P2 = nợ kỹ thuật**.

### 🔴 P0 — chặn phát hành

<a id="g-01"></a>
#### G-01 · ✅ ĐÃ ĐÓNG (GĐ 0) · `identity` không có repo Postgres

**Đã sửa:** thêm [`identity/store_postgres.go`](../godrive/internal/identity/store_postgres.go)
(bảng `accounts` + `otp_challenges`), migration
[`0002`](../godrive/migrations/0002_identity_and_documents.up.sql), và nhánh chọn repo trong `app.New`.
Kèm job dọn thử thách quá hạn chạy trong worker nền (`App.sweepOTPChallenges`, chu kỳ 1 phút).

**Verify:** `TestPostgresFullTripLifecycle`, `TestPostgresOTPChallengeRoundTrip`,
`TestPostgresAccountUpsertIsStable` — bật bằng `TEST_DATABASE_URL`. Đã chạy thật qua HTTP:
`POST /v1/drivers/register` → 201, `POST /v1/trips` → 201, chuyến đi trọn tới `PAID`.

<details><summary>Bối cảnh gốc</summary>

**Bằng chứng** — `idRepo := identity.NewMemoryRepo()` nằm **ngoài** khối `if cfg.InMemory()`,
không có nhánh Postgres như `driver`/`trip`.

**Chuỗi hậu quả:**
```
identity.MemoryRepo  →  bảng `accounts` không bao giờ được INSERT
                     →  drivers.account_id  REFERENCES accounts(id)  ✗ FK
                     →  trips.rider_id      REFERENCES accounts(id)  ✗ FK
                     →  POST /v1/drivers/register  → 409 driver_create_failed
                     →  POST /v1/trips              → 500 db_error
```

> Spec §8.0 mô tả đúng vấn đề này, nhưng **chỉ nêu `drivers`**. Thực tế `trips.rider_id` cũng
> là khoá ngoại tới `accounts` → **cả luồng khách lẫn luồng tài xế đều hỏng**, không chỉ đăng ký tài xế.

Redis vẫn hợp hơn cho OTP challenge (TTL 5 phút, ghi/xoá liên tục); bảng `otp_challenges`
là đường dự phòng cho môi trường chưa có Redis — sẽ thay ở [T-21](07-todo.md#t-21). → [T-01](07-todo.md#t-01)

</details>

<a id="g-02"></a>
#### G-02 · Sổ cái chỉ có bản bộ nhớ — restart là mất sạch tiền

**Bằng chứng** — [`app.go:96`](../godrive/internal/app/app.go#L100): `wallet.NewMemoryLedger()`, không điều kiện.
Không tồn tại file `internal/wallet/store_postgres.go`.

Bảng `ledger_entries` + `ledger_transactions` đã có DDL đầy đủ, index đầy đủ — **không một dòng Go nào chạm vào**.

> Rủi ro số 1 trong spec §10 là *"đối soát tiền mặt — sai một đồng cũng mất niềm tin tài xế"*.
> Ở trạng thái hiện tại, một lần `kubectl rollout restart` xoá toàn bộ công nợ của mọi tài xế.

→ [T-02](07-todo.md#t-02)

</details>

<a id="g-03"></a>
#### G-03 · ✅ ĐÃ ĐÓNG (GĐ 1) · Cổng chặn nợ không hoạt động

**Đã sửa — hai lớp, có chủ đích:**

1. **Đọc số dư THẬT từ sổ cái** ở cả hai điểm quyết định: `matching.candidates()` (qua `matching.WalletPort`)
   và `driver.Reserve()` (qua `driver.BalanceReader`). Cả hai đều là Port do bên tiêu thụ khai báo —
   `matching` và `driver` không import `wallet`.
   Kiểm hai lần là cố ý: giữa lúc gửi lời mời và lúc tài xế bấm nhận, số dư có thể đã đổi.
2. **Đồng bộ cột cache** `drivers.wallet_balance` qua sự kiện mới `wallet.balance_changed`, để bảng
   điều khiển hiển thị đúng. `UpdateWalletBalance` **cố ý không tăng `version`**: version bảo vệ
   chuyển trạng thái, còn số dư là giá trị suy ra — tăng version ở đây sẽ làm hỏng CAS của
   `Reserve`/`SetStatus` đang chạy song song.

**Verify:** `TestCashDebtBlocksDriverEndToEnd` — 21 chuyến tiền mặt → nợ 210.000đ → `Reserve` trả
`wallet_debt_exceeded` → `DispatchRound` gửi **0** lời mời → nạp 300k → nhận lại được ngay.
Qua HTTP thật: 36 chuyến → `in_debt=true`, `amount_to_clear=1.600đ`, bảng điều khiển hiện đúng lý do chặn.

<details><summary>Bối cảnh gốc</summary>

**Bằng chứng** — không có một phép gán `WalletBalance` nào ngoài file test và SQL scan/insert:

| Nơi | Vai trò |
|---|---|
| `SettleCashTrip` ghi `DRIVER_WALLET −fee` | vào **`ledger_entries`** |
| `CanAcceptTrip` đọc `d.WalletBalance` | từ **cột cache `drivers.wallet_balance`** |
| Cầu nối giữa hai chỗ | **không tồn tại** |

**Hệ quả:** `wallet_balance` vĩnh viễn = 0 → `wallet_debt_exceeded` **không bao giờ được trả về**
trong luồng thật → tài xế nợ bao nhiêu cũng nhận chuyến tiếp.

Spec §4.2 gọi đây là *"mô hình bắt buộc từ Phase 1, không phải tính năng phụ"* — hiện nó **chưa hoạt động**.

→ [T-03](07-todo.md#t-03)

</details>

<a id="g-04"></a>
#### G-04 · Chỉ số tài xế đóng băng → 3/5 thành phần chấm điểm là hằng số

**Bằng chứng** — không có code nào ghi `CompletedTrips`, `AcceptanceRate`, `Rating`, `CancelRate`
sau `Onboard`. Mọi tài xế vĩnh viễn `Rating = 5.0`, `AcceptanceRate = 0.8`.

Thay vào công thức chấm điểm:

```
điểm = 1.0 × ETA
     + 60.0 × (5 − 5.0)     = 0        ← hằng số
     + 90.0 × (1 − 0.8)     = 18       ← hằng số, cộng vào MỌI ứng viên
     − 0.25 × idle_giây                 ← xem G-10, ngữ nghĩa sai
     + 0.20 × góc_lệch
```

⇒ Thực tế **chấm điểm ≈ chỉ theo ETA và góc lệch hướng.** Toàn bộ lý lẽ *"tài xế gần nhưng hay bỏ chuyến
làm khách chờ lâu hơn tài xế xa mà luôn nhận"* (spec §3.2) hiện **không có tác dụng**.

**Cần:** subscriber `trip.completed` / `trip.cancelled` / `offer.created` + `offer.accepted`
cập nhật thống kê tài xế (trung bình trượt). → [T-04](07-todo.md#t-04)

<a id="g-09"></a>
#### G-09 · ✅ ĐÃ ĐÓNG (GĐ 1) · Ba phép float trên đường đi của tiền

> ### ⚠️ Đính chính phân tích ban đầu
>
> Bản đối chiếu đầu tiên kết luận *"`RoundTo(1000)` che mất sai số ở giá khách trả"*.
> **Kết luận đó SAI.** Khi viết test hồi quy tôi quét toàn dải và đo được:
>
> | Đo được | Kết quả |
> |---|---|
> | `computeBase` cho kết quả khác nhau | **29.199 / 38.001 cự ly (77%)** |
> | **Tổng cước KHÁCH PHẢI TRẢ khác nhau** | **422 / 190.005 tổ hợp** (cự ly × surge) |
> | Mức lệch mỗi lần | **1.000đ** |
>
> Sai lệch vài đồng ở `computeBase` **không** bị làm tròn nuốt mất — khi nó vắt qua ranh giới
> `RoundTo(1000)` thì giá cuối lệch nguyên một nghìn. Ví dụ cự ly 2.649m: số nguyên cho base 17.001
> → **18.000đ**, float cho 16.997 → **17.000đ**.
>
> Đây không phải vi phạm nguyên tắc suông mà là **tính sai tiền của khách**. Mức ưu tiên P0 của mục
> này lẽ ra phải được lập luận bằng con số này ngay từ đầu.

**Đã sửa:**
- Thêm `money.MulDiv(num, den)` — phép nguyên thuỷ nhân/chia có làm tròn nửa ra xa số 0,
  hoàn toàn bằng số nguyên. `MulPermille` nay là `MulDiv(rate, 1000)`.
- `computeBase` quy quãng đường/thời lượng về số nguyên (mét, giây) **ngay lập tức**, rồi tính bằng `MulDiv`.
- `SurgeProvider` đổi chữ ký: trả **`int64` permille** (1000 = ×1.0) thay vì `float64`.
  `DemandSurge` so sánh bậc thang bằng số nguyên (`demand×10` với `supply×ngưỡng×10`) — tránh cả phép
  chia float lẫn việc 1.2 không biểu diễn chính xác được ở nhị phân.
- `Quote.SurgeMult float64` giữ lại nhưng **chỉ để hiển thị**; `Quote.SurgePermille` là nguồn sự thật.

**Verify:** `TestComputeBaseKnownTruncationCases`, `TestFloatDriftChangesFareByAThousand` (4 ca hồi quy
với giá kỳ vọng chính xác), `TestComputeBaseRoundsHalfUpNotTruncate`, `TestSurgeMultiplyIsExact`.
Kiểm ngược: khôi phục bản float thì **3 test fail**.

<details><summary>Bối cảnh gốc</summary>

Spec §0 quy tắc 6: *"Tiền là `money.VND` (int64 đồng). **Không dùng float cho tiền — kể cả biến tạm.**"*

| Vị trí | Mã |
|---|---|
| [`pricing/service.go:135`](../godrive/internal/pricing/service.go#L135) | `fare += money.VND(extra / 1000 * float64(t.PerKm))` |
| [`pricing/service.go:137`](../godrive/internal/pricing/service.go#L137) | `fare += money.VND(r.DurationS / 60 * float64(t.PerMinute))` |
| [`pricing/service.go:77`](../godrive/internal/pricing/service.go#L77) | `subtotal := money.VND(float64(base+night) * mult)` |

**Đo được, không phải lo xa.** Quét mọi `base` từ 10.000 đến 400.000 (bước 100đ):

| `surge` | Số giá trị lệch so với số nguyên | Lệch |
|---|---:|---|
| 1.2 | 0 / 3.901 | — |
| **1.4** | **923 / 3.901 (24%)** | **−1đ** |
| 1.7 | 0 / 3.901 | — |
| 2.0 | 0 / 3.901 | — |

Ví dụ: `base = 10.300`, `× 1.4` → float cho `14.419`, số nguyên đúng là `14.420`.

*(Phần này của bản gốc cho rằng `RoundTo(1000)` che mất sai số — xem đính chính ở đầu mục.)*

→ [T-09](07-todo.md#t-09)

</details>

---

### 🔴 P1 — chặn vận hành thật

<a id="g-05"></a>
#### G-05 · ✅ ĐÃ ĐÓNG (GĐ 1) · Phí huỷ chuyến không bao giờ được ghi sổ

**Đã sửa:** `wallet.Service.PostCancelFee` (idempotent theo `tx_cancel_<tripID>`) + consumer
`app.onTripCancelled` vừa ghi sổ phí huỷ vừa trả tài xế về `IDLE`.
`trip.Service.Cancel` nay tính `cancelFee` **một lần** rồi dùng lại cho cả nhật ký lẫn sự kiện.

**Verify:** `TestLateCancelCreditsDriver` (tài xế +10.000đ, khách −10.000đ, tài xế về `IDLE`) và
`TestEarlyCancelIsFree` (huỷ trong cửa sổ 2 phút → không ai bị ghi sổ). Cả hai dùng đồng hồ tiêm vào.

<details><summary>Bối cảnh gốc</summary>

Spec §4.4: *"Quá cửa sổ → `CancelFeeVND` (10.000đ) **ghi có cho tài xế qua `wallet.CancelFee`**"*.

Thực tế:
- `trip.Service.cancelFee()` tính đúng, đưa vào `Event.Meta` và payload `trip.cancelled` ✅
- `wallet.CancelFee(...)` là hàm constructor Transaction đã viết xong ✅
- **Không có ai gọi nó.** `app.StartWorkers` chỉ đăng ký `setDriverStatus(IDLE)` cho `trip.cancelled` ❌

⇒ Tài xế bị huỷ chuyến trễ **không nhận được một đồng đền bù nào**, dù giao diện đã hứa.

**Phụ:** `cancelFee(t)` được gọi **hai lần**; nếu đồng hồ nhích qua ranh giới 2 phút giữa hai lần gọi
thì `trip_events.meta` và sự kiện sẽ mâu thuẫn — đúng loại sai lệch không thể giải thích được khi tài
xế khiếu nại. → [T-05](07-todo.md#t-05)

</details>

<a id="g-06"></a>
#### G-06 · Outbox đã viết xong nhưng chưa nối — sự kiện là **at-most-once**

**Bằng chứng:**
- `internal/outbox` chỉ được import ở **một chỗ**: [`cmd/worker/main.go:13`](../godrive/cmd/worker/main.go#L13)
- Chỗ đó tạo `outbox.NewMemoryStore()` **mới toanh** rồi đưa cho relay — **không ai Enqueue vào store đó**
- Không module nghiệp vụ nào import `outbox`

⇒ Relay chạy mỗi giây, đọc rỗng, không làm gì. Trong khi đó `eventbus.inMemory.Publish` spawn goroutine
và **chỉ log lỗi handler rồi bỏ qua** ([bus.go:81](../godrive/internal/platform/eventbus/bus.go#L84)).

**Rủi ro cụ thể:** một lần `wallet.SettleTrip` lỗi trong `onTripCompleted` ⇒ chuyến đó **không bao giờ được ghi sổ**,
và không có gì phát hiện ra. Spec §6 nói *"Outbox — sự kiện ghi cùng transaction nghiệp vụ"* — chưa thực hiện. → [T-06](07-todo.md#t-06)

<a id="g-07"></a>
#### G-07 · Surge vĩnh viễn = 1.0

`DemandSurge.RecordRequest` ([surge.go:30](../godrive/internal/pricing/surge.go#L30)) **không có ai gọi**
(`grep -rn "RecordRequest"` chỉ ra định nghĩa).

⇒ `demand` luôn rỗng ⇒ `ratio = 0` ⇒ nhánh `default` ⇒ **`1.0`**.

Toàn bộ bậc thang surge (§3.4 của spec), việc clamp hai lần, và AC *"surge không bao giờ vượt 2.0"*
đều đang mô tả một đường đi **chưa bao giờ được thực thi**.

**Cần:** gọi `RecordRequest(pickup, now)` trong `Estimate()` (hoặc subscribe `trip.requested` —
đo *nhu cầu thật* thay vì *lượt xem giá*, cần chọn có chủ đích). → [T-07](07-todo.md#t-07)

<a id="g-10"></a>
#### G-10 · `IdleSeconds` đo nhầm thứ — đang **thưởng cho ping cũ**

```go
// internal/matching/engine.go:234
IdleSeconds: now.Sub(s.UpdatedAt).Seconds(),   // s = location.Snapshot
```

`Snapshot.UpdatedAt` là **thời điểm ping cuối**, không phải thời điểm tài xế bắt đầu rảnh.
Kết hợp với `s -= cfg.WeightIdle * c.IdleSeconds` (trừ điểm phạt):

> **Tài xế gửi ping thưa hơn / mạng kém hơn được ưu tiên cao hơn.**

Bị chặn trên bởi `StaleAfter = 45s`, nên chênh lệch tối đa chỉ `0.25 × 45 ≈ 11 điểm` — nhỏ so với
`WeightAcceptance = 90`. Nhưng vì [G-04](#g-04) làm 3/5 thành phần thành hằng số, **11 điểm này lại
đủ sức đảo thứ tự** giữa hai ứng viên có ETA chênh dưới 11 giây.

Spec §3.2 mô tả ý định đúng (*"chờ lâu được ưu tiên — phân bổ thu nhập đều hơn, yếu tố giữ chân tài xế
quan trọng ở VN"*) nhưng code đo sai đại lượng.

**Cần:** thêm `driver.IdleSince` (đặt khi vào `IDLE`) và tính `IdleSeconds = now − IdleSince`. → [T-10](07-todo.md#t-10)

<a id="g-11"></a>
#### G-11 · Không có push — tài xế phải poll để biết có chuyến

`offer.created` được publish nhưng **không ai subscribe**. `notification.Pusher` / `LogPusher`
đã định nghĩa nhưng **không được import ở đâu cả** ngoài chính package đó.

⇒ Ứng dụng tài xế phải gọi `GET /v1/offers` liên tục. Với `OfferTTL = 15s`, muốn không bỏ lỡ
thì phải poll ~3–5 giây/lần — đúng thứ mà spec §9 đã chọn MQTT để tránh
(*"Android giá rẻ, 4G chập chờn, tiết kiệm pin và băng thông"*). → [T-11](07-todo.md#t-11)

<a id="g-12"></a>
#### G-12 · ✅ ĐÃ ĐÓNG (GĐ 1) · Tài xế không có cách nào xem ví hay trả nợ

**Đã sửa:** [`wallet/http.go`](../godrive/internal/wallet/http.go) —
`GET /v1/drivers/me/wallet` (số dư, tiền mặt đang cầm, hạn mức, `in_debt`, **`amount_to_clear`**:
nạp đúng chừng này là nhận chuyến lại được) và `GET /v1/drivers/me/statement` (mặc định 30 ngày, trần 92 ngày).

> **`POST /v1/drivers/me/topup` CHỈ đăng ký khi `DEV_AUTH=true`.** Một endpoint tự ghi có vào ví mà
> không có đối ứng tiền thật chính là máy in tiền. Ở production, tiền vào ví chỉ đến từ webhook cổng
> thanh toán đã xác thực chữ ký ([T-22](07-todo.md#t-22)). Đây là quyết định có chủ đích, không phải thiếu sót.

**Verify:** kịch bản HTTP thật — xem ví, nạp 500k ba lần cùng `Idempotency-Key` (chỉ cộng một lần),
sao kê trả 73 bút toán `{TRIP: 72, TOPUP: 1}`.

<details><summary>Bối cảnh gốc</summary>

`internal/wallet` **không có file `http.go`**. Service đã có `DriverBalance`, `CashOnHand`,
`Statement`, `TopUp` — chỉ `admin` gọi được, tài xế thì không.

⇒ Mô hình công nợ tiền mặt (trụ cột của spec) **không có giao diện người dùng ở phía tài xế**. → [T-12](07-todo.md#t-12)

</details>

<a id="g-13"></a>
#### G-13 · ✅ ĐÃ ĐÓNG (GĐ 1) · Không có nhật ký thao tác admin

**Đã sửa:** bảng `admin_audit_log` (migration `0003`, chỉ thêm mới) + `admin.AuditLog` với bản bộ nhớ
và bản Postgres + `GET /v1/admin/audit?actor=&target_type=&target_id=`.

`ReviewKYC` nay nhận `admin.Actor` (lấy từ token) và ghi lại **cả trạng thái trước lẫn sau**.
Ghi nhật ký lỗi thì trả lỗi luôn: một thay đổi hồ sơ không truy vết được còn tệ hơn một lần duyệt
thất bại, vì nó âm thầm phá bất biến *"mọi thao tác quản trị đều có dấu vết"*.

Interface `AuditLog` **không có phương thức sửa hay xoá** — bất biến "chỉ thêm mới" nằm ngay trong
hình dạng của nó, không phải chỉ trong quy ước.

**Verify:** `TestAdminReviewKYCChangesState` (bộ nhớ), `TestPostgresAuditLogRecordsKYCReview`
(Postgres, hai lần duyệt → 2 dòng, mới nhất lên đầu). Qua HTTP: `GET /v1/admin/audit` trả
`review_kyc`, actor, `PENDING→APPROVED`.

<details><summary>Bối cảnh gốc</summary>

`ReviewKYC` là hành động ghi duy nhất của module `admin` — và **không để lại dấu vết nào**:
không biết ai duyệt, lúc nào, duyệt hồ sơ nào, lý do gì.

Spec §5c đã đánh dấu *"chưa làm, cần cho đối soát nội bộ"*. → [T-13](07-todo.md#t-13)

</details>

<a id="g-14"></a>
#### G-14 · ✅ ĐÃ ĐÓNG (GĐ 0) · Goroutine dispatch không có `recover()`

**Đã sửa:** thêm [`platform/safego`](../godrive/internal/platform/safego/safego.go).
`workers.go` bọc goroutine dispatch bằng `safego.Recover` với cleanup đẩy chuyến về `EXPIRED`
(nếu không nó kẹt `SEARCHING` vĩnh viễn chờ một dispatcher đã chết); `eventbus` bọc goroutine handler.
`Recover` tự phòng cả trường hợp cleanup panic lần hai.

**Verify:** `TestDispatchPanicDoesNotKillProcess` + `TestEventHandlerPanicIsContained`.
Đã kiểm ngược: bỏ `recover` ra thì test **fail** — cả binary test chết vì panic, đúng như triệu chứng thật.

<details><summary>Bối cảnh gốc</summary>


```go
// internal/app/workers.go:33
go func() {
    if err := a.Matcher.Dispatch(root, p.TripID); err != nil { … }
}()
```

`httpx.Recover()` chỉ bảo vệ **handler HTTP**. Panic trong goroutine này (nil pointer từ một
implementation `Port` mới, chia cho 0 trong scoring…) **giết cả tiến trình** — kéo theo toàn bộ
state in-memory: sổ cái, offer, chỉ mục vị trí, idempotency key.

Điều tương tự áp dụng cho goroutine handler trong `eventbus.inMemory.Publish`. → [T-14](07-todo.md#t-14)

</details>

---

### 🟡 P2 — nợ kỹ thuật

| Mã | Vấn đề | Vị trí |
|---|---|---|
| <a id="g-08"></a>**G-08** | `driver.PostgresRepo` **không lưu và không đọc lại `Documents`**. `Create` chỉ ghi `national_id`, `driver_license`, `vehicle_reg_no`; **bỏ hẳn** `insurance_no`, `insurance_until`. `scan()` không đọc cột nào ⇒ `Get()` luôn trả `Documents{}` rỗng ⇒ **admin duyệt KYC mà không xem được giấy tờ** | [`driver/store_postgres.go:26-52`](../godrive/internal/driver/store_postgres.go#L67) |
| <a id="g-15"></a>**G-15** | Rate limit **không bao giờ dọn bucket** → map lớn dần theo số IP đã từng gọi. Rò rỉ bộ nhớ chậm nhưng chắc | [`httpx/middleware.go:120`](../godrive/internal/platform/httpx/middleware.go#L98) |
| <a id="g-16"></a>**G-16** | `trip.MemoryRepo.Save` **không** tăng `t.Version` của caller, `PostgresRepo.Save` **có** (`t.Version++`). Hai repo lệch hành vi ⇒ code chạy đúng in-memory có thể fail ở Postgres | [`trip/store_memory.go:43`](../godrive/internal/trip/store_memory.go#L45) |
| <a id="g-17"></a>**G-17** | `location.MemoryIndex.Nearby` và `FraudDetector` dùng `time.Now()` thay `clock.Clock` tiêm được ⇒ không viết được test tất định cho lọc độ tươi và cửa sổ cờ gian lận. Vi phạm spec §6 | [`index_memory.go:83`](../godrive/internal/location/index_memory.go#L73), [`fraud.go:38`](../godrive/internal/location/fraud.go#L45) |
| <a id="g-18"></a>**G-18** | `SUSPENDED` chỉ được **đọc** ở 4 chỗ, **không có code path nào đặt nó**. Phát hiện gian lận (`FraudDetector`) không dẫn tới hành động nào | `grep StatusSuspended` |
| <a id="g-19"></a>**G-19** | `admin.ListDrivers`/`ListTrips` với "tất cả" = lặp qua từng trạng thái, mỗi lần `LIMIT n`, rồi hợp lại và cắt. Khi dữ liệu lớn kết quả sẽ **thiếu**. Cần phân trang keyset | [`admin/service.go:88`](../godrive/internal/admin/service.go#L72) |
| <a id="g-20"></a>**G-20** | `matching.candidates()` gọi `drivers.Get()` cho **từng** ứng viên (N+1). In-memory không sao; lên Postgres với `BatchSize` lớn sẽ thành điểm nghẽn | [`matching/engine.go:210`](../godrive/internal/matching/engine.go#L201) |
| <a id="g-21"></a>**G-21** | JWT không có `jti`, không có refresh token, không thu hồi được. Đăng xuất chỉ xoá cookie — token vẫn hợp lệ tới 24h. Tài xế bị khoá vẫn dùng token cũ được | `platform/authn` |
| <a id="g-22"></a>**G-22** | `identity.RequestOTP` **không giới hạn theo số điện thoại** — chỉ có rate limit theo IP. Đổi IP là spam được tin nhắn tốn tiền thật | `identity/service.go` |
| <a id="g-23"></a>**G-23** | `Quote.Discount` và `EstimateInput.PromoCode` khai báo nhưng không bao giờ được dùng. `POST /v1/quotes` nhận `vehicle_type` rồi **bỏ qua** (luôn gọi `EstimateAll`) | `pricing` |
| <a id="g-24"></a>**G-24** | `trip.Repository.ActiveByDriver` đã khai báo + cài đặt ở **cả hai** repo, **không ai gọi** | `trip` |
| <a id="g-25"></a>**G-25** | Không có metric (Prometheus), không có tracing, không có `/readyz`. `/healthz` không kiểm tra kết nối DB | `platform` |
| <a id="g-26"></a>**G-26** | `drivers.national_id` / `driver_license` / `vehicle_reg_no` lưu **plaintext** trong CSDL. Migration đã ghi chú *"cân nhắc mã hoá ở tầng ứng dụng"* — chưa làm. NĐ 13/2023 | `migrations` |
| <a id="g-27"></a>**G-27** | *(mới, phát hiện khi chạy thật ở GĐ 0)* `CanAcceptTrip` trả `driver_busy` với thông báo **"Bạn đang trong một chuyến khác."** cho **mọi** trạng thái khác `IDLE` — kể cả `OFFLINE`. Bảng điều khiển vì thế hiển thị "đang trong một chuyến khác" cho tài xế chỉ đơn giản là chưa bật app. Cần tách mã riêng cho `OFFLINE` | [`driver/domain.go`](../godrive/internal/driver/domain.go) |

---

---

<a id="loi-moi"></a>
## 5.6 Lỗi mới phát hiện trong lúc kiểm thử GĐ 1

Năm lỗi dưới đây **không nằm trong bản đối chiếu ban đầu**. Chúng lộ ra khi viết test đồng thời và
test biên — đúng loại lỗi mà đọc code không thấy được. Tất cả đã sửa.

<a id="g-28"></a>
### G-28 · ✅ Khoá idempotency kẹt 24 giờ sau một lần thất bại · **nghiêm trọng**

`trip.Create` giữ khoá idempotency **trước** khi lấy báo giá, nhưng không nhả ra khi thất bại:

```
Lần 1: Idempotency-Key=K, báo giá hết hạn  →  lỗi quote_expired, khoá K VẪN BỊ GIỮ
Lần 2: Idempotency-Key=K, báo giá mới       →  request_in_flight
Lần 3..n: y hệt, suốt 24 giờ
```

⇒ Khách gặp một lỗi tạm thời rồi **không đặt xe lại được nữa** với cùng khoá đó.
Trớ trêu ở chỗ đây chính là tình huống mà idempotency sinh ra để phục vụ: app mobile retry trên mạng
4G chập chờn (spec §0 quy tắc 7).

**Sửa:** thêm `idem.Store.Release`; `trip.Create` nhả khoá trên **mọi** đường thất bại qua `defer`.
`Release` cố ý **không** xoá khoá đã `Complete` — kết quả của một thao tác đã thành công không được
mất chỉ vì có ai gọi `Release` nhầm.
**Verify:** `TestIdempotencyKeyReleasedOnFailure`.

<a id="g-29"></a>
### G-29 · ✅ Cuộc đua dữ liệu trong `pkg/idem` · **nghiêm trọng**

`Reserve` trả **con trỏ `*Record` nội bộ** ra ngoài khoá, trong khi `Complete` ghi vào chính trường
`Response` của bản ghi đó:

```
goroutine A: Reserve(K) -> *rec        ... đọc rec.Response  (NGOÀI khoá)
goroutine B: Complete(K, tripID)       ... ghi rec.Response  (TRONG khoá)
```

Đây là lỗi **có sẵn từ trước**, không phải do thay đổi ở GĐ 1 — `go test -race` chỉ bắt được nó khi
tôi thêm test 10 goroutine cùng retry một khoá. Hai thiết bị cùng retry một `Idempotency-Key` là
chuyện thường ngày trên mạng chập chờn, nên đây không phải tình huống hiếm.

**Sửa:** `Reserve` trả **bản sao** (`copyRecord`), sao chép cả nội dung slice.
**Verify:** `TestConcurrentCreateSameKeyCreatesOneTrip` dưới `-race -count=5`.

<a id="g-30"></a>
### G-30 · ✅ `matching.MemoryStore` dùng đồng hồ khác Engine

`Engine` đặt `Offer.ExpiresAt` theo `clock.Clock` tiêm vào, còn `MemoryStore.PendingForDriver` và
`ClaimTrip` lọc theo `time.Now()`. Hai đồng hồ khác nhau ⇒ **lời mời vừa tạo đã bị coi là hết hạn**.

Triệu chứng phụ thuộc vào giờ chạy: cùng một test xanh lúc 09:00 UTC và đỏ lúc 10:03 UTC, vì đồng hồ
giả đặt ở 10:00 UTC lúc thì ở tương lai lúc thì ở quá khứ so với đồng hồ thật. Đúng loại lỗi tốn cả
buổi để lần ra nếu gặp trong CI.

**Sửa:** `NewMemoryStore(clk)` — Store dùng chung đồng hồ với Engine.
Đây là phần còn sót của [G-17](#g-17): GĐ 0 đã tiêm clock vào `location` nhưng bỏ quên `matching`.

<a id="g-31"></a>
### G-31 · ✅ Sổ cái nhận bút toán không có chủ

`Transaction.Validate()` kiểm tra tổng bằng 0 và tối thiểu 2 vế, nhưng **không** kiểm tra
`account_id` rỗng. Một bút toán vô chủ nằm trong tổng doanh thu mà không thuộc ví của ai
⇒ **không bao giờ đối soát được**, và vì bảng chỉ INSERT nên không sửa lại được ngoài ghi bút toán đảo.

**Sửa ở đúng chỗ chặn duy nhất** — `Validate()`, nơi mọi `Ledger.Post` đều đi qua, nên nó bao phủ cả
sáu bút toán hiện có lẫn mọi bút toán viết sau này. Thêm cả kiểm tra `TxID` rỗng (thiếu mã thì
idempotency vô hiệu). Kèm `CHECK` ở tầng CSDL (migration `0004`) theo đúng triết lý hai lớp phòng thủ
của repo cho phần tiền.
**Verify:** `TestPostCancelFeeRejectsEmptyAccounts`; đã thử `INSERT` trực tiếp vào Postgres → bị `CHECK` chặn.

<a id="g-32"></a>
### G-32 · ✅ `pkg/idem` không bao giờ dọn khoá quá hạn

Cùng loại rò rỉ với [G-15](#g-15): `Reserve` chỉ dọn đúng khoá nó chạm tới, nên khoá không được hỏi
lại nằm lại vĩnh viễn. Với TTL 24 giờ và **mỗi chuyến một khoá**, đây là rò rỉ tăng đều theo lưu lượng.

**Sửa:** quét định kỳ (tối đa 1 phút/lần) giống `httpx.RateLimit`.

---

## 5.4 Chỗ **tài liệu spec** đã lệch khỏi code (cần sửa spec)

| Spec nói | Code thật | Sửa ở đâu |
|---|---|---|
| *"~5.650 dòng / 61 files"* | **5.961 dòng mã (không tính test) / 67 file `.go`** | §header |
| *"pass toàn bộ **24** test"* | **77 test** (29 khi đối chiếu lần đầu; GĐ 0 thêm 14; GĐ 1 thêm 34) | §11 |
| §8.0 *"`drivers.account_id` là FK → `/v1/drivers/register` trả `driver_create_failed`"* | Đúng, **nhưng thiếu**: `trips.rider_id` cũng là FK tới `accounts` ⇒ `POST /v1/trips` cũng hỏng. ✅ Cả hai đã sửa ở GĐ 0 — mục §8.0 nay có thể **xoá khỏi spec** | §8.0 |
| §8 chỉ nêu `identity.MemoryRepo` cần thay | 6 store luôn là bộ nhớ kể cả ở chế độ Postgres. ✅ `identity` và `wallet` đã xong; còn **4**: `matching`, `location`, `pricing`, `idem` | §8 nhóm B |
| §3.2 *"`WeightIdle` × idle_giây"* | Code dùng **độ cũ của ping**, không phải thời gian rảnh | §3.2 |
| §4.4 *"phí huỷ ghi có cho tài xế qua `wallet.CancelFee`"* | ✅ đã nối ở GĐ 1 qua `wallet.Service.PostCancelFee` + consumer `trip.cancelled` | §4.4 |
| §5.2 *"3 loại cờ gian lận"* | ✅ **đã đủ 3** từ GĐ 0. `SPEED_OUTLIER` gắn cờ theo tốc độ **tự khai** nhưng **vẫn nhận ping** — khác `TELEPORT` (suy ra từ hai vị trí liên tiếp, bằng chứng chắc chắn nên từ chối). Spec nên ghi rõ khác biệt này | §5.2 |
| §6 *"Outbox — sự kiện ghi cùng transaction, relay publish sau"* | Outbox **chưa nối vào bất kỳ luồng nào** | §6 |
| §0.6 *"không dùng float cho tiền, kể cả biến tạm"* | ✅ đã sửa ở GĐ 1. **Spec nên bổ sung**: `SurgeProvider` trả permille `int64`, không phải `float64` — chữ ký này là một phần của nguyên tắc | §0.6 + §3.4 |
| §3.4 bậc thang surge ghi bằng số thập phân (`4→2.0`) | Code nay dùng **permille số nguyên** (`4→2000`); so sánh ngưỡng cũng bằng số nguyên | §3.4 |
| §5c *"Nhật ký thao tác admin — chưa làm"* | ✅ đã làm ở GĐ 1 (`admin_audit_log` + `GET /v1/admin/audit`) | §5c |
| §1 sơ đồ vẽ `worker` như tiến trình độc lập | `cmd/worker` dùng **bus in-process riêng** ⇒ không nhận được sự kiện từ `cmd/api` | §1 + §6 |

---

## 5.5 Những chỗ code làm **tốt hơn** spec — cần giữ

1. **Adapter Port của `admin` đặt ở tầng lắp ráp** ([`app/admin.go`](../godrive/internal/app/admin.go))
   với kiểm tra tại thời điểm biên dịch `var _ admin.LocationPort = adminLocation{}`.
   Thêm cả một module `admin` mà **không sửa một dòng nào** trong `driver`/`trip`/`wallet` — đây là bằng chứng
   sống rằng quy ước Port một chiều thực sự hoạt động.

2. **`admin.driverRow` gọi thẳng `Driver.CanAcceptTrip` để lấy `BlockedReason`**
   thay vì tự viết lại điều kiện chặn. Một nguồn sự thật duy nhất, không bao giờ lệch pha.
   Đây là mẫu đúng để [T-03](07-todo.md#t-03) noi theo.

3. **`admin.LiveMap` trả cung + cầu trong một lời gọi**, cùng bán kính, cùng thời điểm —
   nếu để giao diện gọi hai endpoint rồi tự ghép, hai tập sẽ lệch thời điểm và câu trả lời
   *"chỗ nào có khách chờ mà không có tài xế"* sẽ sai.

4. **`httpx.Decode` bật `DisallowUnknownFields`** — trường lạ báo lỗi ngay thay vì âm thầm bỏ qua.
   Bắt được lỗi gõ nhầm tên trường ở client trước khi lên production.

5. **`migrations-nogis/` sinh tự động** bằng [`scripts/gen-nogis.py`](../godrive/scripts/gen-nogis.py)
   với dòng cảnh báo *"đừng sửa tay"* ngay đầu file — hai schema không thể trôi khỏi nhau.

6. **Thông báo lỗi đăng nhập admin giống hệt nhau ở mọi trường hợp** — không lộ số nào là admin,
   và chặn **trước khi** gửi OTP nên không tốn tin nhắn.

7. **`errs.Fail` che message của lỗi `internal`**, chỉ trả `"Đã có lỗi xảy ra"` + `trace_id`.
   Không rò rỉ chi tiết nội bộ ra client.
