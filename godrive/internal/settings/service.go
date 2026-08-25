package settings

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
)

// CacheTTL là hạn của ảnh chụp cấu hình trong bộ nhớ.
//
// Cấu hình được đọc ở mọi lần báo giá và mọi vòng dispatch, nên không thể hỏi
// CSDL mỗi lần. Ngắn thôi: đây cũng là thời gian tối đa một thay đổi mất để lan
// tới các pod khác nếu sự kiện không tới nơi.
const CacheTTL = 5 * time.Second

// Snapshot là toàn bộ cấu hình tại một thời điểm.
type Snapshot struct {
	Pricing  PricingGroup
	Surge    SurgeGroup
	Matching MatchingGroup
	Wallet   WalletGroup
	Location LocationGroup
	// Versions cho biết mỗi nhóm đang ở phiên bản nào.
	Versions map[Key]int
}

// clone trả bản sao SÂU của ảnh chụp.
//
// Snapshot trả về theo giá trị, nhưng map và slice bên trong vẫn trỏ chung một
// chỗ với bản đang chạy. Không nhân bản thì bất kỳ ai cầm ảnh chụp cũng có thể
// ghi ngược vào cấu hình sống của cả hệ thống — kể cả json.Unmarshal khi đang
// kiểm một thay đổi mà cuối cùng bị từ chối.
func (s Snapshot) clone() Snapshot {
	out := s
	out.Pricing.Tariffs = make(map[string]Tariff, len(s.Pricing.Tariffs))
	for k, v := range s.Pricing.Tariffs {
		out.Pricing.Tariffs[k] = v
	}
	out.Surge.Steps = append([]SurgeStep(nil), s.Surge.Steps...)
	out.Versions = make(map[Key]int, len(s.Versions))
	for k, v := range s.Versions {
		out.Versions[k] = v
	}
	return out
}

// Defaults là ảnh chụp mặc định, dùng khi chưa có gì trong CSDL và khi CSDL lỗi.
func Defaults() Snapshot {
	return Snapshot{
		Pricing: DefaultPricing(), Surge: DefaultSurge(), Matching: DefaultMatching(),
		Wallet: DefaultWallet(), Location: DefaultLocation(),
		Versions: map[Key]int{},
	}
}

// Publisher phát tin cấu hình đã đổi. Tuỳ chọn.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload any) error
}

// TopicChanged là sự kiện phát khi một nhóm cấu hình đổi.
const TopicChanged = "settings.changed"

// Service đọc/ghi cấu hình với một ảnh chụp trong bộ nhớ.
type Service struct {
	store Store
	clk   clock.Clock
	pub   Publisher
	newID func(string) string

	mu       sync.RWMutex
	snap     Snapshot
	loadedAt time.Time
}

func NewService(store Store, clk clock.Clock, newID func(string) string) *Service {
	return &Service{store: store, clk: clk, newID: newID, snap: Defaults()}
}

// UsePublisher bật phát tin để thay đổi có hiệu lực NGAY trên mọi pod, thay vì
// chờ hết hạn ảnh chụp.
func (s *Service) UsePublisher(p Publisher) { s.pub = p }

