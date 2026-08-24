-- === BIẾN THỂ KHÔNG CẦN POSTGIS ===
-- Sinh tự động từ migrations/0001_init.up.sql — ĐỪNG sửa tay file này.
-- Chạy lại: python3 scripts/gen-nogis.py
--
-- Bản Postgres của DBngin không kèm extension không gian. Khác biệt duy nhất
-- so với schema chính: driver_locations dùng hai cột lat/lng DOUBLE PRECISION
-- thay cho kiểu GEOGRAPHY, và b-tree thay cho index GIST.
--
-- Chấp nhận được ở dev vì vị trí nóng thực tế nằm ở Redis GEO; bảng này chỉ
-- giữ ảnh chụp mới nhất. KHÔNG dùng biến thể này ở production — truy vấn lân
-- cận theo bán kính cần index GIST để có hiệu năng.

-- Lược đồ khởi tạo. Chạy bằng golang-migrate:
--   migrate -path migrations -database "$DATABASE_URL" up
--
-- Ghi chú tuân thủ: theo Nghị định 13/2023/NĐ-CP, dữ liệu cá nhân của người
-- dùng Việt Nam phải được lưu trữ trong lãnh thổ VN. Cụm CSDL nên đặt tại
-- VNG Cloud / Viettel IDC / FPT Cloud hoặc vùng có bản sao trong nước.


-- ============================ TÀI KHOẢN ============================
CREATE TABLE accounts (
    id          TEXT PRIMARY KEY,
    phone       TEXT        NOT NULL,
    full_name   TEXT        NOT NULL DEFAULT '',
    role        TEXT        NOT NULL CHECK (role IN ('rider','driver','admin')),
    blocked     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (phone, role)
);

