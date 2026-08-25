# GoDrive — Bộ tài liệu kiến trúc hệ thống

> **Nguồn sự thật:** code trong [`godrive/`](../godrive) và [`godrive-admin/`](../godrive-admin).
> Tài liệu này **đã đối chiếu từng mục với code thật** ngày **2026-08-24** (Go 1.26.5, macOS),
> và cập nhật lại sau khi hoàn thành **Giai đoạn 0, 1, 2** và **phần kiểm chứng được của Giai đoạn 3**.
> Khi code và tài liệu lệch nhau, code là chuẩn — và tài liệu phải được sửa, không phải ngược lại.
>
> [`RIDE_HAILING_IMPLEMENTATION_SPEC.md`](../RIDE_HAILING_IMPLEMENTATION_SPEC.md) là **đặc tả gốc**.
> Bộ tài liệu này **mở rộng** đặc tả đó, đồng thời ghi lại **những chỗ đặc tả đã lệch khỏi code**
> (xem [05 — Đối chiếu spec ↔ code](05-doi-chieu-spec-code.md)).

---

## Trạng thái repo

| Hạng mục | Số liệu đã kiểm chứng |
|---|---|
| Module Go | `github.com/example/godrive`, `go 1.22` |
| Phụ thuộc ngoài | **2**: `pgx/v5` (Postgres), `go-redis/v9` (Redis) |
| File `.go` | **98** |
| Dòng mã (không tính test) | **9.668** |
| Migration | **6** (`0001` … `0006`) |
| `go build` / `go vet` / `gofmt -l` | ✅ sạch |
| `go test ./...` | ✅ **102 pass** (in-memory) · **119 pass** (đủ Postgres + Redis) |
| `go test -race ./... -count=6` | ✅ pass toàn bộ, không flake |
| Độ phủ toàn dự án | **63,4%** |
| Chế độ chạy được đầu-cuối | in-memory · Postgres · **Postgres + Redis, nhiều bản sao** |
| Nhiều bản sao | ✅ kiểm chứng với **2 tiến trình thật** dùng chung Postgres + Redis |
| Quan sát | `/metrics` (Prometheus) · `/readyz` kiểm thật DB + Redis |
| Bất biến kiểm trên CSDL | **9 câu SQL**, đều sạch sau mỗi lần chạy đầu-cuối |

---

## Tiến độ kế hoạch

| Giai đoạn | Trạng thái | Nội dung |
|---|---|---|
| [GĐ 0 — Sửa nền](06-ke-hoach-trien-khai.md) | ✅ **xong** | Chạy được với Postgres · giấy tờ tài xế lưu trọn vẹn · goroutine nền có `recover` · dọn rò rỉ bộ nhớ |
| [GĐ 1 — Bền dữ liệu & đúng tiền](06-ke-hoach-trien-khai.md) | ✅ **xong** | Sổ cái Postgres · cổng chặn công nợ hoạt động thật · ghi sổ phí huỷ · bỏ hết float khỏi đường tiền · API ví · nhật ký thao tác admin |
| [GĐ 2 — Đúng nghiệp vụ](06-ke-hoach-trien-khai.md) | ✅ **xong** | Chỉ số tài xế sống · `IdleSince` đo đúng · surge phản ứng cầu thật · Transactional Outbox (at-least-once) · offers + khoá chuyến xuống Postgres |
| [GĐ 3 — Hạ tầng thật](06-ke-hoach-trien-khai.md) | 🟡 **một phần** | ✅ Redis: chỉ mục vị trí GEO, khoá giành chuyến, lời mời, báo giá, idempotency, rate limit toàn cụm · ✅ OSRM `/route` + `/table` (một request cho cả lô) · ✅ metrics + `/readyz`<br>⬜ NATS · MQTT · push FCM/APNs · H3 · tracing |
| [GĐ 4 — Thương mại](06-ke-hoach-trien-khai.md) | ⬜ chưa | Cổng thanh toán · đối soát · hoá đơn điện tử · eKYC |
| [GĐ 5 — Quy mô](06-ke-hoach-trien-khai.md) | ⬜ chưa | Tách service · khuyến mãi · chống gian lận nâng cao |

