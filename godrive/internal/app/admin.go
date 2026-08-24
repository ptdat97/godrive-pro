package app

import (
	"context"
	"time"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/pkg/geo"
)

// Adapter nối các service hiện có vào Port của module admin. Đặt ở đây (tầng
// lắp ráp) để module admin không phải biết chi tiết chữ ký của module khác, và
// module khác không phải sửa gì vì admin.

// adminLocation gộp Index + FraudDetector thành một Port duy nhất.
type adminLocation struct{ svc *location.Service }

func (a adminLocation) Get(ctx context.Context, driverID string) (location.Snapshot, bool, error) {
	return a.svc.Get(ctx, driverID)
}

func (a adminLocation) Nearby(ctx context.Context, c geo.Point, radiusM float64, f location.Filter) ([]location.Snapshot, error) {
	return a.svc.Nearby(ctx, c, radiusM, f)
}

func (a adminLocation) FraudCount(driverID string, within time.Duration) int {
	return a.svc.Fraud().Count(driverID, within)
}

// Kiểm tra tại thời điểm biên dịch: các service thoả mãn Port của admin.
var (
	_ admin.LocationPort = adminLocation{}
)
