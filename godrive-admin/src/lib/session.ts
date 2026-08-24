"use server";

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { api, GoDriveError, SESSION_COOKIE, sessionToken } from "./api";
import type { TokenPair } from "./types";

/**
 * Quản lý phiên đăng nhập quản trị viên.
 *
 * Token lưu trong cookie httpOnly + sameSite=lax: JavaScript trình duyệt không
 * đọc được, nên kể cả có lỗ hổng XSS thì token cũng không bị lấy đi.
 *
 * Việc ai được làm admin do backend quyết định (danh sách ADMIN_PHONES trong
 * godrive) — giao diện chỉ chuyển tiếp và lưu token nhận về.
 */

export interface OtpState {
  ok: boolean;
  challengeId?: string;
  phone?: string;
  /** Mã OTP chỉ có ở chế độ dev (DEV_AUTH=true) để đỡ phải chờ tin nhắn. */
  devCode?: string;
  error?: string;
  code?: string;
}

export async function requestOtp(
  _prev: OtpState,
  formData: FormData,
): Promise<OtpState> {
  const phone = String(formData.get("phone") ?? "").trim();
  if (!phone) {
    return { ok: false, error: "Vui lòng nhập số điện thoại." };
  }
  try {
    const res = await api.post<{ challenge_id: string; dev_code?: string }>(
      "/v1/admin/auth/otp",
      { phone },
      { auth: false },
    );
    return {
      ok: true,
      challengeId: res.challenge_id,
      devCode: res.dev_code,
      phone,
    };
  } catch (err) {
    const e = err as GoDriveError;
    return { ok: false, error: e.message, code: e.code };
  }
}

export interface VerifyState {
  error?: string;
  code?: string;
}

export async function verifyOtp(
  _prev: VerifyState,
  formData: FormData,
): Promise<VerifyState> {
  const challengeId = String(formData.get("challenge_id") ?? "");
  const otp = String(formData.get("code") ?? "").trim();
  if (!challengeId || !otp) {
    return { error: "Vui lòng nhập mã xác thực." };
  }

  let tp: TokenPair;
  try {
    tp = await api.post<TokenPair>(
      "/v1/admin/auth/verify",
      { challenge_id: challengeId, code: otp, device_id: "admin-console" },
      { auth: false },
    );
  } catch (err) {
    const e = err as GoDriveError;
    return { error: e.message, code: e.code };
  }

  const store = await cookies();
  store.set(SESSION_COOKIE, tp.access_token, {
    httpOnly: true,
    sameSite: "lax",
    // Bật secure khi chạy HTTPS; ở dev localhost dùng http nên phải tắt.
    secure: process.env.NODE_ENV === "production",
    path: "/",
    expires: new Date(tp.expires_at),
  });
  redirect("/");
}

export async function logout(): Promise<void> {
  const store = await cookies();
  store.delete(SESSION_COOKIE);
  redirect("/login");
}

/** Kiểm tra phiên còn hiệu lực bằng cách hỏi backend, không tự giải mã token. */
export async function currentAdmin(): Promise<{
  accountId: string;
  role: string;
} | null> {
  if (!(await sessionToken())) return null;
  try {
    const me = await api.get<{ account_id: string; role: string }>(
      "/v1/admin/me",
    );
    return { accountId: me.account_id, role: me.role };
  } catch {
    return null;
  }
}
