import Link from "next/link";

/**
 * Điều hướng trang theo con trỏ. Con trỏ do backend phát ra và mờ — giao diện
 * chỉ chuyển lại nguyên văn, không tự dựng.
 *
 * Không có nút "trang trước": phân trang keyset chỉ đi tiếp được. Muốn quay
 * lại thì về trang đầu, hoặc dùng nút Back của trình duyệt.
 */
export default function Pager({
  path,
  params,
  cursor,
  nextCursor,
}: {
  path: string;
  /** Bộ lọc hiện tại, KHÔNG gồm cursor. */
  params: URLSearchParams;
  /** Con trỏ của trang đang xem; rỗng = trang đầu. */
  cursor: string;
  nextCursor?: string;
}) {
  if (!cursor && !nextCursor) return null;
  const href = (c?: string) => {
    const qs = new URLSearchParams(params);
    if (c) qs.set("cursor", c);
    const s = qs.toString();
    return s ? `${path}?${s}` : path;
  };
  const link =
    "rounded-lg border border-zinc-300 px-3 py-1.5 text-sm font-medium text-zinc-700 hover:bg-zinc-50";
  return (
    <nav className="flex items-center justify-between gap-3">
      {cursor ? (
        <Link href={href()} className={link}>
          ← Về trang đầu
        </Link>
      ) : (
        <span />
      )}
      {nextCursor && (
        <Link href={href(nextCursor)} className={link}>
          Trang sau →
        </Link>
      )}
    </nav>
  );
}
