package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// appendOnlyTables là các bảng lịch sử: chỉ thêm mới, không bao giờ sửa hay
// xoá. Migration 0010 khoá chúng ở CSDL; danh sách này phải khớp với đó.
var appendOnlyTables = []string{
	"ledger_entries", "ledger_transactions", "trip_events", "admin_audit_log", "settings_history",
}

// checkAppendOnlyGuard kiểm rằng tiến trình đang nối CSDL bằng một role KHÔNG
// sửa được lịch sử, và trigger chặn vẫn còn bật.
//
// Vì sao kiểm lúc khởi động thay vì chỉ ghi vào tài liệu: dùng nhầm user chủ
// sở hữu bảng không gây ra lỗi nào — mọi thứ chạy y hệt, chỉ có cánh cửa sửa
// sổ cái lặng lẽ mở ra. Thứ không gây lỗi thì không ai phát hiện.
//
// strict (production): trả lỗi để tiến trình không khởi động. Ngược lại chỉ
// ghi cảnh báo — máy phát triển nối bằng superuser là chuyện bình thường.
//
// Không truy vấn được (CSDL chưa lên) thì chỉ cảnh báo: chặn khởi động vì một
// phép kiểm phụ là biến sự cố mạng thoáng qua thành vòng khởi động lại vô tận.
func checkAppendOnlyGuard(ctx context.Context, db *sql.DB, log *slog.Logger, strict bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var user string
	var writable []string
	rows, err := db.QueryContext(ctx, `
        SELECT current_user, t
        FROM unnest($1::text[]) AS t
        WHERE to_regclass(t) IS NOT NULL
          AND (has_table_privilege(t, 'UPDATE')
            OR has_table_privilege(t, 'DELETE')
            OR has_table_privilege(t, 'TRUNCATE'))`, appendOnlyTables)
	if err != nil {
		log.Warn("không kiểm được quyền trên bảng chỉ-thêm-mới", "err", err)
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&user, &t); err != nil {
			log.Warn("không kiểm được quyền trên bảng chỉ-thêm-mới", "err", err)
			return nil
		}
		writable = append(writable, t)
	}
	if err := rows.Err(); err != nil {
		log.Warn("không kiểm được quyền trên bảng chỉ-thêm-mới", "err", err)
		return nil
	}

	// Trigger là lớp chặn thứ hai, giữ cả khi role sai. 'D' = đã bị tắt.
	var triggers int
	if err := db.QueryRowContext(ctx, `
        SELECT count(*) FROM pg_trigger
        WHERE tgname LIKE '%\_append\_only' AND tgenabled <> 'D'`).Scan(&triggers); err != nil {
		log.Warn("không kiểm được trigger chặn sửa lịch sử", "err", err)
		return nil
	}

	var problems []string
	if len(writable) > 0 {
		problems = append(problems, fmt.Sprintf(
			"role %q sửa/xoá được %s — nối bằng user thuộc role godrive_app (docs/08 §8.3)",
			user, strings.Join(writable, ", ")))
	}
	if triggers < len(appendOnlyTables) {
		problems = append(problems, fmt.Sprintf(
			"chỉ %d/%d trigger chặn sửa lịch sử đang bật — chưa chạy migration 0010 hoặc có người đã tắt",
			triggers, len(appendOnlyTables)))
	}
	if len(problems) == 0 {
		return nil
	}
	msg := "bảng lịch sử KHÔNG được bảo vệ: " + strings.Join(problems, "; ")
	if strict {
		return fmt.Errorf("%s", msg)
	}
	log.Warn(msg)
	return nil
}
