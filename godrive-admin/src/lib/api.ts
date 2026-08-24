import { cookies } from "next/headers";
import type { ApiError } from "./types";

/**
 * Lớp gọi API GoDrive. Chỉ chạy phía máy chủ Next.js:
 *
 *  - Token nằm trong cookie httpOnly, JavaScript trình duyệt không đọc được.
 *  - Không cần CORS vì trình duyệt không gọi thẳng cổng 8080.
 *  - Không cache: dữ liệu vận hành phải luôn tươi.
 *
 * Mọi logic nghiệp vụ nằm ở godrive. Tệp này chỉ chuyển tiếp yêu cầu và
 * chuẩn hoá lỗi.
 */

export const API_BASE = process.env.GODRIVE_API_URL ?? "http://localhost:8080";

export const SESSION_COOKIE = "godrive_admin_token";

/** Lỗi mang theo mã ổn định của API để giao diện xử lý theo mã, không theo chuỗi. */
export class GoDriveError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly status: number,
    readonly traceId?: string,
  ) {
    super(message);
    this.name = "GoDriveError";
  }
}

async function parseError(res: Response): Promise<GoDriveError> {
  let body: Partial<ApiError> = {};
  try {
    body = (await res.json()) as ApiError;
  } catch {
    // Phản hồi không phải JSON (proxy lỗi, backend chết) — giữ thông báo chung.
  }
  return new GoDriveError(
    body.code ?? "network_error",
    body.message ?? `Máy chủ trả về lỗi ${res.status}.`,
    res.status,
    body.trace_id,
  );
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Gắn token phiên hiện tại. Mặc định có. */
  auth?: boolean;
  /** Token truyền thẳng, dùng cho luồng đăng nhập khi cookie chưa được đặt. */
  token?: string;
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, auth = true, token } = opts;

  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const bearer = token ?? (auth ? await sessionToken() : undefined);
  if (bearer) headers["Authorization"] = `Bearer ${bearer}`;

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    });
  } catch {
    throw new GoDriveError(
      "api_unreachable",
      `Không kết nối được tới API GoDrive tại ${API_BASE}. Kiểm tra backend đã chạy chưa.`,
      503,
    );
  }

  if (!res.ok) throw await parseError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export async function sessionToken(): Promise<string | undefined> {
  const store = await cookies();
  return store.get(SESSION_COOKIE)?.value;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>(path, { ...opts, method: "POST", body }),
};
