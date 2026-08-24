package identity

import (
	"context"
	"sync"
	"time"

	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/id"
)

type MemoryRepo struct {
	mu         sync.RWMutex
	accounts   map[string]*Account
	byPhone    map[string]string
	challenges map[string]Challenge
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{
		accounts:   map[string]*Account{},
		byPhone:    map[string]string{},
		challenges: map[string]Challenge{},
	}
}

func (r *MemoryRepo) UpsertAccount(_ context.Context, phone string, role authn.Role, now time.Time) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := phone + "|" + string(role)
	if aid, ok := r.byPhone[key]; ok {
		a := *r.accounts[aid]
		return &a, nil
	}
	a := &Account{ID: id.New("acc"), Phone: phone, Role: role, CreatedAt: now}
	r.accounts[a.ID] = a
	r.byPhone[key] = a.ID
	c := *a
	return &c, nil
}

func (r *MemoryRepo) GetAccount(_ context.Context, aid string) (*Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[aid]
	if !ok {
		return nil, errs.NotFound("account_not_found", "Không tìm thấy tài khoản.")
	}
	c := *a
	return &c, nil
}

func (r *MemoryRepo) SaveChallenge(_ context.Context, c Challenge) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.challenges[c.ID] = c
	return nil
}

func (r *MemoryRepo) GetChallenge(_ context.Context, cid string) (Challenge, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.challenges[cid]
	if !ok {
		return Challenge{}, errs.NotFound("challenge_not_found", "Phiên xác thực không tồn tại.")
	}
	return c, nil
}

func (r *MemoryRepo) DeleteChallenge(_ context.Context, cid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.challenges, cid)
	return nil
}

func (r *MemoryRepo) DeleteExpiredChallenges(_ context.Context, now time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k, c := range r.challenges {
		if now.After(c.ExpiresAt) {
			delete(r.challenges, k)
			n++
		}
	}
	return n, nil
}
