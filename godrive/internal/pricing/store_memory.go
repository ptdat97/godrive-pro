package pricing

import (
	"context"
	"sync"

	"github.com/example/godrive/pkg/errs"
)

type MemoryQuoteStore struct {
	mu   sync.RWMutex
	data map[string]Quote
}

func NewMemoryQuoteStore() *MemoryQuoteStore {
	return &MemoryQuoteStore{data: map[string]Quote{}}
}

func (m *MemoryQuoteStore) Save(_ context.Context, q Quote) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[q.ID] = q
	return nil
}

func (m *MemoryQuoteStore) Get(_ context.Context, quoteID string) (Quote, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q, ok := m.data[quoteID]
	if !ok {
		return Quote{}, errs.NotFound("quote_not_found", "Không tìm thấy báo giá.")
	}
	return q, nil
}
