# GoDrive Pro — TODO hoàn thiện để cạnh tranh Grab / Green SM

> Mục tiêu: biến GoDrive Pro từ ride-hailing backend core thành một nền tảng mobility production-ready tại Việt Nam.
>
> Nguyên tắc:
> - Không viết lại core nếu chưa có lý do đo được.
> - Ưu tiên sản phẩm chạy thật trước khi tối ưu kiến trúc.
> - Modular monolith trước, chỉ tách microservice khi có bottleneck thực tế.
> - Mọi mục `[x]` phải có test/verify.
> - P0 = bắt buộc để pilot production; P1 = marketplace; P2 = vận hành; P3 = lợi thế cạnh tranh.

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

- [ ] MQTT broker production.
- [ ] Driver location publish.
- [ ] Location authentication.
- [ ] Device/session management.
- [ ] Heartbeat.
- [ ] Automatic reconnect.
- [ ] Last-known-location.
- [ ] Location freshness TTL.
- [ ] Redis GEO integration.
- [ ] Driver online index.
- [ ] Driver stale eviction.
- [ ] Realtime trip events.
- [ ] Realtime offer delivery.
- [ ] Realtime rider driver tracking.
- [ ] Realtime status synchronization.
- [ ] Load test location ingestion.
- [ ] Load test offer delivery.
- [ ] Define target: location freshness < 3s.
- [ ] Define target: offer delivery p95 < 1s.

## P0.4 Maps / Routing

- [ ] Production OSRM.
- [ ] PostGIS.
- [ ] Geocoding provider.
- [ ] Reverse geocoding.
- [ ] Route API.
- [ ] ETA API.
- [ ] Distance matrix.
- [ ] Pickup route.
- [ ] Destination route.
- [ ] Route polyline.
- [ ] Toll/fee support.
- [ ] Route cache.
- [ ] Routing fallback.
- [ ] Monitor routing latency.
- [ ] Validate ETA accuracy using real trips.

## P0.5 H3 Spatial Layer

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

- [ ] Candidate generation.
- [ ] Candidate scoring.
- [ ] ETA score.
- [ ] Distance score.
- [ ] Driver quality score.
- [ ] Acceptance probability.
- [ ] Cancellation probability.
- [ ] Driver idle time.
- [ ] Offer timeout.
- [ ] Offer retry.
- [ ] Avoid duplicate assignment.
- [ ] Atomic reservation.
- [ ] Dispatch observability.
- [ ] Dispatch latency metrics.
- [ ] Dispatch failure recovery.

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

- [ ] Cash payment.
- [ ] Wallet.
- [ ] VNPay.
- [ ] MoMo.
- [ ] ZaloPay.
- [ ] Bank transfer/QR.
- [ ] Payment intent.
- [ ] Webhook verification.
- [ ] Idempotency.
- [ ] Payment status machine.
- [ ] Refund.
- [ ] Cancellation fee.
- [ ] Driver earning ledger.
- [ ] Company revenue ledger.
- [ ] Settlement batch.
- [ ] Reconciliation.
- [ ] Failed payment recovery.
- [ ] Daily financial closing.
- [ ] Financial audit report.

---

# P1 — MARKETPLACE ENGINE

## P1.1 Dispatch Intelligence

- [ ] Tách `dispatch` thành domain riêng.
- [ ] Candidate generation.
- [ ] Candidate scoring.
- [ ] Offer strategy.
- [ ] Retry strategy.
- [ ] Batch dispatch.
- [ ] Repositioning.
- [ ] Driver utility.
- [ ] Rider utility.
- [ ] Marketplace balance.
- [ ] Acceptance probability.
- [ ] Cancellation probability.
- [ ] Driver destination bias.
- [ ] Vehicle preference.
- [ ] Fraud/risk score.
- [ ] Dispatch experiments/A-B testing.

## P1.2 Supply / Demand

- [ ] Supply count theo H3.
- [ ] Demand count theo H3.
- [ ] Supply/demand ratio.
- [ ] Search-to-book conversion.
- [ ] Booking-to-completion conversion.
- [ ] Average pickup ETA.
- [ ] Cancellation rate.
- [ ] Demand forecast 5/15/30/60 phút.
- [ ] Supply forecast.
- [ ] Demand heatmap.
- [ ] Supply heatmap.
- [ ] Detect shortage zones.
- [ ] Detect oversupply zones.

## P1.3 Dynamic Pricing

- [ ] Base fare.
- [ ] Distance fare.
- [ ] Time fare.
- [ ] Minimum fare.
- [ ] Cancellation fee.
- [ ] Toll.
- [ ] Airport/venue surcharge.
- [ ] Peak pricing.
- [ ] Supply/demand multiplier.
- [ ] Surge cap.
- [ ] Fare transparency.
- [ ] Price audit trail.
- [ ] Pricing simulation.
- [ ] A/B testing pricing.

## P1.4 Driver Economics

