# GoDrive Pro — TODO hoàn thiện để cạnh tranh Grab / Green SM

> Mục tiêu: biến GoDrive Pro từ ride-hailing backend core thành một nền tảng mobility production-ready tại Việt Nam.
>
> Nguyên tắc:
> - Không viết lại core nếu chưa có lý do đo được.
> - Ưu tiên sản phẩm chạy thật trước khi tối ưu kiến trúc.
> - Modular monolith trước, chỉ tách microservice khi có bottleneck thực tế.
> - Mọi mục `[x]` phải có test/verify.
> - Ký hiệu: `[x]` xong · `[~]` một phần · `[ ]` chưa bắt đầu.
> - P0 = bắt buộc để pilot production; P1 = marketplace; P2 = vận hành; P3 = lợi thế cạnh tranh.

---

## Tình trạng đối chiếu với code (2026-08-27)

Bản gốc đánh dấu **cả 633 mục đều chưa làm** (nay là **652** sau khi thêm P1.8). Đối chiếu với code thật:

| | Số mục | Nghĩa |
|---|---|---|
| `[x]` | **121** | Đã chạy được và **có test hoặc đã kiểm chứng trên hạ tầng thật** |
| `[~]` | **55** | Có một phần — dùng được ở mức dev, thiếu phần production hoặc thiếu mảnh cuối |
| `[ ]` | **476** | Chưa bắt đầu |

Chi tiết bằng chứng cho từng mục `[x]`: [07 — TODO kỹ thuật](07-todo.md) ·
[05 — Đối chiếu spec ↔ code](05-doi-chieu-spec-code.md) · [08 — Vận hành](08-van-hanh.md).

### Bốn quyết định đã chốt (2026-08-27)

