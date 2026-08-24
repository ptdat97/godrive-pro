"use server";

import { revalidatePath } from "next/cache";
import { api, GoDriveError } from "@/lib/api";

export interface KycState {
  error?: string;
  code?: string;
  done?: "approved" | "rejected";
}

/**
 * Duyệt hoặc từ chối hồ sơ tài xế. Quyết định nghiệp vụ nằm ở backend
 * (driver.ReviewKYC) — hành động này chỉ chuyển tiếp và làm mới trang.
 */
export async function reviewKyc(
  _prev: KycState,
  formData: FormData,
): Promise<KycState> {
  const driverId = String(formData.get("driver_id") ?? "");
  const approved = String(formData.get("approved") ?? "") === "true";

  if (!driverId) return { error: "Thiếu mã tài xế." };

  try {
    await api.post(`/v1/admin/drivers/${driverId}/kyc`, { approved });
  } catch (err) {
    const e = err as GoDriveError;
    return { error: e.message, code: e.code };
  }

  revalidatePath(`/drivers/${driverId}`);
  revalidatePath("/drivers");
  revalidatePath("/");
  return { done: approved ? "approved" : "rejected" };
}
