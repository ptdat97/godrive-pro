import type { SettingGroup, SettingHistoryEntry } from "@/lib/types";
import { dateTime } from "@/lib/format";

type Json = Record<string, unknown>;

/** Gom nhãn theo đường dẫn từ lược đồ, để lịch sử đọc được bằng tiếng Việt. */
function labelMap(group: SettingGroup): Record<string, string> {
  const out: Record<string, string> = {};
  for (const sec of group.sections) {
    for (const f of sec.fields) {
      // Biểu giá có cùng tên ô ở ba loại xe, nên phải kèm tên khối.
      out[f.path] = sec.title.startsWith("Biểu giá")
        ? `${f.label} — ${sec.title.replace("Biểu giá ", "")}`
        : f.label;
    }
  }
  return out;
}

interface Change {
  path: string;
  before: string;
  after: string;
}

function show(v: unknown): string {
  if (v === undefined) return "—";
  if (typeof v === "boolean") return v ? "bật" : "tắt";
  if (typeof v === "number") return v.toLocaleString("vi-VN");
  if (Array.isArray(v)) return `${v.length} bậc`;
  return String(v);
}

/**
 * So hai bản cấu hình, trả về đúng những ô đã đổi.
 *
 * Hiện nguyên khối JSON thì không ai đọc; điều người vận hành cần biết là
 * "ai đổi con số nào, từ bao nhiêu sang bao nhiêu".
 */
function diff(before: Json | undefined, after: Json, prefix = ""): Change[] {
  const out: Change[] = [];
  const keys = new Set([...Object.keys(before ?? {}), ...Object.keys(after)]);
  for (const k of keys) {
    const a = before?.[k];
    const b = after[k];
    const path = prefix ? `${prefix}.${k}` : k;
    // Đi sâu vào object lồng (biểu giá theo loại xe). Vế cũ được phép vắng:
    // bản ghi cũ trong CSDL có thể không có giá trị trước. Không xử riêng ca đó
    // thì cả cụm biểu giá in ra thành "[object Object]".
    const descend =
      b !== null &&
      typeof b === "object" &&
      !Array.isArray(b) &&
      (a === undefined ||
        (a !== null && typeof a === "object" && !Array.isArray(a)));
    if (descend) {
      out.push(...diff(a as Json | undefined, b as Json, path));
      continue;
    }
    if (JSON.stringify(a) !== JSON.stringify(b)) {
      out.push({ path, before: show(a), after: show(b) });
    }
  }
  return out;
}

export default function History({
  group,
  entries,
}: {
  group: SettingGroup;
  entries: SettingHistoryEntry[];
}) {
  const labels = labelMap(group);

  if (entries.length === 0) {
    return (
      <p className="px-5 py-8 text-center text-sm text-zinc-500">
        Chưa có thay đổi nào — nhóm này đang chạy bằng giá trị mặc định trong mã
        nguồn.
      </p>
    );
  }

  return (
    <ol className="divide-y divide-zinc-100">
      {entries.map((e) => {
        const changes = diff(e.old_value, e.new_value);
        return (
          <li key={e.id} className="px-5 py-4">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <span className="text-sm font-medium text-zinc-900">
                Phiên bản {e.version}
              </span>
              <span className="text-xs text-zinc-500">{dateTime(e.at)}</span>
              <span className="font-mono text-xs text-zinc-400">
                {e.changed_by}
              </span>
            </div>
            {!e.old_value && (
              <p className="mt-1 text-xs text-zinc-400">
                Lần lưu đầu tiên — trước đó nhóm chạy bằng giá trị mặc định
                trong mã nguồn.
              </p>
            )}
            {e.reason && (
              <p className="mt-1 text-sm text-zinc-600 italic">“{e.reason}”</p>
            )}
            {changes.length === 0 ? (
              <p className="mt-2 text-xs text-zinc-400">
                Không có ô nào đổi giá trị.
              </p>
            ) : (
              <ul className="mt-2 space-y-1">
                {changes.map((c) => (
                  <li key={c.path} className="text-sm">
                    <span className="text-zinc-600">
                      {labels[c.path] ?? c.path}
                    </span>
                    <span className="mx-2 tabular-nums text-zinc-400 line-through">
                      {c.before}
                    </span>
                    <span className="tabular-nums text-zinc-900">
                      → {c.after}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </li>
        );
      })}
    </ol>
  );
}
