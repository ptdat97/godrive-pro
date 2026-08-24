package wallet

import (
	"time"

	"github.com/example/godrive/pkg/money"
)

// SettleCashTrip: khách trả TIỀN MẶT cho tài xế.
//
//	DRIVER_CASH      +fare      (tài xế cầm tiền của khách)
//	PLATFORM_REVENUE -fare      (đối ứng doanh thu gộp)
//	DRIVER_WALLET    -fee       (trừ chiết khấu, ví có thể âm -> công nợ)
//	PLATFORM_REVENUE +fee
//
// Kết quả: ví tài xế giảm đúng phần chiết khấu; số dư âm chính là công nợ
// tài xế phải nạp lại cho nền tảng.
func SettleCashTrip(txID, driverID, tripID string, fare, fee money.VND, at time.Time) Transaction {
	return Transaction{
		ID:      txID,
		RefType: RefTrip,
		RefID:   tripID,
		At:      at,
		Entries: []Entry{
			newEntry(txID, driverID, AccDriverCash, fare, RefTrip, tripID, "Thu tiền mặt từ khách", at),
			newEntry(txID, "platform", AccPlatformRev, fare.Neg(), RefTrip, tripID, "Doanh thu chuyến (tiền mặt)", at),
			newEntry(txID, driverID, AccDriverWallet, fee.Neg(), RefTrip, tripID, "Chiết khấu nền tảng", at),
			newEntry(txID, "platform", AccPlatformRev, fee, RefTrip, tripID, "Chiết khấu nền tảng", at),
		},
	}
}

// SettleOnlineTrip: khách trả qua ví/cổng thanh toán.
//
//	GATEWAY_CLEARING -fare      (tiền về nền tảng, chờ đối soát với cổng)
//	DRIVER_WALLET    +earn      (ghi có thu nhập tài xế)
//	PLATFORM_REVENUE +fee
func SettleOnlineTrip(txID, driverID, tripID string, fare, fee money.VND, at time.Time) Transaction {
	earn := fare - fee
	return Transaction{
		ID:      txID,
		RefType: RefTrip,
		RefID:   tripID,
		At:      at,
		Entries: []Entry{
			newEntry(txID, "platform", AccGatewayClear, fare.Neg(), RefTrip, tripID, "Thu qua cổng thanh toán", at),
			newEntry(txID, driverID, AccDriverWallet, earn, RefTrip, tripID, "Thu nhập chuyến", at),
			newEntry(txID, "platform", AccPlatformRev, fee, RefTrip, tripID, "Chiết khấu nền tảng", at),
		},
	}
}

// TopUp: tài xế nạp tiền vào ví (VietQR / MoMo / ZaloPay) để trả công nợ.
func TopUp(txID, driverID, paymentRef string, amount money.VND, at time.Time) Transaction {
	return Transaction{
		ID:      txID,
		RefType: RefTopUp,
		RefID:   paymentRef,
		At:      at,
		Entries: []Entry{
			newEntry(txID, driverID, AccDriverWallet, amount, RefTopUp, paymentRef, "Nạp ví", at),
			newEntry(txID, "platform", AccGatewayClear, amount.Neg(), RefTopUp, paymentRef, "Nhận tiền nạp ví", at),
		},
	}
}

// Payout: nền tảng chi trả thu nhập về tài khoản ngân hàng tài xế.
func Payout(txID, driverID, batchRef string, amount money.VND, at time.Time) Transaction {
	return Transaction{
		ID:      txID,
		RefType: RefPayout,
		RefID:   batchRef,
		At:      at,
		Entries: []Entry{
			newEntry(txID, driverID, AccDriverWallet, amount.Neg(), RefPayout, batchRef, "Chi trả thu nhập", at),
			newEntry(txID, "platform", AccGatewayClear, amount, RefPayout, batchRef, "Chi hộ tài xế", at),
		},
	}
}

// CancelFee: khách huỷ trễ, phí đền bù chuyển cho tài xế.
func CancelFee(txID, riderID, driverID, tripID string, amount money.VND, at time.Time) Transaction {
	return Transaction{
		ID:      txID,
		RefType: RefCancelFee,
		RefID:   tripID,
		At:      at,
		Entries: []Entry{
			newEntry(txID, riderID, AccRiderWallet, amount.Neg(), RefCancelFee, tripID, "Phí huỷ chuyến", at),
			newEntry(txID, driverID, AccDriverWallet, amount, RefCancelFee, tripID, "Đền bù huỷ chuyến", at),
		},
	}
}

// WithholdTax tách phần thuế GTGT + TNCN khấu trừ tại nguồn trên thu nhập tài xế.
// ratePermille ví dụ 45 = 4,5% (3% GTGT + 1,5% TNCN) theo quy định hiện hành
// cho cá nhân kinh doanh vận tải — CẦN đối chiếu lại với kế toán thuế.
func WithholdTax(txID, driverID, tripID string, earn money.VND, ratePermille int64, at time.Time) Transaction {
	tax := earn.MulPermille(ratePermille)
	return Transaction{
		ID:      txID,
		RefType: RefTrip,
		RefID:   tripID,
		At:      at,
		Entries: []Entry{
			newEntry(txID, driverID, AccDriverWallet, tax.Neg(), RefTrip, tripID, "Khấu trừ thuế", at),
			newEntry(txID, "platform", AccTaxPayable, tax, RefTrip, tripID, "Thuế phải nộp hộ tài xế", at),
		},
	}
}
