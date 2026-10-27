"use server";

import { revalidatePath } from "next/cache";
import { api, GoDriveError } from "@/lib/api";

export interface RefundActionState {
  error?: string;
  code?: string;
  done?: string;
}

function fail(err: unknown): RefundActionState {
  const e = err as GoDriveError;
  return { error: e.message, code: e.code };
}

function refresh(tripId?: string) {
  revalidatePath("/refunds");
  if (tripId) revalidatePath(`/trips/${tripId}`);
}

/**
 * Tạo yêu cầu hoàn tiền. Khoản đến ngưỡng tự duyệt thì backend ghi sổ luôn;
 * trên ngưỡng thì nằm chờ người thứ hai — giao diện chỉ báo lại kết quả.
 */
export async function requestRefund(
  _prev: RefundActionState,
  formData: FormData,
): Promise<RefundActionState> {
  const tripId = String(formData.get("trip_id") ?? "");
  const amount = Number(formData.get("amount") ?? 0);
  try {
    const r = await api.post<{ status: string }>("/v1/admin/refunds", {
      trip_id: tripId,
      amount,
      reason: String(formData.get("reason") ?? ""),
      charge_to: String(formData.get("charge_to") ?? "PLATFORM"),
    });
    refresh(tripId);
    return {
      done:
        r.status === "APPROVED"
          ? "Đã hoàn vào ví khách (dưới ngưỡng, tự duyệt)."
          : "Đã tạo yêu cầu — cần một người khác duyệt ở trang Hoàn tiền.",
    };
  } catch (err) {
    return fail(err);
  }
}

export async function decideRefund(
  _prev: RefundActionState,
  formData: FormData,
): Promise<RefundActionState> {
  const id = String(formData.get("id") ?? "");
  const decision = String(formData.get("decision") ?? "");
  const tripId = String(formData.get("trip_id") ?? "");
  const note = String(formData.get("note") ?? "");
  try {
    await api.post(`/v1/admin/refunds/${id}/${decision === "approve" ? "approve" : "reject"}`, {
      note,
    });
    refresh(tripId);
    return { done: decision === "approve" ? "Đã duyệt và hoàn vào ví khách." : "Đã từ chối." };
  } catch (err) {
    return fail(err);
  }
}
