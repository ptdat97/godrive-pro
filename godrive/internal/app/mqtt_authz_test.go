package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Đây là test ĐẦU-CUỐI thật, không dùng app trong tiến trình.
//
// Lý do: EMQX gọi ngược về backend đang CHẠY để xác thực. Một app dựng trong
// tiến trình test có khoá JWT riêng và CSDL riêng, nên token nó phát ra vô
// nghĩa với broker. Muốn kiểm đúng thứ đang bảo vệ hệ thống thì phải đi qua
// đúng backend mà broker đang hỏi.
func requireSecuredBroker(t *testing.T) (string, string) {
	t.Helper()
	url := os.Getenv("TEST_MQTT_URL")
	api := os.Getenv("TEST_API_URL")
	if url == "" || api == "" {
		t.Skip("bỏ qua: cần TEST_MQTT_URL và TEST_API_URL (backend mà broker đang hỏi)")
	}
	if os.Getenv("TEST_MQTT_USERNAME") == "" {
		t.Skip("bỏ qua: đặt TEST_MQTT_USERNAME/TEST_MQTT_PASSWORD")
	}
	// Nặc danh phải bị chặn. Nếu vào được nghĩa là broker chưa bật xác thực và
	// mọi khẳng định phía dưới sẽ xanh mà không chứng minh được gì.
	if connects(t, url, "kiem-tra-nac-danh-"+stamp(), "", "") {
		t.Fatal("broker đang MỞ: nặc danh vẫn kết nối được. " +
			"Cấu hình authenticator HTTP và no_match=deny, xem docs/08 §8.12")
	}
	return url, api
}

// callAPI gọi backend đang chạy.
func callAPI(t *testing.T, api, method, path, token string, body any) map[string]any {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, api+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 400 {
		t.Fatalf("%s %s → %d: %s", method, path, resp.StatusCode, raw)
	}
	return out
}

// remoteLogin đăng nhập bằng OTP trên backend đang chạy, trả số điện thoại,
// token phiên và mã tài khoản.
func remoteLogin(t *testing.T, api, role string) (string, string, string) {
	t.Helper()
	// Số di động VN: 0 + đầu số + 8 chữ số. Lấy theo đồng hồ để mỗi lần chạy là
	// một tài khoản mới, vì backend thật giữ lại dữ liệu giữa các lần chạy.
	n := time.Now().UnixNano()
	phone := fmt.Sprintf("09%08d", n%1e8)
	ch := callAPI(t, api, "POST", "/v1/auth/otp", "",
		map[string]any{"phone": phone, "role": role})
	code, _ := ch["dev_code"].(string)
	if code == "" {
		t.Skip("bỏ qua: backend không ở chế độ DEV_AUTH nên không lấy được OTP")
	}
	tp := callAPI(t, api, "POST", "/v1/auth/verify", "", map[string]any{
		"challenge_id": ch["challenge_id"], "code": code, "device_id": "test-mqtt",
	})
	tok, _ := tp["access_token"].(string)
	acc, _ := tp["account"].(map[string]any)
	accID, _ := acc["id"].(string)
	return phone, tok, accID
}

// newRemoteDriver tạo một tài xế THẬT trên backend đang chạy và trả về mã tài
// xế cùng token phiên của chính họ.
func newRemoteDriver(t *testing.T, api string) (string, string) {
	t.Helper()
	phone, tok, _ := remoteLogin(t, api, "driver")

	suffix := phone[2:] // 8 chữ số, đủ để mọi giấy tờ là duy nhất
	d := callAPI(t, api, "POST", "/v1/drivers/register", tok, map[string]any{
		"full_name": "Tài xế test", "phone": "+84" + phone[1:], "city": "HCM",
		"vehicle": map[string]any{
			"type":  "BIKE",
			"plate": fmt.Sprintf("59X%s-%s.%s", suffix[:1], suffix[1:4], suffix[4:6]),
		},
		"documents": map[string]any{
			"national_id": "0790" + suffix, "driver_license": "7901" + suffix,
		},
	})
	id, _ := d["id"].(string)
	if id == "" {
		t.Fatalf("không tạo được tài xế: %v", d)
	}
	return id, tok
}

func stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

func connects(t *testing.T, url, clientID, user, pass string) bool {
	t.Helper()
	cl, ok := dial(url, clientID, user, pass)
	if ok {
		cl.Disconnect(50)
	}
	return ok
}

func dial(url, clientID, user, pass string) (mqtt.Client, bool) {
	opts := mqtt.NewClientOptions().AddBroker(url).SetClientID(clientID).
		SetConnectTimeout(5 * time.Second).SetCleanSession(true).
		SetAutoReconnect(false).SetConnectRetry(false)
	if user != "" {
		opts.SetUsername(user).SetPassword(pass)
	}
	cl := mqtt.NewClient(opts)
	tk := cl.Connect()
	if !tk.WaitTimeout(6*time.Second) || tk.Error() != nil {
		return nil, false
	}
	return cl, true
}

