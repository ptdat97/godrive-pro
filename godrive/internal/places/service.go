package places

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// MinQueryRunes: từ khoá ngắn hơn thì không gọi nhà cung cấp. App gọi mỗi lần
// gõ phím; một ký tự thì gợi ý vô nghĩa mà vẫn mất tiền một lượt.
const MinQueryRunes = 2

// MaxQueryRunes chặn chuỗi dài bất thường — không ai gõ địa chỉ 120 ký tự.
const MaxQueryRunes = 120

type Service struct {
	provider Provider
	cache    Cache
	// OnLookup báo mỗi lần tra: kind là autocomplete|detail|reverse, hit là
	// có lấy từ cache không. Để đo tỉ lệ cache — chính là số tiền tiết kiệm.
	OnLookup func(kind string, hit bool)
}

// NewService: provider nil nghĩa là chưa cấu hình nhà cung cấp — mọi lời gọi
// trả places_unavailable thay vì một lỗi khó hiểu.
func NewService(p Provider, c Cache) *Service {
	return &Service{provider: p, cache: c}
}

func (s *Service) Enabled() bool { return s.provider != nil }

var errUnavailable = errs.E(errs.KindUnavailable, "places_unavailable",
	"Tìm kiếm địa điểm tạm thời chưa dùng được. Vui lòng chọn trên bản đồ.")

func (s *Service) Autocomplete(ctx context.Context, input string, near *geo.Point) ([]Suggestion, error) {
	q := normalize(input)
	n := utf8.RuneCountInString(q)
	if n < MinQueryRunes {
		return []Suggestion{}, nil // không phải lỗi: app đang gõ dở
	}
	if n > MaxQueryRunes {
		return nil, errs.Invalid("query_too_long", "Từ khoá quá dài.")
	}
	if near != nil && !near.Valid() {
		return nil, errs.Invalid("point_invalid", "Toạ độ không hợp lệ.")
	}
	// Khoá cache gồm ô ~1 km quanh người dùng: cùng từ khoá ở Quận 1 và ở Thủ
	// Đức cho gợi ý khác nhau, nhưng dịch vài trăm mét thì vẫn như nhau.
	key := "ac:" + q
	if near != nil {
		key += fmt.Sprintf(":%.2f,%.2f", near.Lat, near.Lng)
	}
	var out []Suggestion
	err := s.cached(ctx, "autocomplete", key, TTLAutocomplete, &out, func() (any, error) {
		return s.provider.Autocomplete(ctx, q, near)
	})
	if out == nil {
		out = []Suggestion{}
	}
	return out, err
}

func (s *Service) Detail(ctx context.Context, placeID string) (Place, error) {
	placeID = strings.TrimSpace(placeID)
	if placeID == "" || len(placeID) > 200 {
		return Place{}, errs.Invalid("place_id_invalid", "Mã địa điểm không hợp lệ.")
	}
	var out Place
	err := s.cached(ctx, "detail", "pd:"+placeID, TTLDetail, &out, func() (any, error) {
		return s.provider.Detail(ctx, placeID)
	})
	return out, err
}

func (s *Service) Reverse(ctx context.Context, p geo.Point) (Place, error) {
	if !p.Valid() {
		return Place{}, errs.Invalid("point_invalid", "Toạ độ không hợp lệ.")
	}
	// Làm tròn 4 chữ số (~11 m): GPS nhảy vài mét không được thành một lượt
	// gọi tính tiền mới.
	key := fmt.Sprintf("rv:%.4f,%.4f", p.Lat, p.Lng)
	var out Place
	err := s.cached(ctx, "reverse", key, TTLReverse, &out, func() (any, error) {
		return s.provider.Reverse(ctx, p)
	})
	if err == nil {
		out.Point = p // trả đúng điểm người dùng hỏi, không phải điểm đã làm tròn
	}
	return out, err
}

// cached đọc cache, trượt thì gọi nhà cung cấp rồi ghi lại. Cache lỗi thì vẫn
// phục vụ bằng nhà cung cấp — mất cache là tốn tiền, không phải mất chức năng.
func (s *Service) cached(ctx context.Context, kind, key string, ttl time.Duration, out any, fetch func() (any, error)) error {
	if s.cache != nil {
		if raw, ok, err := s.cache.Get(ctx, key); err == nil && ok && json.Unmarshal(raw, out) == nil {
			s.report(kind, true)
			return nil
		}
	}
	if s.provider == nil {
		return errUnavailable
	}
	v, err := fetch()
	s.report(kind, false)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, key, raw, ttl)
	}
	return json.Unmarshal(raw, out)
}

func (s *Service) report(kind string, hit bool) {
	if s.OnLookup != nil {
		s.OnLookup(kind, hit)
	}
}

// normalize gom khoảng trắng và chữ hoa/thường để "Chợ  Bến Thành" và "chợ bến
// thành" dùng chung một mục cache.
func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
