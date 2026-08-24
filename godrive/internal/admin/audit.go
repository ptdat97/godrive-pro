package admin

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/example/godrive/pkg/id"
)

// Hành động được ghi nhật ký. Thêm hành động ghi mới thì thêm hằng ở đây.
const (
	ActionReviewKYC = "review_kyc"
)

const (
	TargetDriver = "driver"
	TargetTrip   = "trip"
)

// AuditEntry là một dòng nhật ký thao tác quản trị. Bất biến: chỉ thêm mới.
type AuditEntry struct {
	ID         string         `json:"id"`
	ActorID    string         `json:"actor_id"`
	ActorPhone string         `json:"actor_phone,omitempty"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Payload    map[string]any `json:"payload,omitempty"`
	At         time.Time      `json:"at"`
}

// AuditFilter thu hẹp truy vấn nhật ký.
type AuditFilter struct {
	ActorID    string
	TargetType string
	TargetID   string
	Limit      int
}

// AuditLog là nơi lưu nhật ký thao tác. Không có phương thức sửa hay xoá —
// bất biến "chỉ thêm mới" được thể hiện ngay trong hình dạng interface.
type AuditLog interface {
	Record(ctx context.Context, e AuditEntry) error
	List(ctx context.Context, f AuditFilter) ([]AuditEntry, error)
}

// NewAuditEntry điền ID và thời điểm; phần còn lại do nơi gọi cung cấp.
func NewAuditEntry(actorID, actorPhone, action, targetType, targetID string,
	payload map[string]any, at time.Time) AuditEntry {
	return AuditEntry{
		ID: id.New("aud"), ActorID: actorID, ActorPhone: actorPhone,
		Action: action, TargetType: targetType, TargetID: targetID,
		Payload: payload, At: at,
	}
}

// MemoryAuditLog dùng cho dev/test.
type MemoryAuditLog struct {
	mu      sync.RWMutex
	entries []AuditEntry
}

func NewMemoryAuditLog() *MemoryAuditLog { return &MemoryAuditLog{} }

func (m *MemoryAuditLog) Record(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

func (m *MemoryAuditLog) List(_ context.Context, f AuditFilter) ([]AuditEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AuditEntry, 0, len(m.entries))
	for _, e := range m.entries {
		if f.ActorID != "" && e.ActorID != f.ActorID {
			continue
		}
		if f.TargetType != "" && e.TargetType != f.TargetType {
			continue
		}
		if f.TargetID != "" && e.TargetID != f.TargetID {
			continue
		}
		out = append(out, e)
	}
	// Mới nhất lên đầu — vận hành quan tâm thao tác gần đây.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ID > out[j].ID
	})
	if n := clampLimit(f.Limit); len(out) > n {
		out = out[:n]
	}
	return out, nil
}
