"use client";

import { useActionState } from "react";
import { decideRefund, type RefundActionState } from "./actions";

/**
 * Duyệt hoặc từ chối một khoản đang chờ. Người tạo yêu cầu bấm duyệt sẽ bị
 * backend từ chối (second_approver_required) — luật nằm ở backend và CSDL,
 * không ở nút bấm.
 */
export default function Decide({ id, tripId }: { id: string; tripId: string }) {
  const [state, submit] = useActionState<RefundActionState, FormData>(decideRefund, {});
  return (
    <form action={submit} className="flex flex-wrap items-center gap-2">
      <input type="hidden" name="id" value={id} />
      <input type="hidden" name="trip_id" value={tripId} />
      <input
        name="note"
        placeholder="Ghi chú (bắt buộc khi từ chối)"
        className="w-56 rounded-lg border border-zinc-300 px-2 py-1 text-xs"
      />
      <button
        type="submit"
        name="decision"
        value="approve"
        className="rounded-lg bg-emerald-600 px-3 py-1 text-xs font-medium text-white hover:bg-emerald-700"
      >
        Duyệt
      </button>
      <button
        type="submit"
        name="decision"
        value="reject"
        className="rounded-lg border border-zinc-300 px-3 py-1 text-xs font-medium text-zinc-700 hover:bg-zinc-50"
      >
        Từ chối
      </button>
      {state.error && (
        <span className="text-xs text-red-600">
          {state.error}
          {state.code && <span className="ml-1 font-mono text-red-400">({state.code})</span>}
        </span>
      )}
      {state.done && <span className="text-xs text-emerald-600">{state.done}</span>}
    </form>
  );
}
