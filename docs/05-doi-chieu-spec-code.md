# 05 — Đối chiếu spec ↔ code

> Đối chiếu ngày **2026-08-24**. Mọi kết luận **đã xác minh bằng cách đọc code và chạy thật**
> (`go build` / `go vet` / `go test -race` / gọi API trên Postgres) — không có mục nào suy đoán.
>
> **Cập nhật sau Giai đoạn 0, 1 và 2.** **20/26 gap ban đầu đã đóng**, cộng thêm **6 lỗi mới**
> chỉ lộ ra trong lúc kiểm thử ([§5.6](#loi-moi)) — trong đó có hai lỗi nghiêm trọng mà lần đối
> chiếu đầu **không hề nhìn thấy**. Phần còn lại ([§5.7](#con-lai)) chủ yếu là thay hạ tầng.
>
> Kiểm chứng: Postgres 18.4 + PostGIS 3.6.3 · **84 test in-memory / 93 test có Postgres** ·
> `-race -count=6` sạch · 9 bất biến kiểm trực tiếp trên CSDL sau khi chạy đầu-cuối qua HTTP.
>
> Ký hiệu: 🟢 đạt · 🟡 còn hở · 🔴 chưa làm · ✅ **đã sửa trong GĐ 0–2**

---

## 5.1 Điểm số tổng quan

| Vùng | Trạng thái sau GĐ 0–2 | Còn hở |
|---|---|---|
| Kiến trúc & ranh giới module | 🟢 Port một chiều sạch; thêm 4 Port mới (`matching.WalletPort`, `driver.BalanceReader`, `driver.TripPort`, `trip.TxEnqueuer`) vẫn không phá quy ước | — |
| Máy trạng thái chuyến | ✅ 🟢 trip + event + **outbox** cùng một transaction | — |
| Chống ghép trùng | ✅ 🟢 3 lớp: `ClaimTrip` (Postgres nguyên tử) → CAS `Reserve` → **unique index đã kích hoạt** | Redis `SET NX` để bỏ tải khỏi Postgres |
| Sổ cái kép | ✅ 🟢 bền ở Postgres; cân bằng cưỡng chế ở **cả hai tầng**; idempotency chốt bằng PRIMARY KEY | — |
| Tính giá | ✅ 🟢 **toàn số nguyên**; 17 test; phủ 81%; surge phản ứng cầu thật | biểu giá cứng một thành phố |
| Chấm điểm ghép chuyến | ✅ 🟢 **5/5 đầu vào sống**; làm mượt Bayes chống đói chuyến cho tài xế mới | trọng số chưa hiệu chỉnh bằng dữ liệu thật (spec §7) |
| Giao sự kiện | ✅ 🟢 **Transactional Outbox — at-least-once**, có DLQ và đếm tồn đọng | NATS JetStream cho nhiều tiến trình |
| Chế độ Postgres | ✅ 🟡 **6/9 store** có repo; chạy trọn vòng đời qua HTTP | `location.index`, `pricing.quotes`, `idem.keys` còn ở bộ nhớ ⇒ vẫn chỉ 1 bản sao |
| Độ bền dữ liệu | ✅ 🟡 tài khoản, tài xế, chuyến, sổ cái, offer, khoá chuyến, nhật ký admin đều bền | báo giá + chỉ mục vị trí mất khi restart |
| Vận hành / quan sát | ✅ 🟡 `request_id`, cảnh báo admin, **nhật ký thao tác**, đếm outbox tồn đọng | không metric, không tracing, `/healthz` chưa kiểm DB |
| Kiểm thử | ✅ 🟡 **93 test**, `-race -count=6` sạch, phủ ~57% | `identity`/`driver`/`admin` chưa có test riêng |

**Tóm tắt lần đối chiếu đầu:** *bộ khung kiến trúc đã đúng và đáng giữ; phần chưa xong là **nối dây** —
nhiều thành phần đã viết xong nhưng chưa được cắm vào luồng chạy.*

**Sau GĐ 0–2:** dây đã nối xong, và phần **tiền** đã đúng: sổ cái bền, cân bằng cưỡng chế ở hai
tầng, cổng chặn công nợ hoạt động thật, phí huỷ được ghi sổ, đường tính giá hoàn toàn bằng số
nguyên. Phần **ghép chuyến** cũng đã sống: cả năm thành phần chấm điểm đều thay đổi theo hành vi
thật thay vì đứng yên ở giá trị mặc định.

> **Một bài học đáng ghi lại.** Bốn trong sáu lỗi ở [§5.6](#loi-moi) là **cuộc đua hoặc lỗi thứ tự**
> — loại lỗi mà đọc code không thấy được. Chúng chỉ lộ ra khi có test chạy lặp dưới `-race`.
> Riêng lỗi tài xế kẹt `ON_TRIP` xảy ra ở **10% số chuyến** mà không sinh một dòng log lỗi nào.

---

## 5.2 Đối chiếu Acceptance Criteria của spec

**25/25 AC đạt** (lần đối chiếu đầu: 15/25).

### §3 — Matching

| AC | | Bằng chứng |
|---|:---:|---|
| Không tài xế nào bị ghép 2 chuyến, test song song dưới `-race` | 🟢 | `TestOnlyOneDriverWinsTrip`; thêm `TestPostgresClaimTripIsAtomic` (16 goroutine tranh một chuyến) và `TestPostgresOfferUniqueIndexBlocksDoubleAccept` (chốt chặn CSDL) |
| Offer hết hạn + nới bán kính → `EXPIRED` sau `MaxRounds` | ✅ 🟢 | `TestOfferExpiryExpandsRadiusThenExpires`, `TestDispatchWidensRadiusEachRound` |
| Tài xế nợ / KYC chưa duyệt không lọt candidate list | ✅ 🟢 | `TestCashDebtBlocksDriverEndToEnd` — chạy qua **luồng thật**: 21 chuyến tiền mặt → nợ 210k → dispatcher gửi 0 lời mời → nạp tiền → nhận lại được ngay |
| Surge không vượt 2.0, test boundary + clamp ở `Estimate` | ✅ 🟢 | `TestSurgeStaircase` (9 mốc), `TestEstimateClampsRogueSurgeProvider` (provider trả 1.000.000‰ vẫn bị chặn ở trần) |
| Chấm điểm tất định | ✅ 🟢 | `TestScoringDeterministic` — 30 vòng, thứ tự không đổi |

### §4 — Pricing & Wallet

| AC | | Bằng chứng |
|---|:---:|---|
| `computeBase` test: cự ly ngắn, dài, surge 2.0, giờ đêm, làm tròn nghìn | ✅ 🟢 | 17 test; `computeBase` và `isNight` phủ **100%** |
| Không tạo được `Transaction` lệch mà `Validate()` bỏ qua | 🟢 | `TestUnbalancedRejected` + **CHECK ở CSDL** (migration 0004) |
| `SettleTrip` gọi 2 lần chỉ ghi 1 lần | ✅ 🟢 | `TestSettleTripIsIdempotentAcrossRetries` (5 lần) và `TestPostgresLedgerIdempotentUnderConcurrency` (20 goroutine song song → đúng 4 bút toán) |
| Tài xế nợ vượt hạn mức bị `CanAcceptTrip` chặn | ✅ 🟢 | Đã đúng ở **cả tầng tích hợp**, không chỉ tầng domain |
| **Không có phép chia float nào trên đường đi của tiền** | ✅ 🟢 | `TestComputeBaseRoundsHalfUpNotTruncate`, `TestFloatDriftChangesFareByAThousand`, `TestSurgeMultiplyIsExact` |

### §5 — Trip & Location

| AC | | Bằng chứng |
|---|:---:|---|
| Transition không hợp lệ bị reject với mã lỗi rõ ràng | 🟢 | `TestTransitionGraph`, `TestTerminalStates` |
| `trip_events` không bao giờ bị update/delete | 🟢 | Không có câu SQL nào UPDATE/DELETE. **Cấp quyền DB** vẫn là việc triển khai (xem [08 §8.8](08-van-hanh.md)) |
| Trip + event luôn cùng transaction | ✅ 🟢 | Nay là trip + event + **outbox** cùng một transaction; `TestPostgresOutboxDeliversEventsAtLeastOnce` |
| `Create` cùng `Idempotency-Key` hai lần chỉ tạo một chuyến | ✅ 🟢 | `TestIdempotentCreateReturnsSameTrip`, `TestConcurrentCreateSameKeyCreatesOneTrip` (10 goroutine) |
| Ping `Mocked=true` bị gắn cờ và không lọt chỉ mục | ✅ 🟢 | `TestMockedPingRejectedAndFlagged` |

### §5c — Admin

Chín AC đã đạt từ trước. AC cuối — **nhật ký thao tác admin** — nay đã đạt:
`TestAdminReviewKYCChangesState` kiểm nội dung nhật ký, `TestPostgresAuditLogRecordsKYCReview`
kiểm nó nằm trong CSDL với đủ người thực hiện, thời điểm, trạng thái trước và sau.

---

## 5.3 Danh mục gap — trạng thái sau GĐ 0–2

Chi tiết chẩn đoán từng gap giữ nguyên trong lịch sử git của tài liệu này; bảng dưới là trạng thái hiện tại.

### ✅ Đã đóng (20)

| Gap | Nội dung | Sửa thế nào | Kiểm chứng |
|---|---|---|---|
| <a id="g-01"></a>**G-01** | `identity` không có repo Postgres ⇒ cả đăng ký tài xế lẫn đặt chuyến hỏng | `identity.PostgresRepo` + bảng `otp_challenges` + job dọn | `TestPostgresFullTripLifecycle`, `TestPostgresOTPChallengeRoundTrip` |
| <a id="g-02"></a>**G-02** | Sổ cái chỉ có bản bộ nhớ — restart là mất sạch tiền | `wallet.PostgresLedger`; idempotency chốt bằng PK `ledger_transactions` | `TestPostgresLedgerSurvivesRestart` |
| <a id="g-03"></a>**G-03** | `WalletBalance` không ai cập nhật ⇒ cổng chặn nợ chưa từng kích hoạt | `matching` và `driver.Reserve` đọc **thẳng sổ cái**; cột cache đồng bộ qua `wallet.balance_changed` | `TestCashDebtBlocksDriverEndToEnd` |
| <a id="g-04"></a>**G-04** | Chỉ số tài xế đóng băng ⇒ 3/5 thành phần chấm điểm là hằng số | Lưu **số đếm** thay vì tỉ lệ; consumer sự kiện cộng dồn; làm mượt Bayes | `TestPoorAcceptanceRateSinksDriverInRanking` |
| <a id="g-05"></a>**G-05** | Phí huỷ được tính nhưng không ai ghi sổ | `wallet.PostCancelFee` + consumer `trip.cancelled`; tính phí **một lần** | `TestLateCancelCreditsDriver`, `TestEarlyCancelIsFree` |
| <a id="g-06"></a>**G-06** | Outbox viết xong nhưng chưa nối ⇒ sự kiện **at-most-once** | `outbox.PostgresStore`; `trip.Save` ghi outbox **cùng transaction**; relay có DLQ | `TestPostgresOutboxDeliversEventsAtLeastOnce` |
| <a id="g-07"></a>**G-07** | Surge vĩnh viễn 1.0 vì `RecordRequest` không ai gọi | Nối vào `trip.requested` (**cầu thật**, không phải lượt xem giá) | `TestSurgeRisesWithRealDemand` |
| <a id="g-08"></a>**G-08** | Giấy tờ tài xế ghi thiếu và không đọc lại được | Bổ sung cột `insurance_*`; `scan()` đọc đủ; validate định dạng ngày | `TestPostgresFullTripLifecycle` |
| <a id="g-09"></a>**G-09** | Ba phép float trên đường đi của tiền | `money.MulDiv`; surge chuyển sang **permille (int64)** | 3 test riêng (xem §5.2) |
| <a id="g-10"></a>**G-10** | `IdleSeconds` đo độ cũ của ping ⇒ **thưởng cho tài xế mạng kém** | Thêm `drivers.idle_since`, đặt/xoá ngay trong câu CAS đổi trạng thái | `TestIdleSecondsMeasuresIdleNotPingAge` |
| <a id="g-12"></a>**G-12** | Tài xế không có cách nào xem ví hay trả nợ | `GET /wallet`, `GET /statement`, `POST /topup` (**chỉ dev**) | e2e HTTP |
| <a id="g-13"></a>**G-13** | Không có nhật ký thao tác admin | Bảng `admin_audit_log` (chỉ thêm mới) + `GET /v1/admin/audit` | `TestPostgresAuditLogRecordsKYCReview` |
| <a id="g-14"></a>**G-14** | Goroutine dispatch không có `recover` ⇒ panic giết cả tiến trình | `platform/safego`; cleanup đẩy chuyến về `EXPIRED` | `TestDispatchPanicDoesNotKillProcess` |
| <a id="g-15"></a>**G-15** | Rate limit không bao giờ dọn bucket | Quét định kỳ + clock tiêm được | `TestRateLimitSweepsIdleBuckets` |
| <a id="g-16"></a>**G-16** | `MemoryRepo.Save` không tăng `Version` như bản Postgres | Đồng bộ hành vi hai repo | test hiện có |
| <a id="g-17"></a>**G-17** | `location` dùng `time.Now()` thay clock tiêm được | Tiêm `clock.Clock`; đồng thời gán cờ `SPEED_OUTLIER` vốn là hằng số chết | `TestStaleDriverDropsOutOfIndex` |
| <a id="g-19"></a>**G-19** | `matching` không có repo Postgres ⇒ unique index chưa bảo vệ gì | `matching.PostgresStore` + bảng `trip_claims` | `TestPostgresOfferUniqueIndexBlocksDoubleAccept` |
| <a id="g-23"></a>**G-23** | Trường chết trong `pricing` | `SurgePermille` thay `SurgeMult` trên đường tiền | — |
| <a id="g-24"></a>**G-24** | `ActiveByDriver` khai báo và cài đặt nhưng không ai gọi | Dùng cho đường thoát trạng thái kẹt của tài xế | `TestStuckDriverCanRecoverByGoingOnline` |
| **AC §5c** | Nhật ký thao tác admin | như G-13 | — |

<a id="con-lai"></a>
### 🟡 Còn lại (6) — chủ yếu là thay hạ tầng

| Gap | Nội dung | Vì sao chưa làm |
|---|---|---|
| <a id="g-11"></a>**G-11** | Không có push; tài xế phải poll `GET /v1/offers` | Cần FCM/APNs + MQTT (EMQX) — **GĐ 3** |
| <a id="g-18"></a>**G-18** | `SUSPENDED` chỉ được đọc, không có đường nào đặt nó | Cần chính sách ngưỡng gian lận — **GĐ 4**, và là quyết định vận hành chứ không phải kỹ thuật |
| <a id="g-20"></a>**G-20** | `candidates()` gọi `drivers.Get()` cho từng ứng viên (N+1) | Đi cùng OSRM `/table` — **GĐ 3** |
| <a id="g-21"></a>**G-21** | JWT không thu hồi được; đăng xuất chỉ xoá cookie | Cần refresh token + danh sách chặn Redis — **GĐ 4** |
| <a id="g-22"></a>**G-22** | Rate limit OTP theo IP, chưa theo số điện thoại | Cần Redis để giới hạn toàn cụm — **GĐ 3** |
| <a id="g-25"></a>**G-25** | Không metric, không tracing, `/healthz` không kiểm DB | **GĐ 3** |
| <a id="g-26"></a>**G-26** | CCCD/GPLX lưu plaintext | Cần quyết định về quản lý khoá — **GĐ 4** |

| <a id="g-27"></a>**G-27** | Mã lỗi `driver_busy` dùng chung cho cả tài xế OFFLINE lẫn tài xế đang chở khách | Chỉ là thông báo gây hiểu nhầm ở bảng điều khiển — **T-28**, `P2` |

> **G-19 lưu ý:** ba store còn ở bộ nhớ (`location.index`, `pricing.quotes`, `idem.keys`) vẫn khiến
> hệ thống **chỉ chạy đúng với một bản sao**. `app.New()` log cảnh báo nêu đích danh chúng lúc khởi động.

---

<a id="loi-moi"></a>
## 5.4 Lỗi mới phát hiện trong lúc kiểm thử

Sáu lỗi dưới đây **không nằm trong bản đối chiếu ban đầu**. Chúng lộ ra khi viết test đồng thời và
test biên — đúng loại lỗi mà đọc code không thấy được. Tất cả đã sửa.

**Bốn trong sáu là cuộc đua hoặc lỗi thứ tự.** Đây là lý do thực tế để chạy `go test -race -count=N`
chứ không chỉ `go test`: hai lỗi nghiêm trọng nhất (G-33, G-34) chỉ xuất hiện ở một phần nhỏ số lần chạy.

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

<a id="g-33"></a>
### G-33 · ✅ Tài xế kẹt vĩnh viễn ở `ON_TRIP` sau ~10% số chuyến · **nghiêm trọng**

Trạng thái tài xế được suy từ **sự kiện nào vừa tới**, mà bus phát bất đồng bộ — mỗi handler một
goroutine, không bảo đảm thứ tự:

```
Trình tự publish:  trip.started  →  trip.completed
Trình tự chạy:     handler(completed) đặt IDLE
                   handler(started)   đặt ON_TRIP     ← tới sau, ghi đè
Kết quả:           tài xế ON_TRIP nhưng không có chuyến nào
```

Tài xế đó **không nhận được lời mời nào nữa**, và **không có một dòng log lỗi nào** được ghi.
Đo bằng `TestDriverStatusAfterBackToBackStartComplete`: **2/20 lần** (~10%).

Ở production, khoảng cách giữa `start` và `complete` là hàng chục phút nên tỉ lệ thấp hơn nhiều —
nhưng hậu quả thì y hệt, và càng khó phát hiện vì hiếm.

**Sửa hai lớp:**
1. `app.syncDriverStatus` đọc **trạng thái hiện tại của chuyến** rồi mới đặt trạng thái tài xế.
   Phép gán trở nên **hội tụ**: sự kiện đến muộn cũng chỉ đặt lại đúng giá trị đang cần.
2. `GoOnline` cho phép tài xế **tự thoát** khi thực tế không còn chuyến nào chạy — dùng
   `trip.ActiveByDriver`, phương thức đã cài đặt từ đầu mà chưa ai gọi ([G-24](#)).
   Không có lối này, cách duy nhất để họ làm việc lại là gọi tổng đài nhờ sửa tay trong CSDL.

<a id="g-35"></a>
### G-35 · ✅ Mọi thông số vận hành là hằng số biên dịch cứng

Spec §7 liệt kê một loạt con số *"KHÔNG được tự quyết định"* — biểu giá, trọng số ghép chuyến, bậc
thang surge, hạn mức công nợ, thuế khấu trừ. Code phản ánh đúng tinh thần đó bằng cách để chúng
thành hằng số trong `internal/pricing`, `internal/matching`, `internal/wallet`.

Nhưng "không được tự quyết định" nói về **ai có quyền chốt con số**, không phải về **cách con số đi
vào hệ thống**. Đóng chúng thành hằng số làm cả hai việc cùng lúc, và việc thứ hai gây hại: khi
người có thẩm quyền cuối cùng cũng chốt được biểu giá, việc áp dụng nó vẫn cần một lập trình viên
sửa code, một lần build, một lần triển khai. Trên thực tế nghĩa là các con số đó **không bao giờ
được đổi** — hệ thống chạy mãi bằng giá trị mà ai đó đặt tạm lúc viết code.

**Sửa:** đưa 50 ô vào bảng `settings`, sửa từ giao diện quản trị, kèm ba lớp bảo vệ — ngưỡng an toàn
cứng trong code, khoá lạc quan theo `version`, bắt buộc ghi lý do ở tầng API. Xem
[T-29](07-todo.md#t-29) và [08 §8.11](08-van-hanh.md).

Điều quan trọng là **ngưỡng an toàn vẫn nằm trong code và không sửa được từ giao diện**. Đó mới là
chỗ tinh thần của spec §7 thật sự thuộc về: vận hành đổi được biểu giá, nhưng không ai — kể cả
admin — đẩy được chiết khấu lên 60% hay thuế lên 50%. Câu hỏi mỗi ngưỡng trả lời là *"giá trị nào,
nếu gõ nhầm, sẽ gây thiệt hại không sửa được bằng cách sửa lại cấu hình?"*

> **Ba lỗi phát sinh trong lúc làm, cả ba đều là lỗi âm thầm** — hệ thống vẫn chạy, không có báo
> động nào kêu, chỉ có tiền chảy sai chỗ. Chúng được ghi lại đầy đủ ở [T-29](07-todo.md#t-29) vì
> chúng cùng một họ với 8 lỗi nghiêm trọng khác của dự án: **không lỗi nào lộ ra khi đọc code.**

---

<a id="g-34"></a>
### G-34 · ✅ Dùng sai `sync.WaitGroup` trong event bus — tắt êm có thể mất sự kiện

`Publish` gọi `wg.Add(1)` **ngoài khoá**, trong khi `Close` gọi `wg.Wait()`. Đây đúng là trường hợp
tài liệu chuẩn của Go cảnh báo: *một `Add` làm bộ đếm từ 0 đi lên mà chạy song song với `Wait` thì
`Wait` có thể trả về sớm.*

Hệ quả thật: **tắt êm báo "đã dừng" trong khi sự kiện vẫn đang được xếp lịch** — và những sự kiện
đó biến mất không dấu vết. Với `trip.completed` thì đó là một chuyến không được ghi sổ.

**Sửa:** `wg.Add` nằm trong khoá cùng với việc kiểm cờ `closed`. Khi đang tắt, `Publish` **chạy
handler đồng bộ** thay vì spawn goroutine.

> Lần sửa đầu tôi cho `Publish` **trả lỗi** khi bus đã đóng. Cách đó tạo lỗi mới: handler hoàn toàn
> có thể tự publish tiếp (ghi sổ xong thì báo số dư đổi), và từ chối những sự kiện đó khiến chính
> handler đang chạy trả lỗi rồi **bỏ dở phần việc còn lại**. Chạy đồng bộ giữ được ngữ nghĩa "giao đủ"
> mà vẫn không đụng tới `WaitGroup`.

**Sửa kèm:** `wallet.SettleTrip` từng trả lỗi của bước *phát thông báo* như thể là lỗi *ghi sổ*.
Tiền đã vào sổ rồi thì người gọi không được hiểu là thất bại và bỏ dở các bước sau.

---

## 5.5 Chỗ **tài liệu spec** đã lệch khỏi code (cần sửa spec)

| Spec nói | Code thật | Sửa ở đâu |
|---|---|---|
| *"~5.650 dòng / 61 files"* | **8.076 dòng mã (không tính test) / 84 file `.go`** | §header |
| *"pass toàn bộ **24** test"* | **84 test in-memory / 93 test có Postgres** (29 khi đối chiếu lần đầu) | §11 |
| *"1 module Go, zero external dependency"* | Vẫn đúng — 1 phụ thuộc duy nhất là `pgx/v5` để đăng ký driver `database/sql` | §header |
| §8.0 *"`drivers.account_id` là FK → `/v1/drivers/register` trả `driver_create_failed`"* | Đúng, **nhưng thiếu**: `trips.rider_id` cũng là FK tới `accounts` ⇒ `POST /v1/trips` cũng hỏng. ✅ Cả hai đã sửa ở GĐ 0 — mục §8.0 nay có thể **xoá khỏi spec** | §8.0 |
| §8 chỉ nêu `identity.MemoryRepo` cần thay | 6 store luôn là bộ nhớ kể cả ở chế độ Postgres. ✅ `identity`, `wallet`, `matching`, `admin_audit` đã xong; còn **3**: `location`, `pricing`, `idem` | §8 nhóm B |
| §3.2 *"`WeightIdle` × idle_giây"* | ✅ đã sửa ở GĐ 2. Bản cũ dùng độ cũ của ping, tức **thưởng cho tài xế mạng kém**. Nay có `drivers.idle_since` | §3.2 |
| §3.2 không nói gì về tài xế mới | Code nay **làm mượt Bayes** (10 lời mời ảo ở mức 0.8, 5 lượt đánh giá ảo ở mức 5.0). Không có nó, một tài xế bỏ lỡ đúng một lời mời sẽ có acceptance = 0 và **không bao giờ được mời lần thứ hai để gỡ lại**. Spec nên ghi cơ chế này | §3.2 |
| §4.4 *"phí huỷ ghi có cho tài xế qua `wallet.CancelFee`"* | ✅ đã nối ở GĐ 1 qua `wallet.Service.PostCancelFee` + consumer `trip.cancelled` | §4.4 |
| §5.2 *"3 loại cờ gian lận"* | ✅ **đã đủ 3** từ GĐ 0. `SPEED_OUTLIER` gắn cờ theo tốc độ **tự khai** nhưng **vẫn nhận ping** — khác `TELEPORT` (suy ra từ hai vị trí liên tiếp, bằng chứng chắc chắn nên từ chối). Spec nên ghi rõ khác biệt này | §5.2 |
| §6 *"Outbox — sự kiện ghi cùng transaction, relay publish sau"* | ✅ đã nối ở GĐ 2. `Repository.Save` nay nhận thêm `msgs ...Message` và **trả về những sự kiện người gọi vẫn phải tự phát** — nhờ đó "ai phát sự kiện" là chi tiết của tầng lưu trữ, không phải một câu `if` rải trong Service. Spec nên mô tả chữ ký này | §6 |
| §0.6 *"không dùng float cho tiền, kể cả biến tạm"* | ✅ đã sửa ở GĐ 1. **Spec nên bổ sung**: `SurgeProvider` trả permille `int64`, không phải `float64` — chữ ký này là một phần của nguyên tắc | §0.6 + §3.4 |
| §3.4 bậc thang surge ghi bằng số thập phân (`4→2.0`) | Code nay dùng **permille số nguyên** (`4→2000`); so sánh ngưỡng cũng bằng số nguyên | §3.4 |
| §5c *"Nhật ký thao tác admin — chưa làm"* | ✅ đã làm ở GĐ 1 (`admin_audit_log` + `GET /v1/admin/audit`) | §5c |
| §1 sơ đồ vẽ `worker` như tiến trình độc lập | `cmd/worker` vẫn dùng **bus in-process riêng** ⇒ chưa nhận được sự kiện từ `cmd/api`. ✅ Đã bỏ cái relay `MemoryStore` rỗng vô nghĩa; relay thật do `StartWorkers` chạy. Tách tiến trình chỉ có nghĩa **sau khi** thay bus bằng NATS | §1 + §6 |
| §5.1 *"trip và event ghi trong CÙNG transaction"* | Nay là **trip + event + outbox** cùng một transaction | §5.1 |

---

## 5.6 Đính chính phân tích của lần đối chiếu đầu

Hai kết luận trong bản đối chiếu đầu tiên **sai**, và đều sai theo hướng xem nhẹ vấn đề. Ghi lại
ở đây vì chúng minh hoạ một điều: phân tích tĩnh mà không đo thì dễ tự trấn an.

### Đ-01 · Sai lệch float **không** bị làm tròn che đi

Bản đầu viết: *"Hiện tại `RoundTo(1000)` che mất sai số ở giá khách trả."* — **Sai.**

Đo thật trên dải 2–40 km với 5 mức surge:

| Đo | Kết quả |
|---|---|
| `computeBase` cho kết quả khác nhau | **29.199 / 38.001 cự ly (77%)** |
| **Tổng cước khách phải trả** khác nhau | **422 / 190.005 tổ hợp** |
| Mức lệch khi xảy ra | **đúng 1.000đ** |

Cơ chế: sai lệch vài đồng ở `computeBase` **vắt qua ranh giới làm tròn nghìn**. `base = 17.001`
làm tròn lên 18.000, còn `base = 17.000` giữ nguyên 17.000 — một đồng chênh lệch thành một nghìn
đồng trên hoá đơn.

Ví dụ cụ thể (đã thành test hồi quy `TestFloatDriftChangesFareByAThousand`):
`2.219m` với surge 1.7 → số nguyên cho **23.000đ**, bản float cũ cho **22.000đ**.

Nghĩa là G-09 không chỉ là "vi phạm nguyên tắc" — nó là **tính sai tiền của khách**.

### Đ-02 · Lần thử đầu để viết test cho lỗi này là một test rỗng

Test đầu tiên tôi viết cho G-09 tính lại kết quả bằng **chính đường code đang kiểm tra**, nên nó
xanh cả khi khôi phục lại phép float cũ. Chỉ khi so với một phép tính số nguyên **độc lập** và
quét cả dải giá trị thì mới tìm ra 422 tổ hợp lệch.

> Bài học chung: một test hồi quy chưa được nhìn thấy đỏ với lỗi cũ thì chưa phải là test hồi quy.
> Mọi lỗi ở [§5.4](#loi-moi) đều đã được kiểm ngược theo cách này — khôi phục lỗi, xác nhận test đỏ,
> rồi mới sửa lại.

---

## 5.7 Những chỗ code làm **tốt hơn** spec — cần giữ

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

**Bổ sung trong GĐ 0–2 — những quyết định nên giữ:**

8. **Lưu số đếm, không lưu tỉ lệ** (migration 0005). Tỉ lệ suy ra được từ số đếm, chiều ngược lại
   thì không; và cộng dồn số đếm là phép nguyên tử trong một câu `UPDATE`, còn đọc-sửa-ghi một tỉ lệ
   thì không. Ba cột tỉ lệ cũ bị **xoá hẳn** thay vì để lại với giá trị mặc định vĩnh viễn — câu
   truy vấn phân tích đầu tiên sẽ tin vào chúng.

9. **`Repository.Save` trả về "những sự kiện người gọi vẫn phải tự phát".** Nhờ đó việc *ai* phát
   sự kiện là chi tiết của tầng lưu trữ (Postgres → outbox; bộ nhớ → người gọi), không phải một
   câu `if cfg.InMemory()` rải khắp Service.

10. **Endpoint nạp ví chỉ tồn tại ở chế độ dev.** Một endpoint tự ghi có vào ví mà không có đối ứng
    tiền thật chính là máy in tiền; nó được đăng ký có điều kiện theo `cfg.DevAuth` chứ không chỉ
    được ghi chú "đừng dùng ở production".

11. **`ClaimTrip` trên Postgres gộp ba việc vào một câu**: giành khoá nếu trống, giành lại nếu khoá
    cũ hết hạn, giữ nguyên nếu người khác đang giữ — và **chính chủ gọi lại vẫn thắng**, để app
    mobile retry không bị biến thành "chuyến đã có người khác nhận".

12. **`UpdateWalletBalance` và `ApplyStats` cố ý KHÔNG tăng `version`.** `version` bảo vệ chuyển
    trạng thái; số dư và thống kê là giá trị suy ra. Nếu chúng cũng tăng `version` thì việc đồng bộ
    cache sẽ làm hỏng CAS của `Reserve` đang chạy song song.

**Bổ sung khi làm cấu hình động (GĐ 4):**

13. **Lược đồ biểu mẫu do backend phát ra.** Nhãn, đơn vị và ngưỡng hợp lệ là kiến thức nghiệp vụ và
    đã sống cạnh hàm `Validate`; để giao diện chép lại là bảo đảm hai bản sẽ trôi khỏi nhau. Hai test
    chốt cả hai chiều: không ô nào trên giao diện trỏ vào trường không tồn tại, và không trường nào
    sửa được mà lại vắng mặt trên giao diện.

14. **`Current()` không bao giờ trả lỗi.** Cấu hình nằm trên đường đi của *mọi* báo giá và *mọi* vòng
    dispatch. CSDL lỗi thì dùng ảnh chụp cũ (hoặc mặc định) và chạy tiếp — dừng phục vụ vì không đọc
    được cấu hình là biến một sự cố nhỏ thành sự cố lớn. Cùng lý do: một nhóm có giá trị hỏng thì lùi
    về mặc định, các nhóm khác vẫn nạp bình thường.

15. **`Reload` dựng lại từ mặc định, không gộp lên ảnh chụp cũ.** Nếu gộp thì một nhóm bị xoá khỏi
    CSDL vẫn sống mãi trong bộ nhớ — cấu hình "đang chạy" khác cấu hình lưu trữ mà không ai biết.

16. **Cấu hình trả về là bản sao sâu.** `Snapshot` trả theo giá trị, nhưng map và slice bên trong vẫn
    trỏ chung; không nhân bản thì bất kỳ ai cầm ảnh chụp cũng ghi ngược được vào cấu hình sống của cả
    hệ thống.
