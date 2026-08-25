import Link from "next/link";
import { notFound } from "next/navigation";
import { api, GoDriveError } from "@/lib/api";
import { Card, ErrorBox } from "@/components/ui";
import { relative } from "@/lib/format";
import type { SettingGroup, SettingHistoryEntry } from "@/lib/types";
import GroupForm from "./group-form";
import History from "./history";

interface ListResult {
  groups: SettingGroup[];
}
interface HistoryResult {
  entries: SettingHistoryEntry[];
}

export default async function SettingsPage({
  params,
}: {
  params: Promise<{ key: string }>;
}) {
  const { key } = await params;

  let groups: SettingGroup[];
  try {
    // Lấy cả danh sách để vẽ thanh điều hướng — API trả kèm lược đồ biểu mẫu.
    ({ groups } = await api.get<ListResult>("/v1/admin/settings"));
  } catch (err) {
    const e = err as GoDriveError;
    return (
      <ErrorBox code={e.code} message={e.message} traceId={e.traceId} />
    );
  }

  const group = groups.find((g) => g.key === key);
  if (!group) notFound();

  let entries: SettingHistoryEntry[] = [];
  let historyError: GoDriveError | null = null;
  try {
    ({ entries } = await api.get<HistoryResult>(
      `/v1/admin/settings/${key}/history`,
    ));
  } catch (err) {
    // Lịch sử hỏng không được chặn việc sửa cấu hình.
    historyError = err as GoDriveError;
  }

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-xl font-semibold tracking-tight">
          Cấu hình vận hành
        </h1>
        <p className="mt-1 text-sm text-zinc-500">
          Những con số này áp dụng cho toàn hệ thống trong vài giây sau khi lưu,
          không cần triển khai lại. Báo giá đã phát và chuyến đang chạy giữ
          nguyên giá cũ.
        </p>
      </header>

      <nav className="flex flex-wrap gap-2">
        {groups.map((g) => (
          <Link
            key={g.key}
            href={`/settings/${g.key}`}
            className={`rounded-lg px-3 py-1.5 text-sm font-medium transition ${
              g.key === key
                ? "bg-zinc-900 text-white"
                : "border border-zinc-200 text-zinc-600 hover:bg-zinc-50"
            }`}
          >
            {g.label}
            {g.is_default && (
              <span
                title="Chưa từng chỉnh — đang chạy bằng mặc định trong mã nguồn"
                className={`ml-2 text-xs ${g.key === key ? "text-zinc-400" : "text-zinc-400"}`}
              >
                mặc định
              </span>
            )}
          </Link>
        ))}
      </nav>

      <div>
        <h2 className="text-lg font-semibold text-zinc-900">{group.label}</h2>
        <p className="mt-0.5 text-sm text-zinc-500">{group.description}</p>
        {group.updated_at && (
          <p className="mt-1 text-xs text-zinc-400">
            Sửa lần cuối {relative(group.updated_at)}
            {group.updated_by ? ` bởi ${group.updated_by}` : ""}
          </p>
        )}
      </div>

      <GroupForm group={group} />

      <Card title={`Lịch sử thay đổi — ${group.label}`}>
        {historyError ? (
          <div className="px-5 py-4">
            <ErrorBox
              title="Không tải được lịch sử"
              code={historyError.code}
              message={historyError.message}
            />
          </div>
        ) : (
          <History group={group} entries={entries} />
        )}
      </Card>
    </div>
  );
}
