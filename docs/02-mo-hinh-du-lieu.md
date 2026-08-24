# 02 — Mô hình dữ liệu

Nguồn: [`migrations/0001_init.up.sql`](../godrive/migrations/0001_init.up.sql) +
[`0002_identity_and_documents.up.sql`](../godrive/migrations/0002_identity_and_documents.up.sql)
(biến thể không PostGIS: [`migrations-nogis/`](../godrive/migrations-nogis/), sinh bằng
[`scripts/gen-nogis.py`](../godrive/scripts/gen-nogis.py) — **đừng sửa tay**).

## 2.1 Quy ước

| Quy ước | Nội dung | Vì sao |
|---|---|---|
| **Khoá chính** | `TEXT` chứa ID sắp xếp theo thời gian (`pkg/id`: `trp_…`, `drv_…`, `ofr_…`, `led_…`) | UUID v4 ngẫu nhiên làm phân mảnh B-tree khi insert nhiều; ID theo thời gian giữ locality và dễ đọc log |
| **Tiền** | `BIGINT` — số **đồng**, không phần lẻ | float làm lệch đối soát. Xem `pkg/money` |
| **Bảng nhật ký / tài chính** | chỉ `created_at` / `at` | append-only, không sửa, không xoá |
| **Bảng trạng thái động** | `updated_at` + `version INTEGER` | optimistic lock (CAS) |
| **Toạ độ** | `GEOGRAPHY(POINT,4326)` (chính) / `lat,lng DOUBLE` (nogis) | GIST index cho truy vấn bán kính |

---

## 2.2 Sơ đồ quan hệ

```
   accounts ──1:1──► drivers ──1:N──► offers ──N:1──► trips
      │  (rider)        │                              │
      │                 └──1:1──► driver_locations     │
      │                                                │
      └────────────────────1:N────────────────────────►│
                                                       │
                                              1:N ─────┴──► trip_events (append-only)

   ledger_entries  ──N:1(logic, không FK)──► ledger_transactions
   idempotency_keys        (độc lập)
   outbox                  (độc lập)
```

`ledger_entries.account_id` cố tình **không có FK** — nó chứa cả `driverID`, `riderID`, và hằng `"platform"`.
Đây là lựa chọn có chủ đích: sổ cái không nên phụ thuộc vòng đời hồ sơ.

---

## 2.3 Ma trận sở hữu — bảng nào, module nào, đã có repo chưa

