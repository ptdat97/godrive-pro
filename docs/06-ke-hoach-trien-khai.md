# 06 — Kế hoạch triển khai

> **Nguyên tắc sắp xếp:** ưu tiên theo *"cái gì hỏng thì mất tiền hoặc mất niềm tin"*, không theo
> *"cái gì dễ làm"*. Sổ cái và ghép chuyến đứng trước mọi tính năng mới.
>
> Ước lượng công sức tính theo **1 kỹ sư backend toàn thời gian**. `~2n` = khoảng 2 ngày làm việc.

---

## Bản đồ giai đoạn

```
 GĐ 0        GĐ 1              GĐ 2              GĐ 3           GĐ 4          GĐ 5
Sửa nền   Bền dữ liệu     Đúng nghiệp vụ    Hạ tầng thật    Thương mại   Quy mô
~5n          ~12n              ~10n            ~15n            ~25n         (sau)
  │            │                 │                │              │
  ▼            ▼                 ▼                ▼              ▼
Chạy       Không mất        Ghép chuyến      Nhiều pod      Thu tiền     Tách
được       tiền khi         và tính giá      thật, MQTT,    thật được    service
Postgres   restart          đúng như spec    OSRM
```

**Cột mốc chặn:** không sang GĐ 4 (thu tiền thật) khi GĐ 1 chưa xong. Ghi sổ trong RAM
mà nhận tiền thật của khách là rủi ro pháp lý, không phải rủi ro kỹ thuật.

---

## Giai đoạn 0 — Sửa nền · ✅ **XONG** (2026-08-24)

**Mục tiêu:** chạy được đầu-cuối với Postgres. **Đã đạt.**

