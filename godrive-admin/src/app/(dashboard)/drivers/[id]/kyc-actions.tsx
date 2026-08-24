"use client";

import { useActionState } from "react";
import { useFormStatus } from "react-dom";
import { reviewKyc, type KycState } from "./actions";

/**
 * Nút duyệt/từ chối hồ sơ. Quyết định thuộc về backend; ở đây chỉ gửi ý định
 * và hiển thị kết quả trả về.
 */
export default function KycActions({
  driverId,
  kyc,
}: {
  driverId: string;
  kyc: string;
}) {
  const [state, submit] = useActionState<KycState, FormData>(reviewKyc, {});

  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex gap-2">
        <form action={submit}>
          <input type="hidden" name="driver_id" value={driverId} />
          <input type="hidden" name="approved" value="true" />
          <Button
            variant="approve"
            disabled={kyc === "APPROVED"}
            idle={kyc === "APPROVED" ? "Đã duyệt" : "Duyệt hồ sơ"}
            busy="Đang duyệt…"
          />
        </form>
        <form action={submit}>
          <input type="hidden" name="driver_id" value={driverId} />
          <input type="hidden" name="approved" value="false" />
          <Button
            variant="reject"
            disabled={kyc === "REJECTED"}
            idle={kyc === "REJECTED" ? "Đã từ chối" : "Từ chối"}
            busy="Đang xử lý…"
          />
        </form>
      </div>

      {state.error && (
        <p className="text-xs text-red-600">
          {state.error}
          {state.code && (
            <span className="ml-1 font-mono text-red-400">({state.code})</span>
          )}
        </p>
      )}
      {state.done && (
        <p className="text-xs text-emerald-600">
          {state.done === "approved"
            ? "Đã duyệt hồ sơ — tài xế có thể bật nhận chuyến."
            : "Đã từ chối hồ sơ."}
        </p>
      )}
    </div>
  );
}

function Button({
  variant,
  disabled,
  idle,
  busy,
}: {
  variant: "approve" | "reject";
  disabled: boolean;
  idle: string;
  busy: string;
}) {
  const { pending } = useFormStatus();
  const base =
    "rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-40";
  const style =
    variant === "approve"
      ? "bg-emerald-600 text-white hover:bg-emerald-700"
      : "border border-zinc-300 text-zinc-700 hover:bg-zinc-50";
  return (
    <button
      type="submit"
      disabled={disabled || pending}
      className={`${base} ${style}`}
    >
      {pending ? busy : idle}
    </button>
  );
}