| Bảng | Module sở hữu | Repo Postgres | Repo bộ nhớ | Trạng thái |
|---|---|:---:|:---:|---|
| `accounts` | `identity` | ✅ | ✅ | 🟢 **đã thông** — `identity.PostgresRepo`, GĐ 0 |
| `otp_challenges` | `identity` | ✅ | ✅ | 🟢 GĐ 0. Redis hợp hơn (TTL 5'), bảng là đường dự phòng; job dọn chạy trong worker |
| `drivers` | `driver` | ✅ | ✅ | 🟢 **đã đủ giấy tờ** — thêm `insurance_no`/`insurance_until`, `scan()` đọc lại `Documents` |
| `driver_locations` | `location` | ❌ | ✅ (`MemoryIndex`) | 🟡 chỉ mục chỉ tồn tại trong RAM |
| `trips` | `trip` | ✅ | ✅ | 🟢 |
| `trip_events` | `trip` | ✅ (cùng tx với `trips`) | ✅ | 🟢 |
| `offers` | `matching` | ❌ | ✅ | 🔴 offer + khoá chuyến mất khi restart |
| `ledger_entries` | `wallet` | ✅ | ✅ | 🟢 **đã bền** (GĐ 1) + `CHECK (account_id <> '')` |
| `ledger_transactions` | `wallet` | ✅ | ✅ (map `txs`) | 🟢 `PRIMARY KEY (tx_id)` là chốt idempotency |
| `admin_audit_log` | `admin` | ✅ | ✅ | 🟢 GĐ 1, chỉ thêm mới |
| `idempotency_keys` | `pkg/idem` | ❌ | ✅ | 🟡 retry chống trùng chỉ hiệu lực trong 1 tiến trình |
| `outbox` | `outbox` | ❌ | ✅ (không ai ghi) | 🔴 relay chạy nhưng no-op ([G-06](05-doi-chieu-spec-code.md#g-06)) |

**Đọc bảng này thế nào:** cột "Repo Postgres ❌" nghĩa là bảng đã có DDL trong migration nhưng
**không có một dòng Go nào INSERT/SELECT nó**. Còn **3/12** bảng ở tình trạng này
(`offers`, `driver_locations`, `idempotency_keys`, `outbox` — trước GĐ 0 là 6/10).

---

## 2.4 Ràng buộc ở tầng CSDL — chốt chặn cuối

Đây là những ràng buộc mà **kể cả bug ở tầng ứng dụng cũng không vượt qua được**.

```sql
-- Một tài xế tối đa MỘT chuyến đang hoạt động.
CREATE UNIQUE INDEX trips_one_active_per_driver
    ON trips (driver_id) WHERE status IN ('ASSIGNED','ARRIVED','IN_PROGRESS');

-- Mỗi chuyến duy nhất MỘT lời mời được chấp nhận.
CREATE UNIQUE INDEX offers_one_accepted_per_trip
    ON offers (trip_id) WHERE status = 'ACCEPTED';

-- Một biển số một hồ sơ.
CREATE UNIQUE INDEX drivers_plate_uidx ON drivers (vehicle_plate);

-- Một số điện thoại một vai trò một tài khoản.
UNIQUE (phone, role)  -- trên accounts
```

> ⚠️ **Hai unique index quan trọng nhất (`trips_one_active_per_driver`, `offers_one_accepted_per_trip`)
> hiện KHÔNG bảo vệ gì cả**, vì `offers` chưa có repo Postgres và chế độ Postgres thì chưa chạy được.
> Ở chế độ in-memory, chốt chặn duy nhất là `matching.Store.ClaimTrip` + `driver.Reserve` CAS.
> Test `TestOnlyOneDriverWinsTrip` verify đúng hai lớp này — nhưng lớp CSDL vẫn là lớp phòng thủ chưa được kích hoạt.

### Index hiệu năng

| Index | Phục vụ truy vấn |
|---|---|
| `drivers_idle_idx (city, vehicle_type) WHERE status='IDLE'` | dispatcher tìm ứng viên |
| `trips_searching_idx (requested_at) WHERE status='SEARCHING'` | dispatcher + `admin.LiveMap` + cảnh báo `trips_stuck` |
| `driver_locations_geom_idx GIST (geom)` | truy vấn lân cận theo bán kính |
| `ledger_account_idx (account_id, account_type, created_at)` | tính số dư = `SUM(amount_vnd)`, sao kê |
| `offers_driver_pending_idx (driver_id) WHERE status='PENDING'` | `GET /v1/offers` |
| `outbox_unpublished_idx (created_at) WHERE published_at IS NULL` | relay quét việc chưa publish |

---

## 2.5 Sổ cái kép — mô hình số có dấu

**Khác mô hình debit/credit cổ điển:** không có cột `direction`, không có bảng `ledger_accounts`.

```
amount_vnd > 0  →  ghi NỢ vào tài khoản (tài khoản nhận giá trị)
amount_vnd < 0  →  ghi CÓ (tài khoản mất giá trị)

BẤT BIẾN:  SUM(amount_vnd) GROUP BY tx_id  ==  0   (luôn luôn)
```

Bảy loại tài khoản ([ledger.go:22](../godrive/internal/wallet/ledger.go#L21)):

| `account_type` | Ý nghĩa | Số dư âm nghĩa là |
|---|---|---|
| `DRIVER_WALLET` | ví tài xế | **đang nợ chiết khấu** ← trọng tâm mô hình tiền mặt VN |
| `DRIVER_CASH` | tiền mặt tài xế đang cầm hộ | — |
| `RIDER_WALLET` | ví khách | khách nợ |
| `PLATFORM_REVENUE` | doanh thu nền tảng | — |
| `PROMO_EXPENSE` | chi phí khuyến mãi | (chưa dùng — dành cho voucher) |
| `TAX_PAYABLE` | thuế khấu trừ hộ tài xế | — |
| `GATEWAY_CLEARING` | tiền treo ở cổng thanh toán | — |

### Ví dụ: chuyến 50.000đ tiền mặt, chiết khấu 20%

| Tài khoản | Số tiền | Nghĩa |
|---|---:|---|
| `DRIVER_CASH` | **+50.000** | tài xế cầm tiền của khách |
| `PLATFORM_REVENUE` | **−50.000** | đối ứng doanh thu gộp |
| `DRIVER_WALLET` | **−10.000** | trừ chiết khấu |
| `PLATFORM_REVENUE` | **+10.000** | nền tảng thu chiết khấu |
| **Tổng** | **0** ✅ | |

Ví tài xế còn **−10.000đ** — **đó chính là công nợ**. Vượt `driver.DefaultDebtLimit` (200.000đ)
thì `Driver.CanAcceptTrip` chặn nhận chuyến.

> ✅ **Cơ chế chặn này đã hoạt động thật từ GĐ 1.** `matching.candidates()` và `driver.Reserve()`
> đều đọc số dư **thẳng từ sổ cái**, không tin cột cache. Kiểm chứng HTTP: 36 chuyến tiền mặt →
> nợ 201.600đ → bị chặn với `wallet_debt_exceeded` → nạp → nhận chuyến lại ngay.

### Số dư luôn được tính, không bao giờ được lưu

```sql
SELECT SUM(amount_vnd) FROM ledger_entries
WHERE account_id = $1 AND account_type = $2;
```

`drivers.wallet_balance` **chỉ là cache đọc nhanh**. Nguồn sự thật là `ledger_entries`.
Nếu hai giá trị lệch nhau — sổ cái đúng, cache sai.

### Sáu bút toán đã cài đặt

[`internal/wallet/postings.go`](../godrive/internal/wallet/postings.go)

| Hàm | Dùng ở | Đã nối vào luồng? |
|---|---|:---:|
| `SettleCashTrip` | `Service.SettleTrip(cash=true)` | ✅ |
| `SettleOnlineTrip` | `Service.SettleTrip(cash=false)` | ✅ |
| `WithholdTax` | `Service.SettleTrip` khi `TaxPermille > 0` | ✅ (mặc định **tắt**) |
| `TopUp` | `Service.TopUp` + `POST /v1/drivers/me/topup` (**dev**) | ✅ |
| `CancelFee` | `Service.PostCancelFee` ← consumer `trip.cancelled` | ✅ (GĐ 1) |
| `Payout` | — | ❌ **không ai gọi** — chờ job chi trả ([T-22](07-todo.md#t-22)) |

---

## 2.6 Bảng còn thiếu (đã xác định nghiệp vụ, chưa có DDL)

| Bảng cần thêm | Phục vụ | Ưu tiên |
|---|---|---|
| ~~`otp_challenges`~~ | ~~`identity` chế độ Postgres~~ | ✅ **xong** — migration `0002` |
| ~~`admin_audit_log`~~ | ~~truy vết thao tác quản trị~~ | ✅ **xong** — migration `0003` |
| `settlement_batches` + `ledger_entries.settlement_batch_id` | job đối soát/chi trả, chống double-pay | **P1** |
| `driver_status_history` | tranh chấp "lúc đó tôi đang online" | **P1** |
| `document_expiry_alerts` | hạn đăng kiểm / bảo hiểm TNDS / GPLX | **P1** |
| `invoices` | hoá đơn điện tử — Nghị định 123/2020 | **P2** |
| `payment_transactions` | webhook MoMo/ZaloPay/VNPay + đối soát cuối ngày | **P2** |
| `promotions` / `vouchers` / `voucher_redemptions` | dùng `PROMO_EXPENSE` đã có sẵn | **P3** |
| `fraud_flags` | hiện gom in-memory, mất khi restart | **P2** |

---

## 2.7 Lưu trữ và tuân thủ

| Bảng | Thời gian giữ tối thiểu | Căn cứ |
|---|---|---|
| `trip_events` | **3 năm** | hợp đồng vận tải điện tử — NĐ 10/2020, TT 12/2020 |
| `trips` | 3 năm | như trên |
| `ledger_entries` | **10 năm** | Luật Kế toán 2015 (chứng từ kế toán) |
| `driver_locations` | 30–90 ngày (chỉ ảnh chụp mới nhất) | tối thiểu hoá dữ liệu — NĐ 13/2023 |
| `accounts`, `drivers` | tới khi xoá tài khoản + thời hạn luật định | NĐ 13/2023 — quyền xoá dữ liệu |

**Dữ liệu nhạy cảm cần mã hoá ở tầng ứng dụng** (hiện đang lưu thô):
`drivers.national_id` (CCCD), `drivers.driver_license` (GPLX), `drivers.vehicle_reg_no`.
`Driver.Documents` đã được gắn `json:"-"` nên không lọt ra API công khai
([driver/domain.go:70](../godrive/internal/driver/domain.go#L72)) — nhưng trong CSDL vẫn là plaintext.