| Câu hỏi | Chốt | Vì sao |
|---|---|---|
| Làm app nào trước? | **Driver App trước, Rider App sau** | Pilot chết vì thiếu tài xế online, không phải thiếu khách. Chạy thử realtime với vài chục tài xế đo được độ trễ thật trước khi mở cho khách. |
| Chiều xuống realtime dùng gì? | **MQTT hai chiều** | EMQX đã chạy sẵn và app tài xế vốn phải giữ kết nối MQTT để gửi vị trí. QoS 1, phiên bền, Last Will — WebSocket tự cài sẽ phải làm lại từ đầu. |
| H3 làm ngay không? | **Hoãn xuống P1** | Redis GEO đã lo chỉ mục không gian. Ô H3 tính ngược từ toạ độ đã lưu được bất cứ lúc nào, nên hoãn không mất dữ liệu. |
| Việc kế tiếp? | **[T-31](07-todo.md#t-31) + [T-32](07-todo.md#t-32)** | Vừa là nền bắt buộc cho app, vừa vá lỗ bảo mật đang mở. Viết app trước thì phần kết nối phải viết lại. |

---

### Cái đã vững

**Đường tiền và tính đúng đắn** là phần trưởng thành nhất. Sổ kép luôn cân, chi trả chạy bốn lần
vẫn ra một số tiền, giết pod giữa chừng không mất chuyến — tất cả đều có test chạy trên Postgres +
Redis + NATS + MQTT thật, không phải mock. Ghép chuyến có đủ 5 đầu vào sống, biểu giá và surge sửa
được lúc đang chạy. Bộ test 185 ca chạy sạch với `-race`.

### Ba lỗ hổng chặn đường tới pilot

**1. Không có app.** 59 mục của Rider App và Driver App đều chưa bắt đầu. Backend đã có gần đủ
endpoint cho luồng đặt xe, nhưng **không ai đặt được xe** vì không có gì để bấm. Đây là 100% khối
lượng còn lại của P0 tính theo số mục.

**2. Realtime chỉ có chiều lên.** MQTT hiện chỉ nhận vị trí *từ* tài xế; không có một lệnh
`Publish` nào từ máy chủ *xuống* thiết bị. Hệ quả: tài xế nhận lời mời bằng cách **hỏi liên tục**
`GET /v1/offers`, và khách **không thấy được xe đang chạy tới**. Mục tiêu *"offer delivery p95 < 1s"*
không đạt được bằng polling nếu không đánh đổi bằng tải máy chủ. Cần chiều xuống trước khi làm app,
vì nó quyết định kiến trúc app.

**3. MQTT không xác thực.** Broker đang mở: bất kỳ ai kết nối được cũng publish được vào
`drv/{id}/loc` của người khác. Chống gian lận GPS ở tầng ứng dụng (tốc độ bất khả thi, sai số) vẫn
chạy, nhưng nó lọc *nội dung* chứ không xác minh *danh tính người gửi*. Không được mở ra Internet
trước khi có xác thực và ACL theo topic.

### Ba mục bị bỏ sót so với bản gốc

Ngoài danh sách 633 mục, còn ba việc đã biết là thiếu nhưng chưa được liệt kê:

- **Quyền CSDL chặn `UPDATE`/`DELETE` lên bảng append-only** ([07 — T-27](07-todo.md#t-27)).
  Bất biến "chỉ thêm mới" của sổ cái và nhật ký hiện chỉ được giữ bằng quy ước trong code.
- **Tỉ lệ huỷ chuyến chưa vào hàm chấm điểm.** `trips_cancelled` được đếm nhưng không ai đọc —
  tài xế hay huỷ vẫn được xếp hạng như tài xế không bao giờ huỷ.
- **Không có test tải nào.** Mọi ngưỡng hiệu năng trong tài liệu là mục tiêu, chưa có số đo.

---

---

## 0. Mục tiêu chiến lược

- [ ] GoDrive có thể vận hành ride-hailing thật tại Việt Nam.
- [ ] Rider có thể đặt xe end-to-end.
- [ ] Driver có thể online, nhận chuyến, chạy chuyến và nhận tiền.
- [ ] Realtime location/offer đủ nhanh và ổn định.
- [ ] Matching tối ưu ETA và xác suất nhận chuyến.
- [ ] Payment → ledger → settlement → reconciliation nhất quán.
- [ ] Có safety/fraud/control tower.
- [ ] Có driver economics và incentive engine.
- [ ] Có supply/demand intelligence.
- [ ] Có nền tảng mở rộng lên fleet/corporate mobility.
- [ ] Pilot trước tại một khu vực có mật độ cung đủ cao, không triển khai toàn quốc ngay.

---

# P0 — PRODUCT / PRODUCTION PILOT

## P0.1 Rider App

- [ ] Tạo Flutter Rider App.
- [ ] OTP login/register.
- [ ] Permission location.
- [ ] Chọn điểm đón trên bản đồ.
- [ ] Search địa điểm.
- [ ] Chọn điểm đến.
- [ ] Recent places.
- [ ] Home / Work / Favorites.
- [ ] Hiển thị route.
- [ ] Hiển thị distance + ETA.
- [ ] Hiển thị fare estimate.
- [ ] Tạo booking.
- [ ] Hiển thị trạng thái `SEARCHING`.
- [ ] Hiển thị driver được assign.
- [ ] Hiển thị driver location realtime.
- [ ] Hiển thị ETA pickup.
- [ ] Hiển thị vehicle + driver.
- [ ] Call driver.
- [ ] Chat driver.
- [ ] Cancel trip.
- [ ] Theo dõi trip realtime.
- [ ] Hiển thị fare cuối chuyến.
- [ ] Thanh toán.
- [ ] Rating.
- [ ] Trip history.
- [ ] Receipt/invoice.
- [ ] Complaint/support.
- [ ] Push notification.
- [ ] Share trip.
- [ ] Emergency/SOS.

## P0.2 Driver App

- [ ] Tạo Flutter Driver App.
- [ ] OTP login.
- [ ] Driver onboarding.
- [ ] Upload CCCD/GPLX/đăng ký xe/bảo hiểm.
- [ ] KYC status.
- [ ] Vehicle profile.
- [ ] Online/offline.
- [ ] Background location.
- [ ] MQTT connection.
- [ ] Reconnect tự động.
- [ ] Trip offer realtime.
- [ ] Accept/reject offer.
- [ ] Navigation tới pickup.
- [ ] Arrived.
- [ ] Start trip.
- [ ] Complete trip.
- [ ] Fare.
- [ ] Wallet.
- [ ] Statement.
- [ ] Topup.
- [ ] Cash debt.
- [ ] Earnings dashboard.
- [ ] Daily/weekly/monthly earnings.
- [ ] Trip history.
- [ ] Rating.
- [ ] Driver support.
- [ ] SOS.
- [ ] Push notification.

## P0.3 Realtime Platform

- [~] MQTT broker production.
- [x] Driver location publish.
- [ ] Location authentication.
- [~] Device/session management.
- [x] Heartbeat.
- [x] Automatic reconnect.
- [x] Last-known-location.
- [x] Location freshness TTL.
- [x] Redis GEO integration.
- [x] Driver online index.
- [x] Driver stale eviction.
- [x] Realtime trip events.
- [ ] Realtime offer delivery.
- [ ] Realtime rider driver tracking.
- [~] Realtime status synchronization.
- [ ] Load test location ingestion.
- [ ] Load test offer delivery.
- [ ] Define target: location freshness < 3s.
- [ ] Define target: offer delivery p95 < 1s.

## P0.4 Maps / Routing

- [~] Production OSRM.
- [x] PostGIS.
- [ ] Geocoding provider.
- [ ] Reverse geocoding.
- [x] Route API.
- [x] ETA API.
- [x] Distance matrix.
- [x] Pickup route.
- [x] Destination route.
- [x] Route polyline.
- [ ] Toll/fee support.
- [x] Route cache.
- [x] Routing fallback.
- [~] Monitor routing latency.
- [ ] Validate ETA accuracy using real trips.

## ~~P0.5~~ → P1 · H3 Spatial Layer

> **Hoãn (chốt 2026-08-27).** Redis GEO đã lo phần chỉ mục không gian mà ghép chuyến cần. Giá trị
> còn lại của H3 nằm ở tổng hợp cung/cầu và heatmap — mà chưa có lưu lượng thì heatmap trống. Và vì
> mọi ping đều đã lưu kèm toạ độ, ô H3 tính ngược lại được bất cứ lúc nào, nên hoãn không mất gì.

- [ ] Integrate H3.
- [ ] Driver → H3 cell.
- [ ] Rider pickup → H3 cell.
- [ ] Supply per cell.
- [ ] Demand per cell.
- [ ] ETA per cell.
- [ ] Cancellation per cell.
- [ ] Conversion per cell.
- [ ] Heatmap.
- [ ] Service area.
- [ ] Geofence.
- [ ] Airport/station/venue zones.

## P0.6 Dispatch realtime

- [x] Candidate generation.
- [x] Candidate scoring.
- [x] ETA score.
- [x] Distance score.
- [x] Driver quality score.
- [x] Acceptance probability.
- [~] Cancellation probability.
- [x] Driver idle time.
- [x] Offer timeout.
- [x] Offer retry.
- [x] Avoid duplicate assignment.
- [x] Atomic reservation.
- [~] Dispatch observability.
- [~] Dispatch latency metrics.
- [x] Dispatch failure recovery.

## P0.7 Notifications

- [ ] FCM.
- [ ] APNs.
- [ ] Notification templates.
- [ ] Trip notification.
- [ ] Offer notification.
- [ ] Driver arrived notification.
- [ ] Payment notification.
- [ ] KYC notification.
- [ ] Promotional notification.
- [ ] Notification retry.
- [ ] Device token lifecycle.

## P0.8 Payment / Settlement

- [x] Cash payment.
- [x] Wallet.
- [x] VNPay.
- [x] MoMo.
- [x] ZaloPay.
- [ ] Bank transfer/QR.
- [x] Payment intent.
- [x] Webhook verification.
- [x] Idempotency.
- [x] Payment status machine.
- [ ] Refund.
- [x] Cancellation fee.
- [x] Driver earning ledger.
- [x] Company revenue ledger.
- [x] Settlement batch.
- [~] Reconciliation.
- [~] Failed payment recovery.
- [ ] Daily financial closing.
- [ ] Financial audit report.

---

# P1 — MARKETPLACE ENGINE

## P1.1 Dispatch Intelligence

- [ ] Tách `dispatch` thành domain riêng.
- [x] Candidate generation.
- [x] Candidate scoring.
- [~] Offer strategy.
- [x] Retry strategy.
- [x] Batch dispatch.
- [ ] Repositioning.
- [ ] Driver utility.
- [ ] Rider utility.
- [ ] Marketplace balance.
- [x] Acceptance probability.
- [ ] Cancellation probability.
- [ ] Driver destination bias.
- [ ] Vehicle preference.
- [ ] Fraud/risk score.
- [ ] Dispatch experiments/A-B testing.

## P1.2 Supply / Demand

- [ ] Supply count theo H3.
- [ ] Demand count theo H3.
- [~] Supply/demand ratio.
- [ ] Search-to-book conversion.
- [ ] Booking-to-completion conversion.
- [ ] Average pickup ETA.
- [~] Cancellation rate.
- [ ] Demand forecast 5/15/30/60 phút.
- [ ] Supply forecast.
- [ ] Demand heatmap.
- [ ] Supply heatmap.
- [ ] Detect shortage zones.
- [ ] Detect oversupply zones.

## P1.3 Dynamic Pricing

- [x] Base fare.
- [x] Distance fare.
- [x] Time fare.
- [x] Minimum fare.
- [x] Cancellation fee.
- [ ] Toll.
- [ ] Airport/venue surcharge.
- [x] Peak pricing.
- [x] Supply/demand multiplier.
- [x] Surge cap.
- [x] Fare transparency.
- [x] Price audit trail.
- [ ] Pricing simulation.
- [ ] A/B testing pricing.

## P1.4 Driver Economics

- [x] Gross earnings.
- [x] Net earnings.
- [ ] Earnings/hour.
- [ ] Earnings/km.
- [ ] Online hours.
- [ ] Active trip hours.
- [ ] Idle hours.
- [ ] Deadhead distance.
- [ ] Utilization.
- [x] Acceptance rate.
- [~] Cancellation rate.
- [ ] Driver profitability.
- [ ] Driver daily target.
- [ ] Driver weekly target.
- [ ] Driver earning forecast.

## P1.5 Incentive Engine

- [ ] Driver bonus.
- [ ] Quest.
- [ ] Streak.
- [ ] Peak bonus.
- [ ] Area bonus.
- [ ] New driver bonus.
- [ ] Referral.
- [ ] Guaranteed earnings.
- [ ] Reposition incentive.
- [ ] Incentive budget.
- [ ] Incentive ROI.
- [ ] Anti-abuse rules.
- [ ] Incentive experiment/A-B testing.

---

# P1.6 Fraud / Risk Engine

- [ ] `risk` domain.
- [ ] Account risk.
- [ ] Device risk.
- [ ] Trip risk.
- [ ] Payment risk.
- [~] GPS risk.
- [x] Fake GPS detection.
- [x] Impossible travel.
- [x] Teleport detection.
- [ ] Self-booking detection.
- [ ] Driver/rider collusion.
- [ ] Coupon abuse.
- [ ] Referral abuse.
- [ ] Multi-account detection.
- [ ] Device fingerprint.
- [ ] Suspicious cancellation.
- [ ] Suspicious cash trip.
- [ ] Risk score.
- [ ] Rule engine.
- [ ] Case management.
- [ ] Evidence storage.
- [ ] Manual review.
- [ ] Risk event audit.

---

# P1.7 Safety Platform

- [ ] SOS.
- [ ] Emergency contact.
- [ ] Trip sharing.
- [ ] Driver identity verification.
- [ ] Vehicle identity verification.
- [ ] Route deviation detection.
- [ ] Abnormal stop detection.
- [x] Speed anomaly.
- [ ] Dangerous zone.
- [ ] Safety incident.
- [ ] Incident status machine.
- [ ] Incident evidence.
- [ ] Operator escalation.
- [ ] Safety audit.
- [ ] Emergency contact notification.
- [ ] Trip recording architecture.
- [ ] Privacy/data retention policy.

---

# P1.8 GoDrive Geo Platform

> **Vì sao tách riêng.** Định tuyến và chỉ mục không gian đã có ([P0.4](#p04-maps-routing)), nhưng
> đó mới là *đọc* bản đồ. Phần dưới đây là *sở hữu* dữ liệu bản đồ của riêng mình: điểm đón đúng
> chỗ, đường cấm, ranh giới phục vụ, địa chỉ sửa được. Đây là thứ tích luỹ theo thời gian và
> **không mua lại được bằng tiền** — đối thủ có thể copy biểu giá trong một ngày, nhưng không copy
> được ba năm dữ liệu điểm đón đã hiệu chỉnh.
>
> **Nền đã có:** PostGIS đang chạy thật với `driver_locations.geom GEOGRAPHY(POINT, 4326)` + chỉ mục
> GIST. Thêm bảng vùng dạng đa giác là mở rộng cái đã kiểm chứng, không phải dựng mới.

- [~] Map abstraction / MapProvider
- [ ] MapLibre GL
- [x] Leaflet
- [ ] Vector Tiles
- [ ] Map Editor
- [ ] GeoFence Editor
- [ ] GeoFence Rules
- [ ] Restricted Road
- [ ] Service Area
- [ ] POI Management
- [ ] POI Error Detection
- [ ] POI Review Workflow
- [ ] Address Correction
- [ ] Pickup Point Management
- [ ] Pickup Point Intelligence
- [ ] Driver GPS Map Matching
- [ ] Road Quality Feedback
- [ ] Geo Data Quality Score
- [ ] Geo Analytics

### Hiện trạng ba mục không phải số không

| Mục | Trạng thái |
|---|---|
| **Leaflet** | Đang dùng thật ở bản đồ vận hành admin ([osm-map.tsx](../godrive-admin/src/app/%28dashboard%29/live/osm-map.tsx)), tile raster OSM, không cần API key. |
| **Map abstraction** | Mới trừu tượng hoá phần **định tuyến** (`pricing.RouteEngine` — OSRM + đường lùi). Chưa có lớp chung cho tile, geocoding, POI, map matching. |
| **Vector Tiles** | Đang là tile **raster**. Chuyển sang vector là điều kiện của Map Editor và của việc tự host bản đồ. |

### Bốn mục đáng làm sớm nhất tại Việt Nam

**Restricted Road** — đây là mục có giá trị cao nhất và cũng là chỗ OSRM mặc định sai nhiều nhất.
Đường một chiều, tuyến cấm xe máy, cầu cấm theo khung giờ, phố đi bộ cuối tuần. Dẫn tài xế vào
đường cấm là phạt tiền thật cho họ và một chuyến hỏng cho khách. Cần dữ liệu do người vận hành nhập,
không thể chờ OSM có đủ.

**Pickup Point Management** — phần lớn chuyến hỏng ở điểm đón không phải vì tài xế đi sai đường mà
vì *"tới nơi rồi mà không thấy nhau"*: sân bay, trung tâm thương mại, chung cư nhiều cổng, hẻm.
Một điểm đón đặt đúng chỗ tiết kiệm nhiều phút hơn mọi tối ưu ETA cộng lại.

**Service Area** — không có ranh giới thì hệ thống nhận cả chuyến ở nơi không có tài xế nào, rồi để
khách chờ hết ba vòng chào mời mới báo không tìm được xe. Chốt vùng phục vụ là điều kiện của chiến
lược *"mật độ cao trong khu vực nhỏ"* ở [mục 12](#12-chiến-lược-cạnh-tranh).

**Driver GPS Map Matching** — nắn quỹ đạo GPS về đúng tuyến đường. Cần cho ba việc cùng lúc: tính
cước theo quãng đường thật thay vì đường chim bay, phát hiện lệch tuyến ([P1.7](#p17-safety-platform)),
và đo chất lượng đường để nuôi ngược lại ETA. OSRM đã có endpoint `/match` — **hiện chưa ai gọi**,
mới chỉ dùng `/route` và `/table`.

### Thứ tự phụ thuộc

```
Service Area ──┐
GeoFence Rules ─┼──► cần bảng vùng đa giác (PostGIS polygon) — chưa có
Restricted Road ┘

POI Management ──► Address Correction ──► Pickup Point Management
                                              │
                          Pickup Point Intelligence ◄── dữ liệu chuyến thật
                                              ▲
Map Matching ──► Road Quality ──► Geo Analytics ──► Geo Data Quality Score
```

Ba nhánh chạy độc lập được. Nhánh **Pickup Point** phụ thuộc [T-38](07-todo.md#t-38) (tìm kiếm địa
điểm) và chỉ có giá trị khi Rider App tồn tại. Nhánh **Map Matching** cần dữ liệu chuyến thật để đo.
Nhánh **vùng đa giác** làm được ngay, không chờ gì.

> **Map Editor và POI Review Workflow là công cụ vận hành, không phải tính năng.** Chúng chỉ đáng
> làm khi đã có người ngồi sửa dữ liệu bản đồ hằng ngày. Làm sớm sẽ ra một màn hình đẹp mà không ai
> mở.

---

# P2 — OPERATIONS

## P2.1 Control Tower

- [x] Operations dashboard.
- [x] Live map.
- [x] Online drivers.
- [x] Active trips.
- [x] Searching trips.
- [x] Failed trips.
- [ ] SOS incidents.
- [ ] Fraud alerts.
- [ ] Supply/demand heatmap.
- [x] Driver detail.
- [ ] Rider detail.
- [~] Vehicle detail.
- [x] Trip timeline.
- [~] Payment timeline.
- [ ] Risk timeline.
- [x] Audit timeline.
- [~] Operator actions.
- [~] Role-based access.
- [ ] Action approval.
- [ ] Operator performance.

## P2.2 Driver Lifecycle

- [x] Application.
- [x] KYC.
- [x] Approval.
- [x] Rejection.
- [x] Suspension.
- [~] Reactivation.
- [ ] Warning.
- [ ] Training.
- [~] Document expiry.
- [ ] Driver contract.
- [ ] Driver tier.
- [~] Driver performance.
- [ ] Driver retention.
- [ ] Driver churn.

## P2.3 Fleet Management

- [~] Vehicle.
- [ ] Vehicle assignment.
- [ ] Rental contract.
- [ ] Vehicle handover.
- [ ] Odometer.
- [ ] Maintenance.
- [ ] Insurance.
- [ ] Inspection.
- [ ] Repair.
- [ ] Fuel/electricity cost.
- [ ] Vehicle utilization.
- [ ] Vehicle profitability.
- [~] Vehicle status.
- [~] Vehicle location.
- [ ] Fleet dashboard.

## P2.4 Support / CRM

- [ ] Rider support.
- [ ] Driver support.
- [ ] Ticket.
- [ ] Complaint.
- [ ] Refund.
- [ ] Compensation.
- [ ] Incident.
- [ ] SLA.
- [ ] Operator assignment.
- [ ] Internal notes.
- [ ] Evidence attachments.
- [ ] Customer history.
- [ ] Driver history.

## P2.5 Settlement / Finance

- [x] Driver settlement.
- [ ] Fleet settlement.
- [ ] Corporate billing.
- [x] Platform revenue.
- [x] Commission.
- [ ] Incentive expense.
- [ ] Payment fees.
- [ ] Refund.
- [ ] Chargeback.
- [~] Reconciliation.
- [ ] Daily closing.
- [ ] Monthly closing.
- [ ] Export accounting data.
- [~] Financial reports.

---

# P2.6 Corporate / B2B Mobility

- [ ] Corporate account.
- [ ] Organization.
- [ ] Employee.
- [ ] Cost center.
- [ ] Business trip.
- [ ] Corporate wallet.
- [ ] Spending limit.
- [ ] Approval workflow.
- [ ] Monthly invoice.
- [ ] VAT invoice integration.
- [ ] Corporate reporting.
- [ ] Scheduled rides.
- [ ] Employee ride policy.

---

# P2.7 Scheduled / Advanced Ride

- [ ] Scheduled ride.
- [ ] Airport transfer.
- [ ] Multi-stop.
- [ ] Book for someone else.
- [ ] Favorite driver.
- [ ] Vehicle preference.
- [x] Ride category.
- [ ] Premium category.
- [ ] Electric vehicle category.
- [ ] Delivery category.
- [ ] Hourly rental.
- [ ] Intercity ride.

---

# P3 — COMPETITIVE MOAT / DATA / ML

## P3.1 Data Platform

- [ ] Event warehouse.
- [ ] Trip fact table.
- [ ] Driver fact table.
- [ ] Rider fact table.
- [ ] Payment fact table.
- [ ] Location aggregation.
- [ ] H3 aggregation.
- [ ] Supply/demand dataset.
- [ ] Driver earnings dataset.
- [ ] Fraud dataset.
- [ ] ETA dataset.
- [ ] Pricing dataset.
- [ ] Feature store architecture.

## P3.2 ETA Intelligence

- [ ] Collect actual ETA.
- [ ] Compare predicted vs actual.
- [ ] Road segment statistics.
- [ ] Time-of-day model.
- [ ] Day-of-week model.
- [ ] Weather/event factors.
- [ ] Local correction factor.
- [ ] ETA model.
- [ ] ETA confidence interval.

## P3.3 ML Dispatch

- [ ] Acceptance probability model.
- [ ] Cancellation probability model.
- [ ] Driver destination preference.
- [ ] Driver reposition recommendation.
- [ ] Dispatch optimization.
- [ ] Supply prediction.
- [ ] Demand prediction.
- [ ] Incentive optimization.
- [ ] Fraud ML.
- [ ] Personalized pricing experiments.

---

# P3.4 Driver Intelligence

- [ ] "Where should I go?" recommendation.
- [ ] Earnings forecast.
- [ ] Demand forecast.
- [ ] Heatmap.
- [ ] Expected earnings/hour.
- [ ] Deadhead reduction.
- [ ] Shift recommendation.
- [ ] Target achievement.
- [ ] Personalized incentives.

---

# P3.5 Marketplace Moat

- [ ] Measure ETA against competitors.
- [ ] Measure fare against competitors.
- [ ] Measure driver earnings against competitors.
- [ ] Measure rider repeat rate.
- [ ] Measure driver retention.
- [ ] Identify underserved zones.
- [ ] Build density in selected zones.
- [ ] Expand city-by-city.
- [ ] Avoid nationwide expansion before liquidity exists.

---

# 4 — INFRASTRUCTURE / SRE

## 4.1 Production

- [~] Docker images.
- [~] Production config.
- [ ] Secrets management.
- [ ] PostgreSQL HA.
- [ ] Redis HA.
- [ ] NATS/JetStream HA.
- [ ] MQTT HA.
- [ ] OSRM HA.
- [ ] Object storage.
- [ ] CDN.
- [ ] Load balancer.
- [ ] Autoscaling.
- [ ] Backup.
- [ ] Disaster recovery.
- [ ] Restore drill.

## 4.2 Observability

- [x] Prometheus.
- [ ] Grafana.
- [x] Structured logs.
- [x] Trace ID.
- [ ] OpenTelemetry.
- [ ] Distributed tracing.
- [ ] Error tracking.
- [~] Alerting.
- [ ] SLO.
- [ ] SLA.
- [~] Incident management.

## 4.3 Performance

- [ ] API p95 < 100ms target.
- [ ] Matching p95 < 300ms target.
- [ ] Offer delivery p95 < 1s.
- [ ] Location freshness < 3s.
- [ ] Trip creation > 99.99%.
- [x] No duplicate assignment.
- [x] No lost trip.
- [x] Ledger imbalance = 0.
- [ ] Load test 1,000 online drivers.
- [ ] Load test 10,000 online drivers.
- [ ] Load test 100,000 location updates/min.
- [ ] Soak test 24h.
- [ ] Chaos test.
- [~] SIGKILL recovery test.
- [ ] Database failover test.
- [ ] Redis failure test.
- [ ] NATS failure test.
- [ ] MQTT failure test.

---

# 5 — SECURITY

- [~] Production JWT secret management.
- [ ] Token rotation.
- [ ] Refresh token strategy.
- [x] Device/session revocation.
- [ ] Admin MFA.
- [~] Role-based access control.
- [ ] Permission matrix.
- [ ] Database least privilege.
- [ ] Ledger UPDATE/DELETE protection.
- [~] Audit log immutability.
- [x] Webhook signature verification.
- [x] Replay attack protection.
- [x] Rate limiting.
- [~] Abuse protection.
- [x] PII encryption.
- [x] Document encryption.
- [ ] Secrets rotation.
- [ ] Dependency scanning.
- [ ] Container scanning.
- [ ] SAST.
- [ ] DAST.
- [ ] Security incident procedure.

---

# 6 — DATA / COMPLIANCE

- [ ] Driver document expiry job.
- [ ] Insurance expiry job.
- [ ] License expiry job.
- [ ] Data retention policy.
- [~] PII access audit.
- [ ] User data export.
- [ ] Account deletion workflow.
- [ ] Consent management.
- [ ] Privacy policy.
- [ ] Terms of service.
- [ ] Driver terms.
- [ ] Corporate terms.
- [~] Payment records retention.
- [ ] Audit retention.

---

# 7 — TESTING

- [x] Unit tests.
- [x] Integration tests.
- [x] PostgreSQL tests.
- [x] Redis tests.
- [x] NATS tests.
- [x] MQTT tests.
- [x] Routing tests.
- [ ] API contract tests.
- [ ] Mobile integration tests.
- [x] End-to-end rider flow.
- [x] End-to-end driver flow.
- [x] Payment end-to-end.
- [x] Cancellation end-to-end.
- [x] KYC end-to-end.
- [~] Fraud regression tests.
- [ ] Safety regression tests.
- [x] Race tests.
- [ ] Load tests.
- [ ] Chaos tests.
- [ ] Security tests.

---

# 8 — RELEASE CHECKLIST

## Pilot 100 drivers

- [ ] Rider App usable.
- [ ] Driver App usable.
- [~] Realtime stable.
- [~] Payment stable.
- [ ] Support available.
- [ ] Safety operational.
- [~] Control Tower operational.
- [x] Driver earnings correct.
- [x] Ledger balance verified.
- [x] No duplicate trips.
- [x] No lost trips.

## Pilot 500 drivers

- [ ] H3 supply/demand.
- [ ] Dispatch optimization.
- [ ] Driver incentive.
- [ ] Fraud engine.
- [ ] Safety incident process.
- [ ] Settlement automation.
- [ ] Performance test.

## 1,000 drivers

- [ ] 24/7 monitoring.
- [ ] HA infrastructure.
- [ ] Automated reconciliation.
- [ ] Automated driver lifecycle.
- [ ] Driver economics dashboard.
- [ ] Marketplace dashboard.
- [ ] SLO/SLA.

## 10,000 drivers

- [ ] Capacity test passed.
- [ ] 100k+ location updates/min.
- [ ] Dispatch capacity verified.
- [ ] Fraud automation.
- [ ] ML/forecasting foundation.
- [ ] Fleet integration.
- [ ] Corporate mobility.
- [ ] Multi-city operations.

---

# 9 — KPI BUSINESS

- [ ] Trips/hour.
- [ ] Trips/day.
- [ ] Trips/driver/hour.
- [ ] Online drivers.
- [ ] Driver utilization.
- [ ] Driver acceptance rate.
- [ ] Driver cancellation rate.
- [ ] Rider cancellation rate.
- [ ] Pickup ETA.
- [ ] Trip completion rate.
- [ ] Rider repeat rate.
- [ ] Driver retention.
- [ ] Driver churn.
- [ ] Driver earnings/hour.
- [ ] Driver earnings/km.
- [ ] Revenue/trip.
- [ ] Contribution margin.
- [ ] CAC.
- [ ] LTV.
- [ ] Supply/demand ratio.
- [ ] Marketplace liquidity.
- [ ] Fraud loss.
- [ ] Safety incidents.

---

# 10 — ƯU TIÊN THỰC THI

## Sprint 1

- [ ] Flutter Driver App skeleton.
- [ ] Flutter Rider App skeleton.
- [~] MQTT production path.
- [x] Redis GEO.
- [x] Realtime driver location.
- [ ] Realtime offer.
- [x] OSRM.
- [ ] Push notification.

## Sprint 2

- [~] End-to-end booking.
- [~] Driver accept.
- [~] Pickup.
- [~] Start.
- [~] Complete.
- [~] Fare.
- [~] Payment.
- [~] Rating.
- [~] Trip history.

## Sprint 3

- [ ] H3.
- [ ] Supply/demand.
- [ ] Dispatch optimization.
- [ ] ETA.
- [ ] Driver earnings dashboard.
- [ ] Incentive engine.

## Sprint 4

- [ ] Safety.
- [ ] Fraud.
- [ ] Control Tower.
- [ ] Support.
- [ ] Settlement.
- [ ] Reconciliation.

## Sprint 5+

- [ ] Fleet.
- [ ] Corporate.
- [ ] Scheduled rides.
- [ ] Data platform.
- [ ] Forecasting.
- [ ] ML dispatch.
- [ ] Personalized driver intelligence.

---

# 11 — Definition of Done cho GoDrive

GoDrive chỉ được coi là "production-ready" khi:

- [ ] Rider đặt xe thành công từ đầu đến cuối.
- [ ] Driver nhận chuyến realtime.
- [ ] Location realtime ổn định.
- [ ] ETA đủ chính xác.
- [x] Không duplicate assignment.
- [x] Không mất trip khi process chết.
- [x] Payment idempotent.
- [x] Ledger luôn cân bằng.
- [x] Driver nhận đúng tiền.
- [~] Công ty đối soát được tiền.
- [ ] Fraud có thể phát hiện và xử lý.
- [ ] SOS có operator xử lý.
- [~] Admin có Control Tower.
- [ ] Hệ thống chịu được ít nhất 1,000 online drivers.
- [ ] Có monitoring + alerting + backup.
- [ ] Có quy trình incident.
- [ ] Có driver economics.
- [ ] Có supply/demand intelligence.
- [ ] Có kế hoạch mở rộng theo density thay vì mở toàn quốc ngay.

---

# 12 — Chiến lược cạnh tranh

Không clone Grab toàn quốc ngay.

Ưu tiên:

1. Driver economics tốt.
2. Mật độ xe cao trong khu vực nhỏ.
3. ETA thấp.
4. Tỷ lệ nhận chuyến cao.
5. Cancellation thấp.
6. Support nhanh.
7. Safety tốt.
8. Fleet integration.
9. Corporate mobility.
10. Sau khi có dữ liệu mới mở rộng ML.

**Mục tiêu thực tế:**

`100 drivers → 500 → 1,000 → 10,000`

và mở rộng theo:

`density → liquidity → retention → city expansion`

thay vì:

`đăng ký thật nhiều tài xế → không đủ chuyến → tài xế rời platform`.

---

# 13 — Nguyên tắc kiến trúc cuối cùng

**Giữ:**

- Go
- PostgreSQL/PostGIS
- Redis
- NATS/JetStream
- MQTT
- OSRM
- modular monolith
- event-driven domain
- ledger
- idempotency
- optimistic locking

**Chưa cần:**

- microservices hàng loạt
- Kubernetes phức tạp
- ML quá sớm
- event sourcing toàn hệ thống
- distributed database
- rewrite core

**Tập trung tiếp theo vào:**

`Mobile → Realtime → Maps → Dispatch → Payment → Safety → Fraud → Operations → Driver Economics → Marketplace Intelligence`

Đây là con đường ngắn nhất từ **GoDrive backend project → ride-hailing platform có khả năng cạnh tranh thực tế tại Việt Nam**.