-- ============================= TÀI XẾ ==============================
CREATE TABLE drivers (
    id              TEXT PRIMARY KEY,
    account_id      TEXT        NOT NULL UNIQUE REFERENCES accounts(id),
    full_name       TEXT        NOT NULL,
    phone           TEXT        NOT NULL,
    city            TEXT        NOT NULL DEFAULT 'HCM',

    vehicle_type    TEXT        NOT NULL CHECK (vehicle_type IN ('BIKE','CAR_4','CAR_7')),
    vehicle_plate   TEXT        NOT NULL,
    vehicle_model   TEXT        NOT NULL DEFAULT '',
    vehicle_color   TEXT        NOT NULL DEFAULT '',

    -- Giấy tờ bắt buộc theo Nghị định 10/2020. Cân nhắc mã hoá ở tầng ứng dụng.
    national_id     TEXT        NOT NULL,
    driver_license  TEXT        NOT NULL,
    vehicle_reg_no  TEXT        NOT NULL DEFAULT '',
    kyc_state       TEXT        NOT NULL DEFAULT 'PENDING'
                                CHECK (kyc_state IN ('PENDING','APPROVED','REJECTED')),

    status          TEXT        NOT NULL DEFAULT 'OFFLINE'
                                CHECK (status IN ('OFFLINE','IDLE','ASSIGNED','ON_TRIP','SUSPENDED')),
    rating          NUMERIC(3,2) NOT NULL DEFAULT 5.00,
    completed_trips INTEGER     NOT NULL DEFAULT 0,
    acceptance_rate NUMERIC(4,3) NOT NULL DEFAULT 0.800,
    cancel_rate     NUMERIC(4,3) NOT NULL DEFAULT 0.000,

    -- Số dư ví tính bằng ĐỒNG (BIGINT). Âm = tài xế đang nợ chiết khấu.
    wallet_balance  BIGINT      NOT NULL DEFAULT 0,
    version         INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX drivers_plate_uidx ON drivers (vehicle_plate);
-- Index một phần: dispatcher chỉ quan tâm tài xế đang rảnh.
CREATE INDEX drivers_idle_idx ON drivers (city, vehicle_type) WHERE status = 'IDLE';

-- ===================== VỊ TRÍ TÀI XẾ (nóng) =======================
-- Bảng này ghi rất nhiều. Nguồn nhanh thực tế là Redis GEO; Postgres chỉ
-- giữ ảnh chụp mới nhất để phục vụ truy vấn phân tích và khôi phục sự cố.
CREATE TABLE driver_locations (
    driver_id   TEXT PRIMARY KEY REFERENCES drivers(id),
    lat         DOUBLE PRECISION NOT NULL,
    lng         DOUBLE PRECISION NOT NULL,
    bearing_deg REAL        NOT NULL DEFAULT 0,
    speed_mps   REAL        NOT NULL DEFAULT 0,
    battery_pc  SMALLINT    NOT NULL DEFAULT 100,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX driver_locations_pos_idx ON driver_locations (lat, lng);

-- ============================ CHUYẾN ĐI ============================
CREATE TABLE trips (
    id              TEXT PRIMARY KEY,
    rider_id        TEXT        NOT NULL REFERENCES accounts(id),
    driver_id       TEXT        REFERENCES drivers(id),

    pickup_lat      DOUBLE PRECISION NOT NULL,
    pickup_lng      DOUBLE PRECISION NOT NULL,
    pickup_address  TEXT        NOT NULL DEFAULT '',
    pickup_note     TEXT        NOT NULL DEFAULT '',
    dropoff_lat     DOUBLE PRECISION NOT NULL,
    dropoff_lng     DOUBLE PRECISION NOT NULL,
    dropoff_address TEXT        NOT NULL DEFAULT '',
    dropoff_note    TEXT        NOT NULL DEFAULT '',

    vehicle_type    TEXT        NOT NULL,
    quote_id        TEXT        NOT NULL,
    fare            BIGINT      NOT NULL,
    platform_fee    BIGINT      NOT NULL,
    driver_earn     BIGINT      NOT NULL,
    payment_method  TEXT        NOT NULL
                                CHECK (payment_method IN ('CASH','MOMO','ZALOPAY','VNPAY','WALLET')),

    status          TEXT        NOT NULL
                                CHECK (status IN ('CREATED','SEARCHING','ASSIGNED','ARRIVED',
                                                  'IN_PROGRESS','COMPLETED','PAID','CANCELLED','EXPIRED')),
    cancel_by       TEXT,
    cancel_reason   TEXT        NOT NULL DEFAULT '',

    requested_at    TIMESTAMPTZ NOT NULL,
    assigned_at     TIMESTAMPTZ,
    arrived_at      TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    version         INTEGER     NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX trips_rider_idx  ON trips (rider_id, requested_at DESC);
CREATE INDEX trips_driver_idx ON trips (driver_id, requested_at DESC);
-- Dispatcher quét bảng này liên tục -> index một phần cho trạng thái SEARCHING.
CREATE INDEX trips_searching_idx ON trips (requested_at) WHERE status = 'SEARCHING';
-- Một tài xế chỉ được có tối đa một chuyến đang hoạt động.
CREATE UNIQUE INDEX trips_one_active_per_driver
    ON trips (driver_id)
    WHERE status IN ('ASSIGNED','ARRIVED','IN_PROGRESS');

-- Nhật ký chuyển trạng thái, CHỈ THÊM MỚI.
-- Lưu tối thiểu 3 năm để phục vụ hợp đồng vận tải điện tử và khiếu nại.
CREATE TABLE trip_events (
    id          TEXT PRIMARY KEY,
    trip_id     TEXT        NOT NULL REFERENCES trips(id),
    from_status TEXT        NOT NULL,
    to_status   TEXT        NOT NULL,
    actor       TEXT        NOT NULL,
    meta        JSONB       NOT NULL DEFAULT '{}',
    at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX trip_events_trip_idx ON trip_events (trip_id, at);

-- ========================== GHÉP CHUYẾN ===========================
CREATE TABLE offers (
    id                TEXT PRIMARY KEY,
    trip_id           TEXT        NOT NULL REFERENCES trips(id),
    driver_id         TEXT        NOT NULL REFERENCES drivers(id),
    round             SMALLINT    NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL DEFAULT 'PENDING'
                                  CHECK (status IN ('PENDING','ACCEPTED','REJECTED','EXPIRED','LOST')),
    eta_sec           REAL        NOT NULL DEFAULT 0,
    pickup_distance_m REAL        NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX offers_driver_pending_idx ON offers (driver_id) WHERE status = 'PENDING';
-- Chốt chặn cuối cùng: mỗi chuyến chỉ có duy nhất một lời mời được chấp nhận.
CREATE UNIQUE INDEX offers_one_accepted_per_trip ON offers (trip_id) WHERE status = 'ACCEPTED';

-- ============================= SỔ CÁI =============================
-- Sổ kép: mỗi giao dịch gồm nhiều bút toán, TỔNG amount_vnd = 0.
-- Bảng chỉ INSERT: không bao giờ UPDATE/DELETE một bút toán đã ghi.
CREATE TABLE ledger_entries (
    id           TEXT PRIMARY KEY,
    tx_id        TEXT        NOT NULL,
    account_id   TEXT        NOT NULL,
    account_type TEXT        NOT NULL
                             CHECK (account_type IN ('DRIVER_WALLET','DRIVER_CASH','RIDER_WALLET',
                                                     'PLATFORM_REVENUE','PROMO_EXPENSE','TAX_PAYABLE',
                                                     'GATEWAY_CLEARING')),
    amount_vnd   BIGINT      NOT NULL,
    ref_type     TEXT        NOT NULL,
    ref_id       TEXT        NOT NULL,
    memo         TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ledger_tx_idx      ON ledger_entries (tx_id);
CREATE INDEX ledger_account_idx ON ledger_entries (account_id, account_type, created_at);

-- Bảng khoá idempotency cho giao dịch: chống ghi sổ hai lần khi worker retry.
CREATE TABLE ledger_transactions (
    tx_id      TEXT PRIMARY KEY,
    ref_type   TEXT        NOT NULL,
    ref_id     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ======================== IDEMPOTENCY API =========================
CREATE TABLE idempotency_keys (
    key        TEXT PRIMARY KEY,
    response   BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idempotency_expiry_idx ON idempotency_keys (expires_at);

-- ====================== TRANSACTIONAL OUTBOX ======================
CREATE TABLE outbox (
    id           TEXT PRIMARY KEY,
    topic        TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    attempts     SMALLINT    NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
