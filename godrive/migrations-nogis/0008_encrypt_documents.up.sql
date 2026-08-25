-- 0008 — Mã hoá giấy tờ tài xế ở tầng ứng dụng.
--
-- Số CCCD và GPLX là dữ liệu cá nhân mà Nghị định 13/2023 yêu cầu bảo vệ, và
-- một lần lộ là không thu hồi được. Mã hoá đĩa không đủ: nó bảo vệ khi ai đó
-- lấy được ổ cứng, chứ không bảo vệ khi bản sao lưu bị lộ hoặc khi ai đó đọc
-- được cơ sở dữ liệu.
--
-- Cột giữ nguyên kiểu TEXT — bản mã là base64 có tiền tố "enc:v1:". Nhờ tiền tố
-- này mà dữ liệu cũ chưa mã hoá vẫn đọc được trong lúc chuyển đổi dần.
--
-- Bản mã dài hơn bản rõ nên cột phải đủ rộng; TEXT của Postgres không giới hạn
-- nên không cần đổi gì.

-- Chỉ mục mù để trả lời "số CCCD này đã đăng ký chưa" mà không lưu số gốc.
--
-- Bản mã GCM có nonce ngẫu nhiên nên không so khớp trực tiếp được. HMAC tất định
-- giải quyết: cùng đầu vào cho cùng chỉ mục, mà từ chỉ mục không suy ngược ra
-- được số gốc.
ALTER TABLE drivers ADD COLUMN national_id_idx TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX drivers_national_id_uidx
    ON drivers (national_id_idx) WHERE national_id_idx <> '';

COMMENT ON COLUMN drivers.national_id IS
    'Số CCCD, MÃ HOÁ ở tầng ứng dụng (AES-256-GCM). Không truy vấn trực tiếp cột này.';
COMMENT ON COLUMN drivers.driver_license IS
    'Số GPLX, MÃ HOÁ ở tầng ứng dụng (AES-256-GCM).';
COMMENT ON COLUMN drivers.national_id_idx IS
    'Chỉ mục mù (HMAC-SHA256) của số CCCD — dùng để kiểm trùng, không giải ngược được.';
