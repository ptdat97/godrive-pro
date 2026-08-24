/**
 * Danh sách vùng quan sát của bản đồ vận hành.
 *
 * Để ở tệp riêng (không có "use client") vì cả Server Component (trang /live,
 * để tra toạ độ trước khi gọi API) lẫn Client Component (bộ chọn vùng) đều
 * dùng. Ranh giới server/client chỉ cho phép truyền component, không truyền
 * dữ liệu tĩnh — import mảng từ tệp "use client" sẽ nhận về proxy rỗng.
 *
 * Danh sách cố định thay vì cho nhập toạ độ tự do: điều phối viên nghĩ theo
 * khu vực ("Thủ Đức đang thiếu xe"), không theo vĩ độ/kinh độ.
 */
export interface Area {
  key: string;
  name: string;
  lat: number;
  lng: number;
}

export const AREAS: Area[] = [
  { key: "ben-thanh", name: "Chợ Bến Thành, Q.1", lat: 10.7725, lng: 106.698 },
  { key: "tan-binh", name: "Sân bay Tân Sơn Nhất", lat: 10.8188, lng: 106.6519 },
  { key: "thu-duc", name: "TP. Thủ Đức", lat: 10.8494, lng: 106.7537 },
  { key: "phu-nhuan", name: "Phú Nhuận", lat: 10.7995, lng: 106.6802 },
  { key: "ha-noi", name: "Hồ Gươm, Hà Nội", lat: 21.0285, lng: 105.8542 },
];

export const RADII: [string, string][] = [
  ["2000", "2 km"],
  ["5000", "5 km"],
  ["10000", "10 km"],
  ["20000", "20 km"],
];

/** Tra vùng theo khoá, rơi về vùng mặc định nếu khoá lạ. */
export function areaOf(key: string): Area {
  return AREAS.find((a) => a.key === key) ?? AREAS[0];
}
