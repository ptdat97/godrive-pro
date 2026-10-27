// ĐÂY LÀ TỆP SINH TỰ ĐỘNG — ĐỪNG SỬA TAY.
//
// Sinh từ struct Go mà API trả về, bằng godrive/cmd/gentypes:
//
//     cd godrive && make gen-types
//
// Sửa tay sẽ bị ghi đè, và TestGeneratedTypesUpToDate (chạy trong go test)
// báo đỏ khi tệp này lệch với Go. Muốn đổi hình dạng dữ liệu thì đổi struct Go.

export type DriverStatus = "OFFLINE" | "IDLE" | "ASSIGNED" | "ON_TRIP" | "SUSPENDED";

export type KYCState = "PENDING" | "APPROVED" | "REJECTED";

export type VehicleType = "BIKE" | "CAR_4" | "CAR_7";

/** DocumentKind là loại giấy tờ có thời hạn. */
export type DocumentKind = "insurance" | "license" | "inspection";

/** DocumentAlertLevel là mức cảnh báo, theo số ngày còn lại. */
export type DocumentAlertLevel = "expired" | "urgent" | "soon" | "notice";

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

export type CancelBy = "RIDER" | "DRIVER" | "SYSTEM";

export type AlertLevel = "warn" | "info";

export type Role = "rider" | "driver" | "admin";

/** Kind quyết định giao diện vẽ ô nhập nào và định dạng ra sao. */
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

/** Key là tên một nhóm cấu hình. */
export type SettingKey = "pricing" | "surge" | "matching" | "wallet" | "location";

/** Status là trạng thái của một yêu cầu hoàn tiền. */
export type RefundStatus = "PENDING" | "APPROVED" | "REJECTED";

/** ChargeTo là bên chịu khoản hoàn tiền. */
export type ChargeTo = "PLATFORM" | "DRIVER";

export interface Point {
  lat: number;
  lng: number;
}

/**
 * DriverRow là một dòng trong bảng tài xế. Đã gộp sẵn số dư ví, công nợ tiền
 * mặt và vị trí — giao diện không phải gọi thêm endpoint nào.
 */
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
  cancel_rate: number;
  completed_trips: number;
  /**
   * RatingCount là số lượt đánh giá thật. Rating 5.00 với 0 lượt đánh giá là
   * giá trị tiền nghiệm, không phải thành tích — giao diện cần phân biệt được.
   */
  rating_count: number;
  idle_since?: string;
  wallet_balance: number;
  cash_on_hand: number;
  /** InDebt = ví âm quá hạn mức -> bị chặn nhận chuyến. */
  in_debt: boolean;
  /** CanAcceptTrip nêu lý do tài xế không nhận được chuyến (rỗng = nhận được). */
  blocked_reason?: string;
  /**
   * DocumentExpiry là ba hạn giấy tờ hiện lưu — để form gia hạn hiện giá trị
   * đang có. Chỉ ngày tháng, không phải số giấy tờ: số CCCD/GPLX vẫn không
   * bao giờ ra khỏi API.
   */
  document_expiry: DocumentExpiry;
  /** DocumentAlerts là giấy tờ đã hoặc sắp hết hạn (trong 30 ngày). */
  document_alerts?: DocumentAlert[];
  last_seen?: string;
  position?: Point;
  battery_pc?: number;
  /** FraudFlags24h là số cờ gian lận trong 24 giờ gần nhất. */
  fraud_flags_24h: number;
  created_at: string;
}

/** DocumentExpiry là các hạn giấy tờ, YYYY-MM-DD; rỗng = không có hạn / chưa khai. */
export interface DocumentExpiry {
  insurance_until: string;
  license_until: string;
  inspection_until: string;
}

/** DocumentAlert là một giấy tờ sắp hoặc đã hết hạn. */
export interface DocumentAlert {
  document: DocumentKind;
  /** YYYY-MM-DD */
  until: string;
  days_left: number;
  level: DocumentAlertLevel;
}

/** TripRow là một dòng trong bảng chuyến đi. */
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
  /** WaitingSec là thời gian chuyến đã chờ ghép (chỉ có ý nghĩa khi SEARCHING). */
  waiting_sec?: number;
}

/** Event là bản ghi bất biến của một lần chuyển trạng thái. */
export interface TripEvent {
  id: string;
  trip_id: string;
  from: TripStatus;
  to: TripStatus;
  /** account id hoặc "system" */
  actor: string;
  meta?: Record<string, unknown>;
  at: string;
}

export interface Alert {
  level: AlertLevel;
  code: string;
  message: string;
  count: number;
}

