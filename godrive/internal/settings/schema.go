package settings

import "fmt"

// Lược đồ biểu mẫu cho giao diện quản trị.
//
// Vì sao lược đồ nằm ở Go chứ không ở React: nhãn, đơn vị và ngưỡng hợp lệ là
// KIẾN THỨC NGHIỆP VỤ, và nó đã sống sẵn ở groups.go cạnh hàm Validate. Nếu
// giao diện tự chép lại thì hai bản sẽ trôi khỏi nhau — người chỉnh thấy ô
// "tối đa 500.000đ" rồi bị máy chủ từ chối ở 200.000đ mà không hiểu vì sao.
// Ở đây chỉ có MỘT bản, và schema_test.go chốt nó khớp với Validate.
//
// Giao diện vẫn không được tin lược đồ này để bỏ qua kiểm tra: máy chủ mới là
// nơi quyết định. Lược đồ chỉ để vẽ đúng ô và bắt lỗi sớm cho người dùng.

// Kind quyết định giao diện vẽ ô nhập nào và định dạng ra sao.
type Kind string

const (
	KindVND      Kind = "vnd"      // tiền đồng, hiện dấu phân nhóm
	KindPermille Kind = "permille" // phần nghìn, hiện kèm quy đổi ra %
	KindInt      Kind = "int"
	KindFloat    Kind = "float"
	KindBool     Kind = "bool"
	KindHour     Kind = "hour"    // giờ trong ngày 0..23
	KindSeconds  Kind = "seconds" // hiện kèm quy đổi ra phút
	KindMeters   Kind = "meters"  // hiện kèm quy đổi ra km
	// KindSurgeSteps là bậc thang tăng giá: một bảng thêm/bớt được dòng.
	KindSurgeSteps Kind = "surge_steps"
)

