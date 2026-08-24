package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/example/godrive/pkg/errs"
)

// PostgresAuditLog ghi nhật ký thao tác xuống bảng admin_audit_log.
type PostgresAuditLog struct{ db *sql.DB }

func NewPostgresAuditLog(db *sql.DB) *PostgresAuditLog { return &PostgresAuditLog{db: db} }

func (l *PostgresAuditLog) Record(ctx context.Context, e AuditEntry) error {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "audit_payload_invalid", "db", err)
	}
	if e.Payload == nil {
		payload = []byte(`{}`)
	}
	_, err = l.db.ExecContext(ctx, `
        INSERT INTO admin_audit_log (id, actor_id, actor_phone, action, target_type, target_id, payload, at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.ID, e.ActorID, e.ActorPhone, e.Action, e.TargetType, e.TargetID, payload, e.At)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return nil
}

func (l *PostgresAuditLog) List(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	var (
		where []string
		args  []any
	)
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, clause+"$"+strconv.Itoa(len(args)))
	}
	if f.ActorID != "" {
		add("actor_id=", f.ActorID)
	}
	if f.TargetType != "" {
		add("target_type=", f.TargetType)
	}
	if f.TargetID != "" {
		add("target_id=", f.TargetID)
	}
	q := `SELECT id, actor_id, actor_phone, action, target_type, target_id, payload, at
          FROM admin_audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, clampLimit(f.Limit))
	q += " ORDER BY at DESC, id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	defer func() { _ = rows.Close() }()

	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var payload []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorPhone, &e.Action,
			&e.TargetType, &e.TargetID, &payload, &e.At); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
		}
		_ = json.Unmarshal(payload, &e.Payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "db_error", "db", err)
	}
	return out, nil
}
