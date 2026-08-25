package settings

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
)

func newSvc(t *testing.T) (*Service, *clock.Mock) {
	t.Helper()
	clk := clock.NewMock(time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	return NewService(NewMemoryStore(), clk, id.New), clk
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Mặc định phải HỢP LỆ. Nếu không thì hệ thống khởi động với cấu hình mà chính
// nó từ chối — một mâu thuẫn chỉ lộ ra khi ai đó bấm lưu lần đầu.
func TestDefaultsAreValid(t *testing.T) {
	for _, v := range []Validator{
		DefaultPricing(), DefaultSurge(), DefaultMatching(),
		DefaultWallet(), DefaultLocation(),
	} {
		if err := v.Validate(); err != nil {
			t.Fatalf("%T mặc định phải hợp lệ: %v", v, err)
		}
	}
}

// Đây là test quan trọng nhất của package: ngưỡng chặn phải THẬT SỰ chặn.
//
// Một giao diện cấu hình không có ràng buộc là cách phá sập doanh nghiệp bằng
// một lần gõ nhầm.
func TestValidationBlocksCatastrophicValues(t *testing.T) {
	cases := []struct {
		name string
		v    Validator
		want string
	}{
		{"chiết khấu 90%", func() Validator {
			p := DefaultPricing()
			t := p.Tariffs["BIKE"]
			t.PlatformFeePermille = 900
			p.Tariffs["BIKE"] = t
			return p
		}(), "Chiết khấu"},
		{"giá mở cửa âm", func() Validator {
			p := DefaultPricing()
			t := p.Tariffs["BIKE"]
			t.OpeningFare = -1000
			p.Tariffs["BIKE"] = t
			return p
		}(), "Giá mở cửa"},
		{"thiếu hẳn một loại xe", func() Validator {
			p := DefaultPricing()
			delete(p.Tariffs, "CAR_7")
			return p
		}(), "Thiếu biểu giá"},
		{"trần surge ×10", func() Validator {
			s := DefaultSurge()
			s.MaxPermille = 10000
			return s
		}(), "Trần tăng giá"},
		{"bậc thang không tăng dần", func() Validator {
			s := DefaultSurge()
			s.Steps = []SurgeStep{{RatioX10: 12, Permille: 1500}, {RatioX10: 20, Permille: 1200}}
			return s
		}(), "phải lớn hơn bậc trước"},
		{"bậc vượt trần", func() Validator {
			s := DefaultSurge()
			s.MaxPermille = 1500
			return s
		}(), "trần"},
		{"bán kính tối đa nhỏ hơn vòng đầu", func() Validator {
			m := DefaultMatching()
			m.InitialRadiusM, m.MaxRadiusM = 5000, 1000
			return m
		}(), "Bán kính tối đa"},
		{"1000 vòng chào mời", func() Validator {
			m := DefaultMatching()
			m.MaxRounds = 1000
			return m
		}(), "Số vòng"},
		{"hạn lời mời 1 giây", func() Validator {
			m := DefaultMatching()
			m.OfferTTLSeconds = 1
			return m
		}(), "Hạn lời mời"},
		{"hạn mức công nợ 0", func() Validator {
			w := DefaultWallet()
			w.DebtLimitVND = 0
			return w
		}(), "Hạn mức công nợ"},
		{"thuế 50%", func() Validator {
			w := DefaultWallet()
			w.TaxPermille = 500
			return w
		}(), "Thuế"},
		{"ping quá hạn 1 giây", func() Validator {
			l := DefaultLocation()
			l.StaleAfterSeconds = 1
			return l
		}(), "ping quá hạn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.v.Validate()
			if err == nil {
				t.Fatal("giá trị nguy hiểm phải bị CHẶN")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("thông báo lỗi phải nêu rõ trường nào sai, được: %v", err)
			}
		})
	}
}

// Giá trị sai không bao giờ được lưu xuống — chặn ở lúc GHI, không phải lúc đọc.
func TestPutRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	bad := DefaultWallet()
	bad.TaxPermille = 999
	if _, err := s.Put(ctx, KeyWallet, raw(t, bad), 0, "acc_1", "thử"); err == nil {
		t.Fatal("giá trị ngoài ngưỡng phải bị từ chối")
	}
	// Và ảnh chụp phải giữ nguyên giá trị cũ.
	if got := s.Current(ctx).Wallet.TaxPermille; got != 0 {
		t.Fatalf("cấu hình đang chạy không được đổi khi ghi thất bại, là %d", got)
	}
}

