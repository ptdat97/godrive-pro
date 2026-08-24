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
	mu   sync.RWMutex
	subs map[string][]Handler
	log  *slog.Logger
	wg   sync.WaitGroup
}

func NewInMemory(log *slog.Logger) Bus {
	return &inMemory{subs: map[string][]Handler{}, log: log}
}

func (b *inMemory) Publish(ctx context.Context, topic string, payload any) error {
	e, err := NewEvent(topic, payload)
	if err != nil {
		return err
	}
	b.mu.RLock()
	hs := append([]Handler(nil), b.subs[topic]...)
	b.mu.RUnlock()
	for _, h := range hs {
		h := h
		b.wg.Add(1)
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

func (b *inMemory) Close() { b.wg.Wait() }
