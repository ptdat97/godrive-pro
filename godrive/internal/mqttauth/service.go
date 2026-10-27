package mqttauth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/clock"
)

// ClientIDPrefix là tiền tố bắt buộc của clientId thiết bị tài xế.
const ClientIDPrefix = "drv_"

// RiderClientIDPrefix là tiền tố bắt buộc của clientId thiết bị khách.
//
// Tiền tố còn là cách Authorize biết thiết bị đang hỏi là tài xế hay khách:
// broker không gửi lại token ở bước phân quyền, nhưng clientId đã được ràng
// buộc với danh tính ở bước xác thực nên tin được.
const RiderClientIDPrefix = "rdr_"

// ServiceAccount là thông tin đăng nhập của chính backend vào broker.
//
// Backend cần quyền đọc topic của MỌI tài xế để tiêu thụ ping, nên nó không thể
// dùng cùng luật với thiết bị. Tách hẳn ra một tài khoản riêng để quyền rộng đó
// có đúng một chủ, thay vì nới luật của tài xế cho vừa.
type ServiceAccount struct {
	Username string
	Password string
}

// Service quyết định cho phép hay từ chối một kết nối MQTT.
type Service struct {
	tokens  TokenParser
	drivers DriverLookup
	clk     clock.Clock
	svcAcct ServiceAccount
	revoker Revoker
	riders  RiderTrips
}

func NewService(t TokenParser, d DriverLookup, clk clock.Clock, svc ServiceAccount) *Service {
	return &Service{tokens: t, drivers: d, clk: clk, svcAcct: svc}
}

// UseRevoker bật kiểm tra thu hồi phiên.
func (s *Service) UseRevoker(r Revoker) { s.revoker = r }

// UseRiderTrips mở cửa cho thiết bị khách. Không gọi thì mọi khách đều bị từ
// chối — mặc định đóng, không phải mặc định mở.
func (s *Service) UseRiderTrips(r RiderTrips) { s.riders = r }

// Request là những gì broker biết về thiết bị đang xin vào.
type Request struct {
	ClientID string
	Username string
	Password string
}

// Authenticate là toàn bộ luật vào cửa.
func (s *Service) Authenticate(ctx context.Context, r Request) Decision {
	if r.Username == "" || r.Password == "" {
		return Decision{Deny: "thiếu tên đăng nhập hoặc mật khẩu"}
	}
	if s.isServiceAccount(r) {
		return Decision{Allow: true, Superuser: true}
	}
	// Mật khẩu của thiết bị CHÍNH LÀ token phiên. Không có mật khẩu MQTT riêng:
	// thêm một loại thông tin đăng nhập nữa là thêm một thứ phải cấp, phải xoay
	// vòng và phải thu hồi — trong khi token đã có đủ cả ba.
	claims, err := s.tokens.Parse(r.Password, s.clk.Now().UTC())
	if err != nil {
		return Decision{Deny: "token không hợp lệ: " + err.Error()}
	}
	if deny := s.checkRevoked(ctx, claims); deny != "" {
		return Decision{Deny: deny}
	}
	switch claims.Role {
	case authn.RoleDriver:
		return s.authenticateDriver(ctx, r, claims)
	case authn.RoleRider:
		return s.authenticateRider(ctx, r, claims)
	default:
		return Decision{Deny: "vai trò " + string(claims.Role) + " không dùng MQTT"}
	}
}

func (s *Service) checkRevoked(ctx context.Context, claims *authn.Claims) string {
	if s.revoker == nil {
		return ""
	}
	revoked, err := s.revoker.IsRevoked(ctx, claims)
	if err != nil {
		// Fail-closed, giống middleware HTTP: không kiểm được thì từ chối.
		// Cho qua khi Redis chết nghĩa là mọi phiên đã thu hồi sống lại,
		// đúng vào lúc hệ thống đang có sự cố.
		return "không kiểm được thu hồi phiên: " + err.Error()
	}
	if revoked {
		return "phiên đã bị thu hồi"
	}
	return ""
}

// isServiceAccount so sánh bằng thời gian hằng định.
//
// So bằng == sẽ dừng ngay ở byte đầu tiên khác nhau, và chênh lệch thời gian đó
// đủ để dò ra mật khẩu từng byte một.
func (s *Service) isServiceAccount(r Request) bool {
	if s.svcAcct.Username == "" || s.svcAcct.Password == "" {
		return false // chưa cấu hình thì không có tài khoản dịch vụ nào cả
	}
	u := subtle.ConstantTimeCompare([]byte(r.Username), []byte(s.svcAcct.Username))
	p := subtle.ConstantTimeCompare([]byte(r.Password), []byte(s.svcAcct.Password))
	return u == 1 && p == 1
}

func (s *Service) authenticateDriver(ctx context.Context, r Request, claims *authn.Claims) Decision {
	d, err := s.drivers.GetByAccount(ctx, claims.Sub)
	if err != nil {
		return Decision{Deny: "không tra được hồ sơ tài xế: " + err.Error()}
	}
	// Tên đăng nhập phải đúng là mã tài xế của chính token đó. Không có bước
	// này thì token hợp lệ của A vẫn xin được quyền trên topic của B.
	if r.Username != d.ID {
		return Decision{Deny: "tên đăng nhập không khớp tài xế của token"}
	}
	if d.Suspended {
		return Decision{Deny: "tài xế đang bị khoá"}
	}

	// clientId phải mang mã tài xế. Không ràng buộc thì tài xế A đặt clientId
	// trùng của B là ĐÁ ĐƯỢC B RA khỏi broker — MQTT quy định client sau cùng
	// mang một clientId sẽ chiếm phiên. Đây là một đường tấn công từ chối dịch
	// vụ mà luật topic không chặn được, vì nó xảy ra trước khi có topic nào.
	if !strings.HasPrefix(r.ClientID, ClientIDPrefix+d.ID) {
		return Decision{Deny: "clientId phải bắt đầu bằng " + ClientIDPrefix + d.ID}
	}

	return Decision{Allow: true, Rules: driverRules(d.ID)}
}