func TestPutAppliesImmediately(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	if got := s.Current(ctx).Wallet.DebtLimitVND; got != 200000 {
		t.Fatalf("mặc định phải là 200.000đ, là %d", got)
	}
	w := DefaultWallet()
	w.DebtLimitVND = 500000
	rec, err := s.Put(ctx, KeyWallet, raw(t, w), 0, "acc_1", "nới hạn mức dịp Tết")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != 1 {
		t.Fatalf("phiên bản đầu phải là 1, là %d", rec.Version)
	}
	// Có hiệu lực NGAY, không chờ hết hạn ảnh chụp.
	if got := s.Current(ctx).Wallet.DebtLimitVND; got != 500000 {
		t.Fatalf("thay đổi phải có hiệu lực ngay, đang là %d", got)
	}
}

// Hai quản trị viên cùng sửa một nhóm: người sau phải đọc lại, không ghi đè.
func TestPutDetectsConcurrentEdit(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	w := DefaultWallet()
	w.DebtLimitVND = 300000
	if _, err := s.Put(ctx, KeyWallet, raw(t, w), 0, "acc_1", ""); err != nil {
		t.Fatal(err)
	}
	// Người thứ hai vẫn đang cầm phiên bản 0.
	w.DebtLimitVND = 400000
	_, err := s.Put(ctx, KeyWallet, raw(t, w), 0, "acc_2", "")
	if errs.CodeOf(err) != "setting_version_conflict" {
		t.Fatalf("phải trả setting_version_conflict, được %q", errs.CodeOf(err))
	}
	if got := s.Current(ctx).Wallet.DebtLimitVND; got != 300000 {
		t.Fatalf("thay đổi của người đầu không được bị ghi đè, đang là %d", got)
	}
}

// PUT một phần KHÔNG được xoá sạch các trường không gửi.
//
// Đây là loại lỗi im lặng nguy hiểm nhất của một API cấu hình: mọi giá trị 0
// đều "hợp lệ" nên Validate không chặn được, và không ai phát hiện ra cho tới
// khi có người hỏi vì sao hệ thống thôi khấu trừ thuế.
func TestPartialPutDoesNotWipeOtherFields(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)

	// Đặt một cấu hình đầy đủ trước.
	base := DefaultWallet()
	base.TaxPermille = 45
	base.MinPayoutVND = 100000
	base.CancelFeeVND = 15000
	if _, err := s.Put(ctx, KeyWallet, raw(t, base), 0, "acc_1", "khởi tạo"); err != nil {
		t.Fatal(err)
	}

	// Rồi chỉ gửi MỘT trường, kèm một trường không tồn tại.
	if _, err := s.Put(ctx, KeyWallet,
		[]byte(`{"debt_limit_vnd":250000,"khong_ton_tai":123}`), 1, "acc_2", "nới hạn mức"); err != nil {
		t.Fatal(err)
	}

	got := s.Current(ctx).Wallet
	if got.DebtLimitVND != 250000 {
		t.Fatalf("trường gửi lên phải được áp dụng, là %d", got.DebtLimitVND)
	}
	if got.TaxPermille != 45 {
		t.Fatalf("thuế KHÔNG được bị xoá về 0, đang là %d", got.TaxPermille)
	}
	if got.MinPayoutVND != 100000 {
		t.Fatalf("ngưỡng chi trả KHÔNG được bị xoá, đang là %d", got.MinPayoutVND)
	}
	if got.CancelFeeVND != 15000 {
		t.Fatalf("phí huỷ KHÔNG được bị xoá, đang là %d", got.CancelFeeVND)
	}

	rec, _ := s.Get(ctx, KeyWallet)
	if strings.Contains(string(rec.Value), "khong_ton_tai") {
		t.Fatal("trường lạ phải bị loại khỏi giá trị đã lưu")
	}
	var stored WalletGroup
	if err := json.Unmarshal(rec.Value, &stored); err != nil {
		t.Fatal(err)
	}
}

