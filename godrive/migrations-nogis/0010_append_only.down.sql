DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['ledger_entries', 'ledger_transactions', 'trip_events',
                             'admin_audit_log', 'settings_history']
    LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_append_only', t);
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', t || '_no_truncate', t);
    END LOOP;
END
$$;
DROP FUNCTION IF EXISTS forbid_history_rewrite();

-- Gỡ quyền nhưng KHÔNG xoá role: user đăng nhập của ứng dụng đang là thành
-- viên của nó, và xoá role giữa lúc đang chạy là cắt kết nối của ứng dụng.
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM godrive_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON SEQUENCES FROM godrive_app;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM godrive_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM godrive_app;
REVOKE USAGE ON SCHEMA public FROM godrive_app;
