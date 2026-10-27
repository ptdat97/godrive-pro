import Link from "next/link";
import { Badge, Table, Td } from "@/components/ui";
import { CHARGE_TO_LABEL, REFUND_STATUS_LABEL, dateTime, label, vnd } from "@/lib/format";
import type { Refund } from "@/lib/types";
import Decide from "./decide";

function tone(status: string) {
  return status === "APPROVED" ? "green" : status === "REJECTED" ? "neutral" : "amber";
}

/** Bảng yêu cầu hoàn tiền, dùng chung cho trang Hoàn tiền và trang chuyến. */
export default function RefundTable({ rows, showTrip }: { rows: Refund[]; showTrip?: boolean }) {
  return (
    <Table
      head={[...(showTrip ? ["Chuyến"] : []), "Số tiền", "Lý do", "Trạng thái", "Người xin", "Thời gian", ""]}
      empty={rows.length === 0 ? "Chưa có yêu cầu hoàn tiền nào." : undefined}
    >
      {rows.map((r) => (
        <tr key={r.id} className="align-top hover:bg-zinc-50/60">
          {showTrip && (
            <Td>
              <Link href={`/trips/${r.trip_id}`} className="font-mono text-xs underline-offset-2 hover:underline">
                {r.trip_id}
              </Link>
            </Td>
          )}
          <Td>
            <div className="font-medium">{vnd(r.amount)}</div>
            <div className="text-xs text-zinc-500">{label(CHARGE_TO_LABEL, r.charge_to)}</div>
          </Td>
          <Td>
            <div className="max-w-xs text-sm">{r.reason}</div>
            {r.decision_note && <div className="mt-1 text-xs text-zinc-500">↳ {r.decision_note}</div>}
          </Td>
          <Td>
            <Badge tone={tone(r.status)}>{label(REFUND_STATUS_LABEL, r.status)}</Badge>
            {r.self_approved && <div className="mt-1 text-xs text-zinc-500">tự duyệt (dưới ngưỡng)</div>}
            {r.status === "APPROVED" && !r.posted && (
              // Đã duyệt nhưng chưa ghi sổ: lần trước lỗi giữa chừng. Bấm duyệt
              // lại sẽ ghi bù — backend không bao giờ ghi hai lần.
              <div className="mt-1 text-xs text-red-600">chưa vào sổ — bấm Duyệt để ghi bù</div>
            )}
          </Td>
          <Td>
            <span className="font-mono text-xs">{r.requested_by}</span>
            {r.decided_by && !r.self_approved && (
              <div className="font-mono text-xs text-zinc-500">duyệt: {r.decided_by}</div>
            )}
          </Td>
          <Td>
            <span className="text-xs">{dateTime(r.created_at)}</span>
          </Td>
          <Td>
            {(r.status === "PENDING" || (r.status === "APPROVED" && !r.posted)) && (
              <Decide id={r.id} tripId={r.trip_id} />
            )}
          </Td>
        </tr>
      ))}
    </Table>
  );
}
