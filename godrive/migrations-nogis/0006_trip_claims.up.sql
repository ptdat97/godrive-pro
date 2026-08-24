-- 0006 — Khoá giành chuyến.
--
-- matching.Store.ClaimTrip là bất biến quan trọng nhất của toàn hệ thống: nó
-- quyết định trong nhiều tài xế cùng bấm "Nhận chuyến" thì ai thắng. Ở chế độ
-- bộ nhớ nó là một mutex, nên chỉ đúng khi chạy đúng một tiến trình.
--
-- Bảng này là bản dùng được ngay khi chưa có Redis. Production nên chuyển sang
-- `SET trip:{id}:claim NX EX 30`: khoá chuyến ghi rất nhiều và sống rất ngắn,
-- đúng loại tải mà Redis làm tốt hơn Postgres.
CREATE TABLE trip_claims (
    trip_id    TEXT PRIMARY KEY,
    driver_id  TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
-- Dọn khoá đã hết hạn.
CREATE INDEX trip_claims_expiry_idx ON trip_claims (expires_at);
