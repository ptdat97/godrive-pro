package payment

import (
	"context"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

// MinTopUp là số tiền nạp tối thiểu. Dưới ngưỡng này phí cổng ăn hết phần nạp.
const MinTopUp = money.VND(10000)

// MaxTopUp chặn lỗi gõ nhầm số 0.
const MaxTopUp = money.VND(20000000)

// WalletPort là những gì payment cần từ module ví.
//
// Port khai báo ở đây (bên tiêu thụ) nên wallet không phải biết gì về cổng
// thanh toán.
type WalletPort interface {
	TopUp(ctx context.Context, driverID, paymentRef string, amount money.VND) error
}

type Service struct {
	repo      Repository
	wallet    WalletPort
	clk       clock.Clock
	providers map[ProviderName]Provider
}

func NewService(repo Repository, w WalletPort, clk clock.Clock, ps ...Provider) *Service {
	m := make(map[ProviderName]Provider, len(ps))
	for _, p := range ps {
		m[p.Name()] = p
	}
	return &Service{repo: repo, wallet: w, clk: clk, providers: m}
}

// Enabled cho biết có cổng nào được cấu hình không.
func (s *Service) Enabled() bool { return len(s.providers) > 0 }

// Providers liệt kê tên các cổng đang bật.
func (s *Service) Providers() []ProviderName {
	out := make([]ProviderName, 0, len(s.providers))
	for n := range s.providers {
		out = append(out, n)
	}
	return out
}

// CreateTopUpIntent ghi Ý ĐỊNH nạp tiền TRƯỚC khi chuyển khách sang cổng.
//
// Đây là bước không được bỏ. Không có bản ghi ý định thì lúc webhook về, ta
// không có gì để đối chiếu số tiền — mà chữ ký chỉ chứng minh thông báo đến từ
// cổng, không chứng minh số tiền đúng với thứ mình yêu cầu.
func (s *Service) CreateTopUpIntent(ctx context.Context, p ProviderName, driverID string, amount money.VND) (*Transaction, error) {
	if _, ok := s.providers[p]; !ok {
		return nil, errs.Invalid("provider_unsupported", "Cổng thanh toán không được hỗ trợ.")
	}
	if amount < MinTopUp {
		return nil, errs.Invalid("amount_too_small", "Số tiền nạp tối thiểu là 10.000đ.")
	}
	if amount > MaxTopUp {
		return nil, errs.Invalid("amount_too_large", "Số tiền nạp vượt hạn mức một lần.")
	}
	now := s.clk.Now()
	t := &Transaction{
		ID: id.New("pay"), Provider: p,
		// Mã đơn dùng luôn ID giao dịch: sắp xếp theo thời gian, không đoán được,
		// và tra ngược từ webhook về bản ghi chỉ bằng một khoá.
		OrderID:   id.New("ord"),
		AccountID: driverID, Purpose: PurposeTopUp, Amount: amount,
		Status: StatusPending, CreatedAt: now, ExpiresAt: now.Add(IntentTTL),
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// HandleWebhook xử lý thông báo từ cổng.
//
// Thứ tự các bước KHÔNG được đổi:
//
//  1. xác thực chữ ký      — chống thông báo giả
//  2. tra ý định đã ghi     — chống thông báo cho đơn không tồn tại
//  3. đối chiếu SỐ TIỀN     — chống thông báo hợp lệ nhưng sai số tiền
//  4. ghi kết quả một lần   — chống ghi sổ hai lần khi cổng gửi lại
//  5. ghi sổ cái            — sau khi đã chắc chắn mọi thứ ở trên
func (s *Service) HandleWebhook(ctx context.Context, name ProviderName, body []byte, header map[string]string) ([]byte, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, errs.NotFound("provider_unsupported", "Cổng thanh toán không được hỗ trợ.")
	}

	n, err := p.VerifyWebhook(body, header)
	if err != nil {
		return nil, err
	}

	t, err := s.repo.GetByOrderID(ctx, name, n.OrderID)
	if err != nil {
		return nil, err
	}

	// Số tiền phải khớp CHÍNH XÁC. Lệch dù một đồng cũng từ chối: hoặc là cấu
	// hình sai, hoặc là có người đang thử một cách nào đó — cả hai đều cần người
	// xem, không được tự ghi sổ rồi bỏ qua.
	if n.Amount != t.Amount {
		return nil, errs.E(errs.KindForbidden, "webhook_amount_mismatch",
			"Số tiền trong thông báo không khớp với giao dịch.")
	}

	st := StatusFailed
	if n.Success {
		st = StatusSuccess
	}
	now := s.clk.Now()
	if err := s.repo.MarkResult(ctx, t.ID, n, st, now); err != nil {
		// Đã có kết quả rồi = cổng gửi lại. Trả lời "đã nhận" để nó thôi gửi,
		// nhưng KHÔNG ghi sổ lần nữa.
		if errs.CodeOf(err) == "payment_already_settled" {
			return p.AckBody(n), nil
		}
		return nil, err
	}

	if st == StatusSuccess && t.Purpose == PurposeTopUp {
		// paymentRef quyết định TxID của bút toán, nên nó phải tất định và duy
		// nhất: dùng mã giao dịch phía cổng.
		ref := string(name) + ":" + n.ProviderTxID
		if err := s.wallet.TopUp(ctx, t.AccountID, ref, t.Amount); err != nil {
			return nil, err
		}
	}
	return p.AckBody(n), nil
}

// ExpireStale đánh dấu ý định quá hạn mà không có webhook nào về.
//
// Không có bước này thì bảng đầy dần những giao dịch PENDING vĩnh viễn, và báo
// cáo đối soát không phân biệt được "đang chờ" với "khách bỏ dở".
func (s *Service) ExpireStale(ctx context.Context) (int, error) {
	return s.repo.ExpireStale(ctx, s.clk.Now())
}

// History liệt kê giao dịch của một tài khoản.
func (s *Service) History(ctx context.Context, accountID string, limit int) ([]*Transaction, error) {
	type lister interface {
		ListByAccount(ctx context.Context, accountID string, limit int) ([]*Transaction, error)
	}
	l, ok := s.repo.(lister)
	if !ok {
		return nil, errs.E(errs.KindInternal, "not_supported", "Kho lưu trữ không hỗ trợ tra cứu.")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return l.ListByAccount(ctx, accountID, limit)
}
