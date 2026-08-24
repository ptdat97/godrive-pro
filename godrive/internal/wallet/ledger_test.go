package wallet

import (
	"context"
	"testing"
	"time"

	"github.com/example/godrive/pkg/money"
)

func TestCashTripBalances(t *testing.T) {
	ctx := context.Background()
	l := NewMemoryLedger()
	now := time.Now().UTC()

	// Chuyến 50.000đ tiền mặt, chiết khấu 20% = 10.000đ.
	tx := SettleCashTrip("tx1", "drv1", "trp1", money.VND(50000), money.VND(10000), now)
	if err := tx.Validate(); err != nil {
		t.Fatalf("giao dịch phải cân bằng: %v", err)
	}
	if err := l.Post(ctx, tx); err != nil {
		t.Fatal(err)
	}

	wallet, _ := l.Balance(ctx, "drv1", AccDriverWallet)
	if wallet != -10000 {
		t.Fatalf("ví tài xế phải là -10.000 (nợ chiết khấu), được %s", wallet)
	}
	cash, _ := l.Balance(ctx, "drv1", AccDriverCash)
	if cash != 50000 {
		t.Fatalf("tiền mặt cầm tay phải là 50.000, được %s", cash)
	}
}

func TestPostIsIdempotent(t *testing.T) {
	ctx := context.Background()
	l := NewMemoryLedger()
	tx := SettleCashTrip("tx1", "drv1", "trp1", 50000, 10000, time.Now())
	_ = l.Post(ctx, tx)
	_ = l.Post(ctx, tx)
	b, _ := l.Balance(ctx, "drv1", AccDriverWallet)
	if b != -10000 {
		t.Fatalf("post lặp không được nhân đôi, được %s", b)
	}
}

func TestTopUpClearsDebt(t *testing.T) {
	ctx := context.Background()
	l := NewMemoryLedger()
	now := time.Now().UTC()
	_ = l.Post(ctx, SettleCashTrip("tx1", "drv1", "trp1", 50000, 10000, now))
	_ = l.Post(ctx, TopUp("tx2", "drv1", "pay1", money.VND(10000), now))
	b, _ := l.Balance(ctx, "drv1", AccDriverWallet)
	if b != 0 {
		t.Fatalf("sau khi nạp, ví phải về 0, được %s", b)
	}
}

func TestUnbalancedRejected(t *testing.T) {
	bad := Transaction{ID: "x", Entries: []Entry{
		{AccountID: "a", Amount: 100},
		{AccountID: "b", Amount: -90},
	}}
	if err := bad.Validate(); err == nil {
		t.Fatal("giao dịch lệch phải bị từ chối")
	}
}
