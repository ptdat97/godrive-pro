package wallet

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/example/godrive/pkg/money"
)

type MemoryLedger struct {
	mu      sync.RWMutex
	entries []Entry
	txs     map[string]bool
}

func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{txs: map[string]bool{}}
}

func (m *MemoryLedger) Post(_ context.Context, tx Transaction) error {
	if err := tx.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.txs[tx.ID] {
		return nil // idempotent
	}
	m.txs[tx.ID] = true
	m.entries = append(m.entries, tx.Entries...)
	return nil
}

func (m *MemoryLedger) Balance(_ context.Context, accountID string, at AccountType) (money.VND, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var sum money.VND
	for _, e := range m.entries {
		if e.AccountID == accountID && e.AccountType == at {
			sum += e.Amount
		}
	}
	return sum, nil
}

func (m *MemoryLedger) Statement(_ context.Context, accountID string, from, to time.Time) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Entry
	for _, e := range m.entries {
		if e.AccountID == accountID && !e.CreatedAt.Before(from) && e.CreatedAt.Before(to) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryLedger) Exists(_ context.Context, txID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.txs[txID], nil
}
