"use client";

import { useActionState } from "react";
import { useFormStatus } from "react-dom";
import { requestRefund, type RefundActionState } from "./actions";
import { vnd } from "@/lib/format";

/** Form tạo yêu cầu hoàn tiền cho một chuyến. Trần và ngưỡng do backend kiểm. */
export default function RefundForm({ tripId, max }: { tripId: string; max: number }) {
  const [state, submit] = useActionState<RefundActionState, FormData>(requestRefund, {});
  return (
    <form action={submit} className="space-y-3 px-5 py-4">
      <input type="hidden" name="trip_id" value={tripId} />
      <div className="grid grid-cols-2 gap-3">
        <label className="text-sm">
          <span className="text-zinc-500">Số tiền (tối đa {vnd(max)})</span>
          <input
            name="amount"
            type="number"
            min={1}
            max={max}
            required
            className="mt-1 w-full rounded-lg border border-zinc-300 px-2 py-1 text-sm"
          />
        </label>
        <label className="text-sm">
          <span className="text-zinc-500">Ai chịu khoản này</span>
          <select
            name="charge_to"
            defaultValue="PLATFORM"
            className="mt-1 w-full rounded-lg border border-zinc-300 px-2 py-1 text-sm"
          >
            <option value="PLATFORM">Nền tảng (giá sai, lỗi app, thiện chí)</option>
            <option value="DRIVER">Trừ lại từ tài xế (đi sai đường, thu quá tiền)</option>
          </select>
        </label>
      </div>
      <input
        name="reason"
        required
        minLength={5}
        placeholder="Lý do — khách sẽ được báo lý do này"
        className="w-full rounded-lg border border-zinc-300 px-3 py-1.5 text-sm"
      />
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs">
          {state.error && (
            <span className="text-red-600">
              {state.error}
              {state.code && <span className="ml-1 font-mono text-red-400">({state.code})</span>}
            </span>
          )}
          {state.done && <span className="text-emerald-600">{state.done}</span>}
        </p>
        <Submit label="Hoàn tiền vào ví khách" busy="Đang gửi…" />
      </div>
    </form>
  );
}

export function Submit({ label, busy, tone = "dark" }: { label: string; busy: string; tone?: "dark" | "light" }) {
  const { pending } = useFormStatus();
  const style =
    tone === "dark"
      ? "bg-zinc-900 text-white hover:bg-zinc-700"
      : "border border-zinc-300 text-zinc-700 hover:bg-zinc-50";
  return (
    <button
      type="submit"
      disabled={pending}
      className={`rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:opacity-40 ${style}`}
    >
      {pending ? busy : label}
    </button>
  );
}
