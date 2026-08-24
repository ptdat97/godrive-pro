#!/usr/bin/env python3
"""Sinh migrations-nogis/ từ migrations/ — biến thể không cần PostGIS.

Bản Postgres của DBngin không kèm PostGIS. Thay vì duy trì hai schema song song
(chắc chắn sẽ lệch nhau), file này sinh biến thể từ schema chính.

0001 được biến đổi theo RULES. Mọi migration sau đó được chép nguyên văn — nếu
một migration mới dùng tới PostGIS thì script báo lỗi thay vì chép âm thầm.

Chạy lại mỗi khi sửa migrations/:
    python3 scripts/gen-nogis.py
"""
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "migrations"
DST = ROOT / "migrations-nogis"

HEADER = """-- === BIẾN THỂ KHÔNG CẦN POSTGIS ===
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

"""

# (mô tả, chuỗi cần thay, chuỗi thay thế)
RULES = [
    ("bỏ CREATE EXTENSION",
     "CREATE EXTENSION IF NOT EXISTS postgis;\n",
     ""),
    ("đổi cột GEOGRAPHY -> lat/lng",
     "    geom        GEOGRAPHY(POINT, 4326) NOT NULL,",
     "    lat         DOUBLE PRECISION NOT NULL,\n"
     "    lng         DOUBLE PRECISION NOT NULL,"),
    ("đổi index GIST -> b-tree",
     "CREATE INDEX driver_locations_geom_idx ON driver_locations USING GIST (geom);",
     "CREATE INDEX driver_locations_pos_idx ON driver_locations (lat, lng);"),
]


GIS_TOKENS = ("postgis", "geography", "gist")


def strip_comments(sql: str) -> str:
    return "\n".join(l for l in sql.splitlines() if not l.lstrip().startswith("--"))


def main() -> int:
    up = (SRC / "0001_init.up.sql").read_text()

    for desc, old, new in RULES:
        if old not in up:
            print(f"✗ không khớp quy tắc: {desc}\n"
                  f"  Schema chính đã đổi — cập nhật RULES trong {__file__}",
                  file=sys.stderr)
            return 1
        up = up.replace(old, new)

    DST.mkdir(exist_ok=True)
    out = DST / "0001_init.up.sql"
    out.write_text(HEADER + up)
    (DST / "0001_init.down.sql").write_text((SRC / "0001_init.down.sql").read_text())

    # Kiểm tra phần SQL (bỏ qua comment) không còn dấu vết PostGIS.
    for token in GIS_TOKENS:
        if token in strip_comments(out.read_text()).lower():
            print(f"✗ vẫn còn '{token}' trong SQL sau khi biến đổi", file=sys.stderr)
            return 1

    # Migration 0002 trở đi: chép nguyên văn, nhưng chỉ khi chúng thật sự không
    # đụng tới PostGIS. Chép âm thầm một migration có GEOGRAPHY sẽ làm biến thể
    # nogis vỡ ở đúng chỗ khó tìm nhất.
    copied = 0
    for src in sorted(SRC.glob("0*.sql")):
        if src.name.startswith("0001_"):
            continue
        body = src.read_text()
        for token in GIS_TOKENS:
            if token in strip_comments(body).lower():
                print(f"✗ {src.name} dùng '{token}' — cần một quy tắc biến đổi riêng",
                      file=sys.stderr)
                return 1
        (DST / src.name).write_text(body)
        copied += 1

    print(f"✓ đã sinh {out.relative_to(ROOT)} ({len(RULES)} quy tắc áp dụng, "
          f"{copied} migration chép nguyên văn)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