// Field mô tả một ô nhập. Path là đường dẫn dấu chấm vào JSON của nhóm,
// ví dụ "tariffs.BIKE.per_km".
type Field struct {
	Path  string   `json:"path"`
	Label string   `json:"label"`
	Kind  Kind     `json:"kind"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	Hint  string   `json:"hint,omitempty"`
}

// Section gom các ô liên quan lại thành một khối trên giao diện.
type Section struct {
	Title  string  `json:"title"`
	Note   string  `json:"note,omitempty"`
	Fields []Field `json:"fields"`
}

func f(path, label string, kind Kind, lo, hi float64, hint string) Field {
	return Field{Path: path, Label: label, Kind: kind, Min: &lo, Max: &hi, Hint: hint}
}

var vehicleLabels = map[string]string{
	"BIKE": "Xe máy", "CAR_4": "Ô tô 4 chỗ", "CAR_7": "Ô tô 7 chỗ",
}

func tariffSection(vt string) Section {
	p := func(name string) string { return fmt.Sprintf("tariffs.%s.%s", vt, name) }
	return Section{
		Title: "Biểu giá " + vehicleLabels[vt],
		Fields: []Field{
			f(p("opening_fare"), "Giá mở cửa", KindVND, 1000, 500000,
				"Khách trả khoản này ngay khi lên xe."),
			f(p("opening_meter"), "Số mét đã gồm trong giá mở cửa", KindMeters, 0, 20000,
				"Chỉ tính tiền theo km từ mét thứ n+1 trở đi."),
			f(p("per_km"), "Đơn giá mỗi km", KindVND, 1000, 200000, ""),
			f(p("per_minute"), "Đơn giá mỗi phút", KindVND, 0, 20000,
				"Tính theo thời gian dự kiến của lộ trình."),
			f(p("min_fare"), "Giá tối thiểu", KindVND, 1000, 500000,
				"Sàn của cước sau khi cộng mọi khoản."),
			f(p("night_surcharge_permille"), "Phụ phí đêm", KindPermille, 0, 1000,
				"Cộng thêm vào cước trong khung giờ đêm."),
			f(p("platform_fee_permille"), "Chiết khấu nền tảng", KindPermille, 0, MaxPlatformFeePermille,
				"Phần nền tảng giữ lại. Phần còn lại là thu nhập tài xế."),
		},
	}
}

// SchemaFor trả lược đồ biểu mẫu của một nhóm.
func SchemaFor(k Key) []Section {
	switch k {
	case KeyPricing:
		out := make([]Section, 0, len(vehicleTypes)+1)
		for _, vt := range vehicleTypes {
			out = append(out, tariffSection(vt))
		}
		return append(out, Section{
			Title: "Chung",
			Note:  "Khung giờ đêm theo giờ Việt Nam (UTC+7). Được phép vắt qua nửa đêm, ví dụ 22 giờ đến 5 giờ.",
			Fields: []Field{
				f("quote_ttl_seconds", "Hạn của một báo giá", KindSeconds, 30, 3600,
					"Hết hạn thì khách phải lấy giá mới. Báo giá đã phát không bị đổi khi sửa biểu giá."),
				f("night_start_hour", "Giờ bắt đầu tính đêm", KindHour, 0, 23, ""),
				f("night_end_hour", "Giờ kết thúc tính đêm", KindHour, 0, 23, ""),
			},
		})

	case KeySurge:
		return []Section{
			{
				Title: "Chung",
				Fields: []Field{
					{Path: "enabled", Label: "Bật tăng giá theo cầu", Kind: KindBool,
						Hint: "Tắt thì hệ số luôn là 1,0 — không cần xoá bậc thang."},
					f("max_permille", "Trần tăng giá", KindPermille, 1000, AbsoluteMaxSurgePermille,
						"Trần cứng của hệ thống là 3,0 lần. Không bậc nào được vượt trần này."),
					f("window_seconds", "Cửa sổ đếm nhu cầu", KindSeconds, 30, 3600,
						"Đếm số yêu cầu đặt xe trong khoảng thời gian trượt này."),
					f("supply_radius_m", "Bán kính đếm tài xế rảnh", KindMeters, 200, 20000, ""),
				},
			},
			{
				Title: "Bậc thang",
				Note: "Ngưỡng và hệ số đều phải tăng dần. Hệ số 1200 nghĩa là nhân 1,2 lần. " +
					"Bậc nào có ngưỡng cầu/cung thấp hơn tỉ lệ hiện tại thì áp dụng, lấy bậc cao nhất.",
				Fields: []Field{{Path: "steps", Label: "Các bậc", Kind: KindSurgeSteps}},
			},
		}

	case KeyMatching:
		return []Section{
			{
				Title: "Phạm vi tìm kiếm",
				Note:  "Mỗi vòng không tìm được ai thì nới bán kính thêm một bước, tối đa tới bán kính tối đa.",
				Fields: []Field{
					f("initial_radius_m", "Bán kính vòng đầu", KindMeters, 200, 20000, ""),
					f("radius_step_m", "Bước nới bán kính", KindMeters, 0, 20000, ""),
					f("max_radius_m", "Bán kính tối đa", KindMeters, 200, 50000,
						"Phải lớn hơn hoặc bằng bán kính vòng đầu."),
					f("max_rounds", "Số vòng chào mời", KindInt, 1, 10,
						"Hết số vòng mà chưa ai nhận thì chuyến chuyển sang không tìm được tài xế."),
					f("batch_size", "Số tài xế mời mỗi vòng", KindInt, 1, 50, ""),
					f("offer_ttl_seconds", "Hạn một lời mời", KindSeconds, 5, 120,
						"Tài xế có bấy nhiêu giây để bấm nhận."),
					f("empty_round_wait_seconds", "Chờ giữa hai vòng rỗng", KindSeconds, 1, 60, ""),
					f("min_battery_pc", "Pin tối thiểu", KindInt, 0, 50,
						"Máy sắp hết pin thì không mời, tránh rớt giữa chuyến."),
				},
			},
			{
				Title: "Trọng số chấm điểm",
				Note: "Điểm càng THẤP càng được ưu tiên. Chỉ nên đổi khi có số liệu thật hoặc " +
					"kết quả A/B test — đổi mò sẽ làm thu nhập tài xế biến động mà không ai giải thích được.",
				Fields: []Field{
					f("weight_eta", "Trọng số thời gian tới đón", KindFloat, 0, 1000,
						"Nhân với số giây ETA."),
					f("weight_rating", "Trọng số điểm đánh giá", KindFloat, 0, 1000, ""),
					f("weight_acceptance", "Trọng số tỉ lệ nhận chuyến", KindFloat, 0, 1000, ""),
					f("weight_idle", "Trọng số thời gian chờ", KindFloat, 0, 1000,
						"Càng chờ lâu càng được ưu tiên, để chia việc đều hơn."),
					f("weight_heading", "Trọng số hướng xe", KindFloat, 0, 1000,
						"Ưu tiên xe đang chạy về phía điểm đón."),
				},
			},
		}

	case KeyWallet:
		return []Section{{
			Title: "Ví & công nợ",
			Fields: []Field{
				f("debt_limit_vnd", "Hạn mức công nợ tiền mặt", KindVND, 10000, 5000000,
					"Nợ vượt mức này thì tài xế không nhận được chuyến mới cho tới khi nộp tiền."),
				f("tax_permille", "Thuế khấu trừ tại nguồn", KindPermille, 0, MaxTaxPermille,
					"Mức hiện hành cho cá nhân kinh doanh vận tải là 45 phần nghìn (4,5%). "+
						"Cần kế toán thuế xác nhận trước khi bật."),
				f("min_payout_vnd", "Ngưỡng chi trả tối thiểu", KindVND, 0, 5000000,
					"Dưới ngưỡng thì dồn sang kỳ đối soát sau."),
				f("cancel_fee_vnd", "Phí huỷ chuyến", KindVND, 0, 500000, ""),
				f("free_cancel_window_seconds", "Cửa sổ huỷ miễn phí", KindSeconds, 0, 1800,
					"Huỷ trong khoảng này kể từ lúc ghép thì không mất phí."),
			},
		}}

	case KeyLocation:
		return []Section{{
			Title: "Vị trí & chống gian lận",
			Fields: []Field{
				f("stale_after_seconds", "Ngưỡng ping quá hạn", KindSeconds, 10, 300,
					"Quá lâu không có tin thì coi như mất kết nối và không mời chuyến nữa."),
				f("max_plausible_speed_mps", "Tốc độ tối đa hợp lý", KindFloat, 10, 100,
					"Mét mỗi giây. Nhanh hơn mức này thì coi là toạ độ giả và loại bỏ."),
				f("max_accuracy_m", "Sai số GPS tối đa chấp nhận", KindFloat, 10, 2000, ""),
			},
		}}
	}
	return nil
}
