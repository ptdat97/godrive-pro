#!/usr/bin/env bash
# Tạo database godrive trên instance Postgres của DBngin và chạy migration.
#
# Yêu cầu: đã bật một instance PostgreSQL trong DBngin (cổng 5432).
# Chạy:  ./scripts/setup-db.sh
set -euo pipefail

HOST="${PGHOST:-localhost}"
PORT="${PGPORT:-5432}"
USER="${PGUSER:-postgres}"
DB="${PGDATABASE:-godrive}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# libpq của Homebrew là keg-only, không nằm sẵn trong PATH.
if ! command -v psql >/dev/null 2>&1; then
  for p in /opt/homebrew/opt/libpq/bin /usr/local/opt/libpq/bin; do
    [ -d "$p" ] && export PATH="$p:$PATH"
  done
fi
command -v psql >/dev/null 2>&1 || { echo "✗ không tìm thấy psql. Chạy: brew install libpq"; exit 1; }

echo "→ Kiểm tra kết nối tới Postgres ${HOST}:${PORT} ..."
if ! pg_isready -h "$HOST" -p "$PORT" -q 2>/dev/null; then
  cat <<EOF
✗ Không có Postgres nào đang lắng nghe ở ${HOST}:${PORT}.

  Mở DBngin → dấu "+" → PostgreSQL → cổng 5432 → Start.
  Rồi chạy lại script này.
EOF
  exit 1
fi
echo "✓ Postgres đang chạy"

# Tạo database nếu chưa có (psql không có "CREATE DATABASE IF NOT EXISTS").
if psql -h "$HOST" -p "$PORT" -U "$USER" -d postgres -tAc \
     "SELECT 1 FROM pg_database WHERE datname='${DB}'" | grep -q 1; then
  echo "✓ Database '${DB}' đã tồn tại"
else
  psql -h "$HOST" -p "$PORT" -U "$USER" -d postgres -c "CREATE DATABASE ${DB}" >/dev/null
  echo "✓ Đã tạo database '${DB}'"
fi

# PostGIS: bản Postgres của DBngin thường KHÔNG kèm extension này.
# Schema chỉ dùng PostGIS cho bảng driver_locations (cột GEOGRAPHY).
if psql -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -tAc \
     "SELECT 1 FROM pg_available_extensions WHERE name='postgis'" | grep -q 1; then
  psql -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -c "CREATE EXTENSION IF NOT EXISTS postgis" >/dev/null
  echo "✓ PostGIS đã bật"
  MIGRATION="$ROOT/migrations"
else
  echo "⚠ Instance này không có PostGIS. Dùng schema thay thế (driver_locations lưu lat/lng thường)."
  echo "  Vị trí nóng thực tế nằm ở Redis GEO, nên Postgres không cần PostGIS ở giai đoạn dev."
  MIGRATION="$ROOT/migrations-nogis"
fi

echo "→ Chạy migration từ ${MIGRATION#$ROOT/} ..."
psql -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -v ON_ERROR_STOP=1 -q \
     -f "$MIGRATION/0001_init.up.sql"

TABLES=$(psql -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -tAc \
  "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
echo "✓ Migration xong — ${TABLES} bảng trong schema public"
echo
echo "DATABASE_URL=postgres://${USER}@${HOST}:${PORT}/${DB}?sslmode=disable"
