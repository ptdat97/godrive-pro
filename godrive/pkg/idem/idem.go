// Package idem xử lý idempotency key. App mobile ở VN chạy mạng 3G/4G chập chờn
// nên retry rất nhiều — mọi API tạo/huỷ chuyến, thanh toán đều phải idempotent.
package idem

import (
	"context"
	"sync"
	"time"
)

type Record struct {
	Key       string
	Response  []byte
	CreatedAt time.Time
}

type Store interface {
	// Reserve trả về (record cũ, true) nếu key đã tồn tại.
	Reserve(ctx context.Context, key string, ttl time.Duration) (*Record, bool, error)
	Complete(ctx context.Context, key string, response []byte) error
	// Release nhả khoá đã giữ khi thao tác THẤT BẠI.
	//
	// Không có bước này thì một lần thất bại sẽ khoá chết key đó tới hết TTL:
	// client sửa lỗi rồi gửi lại vẫn nhận request_in_flight, đúng vào tình huống
	// mà idempotency lẽ ra phải giúp. Release chỉ có tác dụng khi key CHƯA
	// Complete — đã hoàn tất rồi thì kết quả phải giữ nguyên.
	Release(ctx context.Context, key string) error
}

// SweepEvery giới hạn tần suất quét dọn key quá hạn.
const SweepEvery = time.Minute

type memoryStore struct {
	mu        sync.Mutex
	data      map[string]*Record
	ttl       map[string]time.Time
	lastSweep time.Time
}

func NewMemoryStore() Store {
	return &memoryStore{data: map[string]*Record{}, ttl: map[string]time.Time{}}
}

// sweep xoá key quá hạn. Gọi khi ĐANG giữ m.mu.
//
// Reserve chỉ dọn đúng key nó chạm tới, nên key không bao giờ được hỏi lại sẽ
// nằm lại vĩnh viễn — với TTL 24 giờ và mỗi chuyến một key, đó là một rò rỉ
// tăng đều theo lưu lượng.
func (m *memoryStore) sweep(now time.Time) {
	if now.Sub(m.lastSweep) < SweepEvery {
		return
	}
	m.lastSweep = now
	for k, exp := range m.ttl {
		if now.After(exp) {
			delete(m.data, k)
			delete(m.ttl, k)
		}
	}
}

// Len là số key đang giữ. Dùng cho test và metric.
func (m *memoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.data)
}

func (m *memoryStore) Reserve(_ context.Context, key string, ttl time.Duration) (*Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.lastSweep.IsZero() {
		m.lastSweep = now
	}
	m.sweep(now)
	if exp, ok := m.ttl[key]; ok && now.After(exp) {
		delete(m.data, key)
		delete(m.ttl, key)
	}
	if r, ok := m.data[key]; ok {
		return copyRecord(r), true, nil
	}
	r := &Record{Key: key, CreatedAt: now}
	m.data[key] = r
	m.ttl[key] = now.Add(ttl)
	return copyRecord(r), false, nil
}

// copyRecord trả bản sao để con trỏ nội bộ không thoát ra ngoài khoá.
//
// Trả thẳng *Record là một cuộc đua dữ liệu: người gọi đọc rec.Response ngoài
// khoá trong khi một request khác cùng key đang gọi Complete để ghi vào đúng
// trường đó. Hai thiết bị cùng retry một Idempotency-Key là chuyện thường ngày
// trên mạng 4G chập chờn, nên đây không phải tình huống hiếm.
func copyRecord(r *Record) *Record {
	c := *r
	if r.Response != nil {
		c.Response = append([]byte(nil), r.Response...)
	}
	return &c
}

func (m *memoryStore) Complete(_ context.Context, key string, response []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.data[key]; ok {
		r.Response = response
	}
	return nil
}

func (m *memoryStore) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Đã Complete thì giữ nguyên: kết quả của một thao tác đã thành công không
	// được xoá đi chỉ vì có ai đó gọi Release nhầm.
	if r, ok := m.data[key]; ok && len(r.Response) > 0 {
		return nil
	}
	delete(m.data, key)
	delete(m.ttl, key)
	return nil
}
