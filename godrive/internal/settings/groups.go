package settings

import (
	"fmt"
	"time"

	"github.com/example/godrive/pkg/errs"
)

// Ngưỡng an toàn. Đây là phần quan trọng nhất của cả package.
//
// Mỗi con số dưới đây trả lời câu hỏi: "giá trị nào, nếu ai đó gõ nhầm, sẽ gây
// thiệt hại không sửa được bằng cách sửa lại cấu hình?"
const (
	// Chiết khấu 40% đã là mức cao nhất thị trường; trên nữa thì tài xế bỏ đi,
	// và mất cung tài xế là thứ không mua lại được bằng cách hạ chiết khấu.
	MaxPlatformFeePermille = 400
	// Trần tuyệt đối của hệ số tăng giá. Trần vận hành (SurgeGroup.MaxPermille)
	// có thể thấp hơn, nhưng không bao giờ được vượt con số này.
	AbsoluteMaxSurgePermille = 3000
	// Thuế khấu trừ tại nguồn: 4,5% theo quy định hiện hành cho cá nhân kinh
	// doanh vận tải. Trần 20% để chặn gõ nhầm dấu phẩy.
	MaxTaxPermille = 200
)

// vnd kiểm một khoản tiền nằm trong khoảng.
func vnd(name string, v, lo, hi int64) error {
	if v < lo || v > hi {
		return errs.Invalid("setting_out_of_range",
			fmt.Sprintf("%s phải trong khoảng %s đến %s (đang là %s).",
				name, fmtVND(lo), fmtVND(hi), fmtVND(v)))
	}
	return nil
}

// permille kiểm một tỉ lệ phần nghìn. Tách khỏi vnd() vì thông báo lỗi định
// dạng tiền cho một tỉ lệ đọc ra thành "chiết khấu phải trong khoảng 0đ đến
// 400đ" — người đọc sẽ tưởng đơn vị là đồng và gõ vào một con số hoàn toàn khác.
func permille(name string, v, lo, hi int64) error {
	if v < lo || v > hi {
		return errs.Invalid("setting_out_of_range",
			fmt.Sprintf("%s phải trong khoảng %d đến %d phần nghìn (%g%% đến %g%%), đang là %d.",
				name, lo, hi, float64(lo)/10, float64(hi)/10, v))
	}
	return nil
}

// meters kiểm một khoảng cách theo mét.
func meters(name string, v, lo, hi int64) error {
	if v < lo || v > hi {
		return errs.Invalid("setting_out_of_range",
			fmt.Sprintf("%s phải trong khoảng %d đến %d mét (đang là %d).", name, lo, hi, v))
	}
	return nil
}

func num(name string, v, lo, hi float64) error {
	if v < lo || v > hi {
		return errs.Invalid("setting_out_of_range",
			fmt.Sprintf("%s phải trong khoảng %g đến %g (đang là %g).", name, lo, hi, v))
	}
	return nil
}

func dur(name string, v, lo, hi time.Duration) error {
	if v < lo || v > hi {
		return errs.Invalid("setting_out_of_range",
			fmt.Sprintf("%s phải trong khoảng %s đến %s (đang là %s).", name, lo, hi, v))
	}
	return nil
}

func fmtVND(v int64) string {
	s := fmt.Sprintf("%d", v)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += "."
		}
		out += string(c)
	}
	return out + "đ"
}

// ---------------------------------------------------------------- biểu giá

// Tariff là biểu giá của một loại xe.
//
// CẢNH BÁO PHÁP LÝ: giá cước phải khớp hồ sơ kê khai giá cước đã nộp cho cơ
// quan quản lý. Đổi ở đây mà chưa nộp hồ sơ mới là vi phạm.
type Tariff struct {
	OpeningFare            int64 `json:"opening_fare"`
	OpeningMeter           int64 `json:"opening_meter"`
	PerKm                  int64 `json:"per_km"`
	PerMinute              int64 `json:"per_minute"`
	MinFare                int64 `json:"min_fare"`
	NightSurchargePermille int64 `json:"night_surcharge_permille"`
	PlatformFeePermille    int64 `json:"platform_fee_permille"`
}

