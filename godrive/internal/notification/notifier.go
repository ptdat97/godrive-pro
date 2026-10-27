package notification

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
)

// Sender gửi một tin tới một token. Bản thật là FCM.
type Sender interface {
	Send(ctx context.Context, token string, m Message) error
}

// Notifier gửi tin tới MỌI máy của một tài khoản.
type Notifier struct {
	devices DeviceStore
	sender  Sender
	clk     clock.Clock
	log     *slog.Logger
	// OnSend báo kết quả từng lần gửi (ok|gone|retry|error) để đo.
	OnSend func(result string)
}

func NewNotifier(d DeviceStore, s Sender, clk clock.Clock, log *slog.Logger) *Notifier {
	return &Notifier{devices: d, sender: s, clk: clk, log: log}
}

// Enabled: chưa cấu hình FCM thì vẫn lưu được token (app đăng ký từ sớm),
// chỉ là chưa gửi.
func (n *Notifier) Enabled() bool { return n.sender != nil }

// Notify gửi m tới mọi máy của accountID.
//
// Token chết thì xoá ngay. Có máy lỗi TẠM THỜI thì trả lỗi để bus giao lại —
// máy đã nhận sẽ nhận trùng; app bỏ trùng theo mã trong Data. Thà trùng còn
// hơn tài xế không biết có chuyến.
func (n *Notifier) Notify(ctx context.Context, accountID string, m Message) error {
	if n.sender == nil || accountID == "" {
		return nil
	}
	devs, err := n.devices.ListByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	var retry error
	for _, d := range devs {
		err := n.sender.Send(ctx, d.Token, m)
		switch {
		case err == nil:
			n.report("ok")
		case errors.Is(err, ErrTokenGone):
			n.report("gone")
			_ = n.devices.DeleteToken(ctx, d.Token)
		case errs.KindOf(err) == errs.KindUnavailable:
			n.report("retry")
			retry = err
		default:
			// Tin bị từ chối vì nội dung: gửi lại cũng vậy. Ghi log để sửa mẫu.
			n.report("error")
			n.log.Error("FCM từ chối tin", "account_id", accountID, "device_id", d.DeviceID, "err", err)
		}
	}
	return retry
}

func (n *Notifier) report(r string) {
	if n.OnSend != nil {
		n.OnSend(r)
	}
}

// Register ghi token của máy đang đăng nhập.
func (n *Notifier) Register(ctx context.Context, c *authn.Claims, token, platform string) error {
	return n.devices.Upsert(ctx, Device{
		AccountID: c.Sub, DeviceID: c.DeviceID, Token: token, Platform: platform, UpdatedAt: n.clk.Now(),
	})
}

// ForgetDevice xoá token của một máy — khi đăng xuất. Máy đã đăng xuất mà vẫn
// nhận thông báo là lộ thông tin chuyến của người dùng cũ cho người cầm máy.
func (n *Notifier) ForgetDevice(ctx context.Context, accountID, deviceID string) error {
	return n.devices.DeleteDevice(ctx, accountID, deviceID)
}

// ForgetAccount xoá token của mọi máy — khi đăng xuất mọi nơi.
func (n *Notifier) ForgetAccount(ctx context.Context, accountID string) error {
	return n.devices.DeleteAccount(ctx, accountID)
}

// ---------------------------------------------------------------- HTTP

type DeviceHandler struct {
	n    *Notifier
	auth *authn.Issuer
}

func NewDeviceHandler(n *Notifier, a *authn.Issuer) *DeviceHandler {
	return &DeviceHandler{n: n, auth: a}
}

func (h *DeviceHandler) Register(mux *http.ServeMux) {
	any := h.auth.Require(authn.RoleRider, authn.RoleDriver)
	mux.Handle("POST /v1/devices", any(http.HandlerFunc(h.register)))
	mux.Handle("DELETE /v1/devices/current", any(http.HandlerFunc(h.forget)))
}

type registerReq struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// register: app gọi sau khi đăng nhập và mỗi lần FCM báo token mới.
func (h *DeviceHandler) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.n.Register(r.Context(), authn.MustClaims(r.Context()), req.Token, req.Platform); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"registered": true, "push_enabled": h.n.Enabled()})
}

func (h *DeviceHandler) forget(w http.ResponseWriter, r *http.Request) {
	c := authn.MustClaims(r.Context())
	if err := h.n.ForgetDevice(r.Context(), c.Sub, c.DeviceID); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"forgotten": true})
}
