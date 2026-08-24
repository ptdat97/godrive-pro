// Package eventbus trừu tượng hoá message bus.
// Bản in-memory dùng cho dev/test; production thay bằng NATS JetStream hoặc Kafka
// (chỉ cần implement lại interface Bus, không sửa code nghiệp vụ).
package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/example/godrive/internal/platform/safego"
	"github.com/example/godrive/pkg/id"
)

// Danh sách topic. Đặt tên theo dạng <domain>.<sự kiện quá khứ>.
const (
	TopicTripRequested  = "trip.requested"
	TopicTripAssigned   = "trip.assigned"
	TopicTripStarted    = "trip.started"
	TopicTripCompleted  = "trip.completed"
	TopicTripCancelled  = "trip.cancelled"
	TopicTripRated      = "trip.rated"
	TopicDriverOnline   = "driver.online"
	TopicDriverOffline  = "driver.offline"
	TopicOfferCreated   = "offer.created"
	TopicOfferAccepted  = "offer.accepted"
	TopicPaymentSettled = "payment.settled"
	// TopicWalletBalanceChanged phát mỗi khi số dư một tài khoản trong sổ cái
	// đổi. Dùng để đồng bộ cột cache drivers.wallet_balance.
	TopicWalletBalanceChanged = "wallet.balance_changed"
)

type Event struct {
	ID      string          `json:"id"`
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
	At      time.Time       `json:"at"`
}

type Handler func(ctx context.Context, e Event) error

type Bus interface {
	Publish(ctx context.Context, topic string, payload any) error
	Subscribe(topic string, h Handler)
	Close()
}

func NewEvent(topic string, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{ID: id.New("evt"), Topic: topic, Payload: raw, At: time.Now().UTC()}, nil
}

type inMemory struct {
	mu     sync.RWMutex
	subs   map[string][]Handler
	log    *slog.Logger
	wg     sync.WaitGroup
	closed bool
}

func NewInMemory(log *slog.Logger) Bus {
	return &inMemory{subs: map[string][]Handler{}, log: log}
}

func (b *inMemory) Publish(ctx context.Context, topic string, payload any) error {
	e, err := NewEvent(topic, payload)
	if err != nil {
		return err
	}
	// wg.Add PHẢI nằm trong khoá cùng với việc kiểm tra closed.
	//
	// Bản trước gọi wg.Add ngoài khoá trong khi Close gọi wg.Wait — đúng kiểu
	// dùng sai WaitGroup mà tài liệu chuẩn cảnh báo: một Add làm đếm từ 0 lên
	// mà chạy song song với Wait thì Wait có thể trả về sớm. Hệ quả thật: tắt
	// êm báo "xong" trong khi sự kiện vẫn đang được xếp lịch, và những sự kiện
	// đó biến mất không dấu vết.
	b.mu.RLock()
	hs := append([]Handler(nil), b.subs[topic]...)
	closed := b.closed
	if !closed {
		b.wg.Add(len(hs))
	}
	b.mu.RUnlock()

	// Đang tắt: chạy handler ĐỒNG BỘ ngay tại đây thay vì spawn goroutine.
	//
	// Không từ chối, vì handler hoàn toàn có thể tự publish tiếp (ghi sổ xong
	// thì báo số dư đổi). Từ chối những sự kiện đó sẽ làm chính handler đang
	// chạy trả lỗi rồi bỏ dở phần việc còn lại của nó.
	//
	// Không spawn goroutine, vì wg.Add lúc bộ đếm đang về 0 sẽ đua với wg.Wait.
	// Chạy đồng bộ giữ nguyên ngữ nghĩa "giao đủ" mà không cần đếm thêm gì.
	if closed {
		for _, h := range hs {
			hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			if err := h(hctx, e); err != nil {
				b.log.Error("handler lỗi lúc tắt", "topic", topic, "event_id", e.ID, "err", err)
			}
			cancel()
		}
		return nil
	}

	for _, h := range hs {
		h := h
		// Bất đồng bộ: publisher không bị chặn bởi subscriber chậm.
		go func() {
			defer b.wg.Done()
			// Handler là code nghiệp vụ của module khác: coi như có thể panic.
			defer safego.Recover(b.log, "eventbus.handler:"+topic, nil)
			hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := h(hctx, e); err != nil {
				b.log.Error("handler lỗi", "topic", topic, "event_id", e.ID, "err", err)
			}
		}()
	}
	return nil
}

func (b *inMemory) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], h)
}

// Close ngừng nhận sự kiện mới rồi CHỜ mọi handler đang chạy xong.
//
// Đặt cờ closed trước khi Wait là điều bắt buộc: nếu không, Publish vẫn có thể
// xếp thêm việc trong lúc Wait đang chạy, và Wait sẽ trả về khi công việc chưa
// thật sự hết.
func (b *inMemory) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.wg.Wait()
}
