// Package places là tìm địa điểm cho app: gõ "Chợ Bến Thành" ra danh sách gợi
// ý, chọn một gợi ý ra toạ độ, và đổi toạ độ ra địa chỉ để hiện tên điểm đón.
//
// Nhà cung cấp: Goong (chốt 2026-10-27, docs/07 T-38) — dữ liệu địa chỉ và hẻm
// Việt Nam tốt, rẻ hơn Google. Mọi lời gọi tới nhà cung cấp đều tính tiền, nên
// Service đặt cache phía trước, và từ khoá quá ngắn bị chặn trước khi gọi.
package places

import (
	"context"
	"time"

	"github.com/example/godrive/pkg/geo"
)

// Suggestion là một gợi ý khi đang gõ — chưa có toạ độ. Chọn gợi ý thì gọi
// Detail để lấy toạ độ.
type Suggestion struct {
	PlaceID string `json:"place_id"`
	Title   string `json:"title"`
	Address string `json:"address"`
}

// Place là một địa điểm đã có toạ độ.
type Place struct {
	PlaceID string    `json:"place_id,omitempty"`
	Name    string    `json:"name"`
	Address string    `json:"address"`
	Point   geo.Point `json:"point"`
}

// Provider là nhà cung cấp dữ liệu bản đồ.
type Provider interface {
	Autocomplete(ctx context.Context, input string, near *geo.Point) ([]Suggestion, error)
	Detail(ctx context.Context, placeID string) (Place, error)
	Reverse(ctx context.Context, p geo.Point) (Place, error)
}

// Cache giữ kết quả đã trả tiền để lấy.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
}

// Thời gian giữ cache, theo mức độ một kết quả có thể đổi.
const (
	// Gợi ý đổi theo thời gian (quán mới mở, đóng) nhưng không đổi trong ngày.
	TTLAutocomplete = 6 * time.Hour
	// Toạ độ của một place_id gần như không đổi.
	TTLDetail = 30 * 24 * time.Hour
	// Địa chỉ của một toạ độ (làm tròn ~11 m) không đổi trong ngày.
	TTLReverse = 24 * time.Hour
)
