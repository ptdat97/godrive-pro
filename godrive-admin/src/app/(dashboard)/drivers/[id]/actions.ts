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

export interface DocumentsState {
  error?: string;
  code?: string;
  done?: boolean;
}

/**
 * Ghi hạn giấy tờ mới sau khi đã xem giấy thật. Chỉ gửi những ô có giá trị
 * khác giá trị cũ, để nhật ký ghi đúng thứ đã đổi. Lý do bắt buộc — backend
 * từ chối nếu thiếu.
 */
export async function updateDocuments(
  _prev: DocumentsState,
  formData: FormData,
): Promise<DocumentsState> {
  const driverId = String(formData.get("driver_id") ?? "");
  if (!driverId) return { error: "Thiếu mã tài xế." };

  const body: Record<string, string> = {
    reason: String(formData.get("reason") ?? "").trim(),
  };
  for (const field of ["insurance_until", "license_until", "inspection_until"]) {
    const next = String(formData.get(field) ?? "");
    const prev = String(formData.get(`${field}__prev`) ?? "");
    // Ô ngày rỗng nghĩa là "không có hạn" — gửi chuỗi rỗng để xoá hạn cũ.
    if (next !== prev) body[field] = next;
  }
  if (Object.keys(body).length === 1) {
    return { error: "Chưa đổi hạn nào." };
  }

  try {
    await api.put(`/v1/admin/drivers/${driverId}/documents`, body);
  } catch (err) {
    const e = err as GoDriveError;
    return { error: e.message, code: e.code };
  }

  revalidatePath(`/drivers/${driverId}`);
  revalidatePath("/drivers");
  revalidatePath("/");
  return { done: true };
}
