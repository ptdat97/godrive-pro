# GoDrive — Bộ tài liệu kiến trúc hệ thống

> **Nguồn sự thật:** code trong [`godrive/`](../godrive) và [`godrive-admin/`](../godrive-admin).
> Tài liệu này **đã đối chiếu từng mục với code thật** ngày **2026-08-25** (Go 1.26.5, macOS),
> và cập nhật lại sau khi hoàn thành **Giai đoạn 0, 1, 2**, **phần kiểm chứng được của Giai đoạn 3**,
> phần thương mại của **Giai đoạn 4**, và **cấu hình vận hành sửa được từ giao diện quản trị**.
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
| Phụ thuộc ngoài | **4**: `pgx/v5` · `go-redis/v9` · `nats.go` · `paho.mqtt.golang` |
| File `.go` | **97** (không tính test) · **30** file TypeScript ở `godrive-admin` |
| Dòng mã (không tính test) | **13.662** |
| Migration | **9** (`0001` … `0009`) |
| `go build` / `go vet` / `gofmt -l` | ✅ sạch |
| `go test ./...` | ✅ **146 pass** (in-memory) · **185 pass** (đủ hạ tầng) |
| `go test -race ./...` | ✅ pass toàn bộ, không flake |
| Độ phủ toàn dự án | **68,8%** (`-coverpkg=./...`) |
| Chế độ chạy được đầu-cuối | in-memory · Postgres · **+ Redis + NATS + MQTT, nhiều bản sao** |
| Nhiều bản sao | ✅ **2 tiến trình thật**; **SIGKILL một pod giữa chừng không mất việc** |
| Quan sát | `/metrics` (Prometheus) · `/readyz` kiểm thật **Postgres, Redis, NATS, MQTT** |
| Bất biến kiểm trên CSDL | **9 câu SQL**, đều sạch sau mỗi lần chạy đầu-cuối |
| Cấu hình vận hành | **50 ô** trong **5 nhóm**, sửa từ giao diện quản trị, có hiệu lực ≤ 5 giây, không cần triển khai lại |

---

## Tiến độ kế hoạch

| Giai đoạn | Trạng thái | Nội dung |
|---|---|---|
| [GĐ 0 — Sửa nền](06-ke-hoach-trien-khai.md) | ✅ **xong** | Chạy được với Postgres · giấy tờ tài xế lưu trọn vẹn · goroutine nền có `recover` · dọn rò rỉ bộ nhớ |
| [GĐ 1 — Bền dữ liệu & đúng tiền](06-ke-hoach-trien-khai.md) | ✅ **xong** | Sổ cái Postgres · cổng chặn công nợ hoạt động thật · ghi sổ phí huỷ · bỏ hết float khỏi đường tiền · API ví · nhật ký thao tác admin |
| [GĐ 2 — Đúng nghiệp vụ](06-ke-hoach-trien-khai.md) | ✅ **xong** | Chỉ số tài xế sống · `IdleSince` đo đúng · surge phản ứng cầu thật · Transactional Outbox (at-least-once) · offers + khoá chuyến xuống Postgres |
| [GĐ 3 — Hạ tầng thật](06-ke-hoach-trien-khai.md) | ✅ **xong phần hạ tầng** | ✅ Redis (vị trí GEO, khoá chuyến, lời mời, báo giá, idempotency, rate limit toàn cụm) · ✅ **NATS JetStream** (ack — giết pod giữa chừng không mất việc) · ✅ **MQTT/EMQX** (luồng vị trí + Last Will) · ✅ OSRM `/route` + `/table` · ✅ metrics + `/readyz`<br>⬜ push FCM/APNs · H3 · tracing |
| [GĐ 4 — Thương mại](06-ke-hoach-trien-khai.md) | 🟡 **một phần** | ✅ Cổng thanh toán MoMo/ZaloPay/VNPay (xác thực chữ ký thật) · ✅ Đối soát & chi trả idempotent · ✅ Mã hoá CCCD/GPLX (NĐ 13/2023) · ✅ Thu hồi token · ✅ **Cấu hình vận hành sửa từ giao diện** (biểu giá, surge, ghép chuyến, ví, vị trí)<br>⬜ hoá đơn điện tử · eKYC · theo dõi hạn giấy tờ · SOS |
| [GĐ 5 — Quy mô](06-ke-hoach-trien-khai.md) | ⬜ chưa | Tách service · khuyến mãi · chống gian lận nâng cao |

> **Nguyên tắc xuyên suốt.** Không giao thứ chưa chạy thử được. **8 lỗi nghiêm trọng nhất của dự
> án đều chỉ lộ ra khi chạy thật** — năm trong số đó là cuộc đua hoặc lỗi thứ tự mà đọc code không
> thấy được. Vì vậy NATS và MQTT chỉ được viết sau khi có broker thật để kiểm chứng.
>
> **Ba lỗ hổng chặn đường tới pilot** (xem [lộ trình](godrive-pro-TODO.md)): chưa có app cho khách
> và tài xế · realtime chỉ có chiều lên nên lời mời phải hỏi bằng polling · ~~MQTT chưa xác thực~~
> (đã vá 2026-08-27).
>
> **Còn lại của GĐ 3:** push FCM/APNs (cần credential thật của Google/Apple), H3 (Redis GEO đã lo
> phần chỉ mục không gian nên giá trị giảm hẳn), và tracing OpenTelemetry.

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
| — | [**Lộ trình cạnh tranh**](godrive-pro-TODO.md) | 652 mục để thành nền tảng mobility thật — **122 xong · 55 một phần · 475 chưa**, kèm ba lỗ hổng chặn pilot |

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

4. **Mỗi hạ tầng một việc.** Postgres giữ **sự thật** (tiền, chuyến, nhật ký).
   Redis giữ **dữ liệu nóng** ghi nhiều sống ngắn (vị trí, khoá chuyến, lời mời,
   báo giá, idempotency, rate limit). NATS giữ **sự kiện có ack** — đây là thứ
   khiến giết pod giữa chừng không mất việc. MQTT giữ **luồng vị trí** vì nó
   tiết kiệm pin trên máy Android giá rẻ và có Last Will.
   Mất Redis là mất hiệu năng; mất Postgres là mất dữ liệu.

5. **Webhook cổng thanh toán có ba chốt chặn, không được bỏ chốt nào.**
   ① xác thực chữ ký — webhook là endpoint công khai, chữ ký là thứ duy nhất
   phân biệt thông báo thật với request bất kỳ ai cũng gửi được;
   ② đối chiếu số tiền với ý định đã ghi trước — chữ ký chứng minh thông báo đến
   *từ* cổng, không chứng minh số tiền *đúng*;
   ③ idempotent — cổng gửi lại webhook là hành vi bình thường.

6. **Consumer sự kiện phải có TÊN và phải idempotent.** Tên là danh tính của
   consumer ở broker; nhiều pod cùng tên tạo thành một nhóm, mỗi thông điệp xử
   lý đúng một lần trên toàn cụm. Nhưng `ack` có thể thất bại sau khi việc đã
   xong, nên **mọi handler đều phải chịu được việc chạy lại**.

7. **Không suy trạng thái tài xế từ sự kiện nào vừa tới.** Bus phát bất đồng bộ nên sự kiện có thể
   đến sai thứ tự. Luôn đọc trạng thái **hiện tại** của chuyến rồi mới đặt trạng thái tài xế
   (`app.syncDriverStatus`). Bản cũ làm ngược lại và để tài xế kẹt vĩnh viễn ở `ON_TRIP` trong ~10% số chuyến.
