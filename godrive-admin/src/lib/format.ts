// Hàm định dạng hiển thị. Thuần trình bày — không có luật nghiệp vụ nào ở đây.

/** Tiền VND: backend trả int64 đồng. Không bao giờ chia float. */
export function vnd(amount: number): string {
  const sign = amount < 0 ? "-" : "";
  const abs = Math.abs(amount);
  return `${sign}${abs.toLocaleString("vi-VN")}₫`;
}

export function percent(ratio: number, digits = 0): string {
  return `${(ratio * 100).toFixed(digits)}%`;
}

export function dateTime(iso: string): string {
  return new Date(iso).toLocaleString("vi-VN", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function timeOnly(iso: string): string {
  return new Date(iso).toLocaleTimeString("vi-VN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** Khoảng thời gian tương đối, ví dụ "3 phút trước". */
export function relative(iso: string, now = Date.now()): string {
  const diffMs = now - new Date(iso).getTime();
  const sec = Math.round(diffMs / 1000);
  if (sec < 0) return "vừa xong";
  if (sec < 60) return `${sec} giây trước`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min} phút trước`;
  const hour = Math.round(min / 60);
  if (hour < 24) return `${hour} giờ trước`;
  return `${Math.round(hour / 24)} ngày trước`;
}

export function duration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s} giây`;
  const m = Math.floor(s / 60);
  const rest = s % 60;
  if (m < 60) return rest ? `${m} phút ${rest} giây` : `${m} phút`;
  const h = Math.floor(m / 60);
  return `${h} giờ ${m % 60} phút`;
}

export function coords(p: { lat: number; lng: number }): string {
  return `${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`;
}

// ==== Nhãn tiếng Việt cho mã trạng thái ====

export const DRIVER_STATUS_LABEL: Record<string, string> = {
  OFFLINE: "Ngoại tuyến",
  IDLE: "Sẵn sàng",
  ASSIGNED: "Đang tới đón",
  ON_TRIP: "Đang chở khách",
  SUSPENDED: "Tạm khoá",
};

export const KYC_LABEL: Record<string, string> = {
  PENDING: "Chờ duyệt",
  APPROVED: "Đã duyệt",
  REJECTED: "Từ chối",
};

export const TRIP_STATUS_LABEL: Record<string, string> = {
  CREATED: "Vừa tạo",
  SEARCHING: "Đang tìm tài xế",
  ASSIGNED: "Đã ghép",
  ARRIVED: "Đã tới điểm đón",
  IN_PROGRESS: "Đang chạy",
  COMPLETED: "Hoàn tất",
  PAID: "Đã ghi sổ",
  CANCELLED: "Đã huỷ",
  EXPIRED: "Không tìm được tài xế",
};

export const VEHICLE_LABEL: Record<string, string> = {
  BIKE: "Xe máy",
  CAR_4: "Ô tô 4 chỗ",
  CAR_7: "Ô tô 7 chỗ",
};

export const PAYMENT_LABEL: Record<string, string> = {
  CASH: "Tiền mặt",
  MOMO: "MoMo",
  ZALOPAY: "ZaloPay",
  VNPAY: "VNPay",
  WALLET: "Ví",
};

/** Lý do tài xế bị chặn nhận chuyến — mã ổn định từ backend. */
export const BLOCKED_REASON_LABEL: Record<string, string> = {
  kyc_not_approved: "Chưa duyệt hồ sơ",
  driver_suspended: "Đang bị khoá",
  driver_busy: "Đang có chuyến",
  wallet_debt_exceeded: "Nợ vượt hạn mức",
};

export function label(map: Record<string, string>, key: string): string {
  return map[key] ?? key;
}
