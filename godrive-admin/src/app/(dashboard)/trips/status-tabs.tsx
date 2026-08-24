import Link from "next/link";

// Danh sách trạng thái khớp máy trạng thái ở internal/trip/domain.go.
const TABS: [string, string][] = [
  ["", "Tất cả"],
  ["SEARCHING", "Đang tìm tài xế"],
  ["ASSIGNED", "Đã ghép"],
  ["IN_PROGRESS", "Đang chạy"],
  ["COMPLETED", "Hoàn tất"],
  ["PAID", "Đã ghi sổ"],
  ["CANCELLED", "Đã huỷ"],
  ["EXPIRED", "Không tìm được tài xế"],
];

export default function StatusTabs({ current }: { current: string }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {TABS.map(([value, text]) => {
        const active = current === value;
        const href = value ? `/trips?status=${value}` : "/trips";
        return (
          <Link
            key={value || "all"}
            href={href}
            className={`rounded-lg px-3 py-1.5 text-sm font-medium transition ${
              active
                ? "bg-zinc-900 text-white"
                : "border border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-50"
            }`}
          >
            {text}
          </Link>
        );
      })}
    </div>
  );
}
