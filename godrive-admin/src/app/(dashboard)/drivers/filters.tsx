"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useTransition } from "react";

/**
 * Bộ lọc ghi thẳng vào URL rồi để máy chủ lọc lại. Không lọc mảng ở trình
 * duyệt — nhờ vậy đường dẫn chia sẻ được và luôn khớp dữ liệu thật.
 */
export default function DriverFilters() {
  const router = useRouter();
  const params = useSearchParams();
  const [pending, start] = useTransition();

  function apply(patch: Record<string, string>) {
    const next = new URLSearchParams(params.toString());
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    start(() => router.push(`/drivers?${next.toString()}`));
  }

  const status = params.get("status") ?? "";
  const kyc = params.get("kyc") ?? "";
  const debt = params.get("debt") === "1";
  const q = params.get("q") ?? "";

  return (
    <form
      className="flex flex-wrap items-end gap-3 rounded-xl border border-zinc-200 bg-white px-5 py-4 shadow-sm"
      onSubmit={(e) => {
        e.preventDefault();
        const data = new FormData(e.currentTarget);
        apply({ q: String(data.get("q") ?? "") });
      }}
    >
      <Field label="Tìm kiếm">
        <input
          name="q"
          defaultValue={q}
          placeholder="Tên, số điện thoại, biển số"
          className="w-56 rounded-lg border border-zinc-300 px-3 py-1.5 text-sm outline-none focus:border-zinc-900"
        />
      </Field>

      <Field label="Trạng thái">
        <Select
          value={status}
          onChange={(v) => apply({ status: v })}
          options={[
            ["", "Tất cả"],
            ["IDLE", "Sẵn sàng"],
            ["ASSIGNED", "Đang tới đón"],
            ["ON_TRIP", "Đang chở khách"],
            ["OFFLINE", "Ngoại tuyến"],
            ["SUSPENDED", "Tạm khoá"],
          ]}
        />
      </Field>

      <Field label="Hồ sơ">
        <Select
          value={kyc}
          onChange={(v) => apply({ kyc: v })}
          options={[
            ["", "Tất cả"],
            ["PENDING", "Chờ duyệt"],
            ["APPROVED", "Đã duyệt"],
            ["REJECTED", "Từ chối"],
          ]}
        />
      </Field>

      <label className="flex cursor-pointer items-center gap-2 pb-1.5 text-sm text-zinc-700 select-none">
        <input
          type="checkbox"
          checked={debt}
          onChange={(e) => apply({ debt: e.target.checked ? "1" : "" })}
          className="size-4 accent-zinc-900"
        />
        Chỉ tài xế đang nợ
      </label>

      <button
        type="submit"
        className="rounded-lg bg-zinc-900 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-zinc-800"
      >
        Lọc
      </button>

      {(status || kyc || debt || q) && (
        <button
          type="button"
          onClick={() => start(() => router.push("/drivers"))}
          className="rounded-lg border border-zinc-200 px-3 py-1.5 text-sm text-zinc-600 transition hover:bg-zinc-50"
        >
          Xoá lọc
        </button>
      )}

      {pending && <span className="pb-2 text-xs text-zinc-400">đang lọc…</span>}
    </form>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs font-medium text-zinc-500">{label}</span>
      {children}
    </label>
  );
}

function Select({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: [string, string][];
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-900"
    >
      {options.map(([v, l]) => (
        <option key={v} value={v}>
          {l}
        </option>
      ))}
    </select>
  );
}
