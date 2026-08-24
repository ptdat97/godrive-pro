"use client";

import dynamic from "next/dynamic";
import type { LiveDriver, PendingPickup, Point } from "@/lib/types";

/**
 * Vỏ bọc phía client cho bản đồ.
 *
 * Leaflet đụng thẳng vào `window`/`document` nên không dựng được ở phía máy chủ.
 * `ssr: false` chỉ hợp lệ bên trong Client Component (Server Component dùng sẽ
 * báo lỗi), nên phải có tệp trung gian này giữa trang và bản đồ.
 */
const OsmMap = dynamic(() => import("./osm-map"), {
  ssr: false,
  loading: () => (
    <div className="flex h-[480px] w-full items-center justify-center rounded-lg border border-zinc-200 bg-zinc-50 text-sm text-zinc-400">
      Đang tải bản đồ…
    </div>
  ),
});

export default function MapPanel(props: {
  center: Point;
  radiusM: number;
  drivers: LiveDriver[];
  pending: PendingPickup[];
}) {
  return <OsmMap {...props} />;
}
