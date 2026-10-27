-- 0010 — Bảng chỉ-thêm-mới phải được CSDL giữ, không chỉ được code giữ.
--
-- Năm bảng dưới đây là lịch sử: sổ cái tiền, nhật ký chuyển trạng thái chuyến,
-- nhật ký thao tác quản trị, lịch sử đổi cấu hình. Trước migration này bất biến
-- "không bao giờ sửa, không bao giờ xoá" chỉ là quy ước trong mã nguồn — một câu
-- UPDATE chạy tay, một lỗi trong code, hay một tài khoản bị lộ là đủ để viết lại
-- lịch sử tài chính mà không để lại dấu vết.
--
-- Hai lớp, vì mỗi lớp chặn một kiểu sai khác nhau:
--
--   1. ROLE godrive_app — role ứng dụng KHÔNG có quyền UPDATE/DELETE/TRUNCATE
--      trên các bảng này. Ứng dụng phải nối bằng một user thuộc role này, không
--      phải bằng chủ sở hữu bảng. Xem docs/08 §8.3.
--   2. TRIGGER — chặn cả chủ sở hữu bảng. Lớp này tồn tại vì lớp 1 phụ thuộc
--      vào việc người triển khai nhớ dùng đúng user; quên một lần là lớp 1 vô
--      dụng mà không có gì báo. Superuser vẫn tắt được trigger — nhưng phải làm
--      một cách CỐ Ý, không thể lỡ tay.
--
-- Muốn sửa dữ liệu lịch sử (chỉ khi có quyết định bằng văn bản), superuser
-- chạy trong một transaction: SET LOCAL session_replication_role = replica;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'godrive_app') THEN
        CREATE ROLE godrive_app NOLOGIN;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO godrive_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO godrive_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO godrive_app;

-- Bảng của chính công cụ migration: ứng dụng chỉ được đọc.
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON schema_migrations FROM godrive_app;

-- ledger_transactions là khoá idempotency của sổ cái: xoá một dòng là mở đường
-- cho cùng giao dịch được ghi sổ LẦN HAI. settings_history là bằng chứng ai đổi
-- biểu giá lúc nào — sửa được nó thì nó không còn là bằng chứng.
REVOKE UPDATE, DELETE, TRUNCATE ON
    ledger_entries, ledger_transactions, trip_events, admin_audit_log, settings_history
FROM godrive_app;

-- Bảng tạo bởi migration sau này tự cấp quyền thường cho role ứng dụng. Bảng
-- chỉ-thêm-mới mới thì migration đó phải tự REVOKE như trên.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO godrive_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO godrive_app;

CREATE FUNCTION forbid_history_rewrite() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'bảng % chỉ được thêm mới, không được %', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'insufficient_privilege',
              HINT = 'Ghi một bút toán/sự kiện bù trừ thay vì sửa lịch sử.';
END
$$;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['ledger_entries', 'ledger_transactions', 'trip_events',
                             'admin_audit_log', 'settings_history']
    LOOP
        EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON %I
                        FOR EACH ROW EXECUTE FUNCTION forbid_history_rewrite()',
                       t || '_append_only', t);
        EXECUTE format('CREATE TRIGGER %I BEFORE TRUNCATE ON %I
                        FOR EACH STATEMENT EXECUTE FUNCTION forbid_history_rewrite()',
                       t || '_no_truncate', t);
    END LOOP;
END
$$;
