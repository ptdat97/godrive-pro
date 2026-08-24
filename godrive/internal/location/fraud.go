package location

import (
	"sync"
	"time"

	"github.com/example/godrive/pkg/clock"
)

type FraudReason string

const (
	ReasonMockLocation FraudReason = "MOCK_LOCATION"
	ReasonTeleport     FraudReason = "TELEPORT"
	ReasonSpeedOutlier FraudReason = "SPEED_OUTLIER"
)

type Flag struct {
	DriverID string
	Reason   FraudReason
	At       time.Time
}

// FraudDetector gom cờ cảnh báo trong bộ nhớ. Production nên đẩy sang
// stream riêng (Kafka -> hệ thống risk) và tự động khoá khi vượt ngưỡng.
type FraudDetector struct {
	mu    sync.Mutex
	flags map[string][]Flag
	clk   clock.Clock
}

func NewFraudDetector(clk clock.Clock) *FraudDetector {
	return &FraudDetector{flags: map[string][]Flag{}, clk: clk}
}

func (f *FraudDetector) Flag(driverID string, r FraudReason) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flags[driverID] = append(f.flags[driverID], Flag{DriverID: driverID, Reason: r, At: f.clk.Now()})
}

func (f *FraudDetector) Count(driverID string, within time.Duration) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	cut := f.clk.Now().Add(-within)
	n := 0
	for _, fl := range f.flags[driverID] {
		if fl.At.After(cut) {
			n++
		}
	}
	return n
}
