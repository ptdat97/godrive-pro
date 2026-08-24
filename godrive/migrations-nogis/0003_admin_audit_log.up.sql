-- 0003 — Nhật ký thao tác quản trị.
--
-- ReviewKYC là hành động GHI duy nhất của module admin, và trước migration này
-- nó không để lại dấu vết nào: không biết ai duyệt, lúc nào, hồ sơ nào. Đối
-- soát nội bộ và xử lý tranh chấp đều cần thông tin đó.
--
-- Bảng CHỈ THÊM MỚI, giống trip_events và ledger_entries. Role ứng dụng không
-- được cấp quyền UPDATE/DELETE trên bảng này.
CREATE TABLE admin_audit_log (
    id          TEXT PRIMARY KEY,
    -- Tài khoản quản trị viên thực hiện thao tác (accounts.id).
    actor_id    TEXT        NOT NULL,
    actor_phone TEXT        NOT NULL DEFAULT '',
    action      TEXT        NOT NULL,   -- review_kyc | ...
    target_type TEXT        NOT NULL,   -- driver | trip | ...
    target_id   TEXT        NOT NULL,
    payload     JSONB       NOT NULL DEFAULT '{}',
    at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Hai câu hỏi vận hành hay gặp: "hồ sơ này ai đã đụng vào" và "người này đã làm gì".
CREATE INDEX admin_audit_target_idx ON admin_audit_log (target_type, target_id, at DESC);
CREATE INDEX admin_audit_actor_idx  ON admin_audit_log (actor_id, at DESC);
CREATE INDEX admin_audit_at_idx     ON admin_audit_log (at DESC);
