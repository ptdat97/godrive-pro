import Link from "next/link";
import { api, GoDriveError } from "@/lib/api";
import {
  PAYMENT_LABEL,
  TRIP_STATUS_LABEL,
  VEHICLE_LABEL,
  duration,
  label,
  relative,
  vnd,
} from "@/lib/format";
import { Badge, Card, ErrorBox, Table, Td, tripTone } from "@/components/ui";
import Refresher from "@/components/refresher";
import StatusTabs from "./status-tabs";
import type { TripRow } from "@/lib/types";

export const metadata = { title: "Chuyến đi · GoDrive" };
export const dynamic = "force-dynamic";

interface Props {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function TripsPage({ searchParams }: Props) {
  const sp = await searchParams;
  const status = typeof sp.status === "string" ? sp.status : "";

  const qs = new URLSearchParams();
  if (status) qs.set("status", status);

  let rows: TripRow[];
  try {
    const res = await api.get<{ trips: TripRow[] }>(
      `/v1/admin/trips?${qs.toString()}`,
    );
    rows = res.trips;
  } catch (err) {
    const e = err as GoDriveError;
    return <ErrorBox code={e.code} message={e.message} traceId={e.traceId} />;
  }

  return (
    <div className="space-y-5">
      <div className="flex items-baseline justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Chuyến đi</h1>
          <p className="mt-0.5 text-sm text-zinc-500">{rows.length} chuyến</p>
        </div>
        <Refresher seconds={10} />
      </div>

      <StatusTabs current={status} />

      <Card>
        <Table
          head={[
            "Chuyến",
            "Trạng thái",
            "Hành trình",
            "Cước",
            "Thanh toán",
            "Tài xế",
            "Thời gian",
            "",
          ]}
          empty={rows.length === 0 ? "Chưa có chuyến nào." : undefined}
        >
          {rows.map((t) => (
            <tr key={t.id} className="hover:bg-zinc-50/60">
              <Td>
                <div className="font-mono text-xs text-zinc-600">{t.id}</div>
                <div className="text-xs text-zinc-500">
                  {label(VEHICLE_LABEL, t.vehicle_type)}
                </div>
              </Td>
              <Td>
                <Badge tone={tripTone(t.status)}>
                  {label(TRIP_STATUS_LABEL, t.status)}
                </Badge>
                {t.status === "SEARCHING" && t.waiting_sec ? (
                  <div
                    className={`mt-1 text-xs ${
                      t.waiting_sec > 60 ? "text-red-600" : "text-zinc-500"
                    }`}
                  >
                    chờ {duration(t.waiting_sec)}
                  </div>
                ) : null}
              </Td>
              <Td className="max-w-64">
                <div className="truncate text-xs" title={t.pickup_address}>
                  {t.pickup_address || "—"}
                </div>
                <div
                  className="truncate text-xs text-zinc-500"
                  title={t.dropoff_address}
                >
                  → {t.dropoff_address || "—"}
                </div>
              </Td>
              <Td>
                <div className="font-medium">{vnd(t.fare)}</div>
                <div className="text-xs text-zinc-500">
                  phí {vnd(t.platform_fee)}
                </div>
              </Td>
              <Td>
                <Badge tone={t.payment_method === "CASH" ? "amber" : "blue"}>
                  {label(PAYMENT_LABEL, t.payment_method)}
                </Badge>
              </Td>
              <Td>
                {t.driver_id ? (
                  <Link
                    href={`/drivers/${t.driver_id}`}
                    className="font-mono text-xs text-zinc-600 underline-offset-2 hover:text-zinc-900 hover:underline"
                  >
                    {t.driver_id}
                  </Link>
                ) : (
                  <span className="text-xs text-zinc-400">chưa ghép</span>
                )}
              </Td>
              <Td>
                <div className="text-xs">{relative(t.requested_at)}</div>
              </Td>
              <Td>
                <Link
                  href={`/trips/${t.id}`}
                  className="text-xs font-medium text-zinc-600 underline-offset-2 hover:text-zinc-900 hover:underline"
                >
                  Chi tiết
                </Link>
              </Td>
            </tr>
          ))}
        </Table>
      </Card>
    </div>
  );
}