// PricingGroup là biểu giá theo loại xe cùng hạn báo giá.
type PricingGroup struct {
	Tariffs map[string]Tariff `json:"tariffs"`
	// QuoteTTLSeconds là hạn của một báo giá đã phát.
	QuoteTTLSeconds int `json:"quote_ttl_seconds"`
	// NightStartHour/NightEndHour theo giờ Việt Nam (UTC+7).
	NightStartHour int `json:"night_start_hour"`
	NightEndHour   int `json:"night_end_hour"`
}

func DefaultPricing() PricingGroup {
	return PricingGroup{
		Tariffs: map[string]Tariff{
			"BIKE":  {OpeningFare: 12000, OpeningMeter: 2000, PerKm: 4300, PerMinute: 300, MinFare: 12000, NightSurchargePermille: 100, PlatformFeePermille: 200},
			"CAR_4": {OpeningFare: 29000, OpeningMeter: 2000, PerKm: 9500, PerMinute: 600, MinFare: 29000, NightSurchargePermille: 100, PlatformFeePermille: 250},
			"CAR_7": {OpeningFare: 34000, OpeningMeter: 2000, PerKm: 11500, PerMinute: 700, MinFare: 34000, NightSurchargePermille: 100, PlatformFeePermille: 250},
		},
		QuoteTTLSeconds: 300,
		NightStartHour:  22,
		NightEndHour:    5,
	}
}

var vehicleTypes = []string{"BIKE", "CAR_4", "CAR_7"}

func (p PricingGroup) Validate() error {
	// Thiếu một loại xe nghĩa là loại đó không báo giá được nữa — im lặng mất
	// một dòng doanh thu, nên phải chặn.
	for _, vt := range vehicleTypes {
		t, ok := p.Tariffs[vt]
		if !ok {
			return errs.Invalid("setting_missing_tariff",
				"Thiếu biểu giá cho loại xe "+vt+".")
		}
		if err := vnd("Giá mở cửa ("+vt+")", t.OpeningFare, 1000, 500000); err != nil {
			return err
		}
		if err := meters("Số mét đã gồm trong giá mở cửa ("+vt+")", t.OpeningMeter, 0, 20000); err != nil {
			return err
		}
		if err := vnd("Đơn giá mỗi km ("+vt+")", t.PerKm, 1000, 200000); err != nil {
			return err
		}
		if err := vnd("Đơn giá mỗi phút ("+vt+")", t.PerMinute, 0, 20000); err != nil {
			return err
		}
		if err := vnd("Giá tối thiểu ("+vt+")", t.MinFare, 1000, 500000); err != nil {
			return err
		}
		if err := permille("Phụ phí đêm ("+vt+")", t.NightSurchargePermille, 0, 1000); err != nil {
			return err
		}
		if err := permille("Chiết khấu nền tảng ("+vt+")",
			t.PlatformFeePermille, 0, MaxPlatformFeePermille); err != nil {
			return err
		}
	}
	if err := num("Hạn báo giá (giây)", float64(p.QuoteTTLSeconds), 30, 3600); err != nil {
		return err
	}
	if p.NightStartHour < 0 || p.NightStartHour > 23 || p.NightEndHour < 0 || p.NightEndHour > 23 {
		return errs.Invalid("setting_out_of_range", "Giờ bắt đầu/kết thúc ban đêm phải trong khoảng 0 đến 23.")
	}
	return nil
}

func (p PricingGroup) QuoteTTL() time.Duration {
	return time.Duration(p.QuoteTTLSeconds) * time.Second
}

// ---------------------------------------------------------------- surge

// SurgeStep là một bậc của thang tăng giá.
type SurgeStep struct {
	// RatioX10 là ngưỡng cầu/cung nhân 10 (12 = tỉ lệ 1.2).
	//
	// Nhân 10 để so sánh bằng SỐ NGUYÊN: 1.2 không biểu diễn chính xác được ở
	// nhị phân, và đây là đường đi của tiền.
	RatioX10 int64 `json:"ratio_x10"`
	Permille int64 `json:"permille"`
}

type SurgeGroup struct {
	// Enabled tắt hẳn surge mà không phải xoá bậc thang.
	Enabled bool `json:"enabled"`
	// MaxPermille là trần vận hành, luôn <= AbsoluteMaxSurgePermille.
	MaxPermille int64 `json:"max_permille"`
	// WindowSeconds là cửa sổ trượt đếm nhu cầu.
	WindowSeconds int `json:"window_seconds"`
	// SupplyRadiusM là bán kính đếm tài xế rảnh.
	SupplyRadiusM float64 `json:"supply_radius_m"`
	// Steps sắp theo ngưỡng TĂNG DẦN.
	Steps []SurgeStep `json:"steps"`
}

