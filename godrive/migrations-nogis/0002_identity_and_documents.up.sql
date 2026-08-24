-- 0002 — Mở khoá chế độ Postgres cho module identity, và lưu đủ giấy tờ tài xế.
--
-- Trước migration này, app.New() luôn dùng identity.NewMemoryRepo() nên bảng
-- accounts không bao giờ được ghi. Vì drivers.account_id và trips.rider_id đều
-- là khoá ngoại trỏ tới accounts, cả luồng đăng ký tài xế lẫn luồng đặt chuyến
-- đều hỏng ở chế độ Postgres.

-- ========================= OTP CHALLENGE ==========================
-- Redis hợp hơn cho dữ liệu TTL 5 phút, ghi/xoá liên tục. Bảng này là đường
-- dự phòng cho môi trường chưa có Redis; job dọn chạy trong worker nền.
CREATE TABLE otp_challenges (
    id         TEXT PRIMARY KEY,
    phone      TEXT        NOT NULL,
    role       TEXT        NOT NULL CHECK (role IN ('rider','driver','admin')),
    -- Chỉ lưu SHA-256 của (phone + ":" + code). Mã thô không bao giờ chạm đĩa.
    code_hash  TEXT        NOT NULL,
    attempts   SMALLINT    NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Job dọn quét theo hạn; cũng phục vụ việc đếm challenge còn sống.
CREATE INDEX otp_challenges_expiry_idx ON otp_challenges (expires_at);

-- ========================= GIẤY TỜ TÀI XẾ =========================
-- Bảo hiểm TNDS bắt buộc theo Nghị định 10/2020. Hai trường này đã có trong
-- driver.Documents từ đầu nhưng chưa bao giờ được lưu xuống.
--
-- insurance_until là DATE (không phải TEXT) để job cảnh báo sắp hết hạn ở
-- giai đoạn sau truy vấn được trực tiếp.
ALTER TABLE drivers ADD COLUMN insurance_no    TEXT NOT NULL DEFAULT '';
ALTER TABLE drivers ADD COLUMN insurance_until DATE;