/** Overview là số liệu tổng quan cho trang chủ bảng điều khiển. */
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
  /** Alerts là những thứ vận hành cần để mắt tới ngay. */
  alerts: Alert[];
  /**
   * Since là mốc tính chuyến hoàn tất/huỷ/hết hạn và tiền: 0 giờ hôm nay theo
   * giờ VN. Chuyến đang chờ ghép và đang chạy là số tức thời, không theo mốc.
   */
  since: string;
  generated_at: string;
}

/** Snapshot là trạng thái mới nhất của tài xế trong chỉ mục. */
export interface LiveDriver {
  driver_id: string;
  point: Point;
  bearing_deg: number;
  vehicle_type: VehicleType;
  status: DriverStatus;
  battery_pc: number;
  updated_at: string;
}

/** PendingPickup là một điểm đón đang chờ ghép tài xế — phía "cầu" trên bản đồ. */
export interface PendingPickup {
  trip_id: string;
  point: Point;
  address: string;
  vehicle_type: VehicleType;
  fare: number;
  /** WaitingSec càng lớn càng cần can thiệp. */
  waiting_sec: number;
}

/** LiveMapResult gộp cung (tài xế) và cầu (điểm đón chờ ghép) cùng một thời điểm. */
export interface LiveMapResult {
  center: Point;
  radius_m: number;
  drivers: LiveDriver[];
  pending: PendingPickup[];
  generated_at: string;
}

/** AuditEntry là một dòng nhật ký thao tác quản trị. Bất biến: chỉ thêm mới. */
export interface AuditEntry {
  id: string;
  actor_id: string;
  actor_phone?: string;
  action: string;
  target_type: string;
  target_id: string;
  payload?: Record<string, unknown>;
  at: string;
}

export interface Account {
  id: string;
  /** chuẩn hoá E.164: +84... */
  phone: string;
  full_name: string;
  role: Role;
  created_at: string;
}

export interface TokenPair {
  access_token: string;
  expires_at: string;
  account: Account | null;
}

export interface ApiError {
  code: string;
  message: string;
  trace_id?: string;
}

/**
 * Field mô tả một ô nhập. Path là đường dẫn dấu chấm vào JSON của nhóm,
 * ví dụ "tariffs.BIKE.per_km".
 */
export interface SettingField {
  path: string;
  label: string;
  kind: SettingKind;
  min?: number;
  max?: number;
  hint?: string;
}

/** Section gom các ô liên quan lại thành một khối trên giao diện. */
export interface SettingSection {
  title: string;
  note?: string;
  fields: SettingField[];
}

/** GroupView là một nhóm cấu hình kèm lược đồ biểu mẫu, như API trả về. */
export interface SettingGroup {
  key: SettingKey;
  label: string;
  description: string;
  /**
   * Warning hiện nổi bật trên giao diện. Chỉ đặt khi thay đổi có hệ quả
   * pháp lý hoặc tài chính mà người chỉnh cần biết TRƯỚC khi bấm lưu.
   */
  warning?: string;
  /**
   * Sections là lược đồ biểu mẫu: giao diện vẽ ô nhập từ đây chứ không tự
   * chép lại nhãn và ngưỡng. Xem schema.go.
   */
  sections: SettingSection[];
  value: Record<string, unknown>;
  version: number;
  updated_by?: string;
  updated_at?: string;
  /** IsDefault = true nghĩa là chưa từng lưu, đang chạy bằng giá trị mặc định. */
  is_default: boolean;
}

/** HistoryEntry là một lần thay đổi. */
export interface SettingHistoryEntry {
  id: string;
  key: SettingKey;
  version: number;
  old_value?: Record<string, unknown>;
  new_value: Record<string, unknown>;
  changed_by: string;
  reason?: string;
  at: string;
}

/** Refund là một yêu cầu hoàn tiền. */
export interface Refund {
  id: string;
  trip_id: string;
  rider_id: string;
  driver_id?: string;
  amount: number;
  reason: string;
  charge_to: ChargeTo;
  status: RefundStatus;
  requested_by: string;
  /** SelfApproved: khoản dưới ngưỡng, người xin tự duyệt. */
  self_approved: boolean;
  decided_by?: string;
  decision_note?: string;
  created_at: string;
  decided_at?: string;
  /**
   * Posted: bút toán đã nằm trong sổ cái. APPROVED mà chưa Posted là lần ghi
   * sổ trước bị lỗi giữa chừng — duyệt lại sẽ ghi bù, không ghi hai lần.
   */
  posted: boolean;
}
