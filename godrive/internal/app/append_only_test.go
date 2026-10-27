package app

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/example/godrive/internal/platform/logger"
)

// pgCode trả mã lỗi SQLSTATE, rỗng nếu không phải lỗi của Postgres.
func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

const sqlInsufficientPrivilege = "42501"

// T-27 / AC §5: role ứng dụng KHÔNG sửa, xoá hay làm rỗng được bảng lịch sử.
//
// Kiểm trên chính kết nối của app chứ không SET ROLE bằng tay: thứ cần chứng
// minh là "ứng dụng như đang chạy không viết lại được lịch sử".
func TestAppRoleCannotRewriteHistory(t *testing.T) {
	a, _ := newPostgresApp(t)
	ctx := context.Background()

	var who string
	if err := a.db.QueryRowContext(ctx, "SELECT current_user").Scan(&who); err != nil {
		t.Fatal(err)
	}
	if who != "godrive_app" {
		t.Fatalf("app trong test phải nối bằng godrive_app, đang là %q — test này sẽ không chứng minh được gì", who)
	}

	// Ghi thêm thì vẫn phải được: khoá nhầm INSERT là hỏng cả hệ thống.
	if _, err := a.db.ExecContext(ctx, `INSERT INTO ledger_entries
        (id, tx_id, account_id, account_type, amount_vnd, ref_type, ref_id)
        VALUES ('led_t27', 'tx_t27', 'drv_t27', 'DRIVER_WALLET', 1000, 'test', 't27')`); err != nil {
		t.Fatalf("role ứng dụng phải ghi thêm được bút toán: %v", err)
	}

	// Khoá chính của từng bảng — chỉ để dựng một câu UPDATE hợp lệ về cú pháp.
	pk := map[string]string{"ledger_transactions": "tx_id"}
	for _, tb := range appendOnlyTables {
		col := pk[tb]
		if col == "" {
			col = "id"
		}
		for _, stmt := range []string{
			"UPDATE " + tb + " SET " + col + " = " + col,
			"DELETE FROM " + tb,
			"TRUNCATE " + tb,
		} {
			_, err := a.db.ExecContext(ctx, stmt)
			if code := pgCode(err); code != sqlInsufficientPrivilege {
				t.Errorf("%s: phải bị từ chối với %s, được err=%v", stmt, sqlInsufficientPrivilege, err)
			}
		}
	}
	// Đúng câu trong TODO, trên đúng dòng vừa ghi.
	if _, err := a.db.ExecContext(ctx, "UPDATE ledger_entries SET amount_vnd = 0 WHERE id = 'led_t27'"); pgCode(err) != sqlInsufficientPrivilege {
		t.Fatalf("sửa số tiền một bút toán phải bị từ chối, được %v", err)
	}
}

// Lớp thứ hai: trigger chặn cả chủ sở hữu bảng và superuser — tức chặn được
// trường hợp người triển khai quên dùng đúng role.
func TestTriggerBlocksEvenSuperuser(t *testing.T) {
	_, db := newPostgresApp(t) // db của bộ test là superuser
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO ledger_entries
        (id, tx_id, account_id, account_type, amount_vnd, ref_type, ref_id)
        VALUES ('led_su', 'tx_su', 'drv_su', 'DRIVER_WALLET', 1000, 'test', 'su')`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE ledger_entries SET amount_vnd = 0 WHERE id = 'led_su'",
		"DELETE FROM ledger_entries WHERE id = 'led_su'",
		"TRUNCATE ledger_entries",
	} {
		if _, err := db.ExecContext(ctx, stmt); pgCode(err) != sqlInsufficientPrivilege {
			t.Errorf("%s: trigger phải chặn cả superuser, được %v", stmt, err)
		}
	}
	var amount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_vnd FROM ledger_entries WHERE id = 'led_su'").Scan(&amount); err != nil || amount != 1000 {
		t.Fatalf("bút toán phải còn nguyên 1000đ, được %d (%v)", amount, err)
	}
}

// Kiểm tra lúc khởi động: production nối bằng role sửa được lịch sử thì KHÔNG
// được chạy; nối bằng godrive_app thì chạy bình thường.
func TestStartupRefusesPrivilegedRoleInProduction(t *testing.T) {
	a, superDB := newPostgresApp(t)
	ctx := context.Background()
	log := logger.New("error", false)

	if err := checkAppendOnlyGuard(ctx, superDB, log, true); err == nil {
		t.Fatal("superuser sửa được sổ cái: production phải từ chối khởi động")
	}
	if err := checkAppendOnlyGuard(ctx, superDB, log, false); err != nil {
		t.Fatalf("ngoài production chỉ được cảnh báo, không chặn: %v", err)
	}
	if err := checkAppendOnlyGuard(ctx, a.db, log, true); err != nil {
		t.Fatalf("godrive_app là đúng cấu hình, không được chặn: %v", err)
	}

	// Trigger bị tắt cũng phải lộ ra — kể cả khi role đã đúng.
	disable := func(on bool) {
		verb := "DISABLE"
		if on {
			verb = "ENABLE"
		}
		if _, err := superDB.ExecContext(ctx, "ALTER TABLE trip_events "+verb+" TRIGGER trip_events_append_only"); err != nil {
			t.Fatal(err)
		}
	}
	disable(false)
	t.Cleanup(func() { disable(true) })
	if err := checkAppendOnlyGuard(ctx, a.db, log, true); err == nil {
		t.Fatal("trigger đã bị tắt mà kiểm tra khởi động không phát hiện")
	}
}
