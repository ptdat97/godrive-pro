import Link from "next/link";
import { api, GoDriveError } from "@/lib/api";
import {
  DRIVER_STATUS_LABEL,
  VEHICLE_LABEL,
  coords,
  duration,
  label,
  relative,
  vnd,
} from "@/lib/format";
import {
  Badge,
  Card,
  ErrorBox,
  Stat,
  Table,
  Td,
  driverTone,
} from "@/components/ui";
import Refresher from "@/components/refresher";
import MapPanel from "./map-panel";
import AreaPicker from "./area-picker";
import { areaOf } from "./areas";
import type { LiveMapResult } from "@/lib/types";

export const metadata = { title: "Bản đồ · GoDrive" };
export const dynamic = "force-dynamic";

interface Props {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function LivePage({ searchParams }: Props) {
  const sp = await searchParams;
  const one = (k: string) => (typeof sp[k] === "string" ? sp[k] : "");

  const idleOnly = one("idle") === "1";
  const areaKey = one("area") || "ben-thanh";
  const area = areaOf(areaKey);
  const radius = one("radius") || "5000";

  // Tham số chuyển thẳng cho backend — vùng quan sát và bộ lọc do Go quyết định.
  const qs = new URLSearchParams({
    lat: String(area.lat),
    lng: String(area.lng),
    radius,
  });
  if (idleOnly) qs.set("idle", "1");

  let res: LiveMapResult;
  try {
    res = await api.get<LiveMapResult>(`/v1/admin/live-map?${qs.toString()}`);
  } catch (err) {
    const e = err as GoDriveError;
    return <ErrorBox code={e.code} message={e.message} traceId={e.traceId} />;
  }

  const idle = res.drivers.filter((d) => d.status === "IDLE").length;
  const stuck = res.pending.filter((p) => p.waiting_sec > 60).length;

  return (
    <div className="space-y-5">
      <div className="flex items-baseline justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">
            Bản đồ trực tuyến
          </h1>
          <p className="mt-0.5 text-sm text-zinc-500">
            {area.name} · bán kính {Number(res.radius_m) / 1000} km
          </p>
        </div>
        <Refresher seconds={10} />
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Stat label="Tài xế trong vùng" value={res.drivers.length} />
        <Stat label="Đang sẵn sàng" value={idle} tone="green" />
        <Stat
          label="Khách đang chờ"
          value={res.pending.length}
          tone={res.pending.length > 0 ? "amber" : "neutral"}
        />
        <Stat
          label="Chờ quá 60 giây"
          value={stuck}
          tone={stuck > 0 ? "red" : "neutral"}
          hint={stuck > 0 ? "cần can thiệp" : undefined}
        />
      </div>

      <AreaPicker area={areaKey} radius={radius} idleOnly={idleOnly} />

      <Card title="Bản đồ">
        <div className="p-5">
          <MapPanel
            center={res.center}
            radiusM={res.radius_m}
            drivers={res.drivers}
            pending={res.pending}
          />
        </div>
      </Card>

      {res.pending.length > 0 && (
        <Card title={`Khách đang chờ ghép (${res.pending.length})`}>
          <Table
            head={["Chuyến", "Điểm đón", "Loại xe", "Cước", "Đã chờ", ""]}
          >
            {res.pending.map((p) => (
              <tr key={p.trip_id} className="hover:bg-zinc-50/60">
                <Td>
                  <span className="font-mono text-xs">{p.trip_id}</span>
                </Td>
                <Td className="max-w-72">
                  <div className="truncate text-xs" title={p.address}>
                    {p.address || "—"}
                  </div>
                  <div className="font-mono text-xs text-zinc-400">
                    {coords(p.point)}
                  </div>
                </Td>
                <Td>{label(VEHICLE_LABEL, p.vehicle_type)}</Td>
                <Td>{vnd(p.fare)}</Td>
                <Td>
                  <span
                    className={
                      p.waiting_sec > 60 ? "font-medium text-red-600" : ""
                    }
                  >
                    {duration(p.waiting_sec)}
                  </span>
                </Td>
                <Td>
                  <Link
                    href={`/trips/${p.trip_id}`}
                    className="text-xs font-medium text-zinc-600 underline-offset-2 hover:text-zinc-900 hover:underline"
                  >
                    Chi tiết
                  </Link>
                </Td>
              </tr>
            ))}
          </Table>
        </Card>
      )}

      <Card title={`Tài xế trong vùng (${res.drivers.length})`}>
        <Table
          head={["Tài xế", "Trạng thái", "Loại xe", "Toạ độ", "Hướng", "Pin", "Ping"]}
          empty={
            res.drivers.length === 0
              ? "Không có tài xế nào đang gửi ping trong vùng này."
              : undefined
          }
        >
          {res.drivers.map((d) => (
            <tr key={d.driver_id} className="hover:bg-zinc-50/60">
              <Td>
                <Link
                  href={`/drivers/${d.driver_id}`}
                  className="font-mono text-xs underline-offset-2 hover:underline"
                >
                  {d.driver_id}
                </Link>
              </Td>
              <Td>
                <Badge tone={driverTone(d.status)}>
                  {label(DRIVER_STATUS_LABEL, d.status)}
                </Badge>
              </Td>
              <Td>{label(VEHICLE_LABEL, d.vehicle_type)}</Td>
              <Td>
                <span className="font-mono text-xs">{coords(d.point)}</span>
              </Td>
              <Td>{Math.round(d.bearing_deg)}°</Td>
              <Td>{d.battery_pc}%</Td>
              <Td className="text-xs text-zinc-500">{relative(d.updated_at)}</Td>
            </tr>
          ))}
        </Table>
      </Card>

      <p className="text-xs text-zinc-400">
        Chỉ hiện tài xế có ping dưới 45 giây (<code>location.StaleAfter</code>) —
        bản đồ trống nghĩa là không ai đang gửi ping, không phải lỗi. Vị trí lấy
        từ chỉ mục trong bộ nhớ; bản production dùng Redis GEO.
      </p>
    </div>
  );
}