// authenticateRider: tên đăng nhập là mã tài khoản khách, clientId là
// "rdr_" + mã đó, và khách phải đang có chuyến.
func (s *Service) authenticateRider(ctx context.Context, r Request, claims *authn.Claims) Decision {
	if s.riders == nil {
		return Decision{Deny: "chưa bật MQTT cho khách"}
	}
	if r.Username != claims.Sub {
		return Decision{Deny: "tên đăng nhập không khớp tài khoản của token"}
	}
	// Cùng lý do như tài xế: không ràng buộc clientId thì khách A đá được khách
	// B ra khỏi broker bằng cách mượn clientId của B.
	if !strings.HasPrefix(r.ClientID, RiderClientIDPrefix+claims.Sub) {
		return Decision{Deny: "clientId phải bắt đầu bằng " + RiderClientIDPrefix + claims.Sub}
	}
	active, err := s.riders.HasActiveTripAsRider(ctx, claims.Sub)
	if err != nil {
		return Decision{Deny: "không tra được chuyến của khách: " + err.Error()}
	}
	if !active {
		return Decision{Deny: "khách không có chuyến nào đang diễn ra"}
	}
	return Decision{Allow: true, Rules: riderRules(claims.Sub)}
}

// riderRules là quyền của một thiết bị khách: chỉ NGHE topic của chính mình,
// không publish vào đâu cả.
func riderRules(id string) []Rule {
	return []Rule{
		{Allow: true, Action: ActionSubscribe, Topic: fmt.Sprintf(TopicRiderTrip, id)},
		{Allow: true, Action: ActionSubscribe, Topic: fmt.Sprintf(TopicRiderDriverLoc, id)},
		{Allow: false, Action: ActionAll, Topic: "#"},
	}
}

// driverRules là quyền của một thiết bị tài xế: chỉ trên topic của chính mình.
func driverRules(id string) []Rule {
	return []Rule{
		{Allow: true, Action: ActionPublish, Topic: fmt.Sprintf(TopicDriverLoc, id)},
		{Allow: true, Action: ActionPublish, Topic: fmt.Sprintf(TopicDriverStatus, id)},
		{Allow: true, Action: ActionSubscribe, Topic: fmt.Sprintf(TopicDriverOffer, id)},
		{Allow: true, Action: ActionSubscribe, Topic: fmt.Sprintf(TopicDriverTrip, id)},
		// Chốt chặn cuối: mọi thứ khác đều cấm. Broker cũng đã đặt mặc định là
		// cấm, nhưng viết ra đây để luật đọc được trọn vẹn ở một chỗ và không
		// phụ thuộc vào việc ai đó giữ đúng cấu hình broker.
		{Allow: false, Action: ActionAll, Topic: "#"},
	}
}

// AuthzRequest là câu hỏi phân quyền của broker: client này có được làm việc
// này trên topic này không.
//
// Broker KHÔNG gửi lại mật khẩu ở bước này, và không cần: bước xác thực đã chốt
// rằng tên đăng nhập chính là mã tài xế của token: xem authenticateDriver. Từ
// đó trở đi, tên đăng nhập là danh tính đã được chứng minh.
type AuthzRequest struct {
	ClientID string
	Username string
	Topic    string
	Action   Action
}

// Authorize trả lời một câu hỏi phân quyền.
//
// Vì sao luật nằm ở đây chứ không ở tệp cấu hình của broker: đây là luật nghiệp
// vụ (tài xế chỉ đụng vào topic của mình), và nó được kiểm bằng test Go. Để nó
// trong cấu hình broker nghĩa là một sửa đổi sai chỉ lộ ra khi có người thử tấn
// công thật.
func (s *Service) Authorize(_ context.Context, r AuthzRequest) bool {
	if r.Username == "" || r.Topic == "" {
		return false
	}
	// Backend cần đọc topic của mọi tài xế. Broker vốn đã bỏ qua bước này cho
	// superuser, nhưng không dựa vào đó: một thay đổi cấu hình phía broker
	// không được phép làm câm luồng vị trí.
	if s.svcAcct.Username != "" &&
		subtle.ConstantTimeCompare([]byte(r.Username), []byte(s.svcAcct.Username)) == 1 {
		return true
	}
	rules := driverRules(r.Username)
	if strings.HasPrefix(r.ClientID, RiderClientIDPrefix) {
		rules = riderRules(r.Username)
	}
	for _, rule := range rules {
		if !rule.Allow || !matches(rule, r) {
			continue
		}
		return true
	}
	return false
}

// matches so một luật với câu hỏi. Topic so KHỚP CHÍNH XÁC, không ký tự đại
// diện: luật của tài xế chỉ liệt kê topic cụ thể của chính họ, nên chấp nhận
// ký tự đại diện ở đây là mở một đường không ai cần tới.
func matches(rule Rule, r AuthzRequest) bool {
	if rule.Topic != r.Topic {
		return false
	}
	return rule.Action == ActionAll || rule.Action == r.Action
}
