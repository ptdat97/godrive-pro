package mqttauth

import (
	"log/slog"
	"net/http"

	"github.com/example/godrive/internal/platform/httpx"
)

// Handler phục vụ EMQX, không phục vụ người dùng cuối.
type Handler struct {
	svc *Service
	log *slog.Logger
}

func NewHandler(s *Service, log *slog.Logger) *Handler {
	return &Handler{svc: s, log: log}
}

// Register gắn endpoint xác thực.
//
// KHÔNG có middleware xác thực phía trước: chính nó là cửa xác thực, và người
// gọi là broker chứ không phải người dùng. Đường này phải được chặn ở tầng
// mạng — chỉ broker mới gọi được tới, xem [08 §8.12](../../../docs/08-van-hanh.md).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/mqtt/auth", h.authenticate)
	mux.HandleFunc("POST /internal/mqtt/authz", h.authorize)
}

// emqxRequest khớp với mẫu thân yêu cầu cấu hình ở phía EMQX.
type emqxRequest struct {
	ClientID string `json:"clientid"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type emqxResponse struct {
	Result string `json:"result"` // "allow" | "deny" | "ignore"
	// IsSuperuser bỏ qua mọi kiểm tra topic. Chỉ backend mới được.
	IsSuperuser bool `json:"is_superuser,omitempty"`
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) {
	var req emqxRequest
	if err := httpx.Decode(r, &req); err != nil {
		// Thân yêu cầu hỏng nghĩa là mẫu cấu hình phía broker sai — lỗi vận
		// hành, không phải thiết bị xấu. Ghi log to để còn sửa được.
		h.log.Error("thân yêu cầu từ EMQX không đọc được", "err", err)
		httpx.JSON(w, http.StatusOK, emqxResponse{Result: "deny"})
		return
	}

	d := h.svc.Authenticate(r.Context(), Request{
		ClientID: req.ClientID, Username: req.Username, Password: req.Password,
	})
	if !d.Allow {
		// Lý do chỉ vào log máy chủ. Thiết bị chỉ nhận đúng một chữ "deny":
		// phân biệt "sai mật khẩu" với "tài khoản bị khoá" là chỉ đường cho
		// người đang dò xem tài khoản nào có thật.
		h.log.Warn("từ chối kết nối MQTT",
			"client_id", req.ClientID, "username", req.Username, "ly_do", d.Deny)
		httpx.JSON(w, http.StatusOK, emqxResponse{Result: "deny"})
		return
	}

	// Phản hồi này KHÔNG kèm danh sách quyền.
	//
	// EMQX 5 có chỗ nhận quyền ngay trong phản hồi xác thực, nhưng thử trên
	// 5.6.1 thì nó im lặng bỏ qua — kết nối vào được mà không topic nào dùng
	// được. Im lặng đúng theo hướng an toàn, nhưng vẫn là im lặng: nếu tin vào
	// đường đó, ta sẽ có một danh sách quyền trông rất chắc chắn trong mã nguồn
	// mà broker không hề đọc.
	//
	// Quyền đi qua /internal/mqtt/authz, nơi broker hỏi từng thao tác một.
	httpx.JSON(w, http.StatusOK, emqxResponse{Result: "allow", IsSuperuser: d.Superuser})
}

// emqxAuthzRequest khớp mẫu thân yêu cầu của nguồn phân quyền HTTP.
type emqxAuthzRequest struct {
	ClientID string `json:"clientid"`
	Username string `json:"username"`
	Topic    string `json:"topic"`
	Action   string `json:"action"` // "publish" | "subscribe"
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	var req emqxAuthzRequest
	if err := httpx.Decode(r, &req); err != nil {
		h.log.Error("thân yêu cầu phân quyền không đọc được", "err", err)
		httpx.JSON(w, http.StatusOK, map[string]string{"result": "deny"})
		return
	}
	ok := h.svc.Authorize(r.Context(), AuthzRequest{
		ClientID: req.ClientID, Username: req.Username,
		Topic: req.Topic, Action: Action(req.Action),
	})
	if !ok {
		h.log.Warn("từ chối thao tác MQTT",
			"client_id", req.ClientID, "username", req.Username,
			"topic", req.Topic, "action", req.Action)
		httpx.JSON(w, http.StatusOK, map[string]string{"result": "deny"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"result": "allow"})
}
