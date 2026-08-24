"use client";

import { useEffect, useMemo, useRef } from "react";
import L from "leaflet";
import type { LiveDriver, PendingPickup, Point } from "@/lib/types";
import { VEHICLE_LABEL, duration, label, relative, vnd } from "@/lib/format";

/**
 * Bản đồ vận hành trên nền OpenStreetMap.
 *
 * Vì sao OSM chứ không phải Google Maps: tile raster của OSM miễn phí và không
 * cần API key, phù hợp nguyên tắc tránh phụ thuộc Maps API tính tiền theo lượt
 * (xem README godrive mục 4). Nguyên tắc đó nhắm vào chi phí Google, không phải
 * cấm mọi bản đồ.
 *
 * Component này KHÔNG gọi API và KHÔNG tự lọc gì — dữ liệu do máy chủ Next.js
 * lấy từ /v1/admin/live-map rồi truyền xuống. Nó chỉ vẽ.
 *
 * Leaflet thao tác trực tiếp lên DOM nên phải chạy sau khi mount (không SSR
 * được — window/document không tồn tại phía máy chủ). Trang cha nạp nó qua
 * next/dynamic với ssr:false trong một Client Component.
 */

interface Props {
  center: Point;
  radiusM: number;
  drivers: LiveDriver[];
  pending: PendingPickup[];
}

const IDLE_COLOR = "#10b981";
const BUSY_COLOR = "#3b82f6";
const PICKUP_COLOR = "#f59e0b";
const PICKUP_STUCK_COLOR = "#ef4444";

/** Chuyến chờ quá ngưỡng này được tô đỏ — cùng ngưỡng cảnh báo của backend. */
const STUCK_AFTER_SEC = 60;

