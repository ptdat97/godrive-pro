import Link from "next/link";
import { api, GoDriveError } from "@/lib/api";
import {
  BLOCKED_REASON_LABEL,
  DRIVER_STATUS_LABEL,
  KYC_LABEL,
  VEHICLE_LABEL,
  label,
  relative,
  vnd,
} from "@/lib/format";
import {
  Badge,
  Card,
  ErrorBox,
  Table,
  Td,
  driverTone,
  kycTone,
} from "@/components/ui";
import DriverFilters from "./filters";
import type { DriverRow } from "@/lib/types";

export const metadata = { title: "Tài xế · GoDrive" };
export const dynamic = "force-dynamic";

interface Props {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function DriversPage({ searchParams }: Props) {
  const sp = await searchParams;
  const one = (k: string) => {
    const v = sp[k];
    return typeof v === "string" ? v : "";
  };

  // Bộ lọc chuyển thẳng cho backend — giao diện không tự lọc mảng.
  const qs = new URLSearchParams();
  for (const key of ["status", "kyc", "city", "q", "debt"]) {
    const v = one(key);
    if (v) qs.set(key, v);
  }

  let rows: DriverRow[];
  try {
    const res = await api.get<{ drivers: DriverRow[]; count: number }>(
      `/v1/admin/drivers?${qs.toString()}`,
    );
    rows = res.drivers;
  } catch (err) {
    const e = err as GoDriveError;
    return <ErrorBox code={e.code} message={e.message} traceId={e.traceId} />;
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Tài xế</h1>
        <p className="mt-0.5 text-sm text-zinc-500">
          {rows.length} tài xế khớp bộ lọc
        </p>
      </div>

      <DriverFilters />

      <Card>
        <Table
          head={[
            "Tài xế",
            "Xe",
            "Trạng thái",
            "Hồ sơ",
            "Ví",
            "Tiền mặt giữ",
            "Đánh giá",
            "Vị trí",
            "",
          ]}
          empty={rows.length === 0 ? "Không có tài xế nào khớp bộ lọc." : undefined}
        >
          {rows.map((d) => (
            <tr key={d.id} className="hover:bg-zinc-50/60">
              <Td>
                <div className="font-medium text-zinc-900">{d.full_name}</div>
                <div className="text-xs text-zinc-500">{d.phone}</div>
              </Td>
              <Td>
                <div className="font-mono text-xs">{d.vehicle_plate}</div>
                <div className="text-xs text-zinc-500">
                  {label(VEHICLE_LABEL, d.vehicle_type)}
                </div>
              </Td>
              <Td>
                <Badge tone={driverTone(d.status)}>
                  {label(DRIVER_STATUS_LABEL, d.status)}
                </Badge>
                {d.blocked_reason && (
                  <div className="mt-1">
                    <Badge tone="red" title={d.blocked_reason}>
                      {label(BLOCKED_REASON_LABEL, d.blocked_reason)}
                    </Badge>
                  </div>
                )}
              </Td>
              <Td>
                <Badge tone={kycTone(d.kyc)}>{label(KYC_LABEL, d.kyc)}</Badge>
              </Td>
              <Td className={d.in_debt ? "text-red-600" : ""}>
                <span className="font-medium">{vnd(d.wallet_balance)}</span>
                {d.in_debt && (
                  <div className="text-xs text-red-500">vượt hạn mức</div>
                )}
              </Td>
              <Td>{vnd(d.cash_on_hand)}</Td>
              <Td>
                <div className="whitespace-nowrap">
                  {d.rating.toFixed(2)} ★
                </div>
                <div className="text-xs text-zinc-500">
                  nhận {(d.acceptance_rate * 100).toFixed(0)}% · {d.completed_trips}{" "}
                  chuyến
                </div>
              </Td>
              <Td>
                {d.last_seen ? (
                  <>
                    <div className="text-xs">{relative(d.last_seen)}</div>
                    {d.battery_pc ? (
                      <div className="text-xs text-zinc-500">
                        pin {d.battery_pc}%
                      </div>
                    ) : null}
                  </>
                ) : (
                  <span className="text-xs text-zinc-400">chưa có ping</span>
                )}
                {d.fraud_flags_24h > 0 && (
                  <div className="mt-1">
                    <Badge tone="red">{d.fraud_flags_24h} cờ gian lận</Badge>
                  </div>
                )}
              </Td>
              <Td>
                <Link
                  href={`/drivers/${d.id}`}
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
