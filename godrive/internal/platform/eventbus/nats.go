package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/example/godrive/internal/platform/safego"
	"github.com/example/godrive/pkg/errs"
)

// StreamName là stream JetStream chứa mọi sự kiện nghiệp vụ.
const StreamName = "GODRIVE"

// SubjectPrefix đứng trước mọi topic để nhiều môi trường dùng chung một cụm NATS
// mà không giẫm lên nhau. Topic `trip.requested` thành subject `godrive.trip.requested`.
const SubjectPrefix = "godrive."

// Ngưỡng giao lại. MaxDeliver hữu hạn là bắt buộc: một thông điệp mà handler
// luôn lỗi sẽ được giao lại vô hạn, chiếm băng thông và giấu luôn sự cố.
const (
	MaxDeliver = 10
	AckWait    = 30 * time.Second
)

// backoff là khoảng chờ trước mỗi lần giao lại, theo số lần đã giao.
//
// Nak() trần giao lại NGAY LẬP TỨC. Với lỗi tạm thời — CSDL đang nghẽn, Redis
// vừa mất kết nối — giao lại ngay chỉ làm nghẽn thêm đúng thứ đang hỏng, và
// đốt hết MaxDeliver trong vài mili giây.
var backoff = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second,
	10 * time.Second, 20 * time.Second, 30 * time.Second,
}

// backoffFor trả khoảng chờ cho lần giao thứ n (n bắt đầu từ 1).
func backoffFor(n uint64) time.Duration {
	if n == 0 {
		return backoff[0]
	}
	if int(n) > len(backoff) {
		return backoff[len(backoff)-1]
	}
	return backoff[n-1]
}

// natsBus là Bus trên NATS JetStream.
//
// Khác biệt thật sự so với bản in-memory không phải là "chạy được nhiều tiến
// trình" — outbox đã lo phần đó. Khác biệt là ACK: handler chạy xong mới báo
// nhận. Tiến trình chết giữa chừng thì thông điệp được giao lại cho tiến trình
// khác, thay vì biến mất cùng với goroutine đã chết.
//
// Nhiều tiến trình cùng đăng ký một cặp (topic, name) tự thành một nhóm nhờ
// durable consumer dùng chung tên: mỗi thông điệp xử lý đúng một lần trên toàn
// cụm, không phải một lần cho mỗi pod.
type natsBus struct {
	nc   *nats.Conn
	js   jetstream.JetStream
	log  *slog.Logger
	opts NATSOptions

	mu       sync.Mutex
	consumes []jetstream.ConsumeContext
	closed   bool
	wg       sync.WaitGroup
}

// NATSOptions điều chỉnh hành vi consumer.
type NATSOptions struct {
	// AckWait là thời gian broker chờ ack trước khi coi thông điệp là chưa xử
	// lý và giao cho người khác. Đây chính là thời gian tối đa một việc bị kẹt
	// khi tiến trình đang giữ nó chết đột ngột.
	AckWait time.Duration
	// MaxDeliver là số lần giao tối đa. Hữu hạn là bắt buộc.
	MaxDeliver int
}

func (o NATSOptions) withDefaults() NATSOptions {
	if o.AckWait <= 0 {
		o.AckWait = AckWait
	}
	if o.MaxDeliver <= 0 {
		o.MaxDeliver = MaxDeliver
	}
	return o
}

// NewNATS kết nối và bảo đảm stream tồn tại.
func NewNATS(url string, log *slog.Logger) (Bus, error) {
	return NewNATSWithOptions(url, log, NATSOptions{})
}

// NewNATSWithOptions như NewNATS nhưng chỉnh được ngưỡng consumer.
func NewNATSWithOptions(url string, log *slog.Logger, opts NATSOptions) (Bus, error) {
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1), // mạng chập chờn thì cứ thử lại mãi
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Error("mất kết nối NATS", "err", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info("đã nối lại NATS", "url", c.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "nats_connect_failed", "không nối được NATS", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, errs.Wrap(errs.KindInternal, "nats_jetstream_failed", "không mở được JetStream", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// CreateOrUpdate: nhiều pod khởi động cùng lúc đều gọi hàm này, nên nó phải
	// idempotent chứ không được coi "stream đã tồn tại" là lỗi.
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{SubjectPrefix + ">"},
		// Sự kiện nghiệp vụ phải sống qua việc NATS khởi động lại.
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Discard:   jetstream.DiscardOld,
	})
	if err != nil {
		nc.Close()
		return nil, errs.Wrap(errs.KindInternal, "nats_stream_failed", "không tạo được stream", err)
	}
	return &natsBus{nc: nc, js: js, log: log, opts: opts.withDefaults()}, nil
}

