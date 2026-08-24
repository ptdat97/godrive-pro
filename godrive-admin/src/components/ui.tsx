import type { ReactNode } from "react";

// Bộ component trình bày dùng chung. Không gọi API, không giữ trạng thái
// nghiệp vụ — chỉ nhận props và vẽ.

type Tone = "neutral" | "green" | "amber" | "red" | "blue" | "slate";

const TONE: Record<Tone, string> = {
  neutral: "bg-zinc-100 text-zinc-700 ring-zinc-200",
  green: "bg-emerald-50 text-emerald-700 ring-emerald-200",
  amber: "bg-amber-50 text-amber-800 ring-amber-200",
  red: "bg-red-50 text-red-700 ring-red-200",
  blue: "bg-blue-50 text-blue-700 ring-blue-200",
  slate: "bg-slate-100 text-slate-600 ring-slate-200",
};

export function Badge({
  children,
  tone = "neutral",
  title,
}: {
  children: ReactNode;
  tone?: Tone;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={`inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset whitespace-nowrap ${TONE[tone]}`}
    >
      {children}
    </span>
  );
}

/** Ánh xạ trạng thái tài xế sang màu — cùng quy ước ở mọi trang. */
export function driverTone(status: string): Tone {
  switch (status) {
    case "IDLE":
      return "green";
    case "ASSIGNED":
    case "ON_TRIP":
      return "blue";
    case "SUSPENDED":
      return "red";
    default:
      return "slate";
  }
}

export function tripTone(status: string): Tone {
  switch (status) {
    case "SEARCHING":
      return "amber";
    case "ASSIGNED":
    case "ARRIVED":
    case "IN_PROGRESS":
      return "blue";
    case "COMPLETED":
    case "PAID":
      return "green";
    case "CANCELLED":
    case "EXPIRED":
      return "red";
    default:
      return "slate";
  }
}

export function kycTone(kyc: string): Tone {
  switch (kyc) {
    case "APPROVED":
      return "green";
    case "REJECTED":
      return "red";
    default:
      return "amber";
  }
}

export function Card({
  title,
  children,
  action,
}: {
  title?: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <section className="rounded-xl border border-zinc-200 bg-white shadow-sm">
      {(title || action) && (
        <header className="flex items-center justify-between gap-4 border-b border-zinc-100 px-5 py-3">
          {title && (
            <h2 className="text-sm font-semibold text-zinc-900">{title}</h2>
          )}
          {action}
        </header>
      )}
      {children}
    </section>
  );
}

export function Stat({
  label,
  value,
  hint,
  tone = "neutral",
}: {
  label: string;
  value: string | number;
  hint?: string;
  tone?: Tone;
}) {
  const accent =
    tone === "green"
      ? "text-emerald-600"
      : tone === "amber"
        ? "text-amber-600"
        : tone === "red"
          ? "text-red-600"
          : tone === "blue"
            ? "text-blue-600"
            : "text-zinc-900";
  return (
    <div className="rounded-xl border border-zinc-200 bg-white px-5 py-4 shadow-sm">
      <div className="text-xs font-medium tracking-wide text-zinc-500 uppercase">
        {label}
      </div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${accent}`}>
        {value}
      </div>
      {hint && <div className="mt-1 text-xs text-zinc-500">{hint}</div>}
    </div>
  );
}

export function Table({
  head,
  children,
  empty,
}: {
  head: ReactNode[];
  children: ReactNode;
  /** Hiện khi không có dòng nào. */
  empty?: string;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[52rem] text-sm">
        <thead>
          <tr className="border-b border-zinc-100 text-left">
            {head.map((h, i) => (
              <th
                key={i}
                className="px-5 py-2.5 text-xs font-semibold tracking-wide text-zinc-500 uppercase whitespace-nowrap"
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-50">{children}</tbody>
      </table>
      {empty && <EmptyRow text={empty} />}
    </div>
  );
}

function EmptyRow({ text }: { text: string }) {
  return <p className="px-5 py-10 text-center text-sm text-zinc-500">{text}</p>;
}

export function Td({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return <td className={`px-5 py-3 align-middle ${className}`}>{children}</td>;
}

/** Hiển thị lỗi API kèm mã và trace id để tra log máy chủ. */
export function ErrorBox({
  title = "Không tải được dữ liệu",
  code,
  message,
  traceId,
}: {
  title?: string;
  code?: string;
  message: string;
  traceId?: string;
}) {
  return (
    <div className="rounded-xl border border-red-200 bg-red-50 px-5 py-4">
      <p className="text-sm font-semibold text-red-800">{title}</p>
      <p className="mt-1 text-sm text-red-700">{message}</p>
      {(code || traceId) && (
        <p className="mt-2 font-mono text-xs text-red-500">
          {code}
          {traceId ? ` · ${traceId}` : ""}
        </p>
      )}
    </div>
  );
}
