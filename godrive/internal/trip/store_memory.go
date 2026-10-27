package trip

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/page"
)

type MemoryRepo struct {
	mu     sync.RWMutex
	trips  map[string]*Trip
	events map[string][]Event
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{trips: map[string]*Trip{}, events: map[string][]Event{}}
}

func cloneTrip(t *Trip) *Trip { c := *t; return &c }

func (r *MemoryRepo) Create(_ context.Context, t *Trip) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.trips[t.ID]; ok {
		return errs.Conflict("trip_exists", "Chuyến đã tồn tại.")
	}
	r.trips[t.ID] = cloneTrip(t)
	return nil
}

func (r *MemoryRepo) Get(_ context.Context, id string) (*Trip, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.trips[id]
	if !ok {
		return nil, errs.NotFound("trip_not_found", "Không tìm thấy chuyến đi.")
	}
	return cloneTrip(t), nil
}

// Save mô phỏng transaction: cập nhật trip và append event nguyên tử,
// kèm kiểm tra optimistic lock theo Version.
// Save trả lại msgs cho người gọi tự phát: bản bộ nhớ không có outbox relay.
func (r *MemoryRepo) Save(_ context.Context, t *Trip, e Event, msgs ...Message) ([]Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.trips[t.ID]
	if !ok {
		return nil, errs.NotFound("trip_not_found", "Không tìm thấy chuyến đi.")
	}
	if cur.Version != t.Version {
		return nil, errs.Conflict("trip_version_conflict", "Chuyến vừa được cập nhật, vui lòng thử lại.")
	}
	c := cloneTrip(t)
	c.Version = cur.Version + 1
	r.trips[t.ID] = c
	r.events[t.ID] = append(r.events[t.ID], e)
	// Trả version mới về cho caller, giống PostgresRepo.Save. Nếu hai repo lệch
	// nhau ở điểm này thì code chạy đúng in-memory sẽ vỡ optimistic lock khi
	// chuyển sang Postgres — đúng loại lỗi chỉ lộ ra ở production.
	t.Version = c.Version
	return msgs, nil
}

func (r *MemoryRepo) SetRating(_ context.Context, tripID string, rating int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.trips[tripID]
	if !ok {
		return errs.NotFound("trip_not_found", "Không tìm thấy chuyến đi.")
	}
	if t.Rating != nil {
		return errs.Conflict("trip_already_rated", "Chuyến này đã được đánh giá.")
	}
	t.Rating = &rating
	return nil
}

func (r *MemoryRepo) ListByRider(_ context.Context, riderID string, limit int) ([]*Trip, error) {
	return r.filter(func(t *Trip) bool { return t.RiderID == riderID }, limit), nil
}

func (r *MemoryRepo) ListByStatus(_ context.Context, s Status, limit int) ([]*Trip, error) {
	return r.filter(func(t *Trip) bool { return t.Status == s }, limit), nil
}

func (r *MemoryRepo) ActiveByDriver(_ context.Context, driverID string) (*Trip, error) {
	out := r.filter(func(t *Trip) bool {
		return t.DriverID != nil && *t.DriverID == driverID && !t.Status.IsTerminal()
	}, 1)
	if len(out) == 0 {
		return nil, errs.NotFound("trip_not_found", "Không có chuyến đang hoạt động.")
	}
	return out[0], nil
}

func (r *MemoryRepo) ActiveByRider(_ context.Context, riderID string) (*Trip, error) {
	out := r.filter(func(t *Trip) bool {
		return t.RiderID == riderID && !t.Status.IsTerminal()
	}, 1)
	if len(out) == 0 {
		return nil, errs.NotFound("trip_not_found", "Không có chuyến đang hoạt động.")
	}
	return out[0], nil
}

func (r *MemoryRepo) Events(_ context.Context, tripID string) ([]Event, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Event(nil), r.events[tripID]...), nil
}

func (r *MemoryRepo) filter(pred func(*Trip) bool, limit int) []*Trip {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []*Trip{}
	for _, t := range r.trips {
		if pred(t) {
			out = append(out, cloneTrip(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestedAt.After(out[j].RequestedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (r *MemoryRepo) List(_ context.Context, statuses []Status, after page.Cursor, limit int) ([]*Trip, error) {
	want := map[Status]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	r.mu.RLock()
	out := []*Trip{}
	for _, t := range r.trips {
		if (len(want) == 0 || want[t.Status]) && after.Before(t.RequestedAt, t.ID) {
			out = append(out, cloneTrip(t))
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RequestedAt.Equal(out[j].RequestedAt) {
			return out[i].RequestedAt.After(out[j].RequestedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *MemoryRepo) Stats(_ context.Context, since time.Time) (Stats, error) {
	live := map[Status]bool{}
	for _, s := range LiveStatuses {
		live[s] = true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := Stats{Live: map[Status]int{}, Since: map[Status]int{}}
	for _, t := range r.trips {
		if live[t.Status] {
			st.Live[t.Status]++
		}
		if t.RequestedAt.Before(since) {
			continue
		}
		st.Since[t.Status]++
		if t.Status == StatusCompleted || t.Status == StatusPaid {
			st.Finished++
			st.Gross += t.Fare
			st.PlatformFee += t.PlatformFee
			st.DriverEarn += t.DriverEarn
			if t.PaymentMethod == PayCash {
				st.FinishedCash++
			}
		}
	}
	return st, nil
}
