-- 0005 — Thống kê tài xế: lưu SỐ ĐẾM, không lưu tỉ lệ.
--
-- Trước migration này, rating / acceptance_rate / cancel_rate / completed_trips
-- không có một dòng code nào ghi vào sau khi tạo hồ sơ. Mọi tài xế vĩnh viễn
-- rating 5.00 và acceptance 0.800, nên ba trong năm thành phần của hàm chấm
-- điểm là hằng số — matching thực chất chỉ xếp theo ETA và góc lệch hướng.
--
-- Vì sao lưu số đếm chứ không lưu tỉ lệ:
--   1. Tỉ lệ suy ra được từ số đếm, số đếm KHÔNG suy ra được từ tỉ lệ.
--   2. Chỉ có một nguồn sự thật nên không thể lệch pha như cột cache.
--   3. Cộng dồn số đếm là phép nguyên tử trong một câu UPDATE; đọc-sửa-ghi
--      một tỉ lệ thì không.
-- Ba cột tỉ lệ cũ bị bỏ: giữ lại chúng với giá trị mặc định vĩnh viễn còn tệ
-- hơn, vì câu truy vấn phân tích đầu tiên sẽ tin vào chúng.

ALTER TABLE drivers ADD COLUMN offers_received INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drivers ADD COLUMN offers_accepted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drivers ADD COLUMN trips_cancelled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drivers ADD COLUMN rating_sum      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drivers ADD COLUMN rating_count    INTEGER NOT NULL DEFAULT 0;

-- idle_since: thời điểm tài xế BẮT ĐẦU rảnh.
--
-- Hàm chấm điểm ưu tiên tài xế chờ lâu để thu nhập phân bổ đều hơn — yếu tố giữ
-- chân tài xế quan trọng ở VN. Trước đây nó dùng độ cũ của ping vị trí, tức là
-- vô tình THƯỞNG cho tài xế có mạng kém.
ALTER TABLE drivers ADD COLUMN idle_since TIMESTAMPTZ;

ALTER TABLE drivers DROP COLUMN rating;
ALTER TABLE drivers DROP COLUMN acceptance_rate;
ALTER TABLE drivers DROP COLUMN cancel_rate;

-- Điểm đánh giá khách chấm cho chuyến, 1..5. NULL = chưa chấm.
ALTER TABLE trips ADD COLUMN rating SMALLINT CHECK (rating IS NULL OR rating BETWEEN 1 AND 5);
