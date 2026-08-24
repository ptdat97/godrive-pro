import Link from "next/link";
import { api, GoDriveError } from "@/lib/api";
import {
  PAYMENT_LABEL,
  TRIP_STATUS_LABEL,
  VEHICLE_LABEL,
  coords,
  dateTime,
  duration,
  label,
  timeOnly,
  vnd,
} from "@/lib/format";
import { Badge, Card, ErrorBox, Stat, tripTone } from "@/components/ui";
import type { TripEvent, TripRow } from "@/lib/types";

export const dynamic = "force-dynamic";

interface Props {
  params: Promise<{ id: string }>;
}

export default async function TripDetailPage({ params }: Props) {
  const { id } = await params;

  let trip: TripRow;
  let events: TripEvent[] = [];
  try {
    // Hai lời gọi độc lập — chạy song song cho nhanh.
    const [t, evs] = await Promise.all([
      api.get<TripRow>(`/v1/admin/trips/${id}`),
      api.get<{ events: TripEvent[] }>(`/v1/admin/trips/${id}/events`),
    ]);
    trip = t;
    events = evs.events ?? [];
  } catch (err) {
    const e = err as GoDriveError;
    return (
      <div className="space-y-4">
        <BackLink />
        <ErrorBox
          title="Không tải được chuyến"
          code={e.code}
          message={e.message}
          traceId={e.traceId}
        />
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <BackLink />

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="font-mono text-lg font-semibold tracking-tight">
            {trip.id}
          </h1>
          <p className="mt-0.5 text-sm text-zinc-500">
            Đặt lúc {dateTime(trip.requested_at)} ·{" "}
            {label(VEHICLE_LABEL, trip.vehicle_type)}
          </p>
          <div className="mt-2 flex flex-wrap gap-2">
            <Badge tone={tripTone(trip.status)}>
              {label(TRIP_STATUS_LABEL, trip.status)}
            </Badge>
            <Badge tone={trip.payment_method === "CASH" ? "amber" : "blue"}>
              {label(PAYMENT_LABEL, trip.payment_method)}
            </Badge>
            {trip.status === "SEARCHING" && trip.waiting_sec ? (
              <Badge tone={trip.waiting_sec > 60 ? "red" : "slate"}>
                chờ {duration(trip.waiting_sec)}
              </Badge>
            ) : null}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Stat label="Tổng cước" value={vnd(trip.fare)} />
        <Stat
          label="Chiết khấu nền tảng"
          value={vnd(trip.platform_fee)}
          tone="green"
        />
        <Stat label="Thu nhập tài xế" value={vnd(trip.driver_earn)} />
        <Stat
          label="Kết thúc"
          value={trip.ended_at ? timeOnly(trip.ended_at) : "—"}
          hint={trip.ended_at ? dateTime(trip.ended_at) : "chưa kết thúc"}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="Hành trình">
          <dl className="divide-y divide-zinc-50">
            <Row k="Điểm đón">
              <div>{trip.pickup_address || "—"}</div>
              <div className="font-mono text-xs text-zinc-400">
                {coords(trip.pickup)}
              </div>
            </Row>
            <Row k="Điểm đến">
              <div>{trip.dropoff_address || "—"}</div>
              <div className="font-mono text-xs text-zinc-400">
                {coords(trip.dropoff)}
              </div>
            </Row>
            <Row k="Khách hàng">
              <span className="font-mono text-xs">{trip.rider_id}</span>
            </Row>
            <Row k="Tài xế">
              {trip.driver_id ? (
                <Link
                  href={`/drivers/${trip.driver_id}`}
                  className="font-mono text-xs underline-offset-2 hover:underline"
                >
                  {trip.driver_id}
                </Link>
              ) : (
                <span className="text-zinc-400">chưa ghép</span>
              )}
            </Row>
          </dl>
        </Card>

        <Card title={`Nhật ký chuyển trạng thái (${events.length})`}>
          {events.length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-zinc-500">
              Chưa có sự kiện nào.
            </p>
          ) : (
            <ol className="px-5 py-4">
              {events.map((e, i) => (
                <li key={e.id} className="relative flex gap-3 pb-4 last:pb-0">
                  {i < events.length - 1 && (
                    <span className="absolute top-4 left-[7px] h-full w-px bg-zinc-200" />
                  )}
                  <span
                    className={`relative z-10 mt-1 size-3.5 shrink-0 rounded-full ring-4 ring-white ${dotColor(
                      e.to,
                    )}`}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline justify-between gap-2">
                      <span className="text-sm font-medium text-zinc-900">
                        {label(TRIP_STATUS_LABEL, e.to)}
                      </span>
                      <time className="font-mono text-xs text-zinc-400">
                        {timeOnly(e.at)}
                      </time>
                    </div>
                    <div className="text-xs text-zinc-500">
                      từ {label(TRIP_STATUS_LABEL, e.from)} · bởi {e.actor}
                    </div>
                    {e.meta && Object.keys(e.meta).length > 0 && (
                      <pre className="mt-1 overflow-x-auto rounded bg-zinc-50 px-2 py-1 font-mono text-[11px] text-zinc-600">
                        {JSON.stringify(e.meta)}
                      </pre>
                    )}
                  </div>
                </li>
              ))}
            </ol>
          )}
          <p className="border-t border-zinc-100 px-5 py-3 text-xs text-zinc-400">
            Bảng này chỉ thêm mới, không sửa xoá — hợp đồng vận tải điện tử theo
            Nghị định 10/2020, lưu tối thiểu 3 năm.
          </p>
        </Card>
      </div>
    </div>
  );
}

function dotColor(status: string): string {
  switch (status) {
    case "COMPLETED":
    case "PAID":
      return "bg-emerald-500";
    case "CANCELLED":
    case "EXPIRED":
      return "bg-red-500";
    case "SEARCHING":
      return "bg-amber-500";
    default:
      return "bg-blue-500";
  }
}

function BackLink() {
  return (
    <Link
      href="/trips"
      className="text-sm text-zinc-500 underline-offset-2 hover:text-zinc-900 hover:underline"
    >
      ← Danh sách chuyến
    </Link>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 px-5 py-2.5 text-sm">
      <dt className="shrink-0 text-zinc-500">{k}</dt>
      <dd className="text-right font-medium text-zinc-900">{children}</dd>
    </div>
  );
}