func DefaultSurge() SurgeGroup {
	return SurgeGroup{
		Enabled: true, MaxPermille: 2000, WindowSeconds: 300, SupplyRadiusM: 2000,
		Steps: []SurgeStep{
			{RatioX10: 12, Permille: 1200},
			{RatioX10: 20, Permille: 1400},
			{RatioX10: 30, Permille: 1700},
			{RatioX10: 40, Permille: 2000},
		},
	}
}

func (s SurgeGroup) Validate() error {
	if err := permille("Trần tăng giá", s.MaxPermille, 1000, AbsoluteMaxSurgePermille); err != nil {
		return err
	}
	if err := num("Cửa sổ đếm nhu cầu (giây)", float64(s.WindowSeconds), 30, 3600); err != nil {
		return err
	}
	if err := num("Bán kính đếm tài xế rảnh (m)", s.SupplyRadiusM, 200, 20000); err != nil {
		return err
	}
	if len(s.Steps) > 10 {
		return errs.Invalid("setting_out_of_range", "Tối đa 10 bậc tăng giá.")
	}
	var prevRatio, prevPermille int64
	for i, st := range s.Steps {
		if st.RatioX10 <= prevRatio {
			return errs.Invalid("setting_steps_not_ascending",
				fmt.Sprintf("Ngưỡng của bậc %d phải lớn hơn bậc trước.", i+1))
		}
		// Bậc sau phải cao hơn bậc trước: một thang không tăng dần nghĩa là cầu
		// tăng mà giá lại giảm — vô nghĩa với vận hành và không giải thích được
		// cho người dùng.
		if st.Permille <= prevPermille {
			return errs.Invalid("setting_steps_not_ascending",
				fmt.Sprintf("Hệ số của bậc %d phải lớn hơn bậc trước.", i+1))
		}
		if st.Permille < 1000 || st.Permille > s.MaxPermille {
			return errs.Invalid("setting_out_of_range",
				fmt.Sprintf("Hệ số của bậc %d phải trong khoảng 1000 đến trần %d.", i+1, s.MaxPermille))
		}
		prevRatio, prevPermille = st.RatioX10, st.Permille
	}
	return nil
}

// ---------------------------------------------------------------- ghép chuyến

type MatchingGroup struct {
	InitialRadiusM     float64 `json:"initial_radius_m"`
	RadiusStepM        float64 `json:"radius_step_m"`
	MaxRadiusM         float64 `json:"max_radius_m"`
	MaxRounds          int     `json:"max_rounds"`
	BatchSize          int     `json:"batch_size"`
	OfferTTLSeconds    int     `json:"offer_ttl_seconds"`
	EmptyRoundWaitSecs int     `json:"empty_round_wait_seconds"`
	MinBatteryPc       int     `json:"min_battery_pc"`
	WeightETA          float64 `json:"weight_eta"`
	WeightRating       float64 `json:"weight_rating"`
	WeightAcceptance   float64 `json:"weight_acceptance"`
	WeightIdle         float64 `json:"weight_idle"`
	WeightHeading      float64 `json:"weight_heading"`
}

func DefaultMatching() MatchingGroup {
	return MatchingGroup{
		InitialRadiusM: 1500, RadiusStepM: 1500, MaxRadiusM: 5000,
		MaxRounds: 3, BatchSize: 5, OfferTTLSeconds: 15, EmptyRoundWaitSecs: 2,
		MinBatteryPc: 15,
		WeightETA:    1.0, WeightRating: 60, WeightAcceptance: 90,
		WeightIdle: 0.25, WeightHeading: 0.20,
	}
}

