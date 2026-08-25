package wallet

import (
	"context"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// MinPayout là ngưỡng chi trả tối thiểu.
//
// Dưới ngưỡng này phí chuyển khoản ăn hết phần chi, nên số dư được giữ lại cho
// đợt sau thay vì chuyển. Đây là quyết định VẬN HÀNH — con số phải do bộ phận
// tài chính chốt, ở đây chỉ là giả định khởi điểm.
const MinPayout = money.VND(50000)

type BatchStatus string

const (
	BatchOpen       BatchStatus = "OPEN"
	BatchCalculated BatchStatus = "CALCULATED"
	BatchPaid       BatchStatus = "PAID"
	BatchFailed     BatchStatus = "FAILED"
)

type ItemStatus string

const (
	ItemPending ItemStatus = "PENDING"
	ItemPaid    ItemStatus = "PAID"
	ItemSkipped ItemStatus = "SKIPPED"
	ItemFailed  ItemStatus = "FAILED"
)

// Batch là một đợt đối soát và chi trả cho kỳ [PeriodStart, PeriodEnd).
type Batch struct {
	ID          string      `json:"id"`
	PeriodStart time.Time   `json:"period_start"`
	PeriodEnd   time.Time   `json:"period_end"`
	Status      BatchStatus `json:"status"`
	DriverCount int         `json:"driver_count"`
	Total       money.VND   `json:"total"`
	CreatedAt   time.Time   `json:"created_at"`
	ClosedAt    *time.Time  `json:"closed_at,omitempty"`
}

// Item là một dòng chi trả cho một tài xế trong một đợt.
type Item struct {
	ID        string     `json:"id"`
	BatchID   string     `json:"batch_id"`
	DriverID  string     `json:"driver_id"`
	Amount    money.VND  `json:"amount"`
	Status    ItemStatus `json:"status"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	PaidAt    *time.Time `json:"paid_at,omitempty"`
}

// SettlementStore lưu đợt đối soát.
type SettlementStore interface {
	// CreateBatch tạo đợt cho một kỳ. Trả Conflict nếu kỳ đó đã có đợt —
	// đây là chốt chặn chống chạy job hai lần cho cùng một kỳ.
	CreateBatch(ctx context.Context, b *Batch) error
	GetBatch(ctx context.Context, id string) (*Batch, error)
	GetBatchByPeriod(ctx context.Context, from, to time.Time) (*Batch, error)
	// AddItem thêm dòng chi trả. Trả Conflict nếu tài xế đã có dòng trong đợt.
	AddItem(ctx context.Context, it *Item) error
	ListItems(ctx context.Context, batchID string, st ItemStatus) ([]*Item, error)
	// MarkItemPaid chuyển PENDING -> PAID nguyên tử. Trả Conflict nếu đã trả.
	MarkItemPaid(ctx context.Context, itemID string, at time.Time) error
	MarkItemFailed(ctx context.Context, itemID, reason string) error
	CloseBatch(ctx context.Context, id string, st BatchStatus, count int, total money.VND, at time.Time) error
	// DriversWithBalance liệt kê tài xế có số dư ví dương tại thời điểm chốt.
	DriversWithBalance(ctx context.Context, min money.VND) (map[string]money.VND, error)
}

// Settlement chạy đối soát và chi trả.
//
// Bất biến quan trọng nhất: CHẠY HAI LẦN KHÔNG TRẢ TIỀN HAI LẦN. Nó được bảo
// đảm ở ba tầng độc lập, tầng nào hỏng thì tầng sau vẫn giữ:
//
//  1. UNIQUE (period_start, period_end)  — một kỳ chỉ một đợt
//  2. UNIQUE (batch_id, driver_id)       — một tài xế một dòng trong đợt
//  3. TxID tất định của bút toán Payout  — sổ cái tự khử trùng
type Settlement struct {
	store  SettlementStore
	ledger Ledger
	// MinPayout là ngưỡng chi trả tối thiểu cho đợt này.
	MinPayout money.VND
}

func NewSettlement(store SettlementStore, l Ledger) *Settlement {
	return &Settlement{store: store, ledger: l, MinPayout: MinPayout}
}

// Calculate chốt danh sách phải trả cho kỳ [from, to).
//
// Tách khỏi Pay có chủ đích: kế toán phải xem được danh sách TRƯỚC khi tiền rời
// khỏi tài khoản. Gộp hai bước làm một là bỏ mất chốt kiểm soát cuối cùng của
// con người trên đường tiền ra.
func (s *Settlement) Calculate(ctx context.Context, from, to time.Time, now time.Time, newID func(string) string) (*Batch, error) {
	if !to.After(from) {
		return nil, errs.Invalid("period_invalid", "Kỳ đối soát không hợp lệ.")
	}
	if to.After(now) {
		return nil, errs.Invalid("period_not_ended", "Không thể chốt một kỳ chưa kết thúc.")
	}

	// Kỳ đã có đợt thì trả lại đợt cũ thay vì tạo mới — job chạy lại sau khi
	// lỗi mạng không được sinh ra đợt thứ hai.
	if existing, err := s.store.GetBatchByPeriod(ctx, from, to); err == nil && existing != nil {
		return existing, nil
	} else if err != nil && errs.KindOf(err) != errs.KindNotFound {
		return nil, err
	}

	b := &Batch{
		ID: newID("stl"), PeriodStart: from, PeriodEnd: to,
		Status: BatchOpen, CreatedAt: now,
	}
	if err := s.store.CreateBatch(ctx, b); err != nil {
		// Hai tiến trình cùng chạy job: bên thua đọc lại đợt của bên thắng.
		if errs.KindOf(err) == errs.KindConflict {
			return s.store.GetBatchByPeriod(ctx, from, to)
		}
		return nil, err
	}

	balances, err := s.store.DriversWithBalance(ctx, 1)
	if err != nil {
		return nil, err
	}

	var total money.VND
	count := 0
	for driverID, bal := range balances {
		it := &Item{
			ID: newID("sti"), BatchID: b.ID, DriverID: driverID,
			Amount: bal, Status: ItemPending, CreatedAt: now,
		}
		// Dưới ngưỡng thì GHI LẠI với trạng thái SKIPPED chứ không bỏ qua im
		// lặng: tài xế phải tra được vì sao kỳ này mình không nhận tiền.
		if bal < s.MinPayout {
			it.Status = ItemSkipped
			it.Reason = "dưới ngưỡng chi trả tối thiểu"
		} else {
			total += bal
			count++
		}
		if err := s.store.AddItem(ctx, it); err != nil {
			if errs.KindOf(err) == errs.KindConflict {
				continue // đã có dòng cho tài xế này trong đợt
			}
			return nil, err
		}
	}

	if err := s.store.CloseBatch(ctx, b.ID, BatchCalculated, count, total, now); err != nil {
		return nil, err
	}
	b.Status, b.DriverCount, b.Total = BatchCalculated, count, total
	return b, nil
}

// PayResult tổng kết một lần chạy chi trả.
type PayResult struct {
	BatchID string    `json:"batch_id"`
	Paid    int       `json:"paid"`
	Failed  int       `json:"failed"`
	Total   money.VND `json:"total"`
}

// Pay ghi bút toán chi trả cho các dòng PENDING trong đợt.
//
// Gọi lại bao nhiêu lần cũng chỉ chi một lần cho mỗi tài xế: MarkItemPaid là
// phép chuyển trạng thái nguyên tử, và TxID của bút toán suy ra tất định từ
// (batchID, driverID) nên sổ cái cũng tự khử trùng.
func (s *Settlement) Pay(ctx context.Context, batchID string, now time.Time) (*PayResult, error) {
	b, err := s.store.GetBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if b.Status != BatchCalculated && b.Status != BatchPaid {
		return nil, errs.Conflict("batch_not_calculated", "Đợt chưa được chốt danh sách.")
	}

	items, err := s.store.ListItems(ctx, batchID, ItemPending)
	if err != nil {
		return nil, err
	}

	res := &PayResult{BatchID: batchID}
	for _, it := range items {
		// Giành quyền chi TRƯỚC khi ghi sổ. Ngược lại thì hai tiến trình cùng
		// chạy có thể cùng ghi sổ rồi mới phát hiện ra.
		if err := s.store.MarkItemPaid(ctx, it.ID, now); err != nil {
			if errs.KindOf(err) == errs.KindConflict {
				continue // người khác vừa chi dòng này
			}
			return nil, err
		}
		txID := "tx_payout_" + batchID + "_" + it.DriverID
		tx := Payout(txID, it.DriverID, batchID, it.Amount, now)
		tx.BatchID = batchID
		if err := s.ledger.Post(ctx, tx); err != nil {
			// Ghi sổ hỏng: trả dòng về FAILED để đợt sau xử lý lại, thay vì để
			// nó mang trạng thái PAID mà thực tế chưa có bút toán nào.
			_ = s.store.MarkItemFailed(ctx, it.ID, err.Error())
			res.Failed++
			continue
		}
		res.Paid++
		res.Total += it.Amount
	}

	st := BatchPaid
	if res.Failed > 0 {
		st = BatchFailed
	}
	if err := s.store.CloseBatch(ctx, batchID, st, b.DriverCount, b.Total, now); err != nil {
		return nil, err
	}
	return res, nil
}
