-- 0014 — Token thông báo đẩy của thiết bị (T-33, FCM cho cả Android và iOS).
--
-- Một tài khoản nhiều máy; một máy (device_id trong token phiên) đúng một
-- token FCM — app cài lại hay FCM xoay token thì token mới THAY token cũ của
-- máy đó, không cộng thêm. Token chết (FCM trả UNREGISTERED) bị xoá ngay khi
-- gửi hỏng, nên bảng không phình theo số lần cài app.
CREATE TABLE device_tokens (
    token       TEXT PRIMARY KEY,
    account_id  TEXT        NOT NULL,
    device_id   TEXT        NOT NULL,
    platform    TEXT        NOT NULL CHECK (platform IN ('android','ios')),
    updated_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (account_id, device_id)
);
CREATE INDEX device_tokens_account_idx ON device_tokens (account_id);
