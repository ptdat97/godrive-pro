#!/usr/bin/env bash
# Tạo dữ liệu mẫu cho bảng điều khiển vận hành ở chế độ dev.
#
# Chỉ dùng API công khai — không chọc thẳng vào bộ nhớ hay CSDL, nên dữ liệu
# sinh ra đi qua đúng mọi quy tắc nghiệp vụ (duyệt hồ sơ, báo giá, đặt chuyến).
#
# Yêu cầu: API đang chạy với DEV_AUTH=true.
#   ./scripts/seed-dev.sh
set -euo pipefail

API="${API:-http://localhost:8080}"

command -v python3 >/dev/null || { echo "✗ cần python3 để đọc JSON"; exit 1; }

jqf() { python3 -c "import sys,json;print(json.load(sys.stdin)$1)"; }

curl -sf "$API/healthz" >/dev/null || {
  echo "✗ API không phản hồi ở $API. Chạy 'make run' trước."
  exit 1
}

# login PHONE ROLE -> in ra access token
login() {
  local otp cid code
  otp=$(curl -s "$API/v1/auth/otp" -H 'Content-Type: application/json' \
        -d "{\"phone\":\"$1\",\"role\":\"$2\"}")
  cid=$(echo "$otp" | jqf '["challenge_id"]')
  code=$(echo "$otp" | jqf '.get("dev_code","")')
  [ -n "$code" ] || { echo "✗ DEV_AUTH phải bật để seed" >&2; exit 1; }
  curl -s "$API/v1/auth/verify" -H 'Content-Type: application/json' \
    -d "{\"challenge_id\":\"$cid\",\"code\":\"$code\",\"device_id\":\"seed\"}" \
    | jqf '["access_token"]'
}

# seed_driver PHONE NAME PLATE LAT LNG APPROVE
#
# Số CCCD và GPLX suy ra từ số điện thoại: mỗi tài xế phải có giấy tờ RIÊNG.
# Từ migration 0008, drivers.national_id_idx (chỉ mục mù) là duy nhất — hai hồ
# sơ dùng chung một số CCCD sẽ bị chặn, đúng như ngoài đời.
seed_driver() {
  local phone="$1" name="$2" plate="$3" lat="$4" lng="$5" approve="$6"
  local nid="079${phone:1}" gplx="B2-${phone:1}"
  local tok drv_id
  tok=$(login "$phone" driver)

  drv_id=$(curl -s "$API/v1/drivers/register" -H "Authorization: Bearer $tok" \
    -H 'Content-Type: application/json' -d @- <<JSON | jqf '["id"]'
{"full_name":"$name","phone":"$phone","city":"HCM",
 "vehicle":{"type":"BIKE","plate":"$plate","model":"Wave Alpha","color":"Đỏ"},
 "documents":{"national_id":"$nid","driver_license":"$gplx",
              "vehicle_reg_no":"VN-${phone:6}","insurance_no":"BH-2026-${phone:6}",
              "insurance_until":"2027-12-31"}}
JSON
)
  echo "  tài xế $name -> $drv_id"

  if [ "$approve" = "yes" ]; then
    # Duyệt hồ sơ qua API admin, rồi tài xế tự bật nhận chuyến + gửi ping.
    curl -s "$API/v1/admin/drivers/$drv_id/kyc" -H "Authorization: Bearer $ADMIN_TOKEN" \
      -H 'Content-Type: application/json' -d '{"approved":true}' >/dev/null
    curl -s "$API/v1/drivers/me/online" -H "Authorization: Bearer $tok" -X POST >/dev/null
    curl -s "$API/v1/locations/ping" -H "Authorization: Bearer $tok" \
      -H 'Content-Type: application/json' -d @- >/dev/null <<JSON
{"point":{"lat":$lat,"lng":$lng},"bearing_deg":45,"speed_mps":6,
 "accuracy_m":8,"mocked":false,"battery_pc":78,"at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
JSON
  fi
}

echo "→ Đăng nhập quản trị viên"
ADMIN_PHONE="${ADMIN_PHONE:-0909999999}"
otp=$(curl -s "$API/v1/admin/auth/otp" -H 'Content-Type: application/json' \
      -d "{\"phone\":\"$ADMIN_PHONE\"}")
if ! echo "$otp" | grep -q challenge_id; then
  echo "✗ $ADMIN_PHONE không có quyền admin. Thêm vào ADMIN_PHONES trong .env rồi khởi động lại API."
  echo "  phản hồi: $otp"
  exit 1
fi
cid=$(echo "$otp" | jqf '["challenge_id"]')
code=$(echo "$otp" | jqf '["dev_code"]')
ADMIN_TOKEN=$(curl -s "$API/v1/admin/auth/verify" -H 'Content-Type: application/json' \
  -d "{\"challenge_id\":\"$cid\",\"code\":\"$code\",\"device_id\":\"seed\"}" | jqf '["access_token"]')
echo "  ✓ có token admin"

echo "→ Tạo tài xế"
seed_driver "0911111111" "Nguyễn Văn Tài"  "59X1-111.11" 10.7740 106.6995 yes
seed_driver "0922222222" "Trần Thị Bình"   "59Y2-222.22" 10.7760 106.7010 yes
seed_driver "0933333333" "Lê Hoàng Cường"  "59Z3-333.33" 10.7700 106.6960 yes
seed_driver "0944444444" "Phạm Chờ Duyệt"  "59A4-444.44" 10.7710 106.6970 no

echo "→ Tạo chuyến của khách"
RIDER=$(login "0901234567" rider)
QUOTE=$(curl -s "$API/v1/quotes" -H "Authorization: Bearer $RIDER" \
  -H 'Content-Type: application/json' \
  -d '{"pickup":{"lat":10.7725,"lng":106.6980},"dropoff":{"lat":10.8014,"lng":106.7109}}' \
  | jqf '["quotes"][0]["id"]')

curl -s "$API/v1/trips" -H "Authorization: Bearer $RIDER" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: seed-$(date +%s)" -d @- >/dev/null <<JSON
{"quote_id":"$QUOTE",
 "pickup":{"point":{"lat":10.7725,"lng":106.6980},"address":"Chợ Bến Thành, Q.1","note":"cổng chính"},
 "dropoff":{"point":{"lat":10.8014,"lng":106.7109},"address":"Công viên Lê Văn Tám, Q.3"},
 "payment_method":"CASH"}
JSON
echo "  ✓ đã tạo chuyến tiền mặt"

echo
echo "✓ Xong. Mở http://localhost:3000 và đăng nhập bằng $ADMIN_PHONE"
