package wallet

import (
	"context"
	"time"

	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

type Service struct {
	ledger Ledger
	bus    eventbus.Bus
	clk    clock.Clock
	// TaxPermille = 45 (4,5%). Đặt 0 để tắt trong giai đoạn thử nghiệm.
	TaxPermille int64
}

func NewService(l Ledger, bus eventbus.Bus, clk clock.Clock) *Service {
	return &Service{ledger: l, bus: bus, clk: clk, TaxPermille: 0}
}

// SettleTrip ghi sổ khi chuyến hoàn tất. TxID suy ra từ tripID nên hàm này
// idempotent — worker retry bao nhiêu lần cũng chỉ ghi một lần.
func (s *Service) SettleTrip(ctx context.Context, tripID, driverID string, fare, fee money.VND, cash bool) error {
	txID := "tx_trip_" + tripID
	if ok, err := s.ledger.Exists(ctx, txID); err != nil {
		return err
	} else if ok {
		return nil
	}
	now := s.clk.Now()
	var tx Transaction
	if cash {
		tx = SettleCashTrip(txID, driverID, tripID, fare, fee, now)
	} else {
		tx = SettleOnlineTrip(txID, driverID, tripID, fare, fee, now)
	}
	if err := s.ledger.Post(ctx, tx); err != nil {
		return err
	}
	if s.TaxPermille > 0 {
		taxTx := WithholdTax("tx_tax_"+tripID, driverID, tripID, fare-fee, s.TaxPermille, now)
		if err := s.ledger.Post(ctx, taxTx); err != nil {
			return err
		}
	}
	s.announceBalance(ctx, driverID)
	return s.bus.Publish(ctx, eventbus.TopicPaymentSettled, map[string]any{
		"trip_id": tripID, "driver_id": driverID, "cash": cash,
	})
}

// PostCancelFee ghi có phí huỷ chuyến cho tài xế, ghi nợ khách.
//
// Trước đây phí huỷ được TÍNH ở trip.Service và đưa vào nhật ký, nhưng không ai
// ghi sổ — tài xế bị huỷ chuyến trễ không nhận được đồng nào dù giao diện đã hứa.
//
// TxID suy ra tất định từ tripID nên gọi lại bao nhiêu lần cũng chỉ ghi một lần.
func (s *Service) PostCancelFee(ctx context.Context, tripID, riderID, driverID string, amount money.VND) error {
	if amount <= 0 {
		return nil
	}
	txID := "tx_cancel_" + tripID
	if ok, err := s.ledger.Exists(ctx, txID); err != nil {
		return err
	} else if ok {
		return nil
	}
	if err := s.ledger.Post(ctx, CancelFee(txID, riderID, driverID, tripID, amount, s.clk.Now())); err != nil {
		return err
	}
	s.announceBalance(ctx, driverID)
	return nil
}

// announceBalance báo cho phần còn lại của hệ thống rằng số dư ví tài xế đã đổi.
// Lỗi ở đây không được làm hỏng việc ghi sổ — sổ cái đã ghi xong, và bản cache
// sẽ được đồng bộ lại ở lần sự kiện kế tiếp.
func (s *Service) announceBalance(ctx context.Context, driverID string) {
	_ = s.bus.Publish(ctx, eventbus.TopicWalletBalanceChanged, map[string]any{
		"account_id":   driverID,
		"account_type": string(AccDriverWallet),
	})
}

func (s *Service) TopUp(ctx context.Context, driverID, paymentRef string, amount money.VND) error {
	if amount <= 0 {
		return errs.Invalid("amount_invalid", "Số tiền nạp phải lớn hơn 0.")
	}
	if err := s.ledger.Post(ctx, TopUp("tx_top_"+paymentRef, driverID, paymentRef, amount, s.clk.Now())); err != nil {
		return err
	}
	s.announceBalance(ctx, driverID)
	return nil
}

func (s *Service) DriverBalance(ctx context.Context, driverID string) (money.VND, error) {
	return s.ledger.Balance(ctx, driverID, AccDriverWallet)
}

// CashOnHand là số tiền mặt tài xế đang giữ hộ nền tảng trong ngày.
func (s *Service) CashOnHand(ctx context.Context, driverID string) (money.VND, error) {
	return s.ledger.Balance(ctx, driverID, AccDriverCash)
}

// Statement liệt kê bút toán của một tài khoản trong khoảng thời gian —
// dùng cho đối soát và tra cứu khi tài xế khiếu nại số dư.
func (s *Service) Statement(ctx context.Context, accountID string, from, to time.Time) ([]Entry, error) {
	return s.ledger.Statement(ctx, accountID, from, to)
}
