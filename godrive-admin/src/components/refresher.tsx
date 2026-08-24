"use client";

import { useEffect, useState, useTransition } from "react";
import { useRouter } from "next/navigation";

/**
 * Tự làm mới dữ liệu máy chủ theo chu kỳ. Dùng router.refresh() nên chỉ tải
 * lại phần dữ liệu, không nạp lại cả trang — cuộn trang và ô nhập giữ nguyên.
 *
 * Có công tắc tắt/bật vì màn hình treo tường thì cần tự chạy, còn khi đang
 * đọc kỹ một dòng thì tự nhảy rất khó chịu.
 */
export default function Refresher({ seconds = 15 }: { seconds?: number }) {
  const router = useRouter();
  const [on, setOn] = useState(true);
  const [pending, start] = useTransition();
  const [left, setLeft] = useState(seconds);

  useEffect(() => {
    if (!on) return;
    const tick = setInterval(() => {
      setLeft((v) => {
        if (v <= 1) {
          start(() => router.refresh());
          return seconds;
        }
        return v - 1;
      });
    }, 1000);
    return () => clearInterval(tick);
  }, [on, seconds, router]);

  return (
    <div className="flex items-center gap-2 text-xs text-zinc-500">
      {pending && <span className="text-zinc-400">đang tải…</span>}
      <button
        type="button"
        onClick={() => {
          setLeft(seconds);
          start(() => router.refresh());
        }}
        className="rounded-lg border border-zinc-200 bg-white px-2.5 py-1 font-medium text-zinc-600 transition hover:bg-zinc-50"
      >
        Làm mới
      </button>
      <label className="flex cursor-pointer items-center gap-1.5 select-none">
        <input
          type="checkbox"
          checked={on}
          onChange={(e) => {
            setOn(e.target.checked);
            setLeft(seconds);
          }}
          className="size-3.5 accent-zinc-900"
        />
        <span className="tabular-nums">
          {on ? `tự động ${left}s` : "tự động tắt"}
        </span>
      </label>
    </div>
  );
}