func (m MatchingGroup) Validate() error {
	if err := num("Bán kính vòng đầu (m)", m.InitialRadiusM, 200, 20000); err != nil {
		return err
	}
	if err := num("Bước nới bán kính (m)", m.RadiusStepM, 0, 20000); err != nil {
		return err
	}
	if err := num("Bán kính tối đa (m)", m.MaxRadiusM, 200, 50000); err != nil {
		return err
	}
	// Bán kính tối đa nhỏ hơn bán kính vòng đầu nghĩa là vòng đầu bị cắt cụt —
	// dispatcher sẽ tìm trong bán kính nhỏ hơn cả cấu hình, rất khó nhận ra.
	if m.MaxRadiusM < m.InitialRadiusM {
		return errs.Invalid("setting_inconsistent",
			"Bán kính tối đa phải lớn hơn hoặc bằng bán kính vòng đầu.")
	}
	if err := num("Số vòng chào mời", float64(m.MaxRounds), 1, 10); err != nil {
		return err
	}
	if err := num("Số tài xế mời mỗi vòng", float64(m.BatchSize), 1, 50); err != nil {
		return err
	}
	if err := dur("Hạn lời mời", time.Duration(m.OfferTTLSeconds)*time.Second, 5*time.Second, 120*time.Second); err != nil {
		return err
	}
	if err := dur("Chờ giữa hai vòng rỗng", time.Duration(m.EmptyRoundWaitSecs)*time.Second, time.Second, 60*time.Second); err != nil {
		return err
	}
	if err := num("Pin tối thiểu (%)", float64(m.MinBatteryPc), 0, 50); err != nil {
		return err
	}
	for name, w := range map[string]float64{
		"Trọng số ETA": m.WeightETA, "Trọng số đánh giá": m.WeightRating,
		"Trọng số tỉ lệ nhận": m.WeightAcceptance, "Trọng số thời gian chờ": m.WeightIdle,
		"Trọng số hướng xe": m.WeightHeading,
	} {
		if err := num(name, w, 0, 1000); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- ví & đối soát

type WalletGroup struct {
	DebtLimitVND         int64 `json:"debt_limit_vnd"`
	TaxPermille          int64 `json:"tax_permille"`
	MinPayoutVND         int64 `json:"min_payout_vnd"`
	CancelFeeVND         int64 `json:"cancel_fee_vnd"`
	FreeCancelWindowSecs int   `json:"free_cancel_window_seconds"`
}

func DefaultWallet() WalletGroup {
	return WalletGroup{
		DebtLimitVND: 200000, TaxPermille: 0, MinPayoutVND: 50000,
		CancelFeeVND: 10000, FreeCancelWindowSecs: 120,
	}
}

func (w WalletGroup) Validate() error {
	// Hạn mức 0 nghĩa là tài xế bị chặn ngay khi ví âm một đồng — sau chuyến
	// tiền mặt đầu tiên là không nhận chuyến được nữa.
	if err := vnd("Hạn mức công nợ", w.DebtLimitVND, 10000, 5000000); err != nil {
		return err
	}
	if err := permille("Thuế khấu trừ tại nguồn", w.TaxPermille, 0, MaxTaxPermille); err != nil {
		return err
	}
	if err := vnd("Ngưỡng chi trả tối thiểu", w.MinPayoutVND, 0, 5000000); err != nil {
		return err
	}
	if err := vnd("Phí huỷ chuyến", w.CancelFeeVND, 0, 500000); err != nil {
		return err
	}
	if err := dur("Cửa sổ huỷ miễn phí", time.Duration(w.FreeCancelWindowSecs)*time.Second, 0, 30*time.Minute); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- vị trí

type LocationGroup struct {
	StaleAfterSeconds    int     `json:"stale_after_seconds"`
	MaxPlausibleSpeedMps float64 `json:"max_plausible_speed_mps"`
	MaxAccuracyM         float64 `json:"max_accuracy_m"`
}

func DefaultLocation() LocationGroup {
	return LocationGroup{StaleAfterSeconds: 45, MaxPlausibleSpeedMps: 33, MaxAccuracyM: 200}
}

func (l LocationGroup) Validate() error {
	// Quá ngắn thì tài xế rớt khỏi tập ứng viên giữa hai lần ping; quá dài thì
	// người mất mạng vẫn nhận lời mời mà không bao giờ thấy.
	if err := dur("Ngưỡng ping quá hạn", time.Duration(l.StaleAfterSeconds)*time.Second,
		10*time.Second, 300*time.Second); err != nil {
		return err
	}
	if err := num("Tốc độ tối đa hợp lý (m/s)", l.MaxPlausibleSpeedMps, 10, 100); err != nil {
		return err
	}
	if err := num("Sai số GPS tối đa (m)", l.MaxAccuracyM, 10, 2000); err != nil {
		return err
	}
	return nil
}
