package location

import (
	"context"
	"sort"
	"sync"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/geo"
)

// MemoryIndex lập chỉ mục theo ô lưới, tương đương cách dùng H3 cell làm khoá Redis.
type MemoryIndex struct {
	mu    sync.RWMutex
	snaps map[string]Snapshot        // driverID -> snapshot
	cells map[string]map[string]bool // cellKey -> set(driverID)
	at    map[string]string          // driverID -> cellKey hiện tại
	clk   clock.Clock
}

func NewMemoryIndex(clk clock.Clock) *MemoryIndex {
	return &MemoryIndex{
		snaps: map[string]Snapshot{},
		cells: map[string]map[string]bool{},
		at:    map[string]string{},
		clk:   clk,
	}
}

func (m *MemoryIndex) Upsert(_ context.Context, s Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	newKey := geo.CellOf(s.Point).Key()
	if old, ok := m.at[s.DriverID]; ok && old != newKey {
		delete(m.cells[old], s.DriverID)
		if len(m.cells[old]) == 0 {
			delete(m.cells, old)
		}
	}
	if m.cells[newKey] == nil {
		m.cells[newKey] = map[string]bool{}
	}
	m.cells[newKey][s.DriverID] = true
	m.at[s.DriverID] = newKey
	m.snaps[s.DriverID] = s
	return nil
}

func (m *MemoryIndex) Remove(_ context.Context, driverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key, ok := m.at[driverID]; ok {
		delete(m.cells[key], driverID)
		if len(m.cells[key]) == 0 {
			delete(m.cells, key)
		}
	}
	delete(m.at, driverID)
	delete(m.snaps, driverID)
	return nil
}

func (m *MemoryIndex) Get(_ context.Context, driverID string) (Snapshot, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.snaps[driverID]
	return s, ok, nil
}

func (m *MemoryIndex) Nearby(_ context.Context, center geo.Point, radiusM float64, f Filter) ([]Snapshot, error) {
	// Lọc độ tươi phải dùng đồng hồ tiêm được, nếu không không viết được test
	// tất định cho StaleAfter.
	now := m.clk.Now()
	k := geo.RingsForRadius(radiusM)
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Snapshot
	for _, c := range geo.Ring(geo.CellOf(center), k) {
		for did := range m.cells[c.Key()] {
			s := m.snaps[did]
			if geo.DistanceM(center, s.Point) > radiusM {
				continue
			}
			if !f.match(s, now) {
				continue
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return geo.DistanceM(center, out[i].Point) < geo.DistanceM(center, out[j].Point)
	})
	return out, nil
}