func subjectFor(topic string) string { return SubjectPrefix + topic }

// durableName dựng tên durable consumer. NATS chỉ cho phép [A-Za-z0-9_-].
func durableName(topic, name string) string {
	r := strings.NewReplacer(".", "_", " ", "_")
	return r.Replace(topic) + "__" + r.Replace(name)
}

func (b *natsBus) Publish(ctx context.Context, topic string, payload any) error {
	e, err := NewEvent(topic, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "event_encode_failed", "nats", err)
	}
	// Msg-Id để JetStream tự khử trùng lặp: outbox relay có thể phát lại cùng
	// một sự kiện sau khi tiến trình chết giữa publish và MarkPublished.
	_, err = b.js.PublishMsg(ctx, &nats.Msg{
		Subject: subjectFor(topic),
		Data:    raw,
		Header:  nats.Header{jetstream.MsgIDHeader: []string{e.ID}},
	})
	if err != nil {
		return errs.Wrap(errs.KindInternal, "nats_publish_failed", "nats", err)
	}
	return nil
}

func (b *natsBus) Subscribe(topic, name string, h Handler) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	durable := durableName(topic, name)
	cons, err := b.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: subjectFor(topic),
		// AckExplicit là toàn bộ lý do dùng JetStream: handler chạy xong mới
		// báo nhận, nên tiến trình chết giữa chừng thì việc được giao lại.
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    b.opts.AckWait,
		MaxDeliver: b.opts.MaxDeliver,
	})
	if err != nil {
		b.log.Error("không tạo được consumer NATS", "topic", topic, "name", name, "err", err)
		return
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		b.wg.Add(1)
		defer b.wg.Done()
		defer safego.Recover(b.log, "nats.handler:"+durable, func() {
			// Panic: Nak để việc được giao lại thay vì mất hẳn.
			_ = msg.NakWithDelay(nakDelay(msg))
		})

		var e Event
		if err := json.Unmarshal(msg.Data(), &e); err != nil {
			// Payload hỏng thì giao lại bao nhiêu lần cũng hỏng — bỏ hẳn.
			b.log.Error("sự kiện NATS không giải mã được", "durable", durable, "err", err)
			_ = msg.Term()
			return
		}

		hctx, cancel := context.WithTimeout(context.Background(), b.opts.AckWait)
		defer cancel()
		if err := h(hctx, e); err != nil {
			meta, _ := msg.Metadata()
			d := nakDelay(msg)
			b.log.Error("handler lỗi — sẽ giao lại", "durable", durable, "event_id", e.ID,
				"lần_giao", metaDeliveries(meta), "chờ", d, "err", err)
			_ = msg.NakWithDelay(d)
			return
		}
		if err := msg.Ack(); err != nil {
			// Ack lỗi nghĩa là việc ĐÃ làm xong nhưng NATS không biết, nên nó
			// sẽ giao lại. Đây chính là lý do mọi handler phải idempotent.
			b.log.Error("ack lỗi — sự kiện sẽ được giao lại", "durable", durable, "err", err)
		}
	})
	if err != nil {
		b.log.Error("không bắt đầu được Consume", "topic", topic, "name", name, "err", err)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		cc.Stop()
		return
	}
	b.consumes = append(b.consumes, cc)
}

func metaDeliveries(m *jetstream.MsgMetadata) uint64 {
	if m == nil {
		return 0
	}
	return m.NumDelivered
}

// nakDelay tính khoảng chờ trước lần giao lại kế tiếp.
func nakDelay(msg jetstream.Msg) time.Duration {
	meta, err := msg.Metadata()
	if err != nil {
		return backoff[0]
	}
	return backoffFor(meta.NumDelivered)
}

// Ping kiểm tra kết nối NATS còn dùng được không.
func (b *natsBus) Ping(ctx context.Context) error {
	if !b.nc.IsConnected() {
		return errs.E(errs.KindInternal, "nats_disconnected", "NATS đang mất kết nối")
	}
	// RoundTrip thật, không chỉ xem cờ trạng thái: kết nối có thể còn mở mà
	// server đã không phản hồi.
	if err := b.nc.FlushWithContext(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "nats_unresponsive", "NATS không phản hồi", err)
	}
	return nil
}

func (b *natsBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	cs := b.consumes
	b.consumes = nil
	b.mu.Unlock()

	// Ngừng nhận thông điệp mới TRƯỚC, rồi mới chờ handler đang chạy xong.
	for _, c := range cs {
		c.Stop()
	}
	b.wg.Wait()
	// Drain đẩy nốt những gì đã publish nhưng chưa gửi đi.
	if err := b.nc.Drain(); err != nil {
		b.log.Error("drain NATS lỗi", "err", err)
	}
}
