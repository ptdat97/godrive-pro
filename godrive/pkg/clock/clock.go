// Package clock tách phụ thuộc thời gian để test tất định.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

func Real() Clock { return realClock{} }

// Mock dùng trong unit test.
type Mock struct {
	mu sync.Mutex
	t  time.Time
}

func NewMock(t time.Time) *Mock { return &Mock{t: t.UTC()} }

func (m *Mock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

func (m *Mock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = m.t.Add(d)
}
