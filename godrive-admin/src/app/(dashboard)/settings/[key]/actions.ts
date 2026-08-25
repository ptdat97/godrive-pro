"use server";

import { revalidatePath } from "next/cache";
import { api, GoDriveError } from "@/lib/api";
import type { SettingGroup } from "@/lib/types";

export interface SaveState {
  error?: string;
  code?: string;
  /** Phiên bản mới sau khi lưu, để biểu mẫu gửi đúng số ở lần sửa kế tiếp. */
  savedVersion?: number;
  savedAt?: string;
}

/**
 * Lưu một nhóm cấu hình.
 *
 * Backend mới là nơi kiểm tra ngưỡng — hành động này chỉ chuyển tiếp. Lược đồ
 * phía giao diện chỉ để bắt lỗi sớm cho người dùng, không phải chốt chặn.
 */
export async function saveSettings(
  _prev: SaveState,
  formData: FormData,
): Promise<SaveState> {
  const key = String(formData.get("key") ?? "");
  const version = Number(formData.get("version") ?? -1);
  const reason = String(formData.get("reason") ?? "").trim();
  const payload = String(formData.get("value") ?? "");

  if (!key || !payload) return { error: "Thiếu nội dung cấu hình." };
  // Bắt buộc có lý do: sáu tháng sau, người đi tìm hiểu vì sao chiết khấu là
  // 25% sẽ chỉ còn dòng này để đọc.
  if (reason.length < 5) {
    return { error: "Cần ghi lý do thay đổi (ít nhất 5 ký tự)." };
  }

  let value: unknown;
  try {
    value = JSON.parse(payload);
  } catch {
    return { error: "Biểu mẫu gửi lên không đọc được. Tải lại trang và thử lại." };
  }

  let saved: SettingGroup;
  try {
    saved = await api.put<SettingGroup>(`/v1/admin/settings/${key}`, {
      value,
      version,
      reason,
    });
  } catch (err) {
    const e = err as GoDriveError;
    return { error: e.message, code: e.code };
  }

  revalidatePath(`/settings/${key}`);
  return { savedVersion: saved.version, savedAt: saved.updated_at };
}
