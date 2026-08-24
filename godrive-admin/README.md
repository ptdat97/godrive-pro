# GoDrive Admin — Bảng điều khiển vận hành

Giao diện quản trị cho [godrive](../godrive). **Đây chỉ là lớp hiển thị.**
Mọi logic nghiệp vụ — lọc, tổng hợp, phân quyền, ngưỡng cảnh báo — nằm trong
`godrive/internal/admin`. Ứng dụng này gọi API và vẽ kết quả.

```bash
npm install
npm run dev     # http://localhost:3000
```

Cần backend chạy sẵn ở `http://localhost:8080` (xem [../godrive/README.md](../godrive/README.md)).

---

## 1. Ranh giới trách nhiệm

Quy tắc đã áp dụng xuyên suốt: **nếu một câu hỏi có thể trả lời sai, câu trả
lời phải đến từ Go.**

| Việc | Ai làm |
|---|---|
| Lọc tài xế theo trạng thái / hồ sơ / từ khoá | Go (`ListDriversInput`) |
| Quyết định tài xế có nợ quá hạn mức không | Go (`Driver.CanAcceptTrip`) |
| Lý do tài xế bị chặn nhận chuyến | Go — trả về mã, UI chỉ tra bảng nhãn |
| Đếm số liệu tổng quan, sinh cảnh báo | Go (`Service.Overview`, `alerts`) |
| Sắp xếp: ai lên đầu bảng | Go (nợ trước, chờ duyệt sau, rồi theo tên) |
| Ai được đăng nhập admin | Go (`ADMIN_PHONES`) |
| Dịch mã trạng thái sang tiếng Việt | UI (`src/lib/format.ts`) |
| Màu badge, bố cục, biểu đồ | UI |

UI **không** tự lọc mảng, không tự tính tổng, không tự suy ra "tài xế này có
vấn đề". Khi cần thêm góc nhìn mới, thêm endpoint ở Go trước.

---

## 2. Cấu trúc

```
src/
  app/
    login/               đăng nhập OTP (2 bước)
    (dashboard)/         nhóm route yêu cầu phiên admin
      layout.tsx         chốt chặn phiên + thanh điều hướng
      page.tsx           tổng quan
      drivers/           bảng tài xế, chi tiết, duyệt hồ sơ
      trips/             bảng chuyến, chi tiết + nhật ký sự kiện
      live/              bản đồ phân bố tài xế
  components/
    ui.tsx               Badge, Card, Stat, Table, ErrorBox
    nav-link.tsx         liên kết tự tô đậm
    refresher.tsx        tự làm mới theo chu kỳ, tắt được
  lib/
    api.ts               gọi API phía máy chủ, chuẩn hoá lỗi
    session.ts           đăng nhập / đăng xuất, cookie httpOnly
    types.ts             kiểu khớp JSON của Go
    format.ts            định dạng tiền, thời gian, nhãn tiếng Việt
```

### Vì sao gọi API phía máy chủ

Trình duyệt không bao giờ gọi thẳng cổng 8080. Mọi request đi qua tiến trình
Next.js. Ba lợi ích:

1. **Không cần CORS** — backend Go không có middleware CORS, và không cần thêm.
2. **Token không lộ ra JavaScript** — lưu trong cookie `httpOnly`, kể cả có XSS
   cũng không lấy được.
3. **Địa chỉ backend không lộ ra client** — `GODRIVE_API_URL` là biến máy chủ.

---

## 3. Đăng nhập

Chỉ số điện thoại trong `ADMIN_PHONES` (cấu hình ở backend) mới đăng nhập được.
Số ngoài danh sách bị từ chối **trước khi** gửi OTP — không tốn tin nhắn và
không lộ số nào là admin.

Ở chế độ dev (`DEV_AUTH=true`), mã OTP hiện thẳng trên màn hình và điền sẵn vào
ô nhập.

Luồng OTP dùng chung của rider/driver **không** cấp được token admin: cổng
`/v1/admin/auth/*` kiểm tra danh sách hai lần (trước khi gửi mã, và sau khi
xác thực). Nếu không thế, chỉ cần gửi `role=admin` là leo thang đặc quyền.

---

## 4. Các trang

**Tổng quan** — số tài xế theo trạng thái, chuyến theo trạng thái, doanh thu,
tỉ lệ tiền mặt. Cảnh báo do backend sinh: hồ sơ chờ duyệt, chuyến chờ ghép quá
60 giây, tài xế bị chặn vì nợ, chuyến không tìm được tài xế.

**Tài xế** — bảng gộp sẵn ví, công nợ tiền mặt, vị trí, cờ gian lận 24h. Bộ lọc
ghi vào URL nên chia sẻ được đường dẫn. Trang chi tiết có nút duyệt/từ chối hồ
sơ. Giấy tờ định danh (CCCD, GPLX) **không** hiển thị — API không trả về, theo
Nghị định 13/2023.