// Current trả ảnh chụp hiện hành, tự nạp lại khi hết hạn.
//
// KHÔNG BAO GIỜ trả lỗi: cấu hình nằm trên đường đi của mọi báo giá và mọi vòng
// dispatch. CSDL lỗi thì dùng ảnh chụp cũ (hoặc mặc định) và chạy tiếp — dừng
// phục vụ vì không đọc được cấu hình là biến một sự cố nhỏ thành sự cố lớn.
func (s *Service) Current(ctx context.Context) Snapshot {
	s.mu.RLock()
	snap, at := s.snap, s.loadedAt
	s.mu.RUnlock()
	if !at.IsZero() && s.clk.Now().Sub(at) < CacheTTL {
		return snap.clone()
	}
	if err := s.Reload(ctx); err != nil {
		return snap.clone()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.clone()
}

// Reload nạp lại từ kho lưu trữ ngay lập tức.
func (s *Service) Reload(ctx context.Context) error {
	if s.store == nil {
		s.mu.Lock()
		s.loadedAt = s.clk.Now()
		s.mu.Unlock()
		return nil
	}
	recs, err := s.store.GetAll(ctx)
	if err != nil {
		return err
	}
	// Reload dựng lại từ MẶC ĐỊNH chứ không gộp lên ảnh chụp cũ: nếu gộp thì
	// một nhóm bị xoá khỏi CSDL vẫn sống mãi trong bộ nhớ.
	next := Defaults()
	for k, r := range recs {
		// Giá trị hỏng trong CSDL (do sửa tay) KHÔNG được làm sập cả hệ thống:
		// bỏ qua nhóm đó và dùng mặc định, phần còn lại vẫn nạp bình thường.
		if err := decodeInto(k, r.Value, &next); err != nil {
			continue
		}
		next.Versions[k] = r.Version
	}
	s.mu.Lock()
	s.snap, s.loadedAt = next, s.clk.Now()
	s.mu.Unlock()
	return nil
}

func decodeInto(k Key, raw json.RawMessage, out *Snapshot) error {
	switch k {
	case KeyPricing:
		// Biểu giá lồng thêm một tầng map nên cần gộp sâu, xem mergePricing.
		v, err := mergePricing(raw, out.Pricing)
		if err != nil {
			return err
		}
		if err := v.Validate(); err != nil {
			return err
		}
		out.Pricing = v
	case KeySurge:
		v := out.Surge // gộp lên giá trị đang có
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if err := v.Validate(); err != nil {
			return err
		}
		out.Surge = v
	case KeyMatching:
		v := out.Matching // gộp lên giá trị đang có
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if err := v.Validate(); err != nil {
			return err
		}
		out.Matching = v
	case KeyWallet:
		v := out.Wallet // gộp lên giá trị đang có
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if err := v.Validate(); err != nil {
			return err
		}
		out.Wallet = v
	case KeyLocation:
		v := out.Location // gộp lên giá trị đang có
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if err := v.Validate(); err != nil {
			return err
		}
		out.Location = v
	default:
		return errs.Invalid("setting_key_invalid", "Nhóm cấu hình không hợp lệ.")
	}
	return nil
}

// Get trả bản ghi hiện hành của một nhóm, kèm mặc định nếu chưa từng lưu.
func (s *Service) Get(ctx context.Context, k Key) (Record, error) {
	if !k.Valid() {
		return Record{}, errs.Invalid("setting_key_invalid", "Nhóm cấu hình không hợp lệ.")
	}
	recs, err := s.store.GetAll(ctx)
	if err != nil {
		return Record{}, err
	}
	if r, ok := recs[k]; ok {
		return r, nil
	}
	raw, err := marshalDefault(k)
	if err != nil {
		return Record{}, err
	}
	// version = 0 nghĩa là "đang dùng mặc định, chưa từng lưu". Giao diện gửi
	// lại đúng số này khi ghi lần đầu.
	return Record{Key: k, Value: raw, Version: 0, UpdatedAt: s.clk.Now()}, nil
}

func marshalDefault(k Key) (json.RawMessage, error) {
	var v any
	switch k {
	case KeyPricing:
		v = DefaultPricing()
	case KeySurge:
		v = DefaultSurge()
	case KeyMatching:
		v = DefaultMatching()
	case KeyWallet:
		v = DefaultWallet()
	case KeyLocation:
		v = DefaultLocation()
	default:
		return nil, errs.Invalid("setting_key_invalid", "Nhóm cấu hình không hợp lệ.")
	}
	return json.Marshal(v)
}

// Put kiểm tra rồi ghi một nhóm cấu hình.
//
// Kiểm tra TRƯỚC khi ghi, không phải lúc đọc: giá trị sai phải bị chặn ngay ở
// giao diện với thông báo rõ ràng, chứ không được nằm im trong CSDL rồi mới gây
// ra hành vi lạ ở lần dispatch nào đó.
func (s *Service) Put(ctx context.Context, k Key, raw json.RawMessage,
	expectVersion int, by, reason string) (Record, error) {
	if !k.Valid() {
		return Record{}, errs.Invalid("setting_key_invalid", "Nhóm cấu hình không hợp lệ.")
	}
	// GỘP lên giá trị hiện hành, không dựng từ số 0.
	//
	// json.Unmarshal chỉ ghi đè những trường CÓ MẶT trong JSON. Nếu dựng từ
	// struct rỗng thì một PUT chỉ gửi `debt_limit_vnd` sẽ âm thầm đưa thuế,
	// ngưỡng chi trả và phí huỷ về 0 — tất cả đều là giá trị "hợp lệ" nên
	// Validate không chặn được, và không ai phát hiện ra cho tới khi có người
	// hỏi vì sao hệ thống thôi khấu trừ thuế.
	//
	// Việc gộp đi sâu vào từng loại xe của biểu giá, xem mergePricing.
	probe := s.Current(ctx)
	if err := decodeInto(k, raw, &probe); err != nil {
		return Record{}, err
	}
	normalized, err := marshalGroup(k, probe)
	if err != nil {
		return Record{}, err
	}

	rec, err := s.store.Put(ctx, k, normalized, expectVersion, by, reason, s.clk.Now(), s.newID)
	if err != nil {
		return Record{}, err
	}
	if err := s.Reload(ctx); err != nil {
		return rec, nil // đã ghi xong; ảnh chụp sẽ tự nạp lại khi hết hạn
	}
	if s.pub != nil {
		_ = s.pub.Publish(ctx, TopicChanged, map[string]any{
			"key": string(k), "version": rec.Version, "by": by,
		})
	}
	return rec, nil
}

func marshalGroup(k Key, s Snapshot) (json.RawMessage, error) {
	switch k {
	case KeyPricing:
		return json.Marshal(s.Pricing)
	case KeySurge:
		return json.Marshal(s.Surge)
	case KeyMatching:
		return json.Marshal(s.Matching)
	case KeyWallet:
		return json.Marshal(s.Wallet)
	case KeyLocation:
		return json.Marshal(s.Location)
	}
	return nil, errs.Invalid("setting_key_invalid", "Nhóm cấu hình không hợp lệ.")
}

// History trả lịch sử thay đổi của một nhóm.
func (s *Service) History(ctx context.Context, k Key, limit int) ([]HistoryEntry, error) {
	if !k.Valid() {
		return nil, errs.Invalid("setting_key_invalid", "Nhóm cấu hình không hợp lệ.")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.History(ctx, k, limit)
}

// mergePricing gộp một thay đổi biểu giá lên biểu giá ĐANG CHẠY, kể cả bên
// trong từng loại xe.
//
// Vì sao phải làm tay: json.Unmarshal gộp được các trường ở tầng ngoài cùng,
// nhưng với map thì nó dựng một phần tử RỖNG rồi mới đổ dữ liệu vào. Nghĩa là
// một thay đổi chỉ gửi {"tariffs":{"BIKE":{"per_km":5000}}} sẽ đưa toàn bộ phần
// còn lại của biểu giá xe máy về 0.
//
// Ở đây Validate bắt được giá mở cửa 0đ, nhưng phụ phí đêm 0 và CHIẾT KHẤU NỀN
// TẢNG 0 đều là giá trị hợp lệ — nền tảng sẽ lặng lẽ thôi thu hoa hồng và không
// có báo động nào kêu lên.
func mergePricing(raw json.RawMessage, cur PricingGroup) (PricingGroup, error) {
	// Chụp biểu giá đang chạy TRƯỚC đã. `next := cur` chỉ sao chép struct, map
	// bên trong vẫn là cùng một map — để json.Unmarshal ghi vào đó là mất luôn
	// bản gốc mà ta định lấy làm nền.
	merged := make(map[string]Tariff, len(cur.Tariffs))
	for vt, t := range cur.Tariffs {
		merged[vt] = t
	}
	next := cur
	next.Tariffs = nil // buộc json cấp map mới thay vì ghi đè map đang dùng
	if err := json.Unmarshal(raw, &next); err != nil {
		return cur, err
	}
	// Chỉ những loại xe CÓ MẶT trong thay đổi mới được đụng tới; loại xe không
	// nhắc tới giữ nguyên biểu giá đang chạy.
	var probe struct {
		Tariffs map[string]json.RawMessage `json:"tariffs"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return cur, err
	}
	for vt, frag := range probe.Tariffs {
		t := merged[vt] // nền là biểu giá đang chạy của đúng loại xe đó
		if err := json.Unmarshal(frag, &t); err != nil {
			return cur, err
		}
		merged[vt] = t
	}
	next.Tariffs = merged
	return next, nil
}