// BẤT BIẾN CỦA T-32: tài xế chỉ ghi được vào topic của chính mình.
//
// Trước bản vá, broker không xác thực gì cả: ai kết nối được cũng publish được
// vào drv/{id}/loc của bất kỳ ai. Nghĩa là giả được vị trí của một tài xế khác
// và qua đó giành chuyến ở khu vực mình không hề có mặt — trong khi chống gian
// lận ở tầng ứng dụng chỉ lọc NỘI DUNG chứ không xác minh NGƯỜI GỬI.
//
// Kiểm bằng việc bản tin có tới nơi hay không, KHÔNG tin vào mã trả về của lệnh
// publish: EMQX có thể im lặng bỏ gói mà vẫn trả ACK, tuỳ deny_action.
func TestDriverCannotPublishToAnotherDriverTopic(t *testing.T) {
	url, api := requireSecuredBroker(t)
	victimID, _ := newRemoteDriver(t, api)
	attackerID, attackerTok := newRemoteDriver(t, api)

	// Người quan sát dùng tài khoản dịch vụ để thấy MỌI topic tài xế.
	spy, ok := dial(url, "godrive-spy-"+stamp(),
		os.Getenv("TEST_MQTT_USERNAME"), os.Getenv("TEST_MQTT_PASSWORD"))
	if !ok {
		t.Fatal("tài khoản dịch vụ phải kết nối được")
	}
	defer spy.Disconnect(50)

	var mu sync.Mutex
	seen := map[string]int{}
	if tk := spy.Subscribe("drv/+/loc", 1, func(_ mqtt.Client, m mqtt.Message) {
		mu.Lock()
		seen[m.Topic()]++
		mu.Unlock()
	}); !tk.WaitTimeout(5*time.Second) || tk.Error() != nil {
		t.Fatalf("tài khoản dịch vụ phải đăng ký được drv/+/loc: %v", tk.Error())
	}

	own := fmt.Sprintf("drv/%s/loc", attackerID)
	other := fmt.Sprintf("drv/%s/loc", victimID)
	payload := `{"lat":10.7769,"lng":106.7009,"accuracy_m":10,"battery_pc":90}`

	// MỖI phép thử một kết nối riêng. Broker đặt deny_action=disconnect nên vi
	// phạm đầu tiên là mất phiên — dùng chung một kết nối thì lệnh publish hợp
	// lệ phía sau không bao giờ được gửi, và test sẽ đổ lỗi nhầm cho luật ACL.
	pub := func(name, topic string) {
		cl, ok := dial(url, "drv_"+attackerID+"_"+name, attackerID, attackerTok)
		if !ok {
			t.Fatalf("tài xế hợp lệ phải kết nối được (%s)", name)
		}
		defer cl.Disconnect(50)
		cl.Publish(topic, 1, false, payload).WaitTimeout(3 * time.Second)
		time.Sleep(300 * time.Millisecond) // để broker kịp xử lý trước khi đóng
	}
	pub("thu-topic-nguoi-khac", other)
	pub("thu-topic-cua-minh", own)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := seen[own] > 0
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen[other] > 0 {
		t.Errorf("LỖ HỔNG: tài xế %s ghi được vào topic của %s", attackerID, victimID)
	}
	if seen[own] == 0 {
		t.Errorf("tài xế phải ghi được vào topic của CHÍNH MÌNH (%s) — "+
			"luật chặt quá tay cũng là lỗi, vì nó làm chết luồng vị trí", own)
	}
}

// Token của tài xế khác không mượn được danh nghĩa.
func TestCannotConnectAsAnotherDriver(t *testing.T) {
	url, api := requireSecuredBroker(t)
	victimID, _ := newRemoteDriver(t, api)
	_, attackerTok := newRemoteDriver(t, api)

	if connects(t, url, "drv_"+victimID, victimID, attackerTok) {
		t.Error("token của tài xế này không được nhận danh nghĩa tài xế khác")
	}
	if connects(t, url, "drv_"+victimID, victimID, "token-bia-dat") {
		t.Error("token bịa đặt vẫn vào được")
	}
}

// clientId trùng của người khác là đường đá họ ra khỏi broker — MQTT cho client
// sau cùng chiếm phiên. Phải chặn trước khi có topic nào được nhắc tới.
func TestCannotHijackAnotherDriverClientID(t *testing.T) {
	url, api := requireSecuredBroker(t)
	victimID, _ := newRemoteDriver(t, api)
	attackerID, attackerTok := newRemoteDriver(t, api)

	if connects(t, url, "drv_"+victimID, attackerID, attackerTok) {
		t.Error("không được mang clientId của tài xế khác")
	}
	if !connects(t, url, "drv_"+attackerID+"_pixel", attackerID, attackerTok) {
		t.Error("clientId của chính mình kèm hậu tố thiết bị phải dùng được")
	}
}

