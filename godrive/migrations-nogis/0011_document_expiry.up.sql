-- 0011 — Hạn GPLX và hạn đăng kiểm, cạnh hạn bảo hiểm đã có từ 0002.
--
-- Tài xế chạy với giấy tờ hết hạn là rủi ro pháp lý thuộc về nền tảng. Trước
-- migration này chỉ hạn bảo hiểm được lưu — và không có gì đọc nó.
--
-- NULL nghĩa là "không có hạn" hoặc "chưa khai": GPLX hạng A1 (xe máy) không
-- có thời hạn, và xe máy không phải đăng kiểm định kỳ. Vì vậy NULL không chặn
-- nhận chuyến; chỉ ngày ĐÃ QUA mới chặn.
ALTER TABLE drivers ADD COLUMN license_until DATE;
ALTER TABLE drivers ADD COLUMN inspection_until DATE;
