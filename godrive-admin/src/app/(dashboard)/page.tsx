import Link from "next/link";
import { api, GoDriveError } from "@/lib/api";
import { dateTime, percent, vnd } from "@/lib/format";
import { Badge, Card, ErrorBox, Stat } from "@/components/ui";
import Refresher from "@/components/refresher";
import type { Overview } from "@/lib/types";

export const metadata = { title: "Tổng quan · GoDrive" };
export const dynamic = "force-dynamic";

export default async function OverviewPage() {
  let ov: Overview;
  try {
    ov = await api.get<Overview>("/v1/admin/overview");
  } catch (err) {
    const e = err as GoDriveError;
    return <ErrorBox code={e.code} message={e.message} traceId={e.traceId} />;
  }

  const totalDrivers =
    ov.drivers.online +
    ov.drivers.on_trip +
    ov.drivers.offline +
    ov.drivers.suspended;

  return (
    <div className="space-y-6">
      <div className="flex items-baseline justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Tổng quan</h1>
          <p className="mt-0.5 text-sm text-zinc-500">
            Số liệu lúc {dateTime(ov.generated_at)}
          </p>
        </div>
        <Refresher seconds={15} />
      </div>

      {ov.alerts.length > 0 && (
        <div className="space-y-2">
          {ov.alerts.map((a) => (
            <div
              key={a.code}
              className={`flex items-center gap-3 rounded-lg px-4 py-2.5 text-sm ring-1 ring-inset ${
                a.level === "warn"
                  ? "bg-amber-50 text-amber-900 ring-amber-200"
                  : "bg-blue-50 text-blue-900 ring-blue-200"
              }`}
            >
              <span className="text-base font-semibold tabular-nums">
                {a.count}
              </span>
              <span>{a.message}</span>
              <code className="ml-auto font-mono text-xs opacity-50">
                {a.code}
              </code>
            </div>
          ))}
        </div>
      )}

      <section>
        <h2 className="mb-3 text-sm font-semibold text-zinc-700">Tài xế</h2>
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
          <Stat
            label="Sẵn sàng"
            value={ov.drivers.online}
            tone="green"
            hint="đang chờ ghép chuyến"
          />
          <Stat
            label="Đang chạy"
            value={ov.drivers.on_trip}
            tone="blue"
            hint="đã ghép hoặc chở khách"
          />
          <Stat label="Ngoại tuyến" value={ov.drivers.offline} />
          <Stat
            label="Tạm khoá"
            value={ov.drivers.suspended}
            tone={ov.drivers.suspended > 0 ? "red" : "neutral"}
          />
          <Stat
            label="Chờ duyệt hồ sơ"
            value={ov.drivers.pending_kyc}
            tone={ov.drivers.pending_kyc > 0 ? "amber" : "neutral"}
            hint={totalDrivers > 0 ? `trên ${totalDrivers} tài xế` : undefined}
          />
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-sm font-semibold text-zinc-700">Chuyến đi</h2>
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
          <Stat
            label="Đang tìm tài xế"
            value={ov.trips.searching}
            tone={ov.trips.searching > 0 ? "amber" : "neutral"}
          />
          <Stat label="Đang thực hiện" value={ov.trips.active} tone="blue" />
          <Stat label="Hoàn tất" value={ov.trips.completed} tone="green" />
          <Stat label="Đã huỷ" value={ov.trips.cancelled} />
          <Stat
            label="Không tìm được tài xế"
            value={ov.trips.expired}
            tone={ov.trips.expired > 0 ? "red" : "neutral"}
          />
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-sm font-semibold text-zinc-700">
          Doanh thu (chuyến đã hoàn tất)
        </h2>
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <Stat label="Tổng cước" value={vnd(ov.money.gross)} />
          <Stat
            label="Chiết khấu nền tảng"
            value={vnd(ov.money.platform_fee)}
            tone="green"
          />
          <Stat label="Thu nhập tài xế" value={vnd(ov.money.driver_earn)} />
          <Stat
            label="Tỉ lệ tiền mặt"
            value={percent(ov.money.cash_share)}
            hint="phần chuyến trả tiền mặt"
            tone={ov.money.cash_share > 0.5 ? "amber" : "neutral"}
          />
        </div>
      </section>

      <Card title="Lối tắt">
        <div className="flex flex-wrap gap-2 px-5 py-4">
          <Shortcut href="/drivers?kyc=PENDING" tone="amber">
            Hồ sơ chờ duyệt ({ov.drivers.pending_kyc})
          </Shortcut>
          <Shortcut href="/drivers?debt=1" tone="red">
            Tài xế đang nợ
          </Shortcut>
          <Shortcut href="/trips?status=SEARCHING" tone="blue">
            Chuyến đang tìm tài xế ({ov.trips.searching})
          </Shortcut>
          <Shortcut href="/live" tone="neutral">
            Bản đồ trực tuyến
          </Shortcut>
        </div>
      </Card>
    </div>
  );
}

function Shortcut({
  href,
  children,
  tone,
}: {
  href: string;
  children: React.ReactNode;
  tone: "amber" | "red" | "blue" | "neutral";
}) {
  return (
    <Link href={href}>
      <Badge tone={tone}>{children}</Badge>
    </Link>
  );
}
