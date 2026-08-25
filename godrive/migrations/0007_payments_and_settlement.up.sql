-- 0007 — Đường tiền vào và tiền ra.
--
-- Tới trước migration này, tiền chỉ "vào" ví bằng một endpoint dev tự ghi có,
-- và không có đường nào chi trả cho tài xế. Đây là hai lỗ hổng chặn phát hành
-- thương mại: một bên là máy in tiền, một bên là tài xế không rút được tiền.

-- ===================== GIAO DỊCH CỔNG THANH TOÁN =====================
--
-- Bảng này ghi Ý ĐỊNH thanh toán TRƯỚC khi cổng gọi webhook về.
--
-- Vì sao phải ghi trước: webhook nói "đơn X đã trả 500.000đ". Chữ ký chống được
-- việc giả mạo, nhưng KHÔNG chống được việc số tiền trong thông báo khác số tiền
-- mình thật sự yêu cầu. Có bản ghi ý định từ trước thì mới đối chiếu được.
CREATE TABLE payment_transactions (
    id            TEXT PRIMARY KEY,
    provider      TEXT        NOT NULL CHECK (provider IN ('MOMO','ZALOPAY','VNPAY','VIETQR')),
    -- order_id là mã đơn phía MÌNH, gửi sang cổng và nhận lại trong webhook.
    order_id      TEXT        NOT NULL,
    -- provider_tx_id là mã giao dịch phía CỔNG. NULL cho tới khi webhook về.
    provider_tx_id TEXT,
    account_id    TEXT        NOT NULL,
    purpose       TEXT        NOT NULL CHECK (purpose IN ('TOPUP','TRIP')),
    -- Số tiền YÊU CẦU. Webhook báo số khác thì từ chối.
    amount_vnd    BIGINT      NOT NULL CHECK (amount_vnd > 0),
    status        TEXT        NOT NULL DEFAULT 'PENDING'
                              CHECK (status IN ('PENDING','SUCCESS','FAILED','EXPIRED')),
    -- Nội dung webhook thô, giữ nguyên để đối soát và điều tra tranh chấp.
    raw_callback  JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at       TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ NOT NULL
);
-- Mã đơn phải duy nhất trong phạm vi một cổng.
CREATE UNIQUE INDEX payment_order_uidx ON payment_transactions (provider, order_id);
-- Chốt chặn chống ghi sổ hai lần khi cổng gửi lại webhook: một mã giao dịch
-- phía cổng chỉ được xử lý một lần.
CREATE UNIQUE INDEX payment_provider_tx_uidx
    ON payment_transactions (provider, provider_tx_id)
    WHERE provider_tx_id IS NOT NULL;
CREATE INDEX payment_pending_idx ON payment_transactions (expires_at) WHERE status = 'PENDING';
CREATE INDEX payment_account_idx ON payment_transactions (account_id, created_at DESC);

-- ========================= ĐỢT ĐỐI SOÁT & CHI TRẢ =========================
--
-- Chi trả cho tài xế phải chạy được LẠI mà không trả tiền hai lần. Cách bảo đảm:
-- mỗi bút toán chi trả mang settlement_batch_id, và một tài xế chỉ có một dòng
-- trong một đợt.
CREATE TABLE settlement_batches (
    id          TEXT PRIMARY KEY,
    -- Kỳ đối soát [period_start, period_end).
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'OPEN'
                            CHECK (status IN ('OPEN','CALCULATED','PAID','FAILED')),
    driver_count INTEGER    NOT NULL DEFAULT 0,
    total_vnd   BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at   TIMESTAMPTZ,
    CHECK (period_end > period_start)
);
-- Một kỳ chỉ có một đợt: chạy job hai lần cho cùng kỳ không tạo được đợt thứ hai.
CREATE UNIQUE INDEX settlement_period_uidx ON settlement_batches (period_start, period_end);

CREATE TABLE settlement_items (
    id         TEXT PRIMARY KEY,
    batch_id   TEXT        NOT NULL REFERENCES settlement_batches(id),
    driver_id  TEXT        NOT NULL REFERENCES drivers(id),
    -- Số dư ví tại thời điểm chốt. Dương = phải trả cho tài xế.
    amount_vnd BIGINT      NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'PENDING'
                           CHECK (status IN ('PENDING','PAID','SKIPPED','FAILED')),
    reason     TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at    TIMESTAMPTZ
);
-- Chốt chặn chống trả hai lần: một tài xế một dòng trong một đợt.
CREATE UNIQUE INDEX settlement_item_uidx ON settlement_items (batch_id, driver_id);
CREATE INDEX settlement_item_driver_idx ON settlement_items (driver_id, created_at DESC);

-- Nối bút toán với đợt đối soát đã sinh ra nó.
ALTER TABLE ledger_entries ADD COLUMN settlement_batch_id TEXT;
CREATE INDEX ledger_settlement_idx ON ledger_entries (settlement_batch_id)
    WHERE settlement_batch_id IS NOT NULL;