- [ ] Gross earnings.
- [ ] Net earnings.
- [ ] Earnings/hour.
- [ ] Earnings/km.
- [ ] Online hours.
- [ ] Active trip hours.
- [ ] Idle hours.
- [ ] Deadhead distance.
- [ ] Utilization.
- [ ] Acceptance rate.
- [ ] Cancellation rate.
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
- [Payment risk.
- [GPS risk.
- [ ] Fake GPS detection.
- [ ] Impossible travel.
- [ ] Teleport detection.
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
- [ ] Speed anomaly.
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

# P2 — OPERATIONS

## P2.1 Control Tower

- [ ] Operations dashboard.
- [ ] Live map.
- [ ] Online drivers.
- [ ] Active trips.
- [ ] Searching trips.
- [ ] Failed trips.
- [ ] SOS incidents.
- [ ] Fraud alerts.
- [ ] Supply/demand heatmap.
- [ ] Driver detail.
- [ ] Rider detail.
- [ ] Vehicle detail.
- [ ] Trip timeline.
- [ ] Payment timeline.
- [ ] Risk timeline.
- [ ] Audit timeline.
- [ ] Operator actions.
- [ ] Role-based access.
- [ ] Action approval.
- [ ] Operator performance.

## P2.2 Driver Lifecycle

- [ ] Application.
- [ ] KYC.
- [ ] Approval.
- [ ] Rejection.
- [ ] Suspension.
- [ ] Reactivation.
- [ ] Warning.
- [ ] Training.
- [ ] Document expiry.
- [ ] Driver contract.
- [ ] Driver tier.
- [ ] Driver performance.
- [ ] Driver retention.
- [ ] Driver churn.

## P2.3 Fleet Management

- [ ] Vehicle.
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
- [ ] Vehicle status.
- [ ] Vehicle location.
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

- [ ] Driver settlement.
- [ ] Fleet settlement.
- [ ] Corporate billing.
- [ ] Platform revenue.
- [ ] Commission.
- [ ] Incentive expense.
- [ ] Payment fees.
- [ ] Refund.
- [ ] Chargeback.
- [ ] Reconciliation.
- [ ] Daily closing.
- [ ] Monthly closing.
- [ ] Export accounting data.
- [ ] Financial reports.

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
- [ ] Ride category.
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

- [ ] Docker images.
- [ ] Production config.
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

- [ ] Prometheus.
- [ ] Grafana.
- [ ] Structured logs.
- [ ] Trace ID.
- [ ] OpenTelemetry.
- [ ] Distributed tracing.
- [ ] Error tracking.
- [ ] Alerting.
- [ ] SLO.
- [ ] SLA.
- [ ] Incident management.

## 4.3 Performance

- [ ] API p95 < 100ms target.
- [ ] Matching p95 < 300ms target.
- [ ] Offer delivery p95 < 1s.
- [ ] Location freshness < 3s.
- [ ] Trip creation > 99.99%.
- [ ] No duplicate assignment.
- [ ] No lost trip.
- [ ] Ledger imbalance = 0.
- [ ] Load test 1,000 online drivers.
- [ ] Load test 10,000 online drivers.
- [ ] Load test 100,000 location updates/min.
- [ ] Soak test 24h.
- [ ] Chaos test.
- [ ] SIGKILL recovery test.
- [ ] Database failover test.
- [ ] Redis failure test.
- [ ] NATS failure test.
- [ ] MQTT failure test.

---

# 5 — SECURITY

- [ ] Production JWT secret management.
- [ ] Token rotation.
- [ ] Refresh token strategy.
- [ ] Device/session revocation.
- [ ] Admin MFA.
- [ ] Role-based access control.
- [ ] Permission matrix.
- [ ] Database least privilege.
- [ ] Ledger UPDATE/DELETE protection.
- [ ] Audit log immutability.
- [ ] Webhook signature verification.
- [ ] Replay attack protection.
- [ ] Rate limiting.
- [ ] Abuse protection.
- [ ] PII encryption.
- [ ] Document encryption.
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
- [ ] PII access audit.
- [ ] User data export.
- [ ] Account deletion workflow.
- [ ] Consent management.
- [ ] Privacy policy.
- [ ] Terms of service.
- [ ] Driver terms.
- [ ] Corporate terms.
- [ ] Payment records retention.
- [ ] Audit retention.

---

# 7 — TESTING

- [ ] Unit tests.
- [ ] Integration tests.
- [ ] PostgreSQL tests.
- [ ] Redis tests.
- [ ] NATS tests.
- [ ] MQTT tests.
- [ ] Routing tests.
- [ ] API contract tests.
- [ ] Mobile integration tests.
- [ ] End-to-end rider flow.
- [ ] End-to-end driver flow.
- [ ] Payment end-to-end.
- [ ] Cancellation end-to-end.
- [ ] KYC end-to-end.
- [ ] Fraud regression tests.
- [ ] Safety regression tests.
- [ ] Race tests.
- [ ] Load tests.
- [ ] Chaos tests.
- [ ] Security tests.

---

# 8 — RELEASE CHECKLIST

## Pilot 100 drivers

- [ ] Rider App usable.
- [ ] Driver App usable.
- [ ] Realtime stable.
- [ ] Payment stable.
- [ ] Support available.
- [ ] Safety operational.
- [ ] Control Tower operational.
- [ ] Driver earnings correct.
- [ ] Ledger balance verified.
- [ ] No duplicate trips.
- [ ] No lost trips.

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
- [Safety incidents.

---

# 10 — ƯU TIÊN THỰC THI

## Sprint 1

- [ ] Flutter Driver App skeleton.
- [ ] Flutter Rider App skeleton.
- [ ] MQTT production path.
- [ ] Redis GEO.
- [ ] Realtime driver location.
- [ ] Realtime offer.
- [ ] OSRM.
- [ ] Push notification.

## Sprint 2

- [ ] End-to-end booking.
- [ ] Driver accept.
- [ ] Pickup.
- [ ] Start.
- [ ] Complete.
- [ ] Fare.
- [ ] Payment.
- [ ] Rating.
- [ ] Trip history.

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
- [ ] Không duplicate assignment.
- [ ] Không mất trip khi process chết.
- [ ] Payment idempotent.
- [ ] Ledger luôn cân bằng.
- [ ] Driver nhận đúng tiền.
- [ ] Công ty đối soát được tiền.
- [ ] Fraud có thể phát hiện và xử lý.
- [ ] SOS có operator xử lý.
- [ ] Admin có Control Tower.
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
