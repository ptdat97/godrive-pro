// Package wallet là sổ cái kép (double-entry) cho ví tài xế và công nợ tiền mặt.
//
// Đây là module dễ sai nhất trong một ứng dụng gọi xe ở Việt Nam: phần lớn
// chuyến vẫn thanh toán tiền mặt, tài xế cầm tiền của khách rồi nợ lại nền tảng
// phần chiết khấu. Nguyên tắc bất di bất dịch: TỔNG các bút toán của một giao
// dịch luôn bằng 0, và không bao giờ dùng float.
package wallet

import (
	"context"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
	"github.com/example/godrive/pkg/money"
)

type AccountType string

const (
	AccDriverWallet AccountType = "DRIVER_WALLET"    // số dư ví tài xế (âm = đang nợ)
	AccDriverCash   AccountType = "DRIVER_CASH"      // tiền mặt tài xế đang cầm
	AccRiderWallet  AccountType = "RIDER_WALLET"     // ví khách hàng
	AccPlatformRev  AccountType = "PLATFORM_REVENUE" // doanh thu nền tảng
	AccPromoExpense AccountType = "PROMO_EXPENSE"    // chi phí khuyến mãi
	AccTaxPayable   AccountType = "TAX_PAYABLE"      // thuế khấu trừ hộ tài xế
	AccGatewayClear AccountType = "GATEWAY_CLEARING" // tiền đang treo ở cổng thanh toán
)

type RefType string

const (
	RefTrip      RefType = "TRIP"
	RefTopUp     RefType = "TOPUP"
	RefPayout    RefType = "PAYOUT"
	RefAdjust    RefType = "ADJUSTMENT"
	RefCancelFee RefType = "CANCEL_FEE"
)

// Entry là một bút toán. Bảng ledger_entries chỉ INSERT, không UPDATE/DELETE.
type Entry struct {
	ID          string      `json:"id"`
	TxID        string      `json:"tx_id"`
	AccountID   string      `json:"account_id"` // driverID / riderID / "platform"
	AccountType AccountType `json:"account_type"`
	Amount      money.VND   `json:"amount"` // dương = ghi nợ vào tài khoản, âm = ghi có
	RefType     RefType     `json:"ref_type"`
	RefID       string      `json:"ref_id"`
	Memo        string      `json:"memo"`
	CreatedAt   time.Time   `json:"created_at"`
}

// Transaction là tập bút toán cân bằng.
type Transaction struct {
	ID      string
	Entries []Entry
	RefType RefType
	RefID   string
	At      time.Time
	// BatchID nối bút toán với đợt đối soát đã sinh ra nó. Rỗng với giao dịch
	// thường. Nhờ nó mà kế toán truy được "đợt này đã chi những gì".
	BatchID string
}

// Validate bảo đảm giao dịch ghi được: có mã, đủ hai vế, mọi bút toán có chủ,
// và tổng bằng 0.
//
// Đây là CHỐT CHẶN DUY NHẤT ở tầng ứng dụng — mọi Ledger.Post đều đi qua đây,
// nên kiểm tra đặt ở hàm này bao phủ cả sáu bút toán hiện có lẫn mọi bút toán
// viết sau này. Đừng nhân bản các kiểm tra này vào từng phương thức của Service.
func (t Transaction) Validate() error {
	// TxID quyết định idempotency. Giao dịch không có mã thì ghi bao nhiêu lần
	// cũng thành bấy nhiêu bản sao.
	if t.ID == "" {
		return errs.Invalid("ledger_tx_id_missing", "Giao dịch thiếu mã.")
	}
	if len(t.Entries) < 2 {
		return errs.Invalid("ledger_incomplete", "Giao dịch phải có ít nhất 2 bút toán.")
	}
	var sum money.VND
	for _, e := range t.Entries {
		// Bút toán không có chủ thì không bao giờ đối soát được: nó nằm trong
		// tổng doanh thu nhưng không thuộc về ví của ai.
		if e.AccountID == "" {
			return errs.Invalid("ledger_account_missing", "Bút toán thiếu tài khoản.")
		}
		if e.AccountType == "" {
			return errs.Invalid("ledger_account_type_missing", "Bút toán thiếu loại tài khoản.")
		}
		sum += e.Amount
	}
	if sum != 0 {
		return errs.Invalid("ledger_unbalanced", "Giao dịch không cân bằng.")
	}
	return nil
}

type Ledger interface {
	// Post ghi giao dịch. Phải idempotent theo TxID.
	Post(ctx context.Context, tx Transaction) error
	Balance(ctx context.Context, accountID string, at AccountType) (money.VND, error)
	Statement(ctx context.Context, accountID string, from, to time.Time) ([]Entry, error)
	Exists(ctx context.Context, txID string) (bool, error)
}

func newEntry(txID, accountID string, at AccountType, amt money.VND, rt RefType, refID, memo string, ts time.Time) Entry {
	return Entry{
		ID: id.New("led"), TxID: txID, AccountID: accountID, AccountType: at,
		Amount: amt, RefType: rt, RefID: refID, Memo: memo, CreatedAt: ts,
	}
}
