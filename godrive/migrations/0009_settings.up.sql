-- 0009 — Cấu hình nghiệp vụ chỉnh được từ bảng điều khiển.
--
-- Trước migration này, biểu giá, trọng số chấm điểm, bậc thang surge, hạn mức
-- công nợ... đều là hằng số trong mã nguồn. Đổi một con số nghĩa là sửa code,
-- biên dịch lại và triển khai lại — vận hành không tự làm được, mà đây đúng là
-- những thứ vận hành phải điều chỉnh thường xuyên theo thị trường.
--
-- Lưu theo NHÓM (mỗi nhóm một tài liệu JSON) chứ không theo từng khoá rời:
-- các giá trị trong một nhóm ràng buộc lẫn nhau (bán kính vòng đầu phải nhỏ hơn
-- bán kính tối đa; bậc thang surge phải tăng dần), nên chúng phải được đọc,
-- kiểm tra và ghi như một khối.
CREATE TABLE settings (
    -- Tên nhóm: pricing | surge | matching | wallet | location
    key        TEXT PRIMARY KEY,
    value      JSONB       NOT NULL,
    -- version tăng mỗi lần ghi, dùng cho khoá lạc quan: hai quản trị viên cùng
    -- sửa một nhóm thì người sau phải đọc lại thay vì ghi đè âm thầm.
    version    INTEGER     NOT NULL DEFAULT 1,
    updated_by TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lịch sử thay đổi, CHỈ THÊM MỚI.
--
-- admin_audit_log đã ghi "ai đổi cái gì lúc nào", nhưng bảng này giữ nguyên vẹn
-- GIÁ TRỊ TRƯỚC và SAU. Khi khách khiếu nại giá của một chuyến ba tháng trước,
-- phải trả lời được "lúc đó biểu giá là gì" — chứ không phải biểu giá hôm nay.
CREATE TABLE settings_history (
    id         TEXT PRIMARY KEY,
    key        TEXT        NOT NULL,
    version    INTEGER     NOT NULL,
    old_value  JSONB,
    new_value  JSONB       NOT NULL,
    changed_by TEXT        NOT NULL DEFAULT '',
    reason     TEXT        NOT NULL DEFAULT '',
    at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX settings_history_key_idx ON settings_history (key, at DESC);
