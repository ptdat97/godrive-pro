import Link from "next/link";
import { api, GoDriveError } from "@/lib/api";
import {
  BLOCKED_REASON_LABEL,
  DRIVER_STATUS_LABEL,
  KYC_LABEL,
  VEHICLE_LABEL,
  coords,
  dateTime,
  label,
  relative,
  vnd,
} from "@/lib/format";
import {
  Badge,
  Card,
  ErrorBox,
  Stat,
  driverTone,
  kycTone,
} from "@/components/ui";
import KycActions from "./kyc-actions";
import type { DriverRow } from "@/lib/types";

export const dynamic = "force-dynamic";

interface Props {
  params: Promise<{ id: string }>;
}

export default async function DriverDetailPage({ params }: Props) {
  const { id } = await params;

  let d: DriverRow;
  try {
    d = await api.get<DriverRow>(`/v1/admin/drivers/${id}`);
  } catch (err) {
    const e = err as GoDriveError;
    return (
      <div className="space-y-4">
        <BackLink />
        <ErrorBox
          title="Không tải được hồ sơ tài xế"
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
          <h1 className="text-xl font-semibold tracking-tight">
            {d.full_name}
          </h1>
          <p className="mt-0.5 text-sm text-zinc-500">
            {d.phone} · {d.city} · tham gia {dateTime(d.created_at)}
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Badge tone={driverTone(d.status)}>
              {label(DRIVER_STATUS_LABEL, d.status)}
            </Badge>
            <Badge tone={kycTone(d.kyc)}>{label(KYC_LABEL, d.kyc)}</Badge>
            {d.blocked_reason && (
              <Badge tone="red" title={d.blocked_reason}>
                Không nhận được chuyến:{" "}
                {label(BLOCKED_REASON_LABEL, d.blocked_reason)}
              </Badge>
            )}
          </div>
        </div>
        <KycActions driverId={d.id} kyc={d.kyc} />
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Stat
          label="Số dư ví"
          value={vnd(d.wallet_balance)}
          tone={d.in_debt ? "red" : d.wallet_balance < 0 ? "amber" : "neutral"}
          hint={d.wallet_balance < 0 ? "âm = đang nợ chiết khấu" : undefined}
        />
        <Stat
          label="Tiền mặt đang giữ"
          value={vnd(d.cash_on_hand)}
          hint="thu hộ nền tảng"
        />
        <Stat label="Đánh giá" value={`${d.rating.toFixed(2)} ★`} />
        <Stat
          label="Tỉ lệ nhận chuyến"
          value={`${(d.acceptance_rate * 100).toFixed(0)}%`}
          hint={`${d.completed_trips} chuyến hoàn tất`}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="Phương tiện">
          <dl className="divide-y divide-zinc-50">
            <Row k="Biển số">
              <span className="font-mono">{d.vehicle_plate}</span>
            </Row>
            <Row k="Loại xe">{label(VEHICLE_LABEL, d.vehicle_type)}</Row>
            <Row k="Thành phố">{d.city}</Row>
          </dl>
        </Card>

        <Card title="Vị trí & an toàn">
          <dl className="divide-y divide-zinc-50">
            <Row k="Toạ độ">
              {d.position ? (
                <span className="font-mono text-xs">{coords(d.position)}</span>
              ) : (
                <span className="text-zinc-400">chưa có ping</span>
              )}
            </Row>
            <Row k="Ping gần nhất">
              {d.last_seen ? relative(d.last_seen) : "—"}
            </Row>
            <Row k="Pin thiết bị">
              {d.battery_pc ? `${d.battery_pc}%` : "—"}
            </Row>
            <Row k="Cờ gian lận 24h">
              {d.fraud_flags_24h > 0 ? (
                <Badge tone="red">{d.fraud_flags_24h}</Badge>
              ) : (
                <span className="text-zinc-400">không có</span>
              )}
            </Row>
          </dl>
        </Card>
      </div>

      <p className="text-xs text-zinc-400">
        Giấy tờ định danh (CCCD, GPLX) không hiển thị ở đây — dữ liệu nhạy cảm
        theo Nghị định 13/2023, API không trả về.
      </p>
    </div>
  );
}

function BackLink() {
  return (
    <Link
      href="/drivers"
      className="text-sm text-zinc-500 underline-offset-2 hover:text-zinc-900 hover:underline"
    >
      ← Danh sách tài xế
    </Link>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-5 py-2.5 text-sm">
      <dt className="text-zinc-500">{k}</dt>
      <dd className="text-right font-medium text-zinc-900">{children}</dd>
    </div>
  );
}