// Lịch sử phải giữ cả giá trị TRƯỚC và SAU.
//
// Khi khách khiếu nại giá của một chuyến ba tháng trước, phải trả lời được biểu
// giá lúc đó — chứ không phải biểu giá hôm nay.
func TestHistoryKeepsBeforeAndAfter(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)

	p := DefaultPricing()
	tf := p.Tariffs["BIKE"]
	tf.PerKm = 5000
	p.Tariffs["BIKE"] = tf
	if _, err := s.Put(ctx, KeyPricing, raw(t, p), 0, "acc_1", "tăng giá xăng"); err != nil {
		t.Fatal(err)
	}
	tf.PerKm = 5500
	p.Tariffs["BIKE"] = tf
	if _, err := s.Put(ctx, KeyPricing, raw(t, p), 1, "acc_2", "điều chỉnh quý 4"); err != nil {
		t.Fatal(err)
	}

	h, err := s.History(ctx, KeyPricing, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 {
		t.Fatalf("phải có 2 dòng lịch sử, có %d", len(h))
	}
	// Mới nhất lên đầu.
	if h[0].Version != 2 || h[0].ChangedBy != "acc_2" || h[0].Reason != "điều chỉnh quý 4" {
		t.Fatalf("dòng mới nhất sai: %+v", h[0])
	}
	if !strings.Contains(string(h[0].OldValue), "5000") {
		t.Fatal("lịch sử phải giữ giá TRƯỚC khi đổi")
	}
	if !strings.Contains(string(h[0].NewValue), "5500") {
		t.Fatal("lịch sử phải giữ giá SAU khi đổi")
	}
	// Lần lưu đầu tiên so với MẶC ĐỊNH, vì đó mới là thứ hệ thống đang chạy
	// trước đó — xem TestFirstChangeComparesAgainstDefaults.
	var was PricingGroup
	if err := json.Unmarshal(h[1].OldValue, &was); err != nil {
		t.Fatalf("lần lưu đầu phải kèm giá trị cũ là mặc định: %v", err)
	}
	if was.Tariffs["BIKE"].PerKm != DefaultPricing().Tariffs["BIKE"].PerKm {
		t.Fatalf("giá trị cũ của lần đầu phải là mặc định, được %+v", was.Tariffs["BIKE"])
	}
}

