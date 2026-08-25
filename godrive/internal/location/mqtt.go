package location

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/example/godrive/pkg/errs"
)

// Topic MQTT của luồng vị trí.
//
// Vì sao MQTT chứ không phải WebSocket hay HTTP: tài xế ở VN chủ yếu dùng máy
// Android giá rẻ trên mạng 4G chập chờn. MQTT có header vài byte thay vì vài
// trăm, giữ một kết nối duy nhất, có QoS 1 để ping không mất khi sóng chờn, và
// có Last Will — broker tự báo khi thiết bị mất kết nối mà không cần chờ hết
// hạn độ tươi.
//
// Với ping 4 giây một lần, HTTP tốn pin và băng thông gấp nhiều lần.
const (
	// TopicPing: app tài xế publish vào drv/{driverID}/loc, QoS 1.
	TopicPing = "drv/+/loc"
	// TopicStatus: Last Will của app đặt ở drv/{driverID}/status.
	TopicStatus = "drv/+/status"
)

// MQTTConsumer nhận ping vị trí từ broker MQTT và đưa vào Service.Ingest.
type MQTTConsumer struct {
	svc    *Service
	log    *slog.Logger
	client mqtt.Client
	// IngestTimeout chặn một ping xử lý quá lâu làm nghẽn hàng đợi của broker.
	IngestTimeout time.Duration
}

// NewMQTTConsumer dựng consumer. clientID phải KHÁC NHAU giữa các pod — hai
// client trùng ID sẽ liên tục đá nhau ra khỏi broker.
func NewMQTTConsumer(url, clientID string, svc *Service, log *slog.Logger) (*MQTTConsumer, error) {
	c := &MQTTConsumer{svc: svc, log: log, IngestTimeout: 5 * time.Second}

	opts := mqtt.NewClientOptions().
		AddBroker(url).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(2 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		// CleanSession=false: broker giữ lại đăng ký và thông điệp QoS 1 chưa
		// giao khi consumer mất kết nối. Đặt true là tự nguyện mất ping.
		SetCleanSession(false).
		SetOrderMatters(false). // xử lý song song; mỗi ping độc lập với nhau
		SetOnConnectHandler(func(cl mqtt.Client) {
			log.Info("đã nối MQTT", "client_id", clientID)
			// Đăng ký lại SAU MỖI LẦN nối lại, không phải một lần lúc khởi động:
			// nối lại mà không đăng ký lại thì kết nối sống nhưng câm.
			if t := cl.Subscribe(TopicPing, 1, c.onPing); t.Wait() && t.Error() != nil {
				log.Error("không đăng ký được topic ping", "err", t.Error())
			}
			if t := cl.Subscribe(TopicStatus, 1, c.onStatus); t.Wait() && t.Error() != nil {
				log.Error("không đăng ký được topic status", "err", t.Error())
			}
		}).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Error("mất kết nối MQTT", "err", err)
		})

	c.client = mqtt.NewClient(opts)
	if t := c.client.Connect(); t.WaitTimeout(10*time.Second) && t.Error() != nil {
		return nil, errs.Wrap(errs.KindInternal, "mqtt_connect_failed", "không nối được MQTT", t.Error())
	}
	return c, nil
}

// driverIDFrom lấy driverID từ topic drv/{id}/loc.
//
// Lấy từ TOPIC chứ không lấy từ payload là có chủ đích: broker kiểm soát được
// ai publish vào topic nào (qua ACL), còn payload thì thiết bị tự khai. Tin vào
// payload nghĩa là bất kỳ tài xế nào cũng gửi được vị trí giả cho người khác.
func driverIDFrom(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) < 3 || parts[0] != "drv" {
		return ""
	}
	return parts[1]
}

func (c *MQTTConsumer) onPing(_ mqtt.Client, m mqtt.Message) {
	driverID := driverIDFrom(m.Topic())
	if driverID == "" {
		c.log.Error("topic MQTT sai định dạng", "topic", m.Topic())
		return
	}
	var p Ping
	if err := json.Unmarshal(m.Payload(), &p); err != nil {
		c.log.Error("ping MQTT không giải mã được", "driver_id", driverID, "err", err)
		return
	}
	// Ghi đè bằng ID lấy từ topic — không tin trường trong payload.
	p.DriverID = driverID

	ctx, cancel := context.WithTimeout(context.Background(), c.IngestTimeout)
	defer cancel()
	if err := c.svc.Ingest(ctx, p); err != nil {
		// Ping bị từ chối là chuyện BÌNH THƯỜNG (GPS giả, tín hiệu yếu, nhảy vị
		// trí) chứ không phải sự cố hệ thống — ghi ở mức debug để không lấp log.
		c.log.Debug("ping bị từ chối", "driver_id", driverID, "err", err)
	}
}

// onStatus xử lý Last Will: broker tự phát khi thiết bị mất kết nối.
//
// Đây là thứ HTTP không có. Không có nó thì tài xế mất mạng vẫn nằm trong tập
// ứng viên cho tới khi hết hạn độ tươi (45 giây) — đủ lâu để nhận một lời mời
// mà họ không bao giờ thấy, và khách thì chờ hết hạn lời mời.
func (c *MQTTConsumer) onStatus(_ mqtt.Client, m mqtt.Message) {
	driverID := driverIDFrom(m.Topic())
	if driverID == "" {
		return
	}
	if strings.TrimSpace(string(m.Payload())) != "offline" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.IngestTimeout)
	defer cancel()
	if err := c.svc.Remove(ctx, driverID); err != nil {
		c.log.Error("không gỡ được tài xế khỏi chỉ mục", "driver_id", driverID, "err", err)
		return
	}
	c.log.Info("tài xế mất kết nối, đã gỡ khỏi chỉ mục", "driver_id", driverID)
}

// Ping kiểm tra kết nối MQTT.
func (c *MQTTConsumer) Ping(_ context.Context) error {
	if !c.client.IsConnectionOpen() {
		return errs.E(errs.KindInternal, "mqtt_disconnected", "MQTT đang mất kết nối")
	}
	return nil
}

func (c *MQTTConsumer) Close() {
	// 250ms để gửi nốt gói DISCONNECT trước khi đóng.
	c.client.Disconnect(250)
}
