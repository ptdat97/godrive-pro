package driver

import (
	"context"
	"sort"
	"sync"

	"github.com/example/godrive/pkg/clock"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/money"
)

// MemoryRepo là bản cài đặt in-memory cho dev/test.
// Bản Postgres nằm ở store_postgres.go.
type MemoryRepo struct {
	mu   sync.RWMutex
	data map[string]*Driver
	byAc map[string]string
	clk  clock.Clock
}

func NewMemoryRepo(clk clock.Clock) *MemoryRepo {
	return &MemoryRepo{data: map[string]*Driver{}, byAc: map[string]string{}, clk: clk}
}

func clone(d *Driver) *Driver { c := *d; return &c }

func (r *MemoryRepo) Create(_ context.Context, d *Driver) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byAc[d.AccountID]; ok {
		return errs.Conflict("driver_exists", "Tài khoản này đã đăng ký tài xế.")
	}
	r.data[d.ID] = clone(d)
	r.byAc[d.AccountID] = d.ID
	return nil
}

func (r *MemoryRepo) Get(_ context.Context, id string) (*Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.data[id]
	if !ok {
		return nil, errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	return clone(d), nil
}

func (r *MemoryRepo) GetByAccount(ctx context.Context, accountID string) (*Driver, error) {
	r.mu.RLock()
	did, ok := r.byAc[accountID]
	r.mu.RUnlock()
	if !ok {
		return nil, errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	return r.Get(ctx, did)
}

func (r *MemoryRepo) UpdateStatus(_ context.Context, id string, from, to Status, version int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.data[id]
	if !ok {
		return errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	if d.Version != version || d.Status != from {
		return errs.Conflict("driver_state_changed", "Trạng thái tài xế vừa thay đổi, vui lòng thử lại.")
	}
	d.Status = to
	d.Version++
	// Giữ hành vi giống PostgresRepo: idle_since đặt khi vào IDLE, xoá khi ra.
	if to == StatusIdle {
		now := r.clk.Now()
		d.IdleSince = &now
	} else {
		d.IdleSince = nil
	}
	return nil
}

func (r *MemoryRepo) ApplyStats(_ context.Context, driverID string, d StatsDelta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.data[driverID]
	if !ok {
		return errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	cur.Stats.OffersReceived += d.OffersReceived
	cur.Stats.OffersAccepted += d.OffersAccepted
	cur.Stats.TripsCompleted += d.TripsCompleted
	cur.Stats.TripsCancelled += d.TripsCancelled
	cur.Stats.RatingSum += d.RatingSum
	cur.Stats.RatingCount += d.RatingCount
	return nil
}

func (r *MemoryRepo) UpdateWalletBalance(_ context.Context, driverID string, bal money.VND) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.data[driverID]
	if !ok {
		return errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	d.WalletBalance = bal
	return nil
}

func (r *MemoryRepo) Update(_ context.Context, in *Driver) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.data[in.ID]
	if !ok {
		return errs.NotFound("driver_not_found", "Không tìm thấy tài xế.")
	}
	v := cur.Version
	c := clone(in)
	c.Version = v + 1
	r.data[in.ID] = c
	return nil
}

func (r *MemoryRepo) ListByStatus(_ context.Context, s Status, limit int) ([]*Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []*Driver{}
	for _, d := range r.data {
		if d.Status == s {
			out = append(out, clone(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