> **Vì sao GĐ 3 dừng ở đây.** Bốn hạng mục còn lại **không kiểm chứng được** trên máy phát triển
> này: không có Docker, không có NATS/EMQX nào đang chạy, và push cần credential thật của
> FCM/APNs. Viết code cho chúng mà không chạy thử được thì chỉ là đoán — và bài học lớn nhất
> của ba giai đoạn trước đúng là ở chỗ đó: **7 lỗi nghiêm trọng nhất đều chỉ lộ ra khi chạy thật**,
> không lỗi nào tìm thấy bằng cách đọc code.

---

## Cách đọc bộ tài liệu

Đọc theo thứ tự nếu bạn mới vào dự án. Nhảy thẳng tới 05 nếu bạn cần biết *"cái gì đang thiếu"*.

| # | Tài liệu | Trả lời câu hỏi |
|---|---|---|
| 01 | [Kiến trúc tổng thể](01-kien-truc-tong-the.md) | Hệ thống gồm những gì, phụ thuộc đi theo chiều nào, một chuyến đi chạy qua đâu |
| 02 | [Mô hình dữ liệu](02-mo-hinh-du-lieu.md) | Bảng nào, ai sở hữu, bảng nào mới chỉ có schema mà chưa có code |
| 03 | [Hợp đồng module](03-hop-dong-module.md) | Mỗi module phơi ra Port gì, giữ bất biến gì, cấu hình ở đâu |
| 04 | [API reference](04-api-reference.md) | Endpoint thật, phân quyền thật, mã lỗi thật |
| 05 | [**Đối chiếu spec ↔ code**](05-doi-chieu-spec-code.md) | **Spec nói gì mà code chưa làm, và code làm gì mà spec chưa ghi** |
| 06 | [Kế hoạch triển khai](06-ke-hoach-trien-khai.md) | Làm gì trước, làm gì sau, mỗi giai đoạn xong khi nào |
| 07 | [TODO](07-todo.md) | Danh sách việc thực thi được, kèm lệnh verify |
| 08 | [Vận hành](08-van-hanh.md) | Biến môi trường, chạy, triển khai, sự cố thường gặp |

---

## Ba điều cần biết trước khi sửa bất cứ thứ gì

1. **Phụ thuộc chỉ đi một chiều, qua Port do bên tiêu thụ định nghĩa.**
   `matching.DriverPort` nằm trong package `matching`, không nằm trong `driver`.
   Nếu bạn thấy mình cần import struct nội bộ của module khác — dừng lại, bạn đang phá quy ước.

2. **Mọi thay đổi tiền phải qua sổ cái kép, và mọi giao dịch phải cân bằng về 0.**
   `wallet.Transaction.Validate()` chặn ở tầng ứng dụng ([ledger.go:75](../godrive/internal/wallet/ledger.go#L63)).
   Không bao giờ `UPDATE` một số dư.

3. **`trip_events` là append-only.** Chuyển trạng thái, ghi sự kiện **và ghi outbox** phải nằm trong
   **cùng một transaction** (`Repository.Save(ctx, trip, event, msgs...)`). Đây là hợp đồng vận tải
   điện tử theo Nghị định 10/2020 — lưu ≥ 3 năm.

4. **Redis giữ dữ liệu NÓNG, Postgres giữ SỰ THẬT.** Vị trí tài xế, khoá giành
   chuyến, lời mời, báo giá, khoá idempotency và bộ đếm rate limit nằm ở Redis vì
   chúng ghi rất nhiều và sống rất ngắn. Tiền, chuyến và nhật ký nằm ở Postgres.
   Mất Redis là mất hiệu năng; mất Postgres là mất dữ liệu.

5. **Không suy trạng thái tài xế từ sự kiện nào vừa tới.** Bus phát bất đồng bộ nên sự kiện có thể
   đến sai thứ tự. Luôn đọc trạng thái **hiện tại** của chuyến rồi mới đặt trạng thái tài xế
   (`app.syncDriverStatus`). Bản cũ làm ngược lại và để tài xế kẹt vĩnh viễn ở `ON_TRIP` trong ~10% số chuyến.