// Giá trị hỏng trong CSDL (do sửa tay) không được làm sập cả hệ thống.
func TestCorruptStoredValueFallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clk := clock.NewMock(time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	// Ghi thẳng vào kho, bỏ qua kiểm tra của Service.
	if _, err := store.Put(ctx, KeyWallet, []byte(`{"debt_limit_vnd":-999}`),
		0, "sua-tay", "", clk.Now(), id.New); err != nil {
		t.Fatal(err)
	}
	s := NewService(store, clk, id.New)
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.Current(ctx).Wallet.DebtLimitVND; got != 200000 {
		t.Fatalf("giá trị hỏng phải lùi về mặc định, đang là %d", got)
	}
	// Và các nhóm khác vẫn nạp bình thường.
	if s.Current(ctx).Matching.MaxRounds != 3 {
		t.Fatal("một nhóm hỏng không được ảnh hưởng nhóm khác")
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	if _, err := s.Get(ctx, Key("khong_ton_tai")); err == nil {
		t.Fatal("nhóm không tồn tại phải bị từ chối")
	}
	if _, err := s.Put(ctx, Key("khong_ton_tai"), []byte(`{}`), 0, "a", ""); err == nil {
		t.Fatal("ghi vào nhóm không tồn tại phải bị từ chối")
	}
}

// Sửa MỘT con số trong biểu giá không được xoá phần còn lại của loại xe đó.
//
// Đây là trường hợp nguy hiểm nhất trong cả nhóm cấu hình: chiết khấu nền tảng
// bằng 0 là một giá trị HỢP LỆ, nên Validate không chặn. Nền tảng sẽ thôi thu
// hoa hồng và mỗi chuyến trả toàn bộ tiền cho tài xế cho tới khi có người nhìn
// vào sổ và hỏi vì sao doanh thu bằng không.
func TestPartialTariffEditKeepsRestOfThatVehicle(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)

	base := DefaultPricing()
	if _, err := svc.Put(ctx, KeyPricing, raw(t, base), 0, "acc_admin", "khởi tạo"); err != nil {
		t.Fatal(err)
	}

	// Chỉ gửi đúng đơn giá mỗi km của xe máy.
	partial := json.RawMessage(`{"tariffs":{"BIKE":{"per_km":5000}}}`)
	if _, err := svc.Put(ctx, KeyPricing, partial, 1, "acc_admin", "theo giá xăng"); err != nil {
		t.Fatalf("sửa một trường phải thành công: %v", err)
	}

	got := svc.Current(ctx).Pricing
	bike := got.Tariffs["BIKE"]
	if bike.PerKm != 5000 {
		t.Fatalf("đơn giá km phải là 5000, được %d", bike.PerKm)
	}
	want := base.Tariffs["BIKE"]
	if bike.PlatformFeePermille != want.PlatformFeePermille {
		t.Fatalf("chiết khấu nền tảng bị xoá: %d → %d",
			want.PlatformFeePermille, bike.PlatformFeePermille)
	}
	if bike.OpeningFare != want.OpeningFare || bike.MinFare != want.MinFare ||
		bike.PerMinute != want.PerMinute || bike.OpeningMeter != want.OpeningMeter ||
		bike.NightSurchargePermille != want.NightSurchargePermille {
		t.Fatalf("các trường khác của xe máy bị xoá:\n muốn %+v\n được %+v", want, bike)
	}
	// Và loại xe không nhắc tới thì không được đụng vào.
	if got.Tariffs["CAR_4"] != base.Tariffs["CAR_4"] {
		t.Fatalf("biểu giá ô tô bị đổi dù không gửi lên: %+v", got.Tariffs["CAR_4"])
	}
}

// Gửi đủ cả biểu giá thì phải ghi đè đúng như gửi, không được gộp nhầm thành
// giá cũ — nếu không thì không ai hạ được một con số xuống 0 hợp lệ.
func TestFullTariffPutOverwrites(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)
	if _, err := svc.Put(ctx, KeyPricing, raw(t, DefaultPricing()), 0, "acc", "khởi tạo"); err != nil {
		t.Fatal(err)
	}

	next := DefaultPricing()
	tf := next.Tariffs["BIKE"]
	tf.PerMinute = 0 // hạ về 0: hợp lệ, và phải ghi được
	next.Tariffs["BIKE"] = tf
	if _, err := svc.Put(ctx, KeyPricing, raw(t, next), 1, "acc", "bỏ tính tiền theo phút"); err != nil {
		t.Fatal(err)
	}
	if got := svc.Current(ctx).Pricing.Tariffs["BIKE"].PerMinute; got != 0 {
		t.Fatalf("phải hạ được đơn giá phút về 0, đang là %d", got)
	}
}

