"use client";

import { useRouter } from "next/navigation";
import { useTransition } from "react";
import { AREAS, RADII } from "./areas";

/**
 * Chọn vùng quan sát. Ghi vào URL rồi để máy chủ truy vấn lại — đường dẫn chia
 * sẻ được và dữ liệu luôn khớp bộ lọc mà backend thực sự áp dụng.
 */
export default function AreaPicker({
  area,
  radius,
  idleOnly,
}: {
  area: string;
  radius: string;
  idleOnly: boolean;
}) {
  const router = useRouter();
  const [pending, start] = useTransition();

  function apply(patch: Record<string, string>) {
    const next = new URLSearchParams({ area, radius });
    if (idleOnly) next.set("idle", "1");
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    start(() => router.push(`/live?${next.toString()}`));
  }

  return (
    <div className="flex flex-wrap items-end gap-4 rounded-xl border border-zinc-200 bg-white px-5 py-4 shadow-sm">
      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-zinc-500">Khu vực</span>
        <select
          value={area}
          onChange={(e) => apply({ area: e.target.value })}
          className="rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-900"
        >
          {AREAS.map((a) => (
            <option key={a.key} value={a.key}>
              {a.name}
            </option>
          ))}
        </select>
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-zinc-500">Bán kính</span>
        <select
          value={radius}
          onChange={(e) => apply({ radius: e.target.value })}
          className="rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-900"
        >
          {RADII.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      </label>

      <label className="flex cursor-pointer items-center gap-2 pb-1.5 text-sm text-zinc-700 select-none">
        <input
          type="checkbox"
          checked={idleOnly}
          onChange={(e) => apply({ idle: e.target.checked ? "1" : "" })}
          className="size-4 accent-zinc-900"
        />
        Chỉ tài xế sẵn sàng
      </label>

      {pending && <span className="pb-2 text-xs text-zinc-400">đang tải…</span>}
    </div>
  );
}