// T-31 qua broker THẬT: khách có chuyến nối được, nhận được lần đổi trạng thái
// chuyến của mình, và chỉ của mình.
//
// Đi trọn đường thật: HTTP huỷ chuyến → outbox → NATS → consumer đẩy tin →
// EMQX hỏi ACL → thiết bị. Test với Downlink giả chỉ chứng minh được khúc giữa.
func TestRiderReceivesOwnTripPushOverBroker(t *testing.T) {
	url, api := requireSecuredBroker(t)

	_, tok, riderID := remoteLogin(t, api, "rider")
	tripID := remoteTrip(t, api, tok)
	// Kẻ nghe trộm là một khách THẬT đang có chuyến — tức vượt qua được bước
	// xác thực — để "không nhận được gì" chứng minh được ACL chứ không chỉ
	// chứng minh rằng người lạ không vào được cửa.
	_, tok2, rider2 := remoteLogin(t, api, "rider")
	remoteTrip(t, api, tok2)

	own := "rdr/" + riderID + "/trip"
	var mu sync.Mutex
	var mine, stolen []string
	record := func(dst *[]string) mqtt.MessageHandler {
		return func(_ mqtt.Client, m mqtt.Message) {
			var p PushMessage
			_ = json.Unmarshal(m.Payload(), &p)
			mu.Lock()
			defer mu.Unlock()
			if p.Trip != nil {
				*dst = append(*dst, string(p.Trip.Status))
			}
		}
	}

	victim, ok := dial(url, "rdr_"+riderID+"-test", riderID, tok)
	if !ok {
		t.Fatal("khách đang có chuyến phải nối được broker")
	}
	defer victim.Disconnect(50)
	victim.Subscribe(own, 1, record(&mine)).WaitTimeout(3 * time.Second)

	// Kết nối RIÊNG cho kẻ nghe trộm: broker đặt deny_action=disconnect nên
	// lần đăng ký trái phép sẽ cắt luôn kết nối đó — dùng chung kết nối với
	// nạn nhân thì chính nạn nhân bị đá ra.
	spy, ok := dial(url, "rdr_"+rider2+"-test", rider2, tok2)
	if !ok {
		t.Fatal("khách thứ hai đang có chuyến phải nối được broker")
	}
	defer spy.Disconnect(50)
	spy.Subscribe(own, 1, record(&stolen)).WaitTimeout(3 * time.Second)
	time.Sleep(300 * time.Millisecond) // để đăng ký kịp có hiệu lực trên broker

	callAPI(t, api, "POST", "/v1/trips/"+tripID+"/cancel", tok, map[string]any{"reason": "test"})

	got := func() ([]string, []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), mine...), append([]string(nil), stolen...)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m, _ := got(); contains(m, "CANCELLED") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	m, s := got()
	if !contains(m, "CANCELLED") {
		t.Fatalf("khách không nhận được tin CANCELLED của chuyến mình, nhận được %v", m)
	}
	if len(s) > 0 {
		t.Fatalf("khách khác nghe được chuyến không phải của mình: %v", s)
	}

	// Chuyến đã kết thúc → không còn lý do giữ kết nối, broker phải từ chối.
	if connects(t, url, "rdr_"+riderID+"-lan2", riderID, tok) {
		t.Fatal("khách không còn chuyến nào mà vẫn nối được broker")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// remoteTrip đặt một chuyến xe máy trên backend đang chạy, trả mã chuyến.
func remoteTrip(t *testing.T, api, tok string) string {
	t.Helper()
	q := callAPI(t, api, "POST", "/v1/quotes", tok, map[string]any{
		"pickup":  map[string]any{"lat": pickup.Lat, "lng": pickup.Lng},
		"dropoff": map[string]any{"lat": dropoff.Lat, "lng": dropoff.Lng},
	})
	var quoteID any
	for _, raw := range q["quotes"].([]any) {
		if m := raw.(map[string]any); m["vehicle_type"] == "BIKE" {
			quoteID = m["id"]
		}
	}
	tr := callAPI(t, api, "POST", "/v1/trips", tok, map[string]any{
		"quote_id":       quoteID,
		"pickup":         map[string]any{"point": map[string]any{"lat": pickup.Lat, "lng": pickup.Lng}},
		"dropoff":        map[string]any{"point": map[string]any{"lat": dropoff.Lat, "lng": dropoff.Lng}},
		"payment_method": "CASH",
	})
	id, _ := tr["id"].(string)
	if id == "" {
		t.Fatalf("không đặt được chuyến: %v", tr)
	}
	return id
}
