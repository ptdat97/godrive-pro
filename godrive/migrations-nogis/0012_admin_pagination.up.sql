-- 0012 — Chỉ mục cho phân trang keyset ở bảng điều khiển (T-26).
--
-- Danh sách tài xế xếp theo (created_at, id) giảm dần, danh sách chuyến theo
-- (requested_at, id) giảm dần. Không có chỉ mục khớp thứ tự thì mỗi trang là
-- một lần sắp xếp TOÀN BẢNG — trang thứ 200 cũng tốn như trang đầu.
CREATE INDEX drivers_created_page_idx ON drivers (created_at DESC, id DESC);
CREATE INDEX trips_requested_page_idx ON trips (requested_at DESC, id DESC);

-- Đếm chuyến ĐANG diễn ra cho trang tổng quan mà không quét cả bảng. Chỉ mục
-- một phần nên nhỏ: chỉ chứa chuyến chưa xong, vốn luôn ít.
CREATE INDEX trips_live_status_idx ON trips (status)
    WHERE status IN ('SEARCHING','ASSIGNED','ARRIVED','IN_PROGRESS','COMPLETED');