// Một thay đổi BỊ TỪ CHỐI không được để lại dấu vết gì lên cấu hình đang chạy.
//
// Ảnh chụp trả về theo giá trị nhưng map bên trong dùng chung, nên quá trình
// kiểm tra một thay đổi từng ghi thẳng vào biểu giá sống: gửi lên một con số
// ngoài ngưỡng thì API trả lỗi đúng như mong đợi, mà mọi báo giá trong vài giây
// sau đó vẫn tính bằng biểu giá đã hỏng — cho tới khi ảnh chụp tự nạp lại và
// mọi thứ trở lại bình thường như chưa có gì xảy ra.
func TestRejectedChangeDoesNotCorruptRunningConfig(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)
	if _, err := svc.Put(ctx, KeyPricing, raw(t, DefaultPricing()), 0, "acc", "khởi tạo"); err != nil {
		t.Fatal(err)
	}
	before := svc.Current(ctx).Pricing.Tariffs["BIKE"]

	// Ngoài ngưỡng: phải bị chặn.
	bad := json.RawMessage(`{"tariffs":{"BIKE":{"per_km":999999999}}}`)
	if _, err := svc.Put(ctx, KeyPricing, bad, 1, "acc", "gõ nhầm"); err == nil {
		t.Fatal("giá trị ngoài ngưỡng phải bị từ chối")
	}

	// Đồng hồ KHÔNG nhích, nên ảnh chụp chưa hết hạn: đây đúng là khoảng thời
	// gian mà cấu hình hỏng sẽ được đem ra tính tiền thật.
	if after := svc.Current(ctx).Pricing.Tariffs["BIKE"]; after != before {
		t.Fatalf("thay đổi bị từ chối vẫn làm hỏng biểu giá đang chạy:\n trước %+v\n sau   %+v",
			before, after)
	}

	// Bậc thang tăng giá cũng là cấu trúc dùng chung, kiểm luôn.
	steps := append([]SurgeStep(nil), svc.Current(ctx).Surge.Steps...)
	badSurge := json.RawMessage(`{"steps":[{"ratio_x10":12,"permille":900}]}`)
	if _, err := svc.Put(ctx, KeySurge, badSurge, 0, "acc", "gõ nhầm"); err == nil {
		t.Fatal("hệ số dưới 1000 phải bị từ chối")
	}
	got := svc.Current(ctx).Surge.Steps
	if len(got) != len(steps) {
		t.Fatalf("bậc thang đang chạy bị thay: %d bậc → %d bậc", len(steps), len(got))
	}
	for i := range steps {
		if got[i] != steps[i] {
			t.Fatalf("bậc %d bị đổi: %+v → %+v", i+1, steps[i], got[i])
		}
	}
}

// Người cầm ảnh chụp không được sửa được cấu hình của cả hệ thống.
func TestSnapshotIsIsolatedFromRunningConfig(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)
	if _, err := svc.Put(ctx, KeyPricing, raw(t, DefaultPricing()), 0, "acc", "khởi tạo"); err != nil {
		t.Fatal(err)
	}

	mine := svc.Current(ctx)
	mine.Pricing.Tariffs["BIKE"] = Tariff{} // một module nghịch vào bản của mình
	mine.Surge.Steps[0].Permille = 9999

	fresh := svc.Current(ctx)
	if fresh.Pricing.Tariffs["BIKE"].PerKm != DefaultPricing().Tariffs["BIKE"].PerKm {
		t.Fatal("sửa ảnh chụp làm đổi biểu giá của cả hệ thống")
	}
	if fresh.Surge.Steps[0].Permille != DefaultSurge().Steps[0].Permille {
		t.Fatal("sửa ảnh chụp làm đổi bậc thang của cả hệ thống")
	}
}

// Lần sửa đầu tiên phải so được với mặc định.
//
// Trước lần ghi đầu, nhóm không phải là "rỗng" — nó đang chạy bằng mặc định
// trong mã nguồn. Không ghi lại điều đó thì bản ghi lịch sử đầu tiên hiện ra như
// thể toàn bộ cấu hình vừa xuất hiện từ hư không, và không ai đọc ra được người
// đó đã thật sự đổi con số nào.
func TestFirstChangeComparesAgainstDefaults(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)

	partial := json.RawMessage(`{"debt_limit_vnd":300000}`)
	if _, err := svc.Put(ctx, KeyWallet, partial, 0, "acc", "nới hạn mức dịp lễ"); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.History(ctx, KeyWallet, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("phải có 1 bản ghi, được %d", len(entries))
	}
	if len(entries[0].OldValue) == 0 {
		t.Fatal("bản ghi đầu tiên phải kèm giá trị cũ là mặc định")
	}
	var old WalletGroup
	if err := json.Unmarshal(entries[0].OldValue, &old); err != nil {
		t.Fatal(err)
	}
	if old != DefaultWallet() {
		t.Fatalf("giá trị cũ phải đúng bằng mặc định:\n muốn %+v\n được %+v", DefaultWallet(), old)
	}
	var next WalletGroup
	if err := json.Unmarshal(entries[0].NewValue, &next); err != nil {
		t.Fatal(err)
	}
	if next.DebtLimitVND != 300000 {
		t.Fatalf("giá trị mới sai: %d", next.DebtLimitVND)
	}
	if next.MinPayoutVND != DefaultWallet().MinPayoutVND {
		t.Fatalf("trường không gửi lên bị đổi: %d", next.MinPayoutVND)
	}
}