export default function OsmMap({ center, radiusM, drivers, pending }: Props) {
  const nodeRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<L.Map | null>(null);
  // Lớp riêng cho dữ liệu động: xoá và vẽ lại mỗi lần làm mới, giữ nguyên
  // vị trí/zoom mà người dùng đã kéo.
  const layerRef = useRef<L.LayerGroup | null>(null);

  // Khởi tạo bản đồ một lần duy nhất.
  useEffect(() => {
    if (!nodeRef.current || mapRef.current) return;

    const map = L.map(nodeRef.current, {
      center: [center.lat, center.lng],
      zoom: 14,
      // Cuộn trang là thao tác thường xuyên hơn zoom bản đồ; bắt buộc Ctrl
      // để không "nuốt" cú cuộn khi người dùng đang lướt trang.
      scrollWheelZoom: false,
    });

    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      // Ghi công là BẮT BUỘC theo giấy phép ODbL của OpenStreetMap.
      attribution:
        '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
    }).addTo(map);

    L.control.scale({ imperial: false, metric: true }).addTo(map);

    // Bật zoom bằng con lăn sau khi người dùng bấm vào bản đồ.
    map.on("click", () => map.scrollWheelZoom.enable());
    map.on("mouseout", () => map.scrollWheelZoom.disable());

    mapRef.current = map;
    layerRef.current = L.layerGroup().addTo(map);

    return () => {
      map.remove();
      mapRef.current = null;
      layerRef.current = null;
    };
    // Cố ý chỉ chạy một lần: tâm ban đầu không nên ép bản đồ nhảy về khi
    // người dùng đã kéo đi chỗ khác.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Vẽ lại dữ liệu mỗi khi nhận dữ liệu mới từ máy chủ.
  useEffect(() => {
    const map = mapRef.current;
    const layer = layerRef.current;
    if (!map || !layer) return;

    layer.clearLayers();

    // Vùng quan sát mà backend đã dùng để lọc — cho biết ranh giới dữ liệu.
    L.circle([center.lat, center.lng], {
      radius: radiusM,
      color: "#71717a",
      weight: 1,
      dashArray: "4 4",
      fill: false,
      interactive: false,
    }).addTo(layer);

    for (const p of pending) {
      const stuck = p.waiting_sec > STUCK_AFTER_SEC;
      const color = stuck ? PICKUP_STUCK_COLOR : PICKUP_COLOR;
      // Điểm đón vẽ hình vuông để phân biệt với tài xế kể cả khi in đen trắng.
      L.marker([p.point.lat, p.point.lng], {
        icon: L.divIcon({
          className: "",
          html: `<span style="display:block;width:12px;height:12px;background:${color};border:2px solid #fff;box-shadow:0 0 0 1px ${color}"></span>`,
          iconSize: [12, 12],
          iconAnchor: [6, 6],
        }),
      })
        .bindPopup(
          popupHtml([
            ["Chuyến", `<code>${escapeHtml(p.trip_id)}</code>`],
            ["Điểm đón", escapeHtml(p.address || "—")],
            ["Loại xe", label(VEHICLE_LABEL, p.vehicle_type)],
            ["Cước", vnd(p.fare)],
            [
              "Đã chờ",
              `<strong style="color:${color}">${duration(p.waiting_sec)}</strong>`,
            ],
          ]),
        )
        .addTo(layer);
    }

    for (const d of drivers) {
      const color = d.status === "IDLE" ? IDLE_COLOR : BUSY_COLOR;
      L.circleMarker([d.point.lat, d.point.lng], {
        radius: 6,
        color,
        weight: 2,
        fillColor: color,
        fillOpacity: 0.85,
      })
        .bindPopup(
          popupHtml([
            ["Tài xế", `<code>${escapeHtml(d.driver_id)}</code>`],
            ["Trạng thái", d.status === "IDLE" ? "Sẵn sàng" : "Đang bận"],
            ["Loại xe", label(VEHICLE_LABEL, d.vehicle_type)],
            ["Hướng", `${Math.round(d.bearing_deg)}°`],
            ["Pin", `${d.battery_pc}%`],
            ["Ping", relative(d.updated_at)],
          ]),
        )
        .addTo(layer);

      // Kim chỉ hướng di chuyển: bộ ghép chuyến có phạt xe chạy ngược hướng
      // điểm đón, nên hướng là thông tin vận hành thật sự, không phải trang trí.
      const rad = (d.bearing_deg * Math.PI) / 180;
      const meters = 60;
      L.polyline(
        [
          [d.point.lat, d.point.lng],
          [
            d.point.lat + (meters * Math.cos(rad)) / 110_540,
            d.point.lng +
              (meters * Math.sin(rad)) /
                (111_320 * Math.cos((d.point.lat * Math.PI) / 180)),
          ],
        ],
        { color, weight: 2, opacity: 0.7, interactive: false },
      ).addTo(layer);
    }
  }, [center.lat, center.lng, radiusM, drivers, pending]);

  const counts = useMemo(() => {
    const idle = drivers.filter((d) => d.status === "IDLE").length;
    const stuck = pending.filter((p) => p.waiting_sec > STUCK_AFTER_SEC).length;
    return { idle, busy: drivers.length - idle, stuck };
  }, [drivers, pending]);

  return (
    <div className="space-y-3">
      <div
        ref={nodeRef}
        className="h-[480px] w-full rounded-lg border border-zinc-200"
        // Leaflet cần chiều cao tường minh trước khi khởi tạo.
        style={{ minHeight: 480 }}
      />
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-zinc-600">
        <LegendDot color={IDLE_COLOR} text={`Sẵn sàng (${counts.idle})`} />
        <LegendDot color={BUSY_COLOR} text={`Đang bận (${counts.busy})`} />
        <LegendSquare
          color={PICKUP_COLOR}
          text={`Khách chờ (${pending.length})`}
        />
        {counts.stuck > 0 && (
          <LegendSquare
            color={PICKUP_STUCK_COLOR}
            text={`Chờ quá ${STUCK_AFTER_SEC}s (${counts.stuck})`}
          />
        )}
        <span className="text-zinc-400">
          Bấm vào bản đồ để bật zoom bằng con lăn
        </span>
      </div>
    </div>
  );
}

function LegendDot({ color, text }: { color: string; text: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span
        className="inline-block size-3 rounded-full"
        style={{ backgroundColor: color }}
      />
      {text}
    </span>
  );
}

function LegendSquare({ color, text }: { color: string; text: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span
        className="inline-block size-3"
        style={{ backgroundColor: color }}
      />
      {text}
    </span>
  );
}

function popupHtml(rows: [string, string][]): string {
  const body = rows
    .map(
      ([k, v]) =>
        `<tr><td style="color:#71717a;padding-right:10px;white-space:nowrap">${k}</td><td style="font-weight:500">${v}</td></tr>`,
    )
    .join("");
  return `<table style="font-size:12px;border-collapse:collapse">${body}</table>`;
}

/**
 * Popup của Leaflet nhận chuỗi HTML thô, nên mọi giá trị từ máy chủ (địa chỉ
 * do người dùng nhập, mã chuyến) phải được thoát trước khi ghép vào.
 */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
