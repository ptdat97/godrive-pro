import { api, GoDriveError } from "@/lib/api";
import { Card, ErrorBox } from "@/components/ui";
import type { Refund } from "@/lib/types";
import RefundTable from "./refund-table";

export const metadata = { title: "Hoàn tiền · GoDrive" };
export const dynamic = "force-dynamic";

export default async function RefundsPage() {
  let pending: Refund[];
  let recent: Refund[];
  try {
    const [p, r] = await Promise.all([
      api.get<{ refunds: Refund[] }>("/v1/admin/refunds?status=PENDING&limit=100"),
      api.get<{ refunds: Refund[] }>("/v1/admin/refunds?limit=50"),
    ]);
    pending = p.refunds;
    recent = r.refunds.filter((x) => x.status !== "PENDING");
  } catch (err) {
    const e = err as GoDriveError;
    return <ErrorBox code={e.code} message={e.message} traceId={e.traceId} />;
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Hoàn tiền</h1>
        <p className="mt-0.5 text-sm text-zinc-500">
          Hoàn vào ví GoDrive của khách. Khoản vượt ngưỡng tự duyệt cần một người khác duyệt —
          người tạo yêu cầu không tự duyệt được. Tạo yêu cầu ở trang chi tiết chuyến.
        </p>
      </div>
      <Card title={`Chờ duyệt (${pending.length})`}>
        <RefundTable rows={pending} showTrip />
      </Card>
      <Card title="Đã quyết gần đây">
        <RefundTable rows={recent} showTrip />
      </Card>
    </div>
  );
}
