// Kiểu dữ liệu khớp JSON trả về từ API Go (internal/admin/domain.go).
// Đây chỉ là mô tả hình dạng dữ liệu — mọi logic nghiệp vụ nằm ở godrive.

export type DriverStatus =
  "OFFLINE" | "IDLE" | "ASSIGNED" | "ON_TRIP" | "SUSPENDED";

export type KYCState = "PENDING" | "APPROVED" | "REJECTED";

export type VehicleType = "BIKE" | "CAR_4" | "CAR_7";

export type TripStatus =
  | "CREATED"
  | "SEARCHING"
  | "ASSIGNED"
  | "ARRIVED"
  | "IN_PROGRESS"
  | "COMPLETED"
  | "PAID"
  | "CANCELLED"
  | "EXPIRED";

export type PaymentMethod = "CASH" | "MOMO" | "ZALOPAY" | "VNPAY" | "WALLET";

export interface Point {
  lat: number;
  lng: number;
}

export interface DriverRow {
  id: string;
  full_name: string;
  phone: string;
  city: string;
  vehicle_type: VehicleType;
  vehicle_plate: string;
  kyc: KYCState;
  status: DriverStatus;
  rating: number;
  acceptance_rate: number;
  completed_trips: number;
  wallet_balance: number;
  cash_on_hand: number;
  in_debt: boolean;
  /** Mã lý do tài xế không nhận được chuyến; rỗng nghĩa là nhận được. */
  blocked_reason?: string;
  last_seen?: string;
  position?: Point;
  battery_pc?: number;
  fraud_flags_24h: number;
  created_at: string;
}

export interface TripRow {
  id: string;
  status: TripStatus;
  rider_id: string;
  driver_id?: string;
  vehicle_type: VehicleType;
  pickup_address: string;
  dropoff_address: string;
  pickup: Point;
  dropoff: Point;
  fare: number;
  platform_fee: number;
  driver_earn: number;
  payment_method: PaymentMethod;
  requested_at: string;
  ended_at?: string;
  waiting_sec?: number;
}

export interface TripEvent {
  id: string;
  trip_id: string;
  from: TripStatus;
  to: TripStatus;
  actor: string;
  meta?: Record<string, unknown>;
  at: string;
}

export interface Alert {
  level: "warn" | "info";
  code: string;
  message: string;
  count: number;
}

export interface Overview {
  drivers: {
    online: number;
    on_trip: number;
    offline: number;
    suspended: number;
    pending_kyc: number;
  };
  trips: {
    searching: number;
    active: number;
    completed: number;
    cancelled: number;
    expired: number;
  };
  money: {
    gross: number;
    platform_fee: number;
    driver_earn: number;
    cash_share: number;
  };
  alerts: Alert[];
  generated_at: string;
}

export interface LiveDriver {
  driver_id: string;
  point: Point;
  bearing_deg: number;
  vehicle_type: VehicleType;
  status: DriverStatus;
  battery_pc: number;
  updated_at: string;
}

/** Điểm đón đang chờ ghép tài xế — phía "cầu" trên bản đồ. */
export interface PendingPickup {
  trip_id: string;
  point: Point;
  address: string;
  vehicle_type: VehicleType;
  fare: number;
  waiting_sec: number;
}

export interface LiveMapResult {
  center: Point;
  radius_m: number;
  drivers: LiveDriver[];
  pending: PendingPickup[];
  generated_at: string;
}

export interface Account {
  id: string;
  phone: string;
  full_name: string;
  role: "rider" | "driver" | "admin";
  created_at: string;
}

export interface TokenPair {
  access_token: string;
  expires_at: string;
  account: Account;
}

/** Hình dạng lỗi chuẩn của API: {"code","message","trace_id"}. */
export interface ApiError {
  code: string;
  message: string;
  trace_id?: string;
}

// ==== Cấu hình vận hành ====

/** Kiểu ô nhập, do backend quyết định (internal/settings/schema.go). */
export type SettingKind =
  | "vnd"
  | "permille"
  | "int"
  | "float"
  | "bool"
  | "hour"
  | "seconds"
  | "meters"
  | "surge_steps";

export interface SettingField {
  /** Đường dẫn dấu chấm vào JSON của nhóm, ví dụ "tariffs.BIKE.per_km". */
  path: string;
  label: string;
  kind: SettingKind;
  min?: number;
  max?: number;
  hint?: string;
}

export interface SettingSection {
  title: string;
  note?: string;
  fields: SettingField[];
}

/**
 * Một nhóm cấu hình kèm lược đồ biểu mẫu.
 *
 * Nhãn, đơn vị và ngưỡng đều do backend phát ra — giao diện không tự chép lại,
 * nếu không hai bên sẽ trôi khỏi nhau.
 */
export interface SettingGroup {
  key: string;
  label: string;
  description: string;
  /** Cảnh báo hệ quả pháp lý hoặc tài chính. Hiện nổi bật trước khi lưu. */
  warning?: string;
  sections: SettingSection[];
  value: Record<string, unknown>;
  version: number;
  updated_by?: string;
  updated_at?: string;
  /** true nghĩa là chưa từng lưu, đang chạy bằng giá trị mặc định trong code. */
  is_default: boolean;
}

export interface SettingHistoryEntry {
  id: string;
  key: string;
  version: number;
  /** Vắng ở lần ghi đầu tiên: trước đó nhóm chạy bằng mặc định trong code. */
  old_value?: Record<string, unknown>;
  new_value: Record<string, unknown>;
  changed_by: string;
  reason?: string;
  at: string;
}
