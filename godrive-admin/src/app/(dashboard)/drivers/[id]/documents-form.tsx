"use client";

import { useActionState } from "react";
import { useFormStatus } from "react-dom";
import { updateDocuments, type DocumentsState } from "./actions";
import { DOCUMENT_LABEL, daysLeft } from "@/lib/format";
import type { DocumentAlert, DocumentExpiry } from "@/lib/types";

const FIELDS: { name: keyof DocumentExpiry; doc: string; hint: string }[] = [
  { name: "insurance_until", doc: "insurance", hint: "Bắt buộc với mọi xe" },
  { name: "license_until", doc: "license", hint: "Để trống nếu hạng A1 (không thời hạn)" },
  { name: "inspection_until", doc: "inspection", hint: "Chỉ ô tô; để trống với xe máy" },
];

const LEVEL_STYLE: Record<string, string> = {
  expired: "text-red-700",
  urgent: "text-red-600",
  soon: "text-amber-700",
  notice: "text-zinc-600",
};

/**
 * Hạn giấy tờ và form gia hạn. Đây là đường DUY NHẤT để gia hạn: tài xế không
 * tự khai được, vì một ngày tự khai không kèm giấy thật thì vô giá trị pháp lý.
 */
export default function DocumentsForm({
  driverId,
  expiry,
  alerts,
}: {
  driverId: string;
  expiry: DocumentExpiry;
  alerts: DocumentAlert[];
}) {
  const [state, submit] = useActionState<DocumentsState, FormData>(updateDocuments, {});
  const alertOf = (doc: string) => alerts.find((a) => a.document === doc);

  return (
    <form action={submit} className="space-y-3 px-5 py-4">
      <input type="hidden" name="driver_id" value={driverId} />
      {FIELDS.map((f) => {
        const a = alertOf(f.doc);
        return (
          <div key={f.name} className="grid grid-cols-[1fr_auto] items-center gap-3">
            <label htmlFor={f.name} className="text-sm">
              <span className="font-medium text-zinc-900">{DOCUMENT_LABEL[f.doc]}</span>
              <span className="block text-xs text-zinc-500">
                {a ? (
                  <span className={LEVEL_STYLE[a.level]}>{daysLeft(a.days_left)}</span>
                ) : (
                  f.hint
                )}
              </span>
            </label>
            <input type="hidden" name={`${f.name}__prev`} value={expiry[f.name]} />
            <input
              id={f.name}
              name={f.name}
              type="date"
              defaultValue={expiry[f.name]}
              className="rounded-lg border border-zinc-300 px-2 py-1 text-sm"
            />
          </div>
        );
      })}
      <input
        name="reason"
        required
        minLength={5}
        placeholder="Lý do — ví dụ: đã xem hợp đồng bảo hiểm số …"
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
          {state.done && <span className="text-emerald-600">Đã lưu hạn giấy tờ và ghi nhật ký.</span>}
        </p>
        <Submit />
      </div>
    </form>
  );
}

function Submit() {
  const { pending } = useFormStatus();
  return (
    <button
      type="submit"
      disabled={pending}
      className="rounded-lg bg-zinc-900 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-zinc-700 disabled:opacity-40"
    >
      {pending ? "Đang lưu…" : "Lưu hạn giấy tờ"}
    </button>
  );
}
