package matching

import (
	"context"
	"sync"
	"time"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
)

// MemoryStore mô phỏng Redis: claims đóng vai trò khoá SET NX.
//
// Store PHẢI dùng cùng một đồng hồ với Engine. Engine đặt Offer.ExpiresAt theo
// clock của nó, còn Store lại lọc theo đồng hồ của mình — nếu hai đồng hồ khác
// nhau thì lời mời vừa tạo đã bị coi là hết hạn, và triệu chứng phụ thuộc vào
// giờ chạy test nên rất khó lần ra.
type MemoryStore struct {
	mu     sync.Mutex
	offers map[string]Offer
	claims map[string]claim
	clk    clock.Clock
}

type claim struct {
	driverID  string
	expiresAt time.Time
}

func NewMemoryStore(clk clock.Clock) *MemoryStore {
	return &MemoryStore{offers: map[string]Offer{}, claims: map[string]claim{}, clk: clk}
}

func (m *MemoryStore) SaveOffers(_ context.Context, offers []Offer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range offers {
		m.offers[o.ID] = o
	}
	return nil
}

func (m *MemoryStore) GetOffer(_ context.Context, offerID string) (Offer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.offers[offerID]
	if !ok {
		return Offer{}, errs.NotFound("offer_not_found", "Không tìm thấy lời mời.")
	}
	return o, nil
}

// ClaimTrip tương đương `SET trip:{id}:claim {driver} NX EX 30` trong Redis.
func (m *MemoryStore) ClaimTrip(_ context.Context, tripID, driverID string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clk.Now()
	if c, ok := m.claims[tripID]; ok && now.Before(c.expiresAt) {
		return c.driverID == driverID, nil
	}
	m.claims[tripID] = claim{driverID: driverID, expiresAt: now.Add(ttl)}
	return true, nil
}

func (m *MemoryStore) UpdateStatus(_ context.Context, offerID string, s OfferStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.offers[offerID]
	if !ok {
		return errs.NotFound("offer_not_found", "Không tìm thấy lời mời.")
	}
	o.Status = s
	m.offers[offerID] = o
	return nil
}

func (m *MemoryStore) PendingForDriver(_ context.Context, driverID string) ([]Offer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clk.Now()
	var out []Offer
	for _, o := range m.offers {
		if o.DriverID == driverID && o.Status == OfferPending && now.Before(o.ExpiresAt) {
			out = append(out, o)
		}
	}
	return out, nil
}

func (m *MemoryStore) ExpireOffers(_ context.Context, tripID, except string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, o := range m.offers {
		if o.TripID == tripID && o.ID != except && o.Status == OfferPending {
			o.Status = OfferLost
			m.offers[k] = o
		}
	}
	return nil
}