**Chuyến đi** — lọc theo trạng thái, hiện thời gian chờ ghép (tô đỏ khi quá 60
giây). Trang chi tiết có nhật ký chuyển trạng thái dạng timeline — đây là bằng
chứng đối soát khi khách khiếu nại, bảng `trip_events` chỉ thêm mới.

**Bản đồ** — nền OpenStreetMap qua Leaflet. Hiển thị đồng thời **cung và cầu**:
tài xế (chấm tròn: xanh lá = sẵn sàng, xanh dương = đang bận) và điểm đón đang
chờ ghép (hình vuông: cam = bình thường, đỏ = chờ quá 60 giây). Vạch kẻ từ mỗi
tài xế là hướng xe — bộ ghép chuyến có phạt xe chạy ngược hướng điểm đón.

Câu hỏi vận hành thật không phải "tài xế ở đâu" mà là "chỗ nào có khách chờ mà
không có tài xế" — nên backend trả cả hai tập trong **một** lời gọi
`/v1/admin/live-map`, bảo đảm cùng thời điểm và cùng bán kính.

Chọn được khu vực (5 vùng cấu hình sẵn) và bán kính; lựa chọn ghi vào URL nên
chia sẻ được đường dẫn.

> Bản đồ chỉ hiện tài xế có ping **dưới 45 giây** (`location.StaleAfter`).
> Trống không có nghĩa là lỗi — nghĩa là không ai đang gửi ping.

### Vì sao OpenStreetMap, và giới hạn cần biết

Tile raster của OSM miễn phí, không cần API key. Nguyên tắc "tránh phụ thuộc
Maps API" của dự án nhắm vào **chi phí tính theo lượt gọi của Google**, không
phải cấm mọi bản đồ.

> ⚠️ **`tile.openstreetmap.org` chỉ hợp cho bảng điều khiển nội bộ vài người
> dùng.** [Tile Usage Policy](https://operations.osmfoundation.org/policies/tiles/)
> cấm dùng cho ứng dụng tải cao. Trước khi mở rộng, đổi sang tile tự host
> (đúng định hướng dự án — OSRM đã tự host) hoặc dịch vụ trả phí. URL tile nằm
> ở một chỗ duy nhất trong [`osm-map.tsx`](src/app/(dashboard)/live/osm-map.tsx).

Ghi công OpenStreetMap hiển thị trên bản đồ là **bắt buộc** theo giấy phép
ODbL — đừng gỡ.

### Ràng buộc kỹ thuật

Leaflet đụng thẳng vào `window`/`document` nên **không dựng được phía máy chủ**
(đã kiểm chứng: `require('leaflet')` trong Node ném `ReferenceError: window is
not defined`). Vì vậy cần `next/dynamic` với `ssr: false` — mà tuỳ chọn này chỉ
hợp lệ **bên trong Client Component**, nên có tệp trung gian
[`map-panel.tsx`](src/app/(dashboard)/live/map-panel.tsx) giữa trang (Server
Component) và bản đồ.

Danh sách khu vực nằm ở [`areas.ts`](src/app/(dashboard)/live/areas.ts) — tệp
riêng không có `"use client"`, vì ranh giới server/client chỉ truyền được
component, không truyền dữ liệu tĩnh. Import mảng từ tệp `"use client"` sẽ nhận
về proxy rỗng và lỗi lúc chạy.

---

## 5. Biến môi trường

```bash
GODRIVE_API_URL=http://localhost:8080   # địa chỉ API Go
```

Chỉ dùng phía máy chủ, không có tiền tố `NEXT_PUBLIC_` nên không lọt vào bundle
trình duyệt.

---

## 6. Kiểm thử

```bash
npm run build   # gồm cả kiểm tra kiểu TypeScript
npm run lint
```

Chưa có test tự động cho UI. Đã kiểm chứng thủ công bằng curl với cookie phiên:
mọi route dashboard trả 307 về `/login` khi chưa đăng nhập; các trang render
đúng dữ liệu thật từ backend.

---

## 7. Việc chưa làm

1. **Test tự động** — Playwright cho luồng đăng nhập và duyệt hồ sơ.
2. **Phân trang** — backend chặn ở 200 dòng (`admin.MaxPageSize`); chưa có con
   trỏ phân trang nên danh sách lớn sẽ bị cắt.
3. **Tile tự host** — đang dùng `tile.openstreetmap.org` trực tiếp, chỉ hợp cho
   nội bộ vài người dùng. Xem giới hạn ở mục 4.
4. **Cập nhật trực tiếp** — đang dùng polling `router.refresh()`. Có thể chuyển
   sang SSE khi backend phát sự kiện qua NATS.
5. **Nhật ký thao tác admin** — hiện chưa ghi lại ai duyệt hồ sơ nào, lúc nào.
   Cần cho đối soát nội bộ.
6. **Đối soát ví** — `wallet.Statement` đã có ở backend nhưng chưa có endpoint
   admin và chưa có màn hình.