| Việc | Gap | Trạng thái |
|---|---|---|
| `identity.PostgresRepo` (bảng `accounts` + `otp_challenges`) + job dọn + nhánh chọn repo trong `app.New` | [G-01](05-doi-chieu-spec-code.md#g-01) | ✅ [T-01](07-todo.md#t-01) |
| `driver.PostgresRepo` lưu + đọc lại đủ `Documents`, thêm `insurance_no`/`insurance_until` | [G-08](05-doi-chieu-spec-code.md#g-08) | ✅ [T-08](07-todo.md#t-08) |
| `recover()` cho goroutine dispatch và goroutine handler của eventbus | [G-14](05-doi-chieu-spec-code.md#g-14) | ✅ [T-14](07-todo.md#t-14) |
| Dọn bucket rate limit | [G-15](05-doi-chieu-spec-code.md#g-15) | ✅ [T-15](07-todo.md#t-15) |
| Đồng bộ hành vi `Version` giữa `MemoryRepo` và `PostgresRepo` của `trip` | [G-16](05-doi-chieu-spec-code.md#g-16) | ✅ [T-16](07-todo.md#t-16) |
| Tiêm `clock.Clock` vào `location`; bổ sung cờ `SPEED_OUTLIER` | [G-17](05-doi-chieu-spec-code.md#g-17) | ✅ [T-17](07-todo.md#t-17) |
| Test tích hợp Postgres (bật bằng `TEST_DATABASE_URL`) | — | ✅ 3 test |

### Điều kiện hoàn thành

- [x] `DATABASE_URL=… make run` → chạy trọn: OTP → đăng ký tài xế → duyệt KYC → online → ping → báo giá → đặt chuyến → nhận → hoàn tất
- [x] `POST /v1/drivers/register` và `POST /v1/trips` đều trả 201 ở chế độ Postgres
- [x] `GET /v1/admin/drivers/{id}` trả về đủ trường giấy tờ
- [x] Panic trong `Dispatch` được log + chuyến chuyển `EXPIRED`, **tiến trình không chết**
- [x] `go test ./... -race` xanh ở **cả hai** chế độ (in-memory và Postgres)

**Kết quả đo được:** 29 → **43 test**; `location` và `httpx` từ 0 test lên lần lượt 6 và 3;
1 Acceptance Criteria của spec §5 chuyển từ 🔴 sang 🟢.

> **Còn nợ từ GĐ 0:** hệ thống chạy được với Postgres nhưng **5 store vẫn ở bộ nhớ**
> (`wallet.ledger`, `matching.offers`, `location.index`, `pricing.quotes`, `idem.keys`).
> `app.New` nay log cảnh báo liệt kê đúng danh sách này khi khởi động ở chế độ Postgres.
> **Vẫn chỉ được chạy 1 bản sao.** Sổ cái là món nợ nặng nhất — đó là việc đầu tiên của GĐ 1.

---

## Giai đoạn 1 — Bền dữ liệu & đúng tiền · ✅ **XONG** (2026-08-24)

**Mục tiêu:** không mất tiền khi restart; cổng chặn nợ thực sự hoạt động; phí huỷ được ghi sổ. **Đã đạt.**

| Việc | Gap | Trạng thái |
|---|---|---|
| `wallet.PostgresLedger` — một transaction ghi cả `ledger_transactions` (PK = idempotency) lẫn `ledger_entries` | [G-02](05-doi-chieu-spec-code.md#g-02) | ✅ [T-02](07-todo.md#t-02) |
| Cổng chặn nợ đọc số dư thật từ sổ cái ở **cả hai** chỗ (chấm điểm + `Reserve`); cache đồng bộ qua `wallet.balance_changed` | [G-03](05-doi-chieu-spec-code.md#g-03) | ✅ [T-03](07-todo.md#t-03) |
| Worker ghi sổ phí huỷ; `cancelFee` tính **một lần** | [G-05](05-doi-chieu-spec-code.md#g-05) | ✅ [T-05](07-todo.md#t-05) |
| Bỏ hết float trên đường tiền; `SurgeProvider` trả permille `int64`; thêm `money.MulDiv` | [G-09](05-doi-chieu-spec-code.md#g-09) | ✅ [T-09](07-todo.md#t-09) |
| Bộ test `pricing` theo AC §4.1 của spec | AC | ✅ [T-18](07-todo.md#t-18) |
| API ví cho tài xế: số dư, công nợ, sao kê, nạp tiền (nạp **chỉ ở dev**) | [G-12](05-doi-chieu-spec-code.md#g-12) | ✅ [T-12](07-todo.md#t-12) |
| Nhật ký thao tác admin (`admin_audit_log` + `GET /v1/admin/audit`) | [G-13](05-doi-chieu-spec-code.md#g-13) | ✅ [T-13](07-todo.md#t-13) |

### Điều kiện hoàn thành

- [x] Restart tiến trình → số dư ví và công nợ **không đổi một đồng** (`TestPostgresLedgerSurvivesRestart`)
- [x] Kịch bản đầu-cuối qua HTTP: **36 chuyến tiền mặt → nợ 201.600đ → bị chặn** với `wallet_debt_exceeded`
      → nạp 500k → **nhận chuyến lại được ngay**
- [x] `SUM(amount_vnd) = 0` cho **mọi** `tx_id` — kiểm trên dữ liệu thật: 38 giao dịch, 149 bút toán, tổng toàn sổ = **0**
- [x] Không còn `float64` nào trên đường đi của tiền
- [x] `pricing` đạt **80,7%** độ phủ; `computeBase` và `isNight` đạt **100%**
- [x] Duyệt/từ chối hồ sơ để lại bản ghi: ai · lúc nào · hồ sơ nào · trước→sau

**Kết quả đo được:** 43 → **77 test**; độ phủ toàn dự án **57,2%**; 4 migration.

> **Còn nợ từ GĐ 1:** bốn store vẫn ở bộ nhớ (`matching.offers`, `location.index`, `pricing.quotes`,
> `idem.keys`) ⇒ **vẫn chỉ chạy được 1 bản sao**. Đó là GĐ 3.
>
> **Nạp ví thủ công (`POST /v1/drivers/me/topup`) CHỈ đăng ký ở chế độ dev.** Ở production, tiền vào
> ví chỉ được đến từ webhook cổng thanh toán đã xác thực chữ ký ([T-22](07-todo.md#t-22)) — một
> endpoint tự ghi có mà không có đối ứng tiền thật chính là máy in tiền.

---

## Giai đoạn 2 — Đúng nghiệp vụ (~10 ngày) · **P1** ← **đang tới**

**Mục tiêu:** ghép chuyến và tính giá hành xử **đúng như spec mô tả**, không phải chỉ có mã nguồn giống.

| Việc | Gap | Ước lượng |
|---|---|---|
| Cập nhật thống kê tài xế: `AcceptanceRate`, `CancelRate`, `Rating`, `CompletedTrips` (trung bình trượt, subscriber sự kiện) | [G-04](05-doi-chieu-spec-code.md#g-04) | ~2,5n |
| `driver.IdleSince` + sửa `IdleSeconds` đo đúng thời gian rảnh | [G-10](05-doi-chieu-spec-code.md#g-10) | ~1n |
| Nối `DemandSurge.RecordRequest` vào luồng thật | [G-07](05-doi-chieu-spec-code.md#g-07) | ~0,5n |
| Nối `outbox` vào `trip`/`wallet`; relay đọc từ Postgres; **at-least-once** thật | [G-06](05-doi-chieu-spec-code.md#g-06) | ~3n |
| `matching.PostgresStore` cho `offers` (kích hoạt `offers_one_accepted_per_trip`) | — | ~1,5n |
| Test AC còn thiếu: offer hết hạn + nới bán kính → `EXPIRED`; idempotency `Create`; ping `Mocked` | AC | ~1,5n |

### Điều kiện hoàn thành

- [ ] Tài xế từ chối 10 offer liên tiếp → `acceptance_rate` giảm → **rơi xuống cuối** danh sách chấm điểm
- [ ] Chuyến ở khu vực không có tài xế → 3 vòng nới bán kính 1500/3000/4500m → `EXPIRED` — **có test**
- [ ] Tăng mật độ request trong một ô lưới → surge tăng theo bậc thang, **không bao giờ vượt 2.0** — có test boundary
- [ ] Giết tiến trình giữa lúc ghi sổ → khởi động lại → outbox relay publish nốt, sổ cái đúng
- [ ] `offers_one_accepted_per_trip` thực sự chặn được (test cố tình ghi 2 offer `ACCEPTED`)

---

## Giai đoạn 3 — Hạ tầng thật (~15 ngày) · **P1**

**Mục tiêu:** chạy nhiều pod. Đây là chỗ *"đổi implementation, không sửa nghiệp vụ"* của spec §8 nhóm B.

| Hiện tại | Thay bằng | Ước lượng |
|---|---|---|
| `location.MemoryIndex` | Redis `GEOSEARCH` (hoặc H3 cell → Redis Set) | ~2,5n |
| `matching.MemoryStore.ClaimTrip` | Redis `SET trip:{id}:claim NX EX 30` | ~1,5n |
| `pkg/geo` lưới ô vuông | `github.com/uber/h3-go/v4` res 8–9 (chỉ đổi `CellOf`/`Ring`) | ~1,5n |
| `idem.NewMemoryStore` | Postgres `idempotency_keys` hoặc Redis | ~1n |
| `pricing.NewMemoryQuoteStore` | Redis, TTL 5 phút | ~0,5n |
| `httpx.NewRateLimit` | Redis (giới hạn toàn cụm) + giới hạn OTP theo **số điện thoại** ([G-22](05-doi-chieu-spec-code.md#g-22)) | ~1,5n |
| `eventbus.NewInMemory` | **NATS JetStream** — sau bước này `cmd/worker` mới có ý nghĩa | ~3n |
| `pricing.HaversineEngine` | OSRM `/route` tự host (Goong/VietMap dự phòng) | ~2n |
| `matching.SimpleETA` | OSRM `/table` — **một request cho cả lô**, cache theo cặp ô lưới | ~2n |
| `notification.LogPusher` | FCM (Android) + APNs; `LogOTPSender` → **Zalo ZNS** | ~2,5n |
| — | **MQTT (EMQX)** cho luồng vị trí, topic `drv/{id}/loc` QoS 1 + Last Will | ~3n |

**Đồng thời:** metric Prometheus, tracing OpenTelemetry, `/readyz` kiểm tra DB + Redis ([G-25](05-doi-chieu-spec-code.md#g-25)).

### Điều kiện hoàn thành

- [ ] **3 pod API + 2 pod worker** cùng phục vụ; giết 1 pod bất kỳ không mất chuyến nào
- [ ] Báo giá ở pod A, đặt chuyến ở pod B → thành công (quote store dùng chung)
- [ ] Hai tài xế nhận cùng chuyến **từ hai pod khác nhau** → đúng một người thắng
- [ ] Ứng dụng tài xế nhận offer qua **push**, không cần poll
- [ ] Chi phí Maps: đo được số request OSRM/chuyến, có tỉ lệ cache hit
- [ ] Load test riêng `matching` — spec §8 nhóm C #13 gọi đây là điểm nghẽn đầu tiên

---

## Giai đoạn 4 — Sẵn sàng thương mại (~25 ngày) · **P2**

**Không sang giai đoạn này khi GĐ 1 chưa xong.**

| Nhóm | Việc | Ước lượng |
|---|---|---|
| **Tiền** | Cổng thanh toán MoMo / ZaloPay / VNPay / VietQR: webhook **xác thực chữ ký** + đối soát tự động cuối ngày | ~7n |
| | Job đối soát & chi trả: `settlement_batches` + `ledger_entries.settlement_batch_id`. **Bắt buộc idempotent — chạy 2 lần không double-pay** | ~4n |
| | Hoá đơn điện tử (NĐ 123/2020): `internal/wallet/einvoice/` + `EInvoiceProvider` adapter (Viettel/VNPT/MISA). Retry backoff, **không chặn hoàn tất chuyến** | ~5n |
| | Bật `TaxPermille` sau khi kế toán thuế xác nhận | ~1n |
| **Tuân thủ** | Theo dõi hạn giấy tờ: `document_expiry_alerts` + job cảnh báo trước hạn (đăng kiểm, TNDS, GPLX) | ~2n |
| | `driver_status_history` — lịch sử để đối soát tranh chấp | ~1,5n |
| | Mã hoá CCCD/GPLX ở tầng ứng dụng ([G-26](05-doi-chieu-spec-code.md#g-26)) | ~2n |
| | eKYC FPT.AI / VNPT — đối chiếu CCCD gắn chip với GPLX | ~4n |
| **An toàn** | Nút SOS, chia sẻ hành trình | ~3n |
| **Bảo mật** | Refresh token + thu hồi (`jti` + danh sách chặn Redis) ([G-21](05-doi-chieu-spec-code.md#g-21)) | ~2n |

### Điều kiện hoàn thành

- [ ] Webhook giả mạo chữ ký bị từ chối — **có test**
- [ ] Đối soát cuối ngày khớp 100% với sao kê cổng thanh toán trên dữ liệu thật
- [ ] Job chi trả chạy **hai lần** cho cùng kỳ → tổng chi **không đổi**
- [ ] Hoá đơn phát hành lỗi → chuyến vẫn hoàn tất bình thường, hoá đơn vào hàng đợi retry
- [ ] Tài xế có giấy tờ sắp hết hạn nhận cảnh báo trước 30/15/7 ngày

---

## Giai đoạn 5 — Quy mô & tính năng (sau khi có lưu lượng thật)

**Chỉ làm khi có dữ liệu thật để ra quyết định — spec §7 cấm tune ngầm các tham số này.**

| Việc | Điều kiện tiên quyết |
|---|---|
| Tách `location` + `matching` thành service riêng | GĐ 3 xong; `matching` đo được là điểm nghẽn |
| Khuyến mãi / voucher (`PROMO_EXPENSE` đã có sẵn trong sổ cái) | có ngân sách marketing |
| Chống gian lận nâng cao — phân tích đồ thị phát hiện chuyến ảo giữa cặp rider–driver quen | có ≥ 3 tháng dữ liệu chuyến |
| Kho dữ liệu ClickHouse + BI; ML cho ETA và surge | có ≥ 6 tháng dữ liệu |
| Đặt trước, ghép chuyến chung, giao hàng / đồ ăn | quyết định sản phẩm |
| Multi-region / sharding | **spec §7: quyết định ở Phase 5, không sớm hơn** |

---

## Việc **không được tự quyết** (spec §7)

Các giá trị này **đã có trong code nhưng chỉ là giả định khởi điểm**. Phải hỏi trước khi coi là chốt,
và **tuyệt đối không tune ngầm**.

| Hạng mục | Giá trị hiện tại | Cần gì để chốt | Ai quyết |
|---|---|---|---|
| Trọng số chấm điểm (§3.2) | `1.0 / 60 / 90 / 0.25 / 0.20` | dữ liệu thật + A/B test | Sản phẩm + Dữ liệu |
| Bậc thang surge (§3.4) | `4→2.0, 3→1.7, 2→1.4, 1.2→1.2` | dữ liệu cung/cầu thật | Sản phẩm |
| Biểu giá (§4.1) | bảng TP.HCM mẫu | **hồ sơ kê khai giá cước đã nộp** | Pháp chế + Tài chính |
| `TaxPermille` | 0 (tắt) | xác nhận của kế toán thuế | Tài chính |
| `DefaultDebtLimit` | 200.000đ | chính sách vận hành | Vận hành |
| Chu kỳ settlement + payout | chưa có | chính sách tài chính | Tài chính |
| Provider e-invoice | chưa chọn | Viettel/VNPT/MISA + credential sandbox | Tài chính |
| Multi-region / sharding | chưa | quyết định ở GĐ 5 | Kỹ thuật |

---

## Rủi ro lớn nhất (spec §10, giữ nguyên — vẫn đúng)

1. **Đối soát tiền mặt.** Sai lệch một đồng cũng làm mất niềm tin tài xế.
   Đây là lý do sổ cái kép + idempotency là **bắt buộc**, không phải tuỳ chọn.
   → GĐ 1 là giai đoạn quan trọng nhất của toàn bộ kế hoạch này.
2. **Pháp lý.** Phải đăng ký phần mềm kết nối vận tải với Sở GTVT **trước khi chạy thương mại**.
   Nên có luật sư từ ngày đầu.
3. **Cung tài xế.** Kỹ thuật hoàn hảo mà không có tài xế thì matching vô nghĩa.
   Ngân sách incentive thường lớn hơn ngân sách kỹ thuật.
4. **Chi phí Maps.** Cache thật mạnh, nếu không hoá đơn API sẽ vượt tiền server.
   → Đây là lý do `matching.SimpleETA` phải thành OSRM `/table` **một request cho cả lô**, không phải N request.
