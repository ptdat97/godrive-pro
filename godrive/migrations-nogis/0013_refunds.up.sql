-- 0013 — Yêu cầu hoàn tiền (T-36).
--
-- Bảng này là QUY TRÌNH (ai xin, ai duyệt, lúc nào), không phải tiền. Tiền nằm
-- ở ledger_entries dưới mã giao dịch tx_refund_<id> — bút toán đảo mới, không
-- sửa bút toán cũ của chuyến.
CREATE TABLE refunds (
    id           TEXT PRIMARY KEY,
    trip_id      TEXT        NOT NULL REFERENCES trips(id),
    rider_id     TEXT        NOT NULL,
    driver_id    TEXT        NOT NULL DEFAULT '',
    amount_vnd   BIGINT      NOT NULL CHECK (amount_vnd > 0),
    reason       TEXT        NOT NULL,
    charge_to    TEXT        NOT NULL CHECK (charge_to IN ('PLATFORM','DRIVER')),
    status       TEXT        NOT NULL CHECK (status IN ('PENDING','APPROVED','REJECTED')),
    requested_by TEXT        NOT NULL,
    decided_by   TEXT        NOT NULL DEFAULT '',
    decision_note TEXT       NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    decided_at   TIMESTAMPTZ,
    -- self_approved: khoản dưới ngưỡng, người xin tự duyệt luôn. Ngưỡng là cấu
    -- hình động nên CSDL không kiểm được nó — service kiểm.
    self_approved BOOLEAN    NOT NULL DEFAULT FALSE,
    -- Khoản trên ngưỡng: người duyệt PHẢI khác người xin. Chốt ở CSDL chứ
    -- không chỉ ở code: đây là kiểm soát chống gian lận nội bộ, không được
    -- phụ thuộc vào việc mọi đường code đều nhớ kiểm tra.
    CHECK (status <> 'APPROVED' OR self_approved OR (decided_by <> '' AND decided_by <> requested_by))
);
CREATE INDEX refunds_trip_idx ON refunds (trip_id);
CREATE INDEX refunds_pending_idx ON refunds (created_at) WHERE status = 'PENDING';
