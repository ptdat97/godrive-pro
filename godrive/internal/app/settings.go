package app

import (
	"context"
	"time"

	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/matching"
	"github.com/example/godrive/internal/platform/eventbus"
	"github.com/example/godrive/internal/pricing"
	"github.com/example/godrive/internal/settings"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/money"
)

// wireSettings nối nguồn cấu hình động vào từng module.
//
// Việc DỊCH một nhóm cấu hình thành kiểu dữ liệu của module nằm ở đây, tại tầng
// lắp ráp — không module nghiệp vụ nào import internal/settings. Nhờ vậy
// `settings` không biết gì về nghiệp vụ, và nghiệp vụ không biết cấu hình đến
// từ đâu (CSDL, tệp, hay hằng số).
func (a *App) wireSettings() {
	s := a.Settings
	if s == nil {
		return
	}

	a.Pricing.UseConfig(func(ctx context.Context) pricing.RuntimeConfig {
		g := s.Current(ctx).Pricing
		tariffs := make(map[driver.VehicleType]pricing.Tariff, len(g.Tariffs))
		for vt, t := range g.Tariffs {
			tariffs[driver.VehicleType(vt)] = pricing.Tariff{
				City: "HCM", VehicleType: driver.VehicleType(vt),
				OpeningFare: money.VND(t.OpeningFare), OpeningMeter: float64(t.OpeningMeter),
				PerKm: money.VND(t.PerKm), PerMinute: money.VND(t.PerMinute),
				MinFare:                money.VND(t.MinFare),
				NightSurchargePermille: t.NightSurchargePermille,
				PlatformFeePermille:    t.PlatformFeePermille,
			}
		}
		return pricing.RuntimeConfig{
			Tariffs: tariffs, QuoteTTL: g.QuoteTTL(),
			NightStartHour: g.NightStartHour, NightEndHour: g.NightEndHour,
		}
	})

	a.Surge.UseConfig(func(ctx context.Context) pricing.SurgeRuntime {
		g := s.Current(ctx).Surge
		steps := make([]pricing.SurgeStep, 0, len(g.Steps))
		for _, st := range g.Steps {
			steps = append(steps, pricing.SurgeStep{RatioX10: st.RatioX10, Permille: st.Permille})
		}
		return pricing.SurgeRuntime{
			Enabled: g.Enabled, MaxPermille: g.MaxPermille,
			Window:        time.Duration(g.WindowSeconds) * time.Second,
			SupplyRadiusM: g.SupplyRadiusM, Steps: steps,
		}
	})

	a.Matcher.UseConfig(func(ctx context.Context) matching.Config {
		g := s.Current(ctx).Matching
		w := s.Current(ctx).Wallet
		return matching.Config{
			InitialRadiusM: g.InitialRadiusM, RadiusStepM: g.RadiusStepM,
			MaxRadiusM: g.MaxRadiusM, MaxRounds: g.MaxRounds, BatchSize: g.BatchSize,
			OfferTTL:       time.Duration(g.OfferTTLSeconds) * time.Second,
			EmptyRoundWait: time.Duration(g.EmptyRoundWaitSecs) * time.Second,
			MinBatteryPc:   g.MinBatteryPc,
			DebtLimit:      money.VND(w.DebtLimitVND),
			WeightETA:      g.WeightETA, WeightRating: g.WeightRating,
			WeightAcceptance: g.WeightAcceptance, WeightIdle: g.WeightIdle,
			WeightHeading: g.WeightHeading,
		}
	})

	a.Drivers.UseDebtLimit(func(ctx context.Context) money.VND {
		return money.VND(s.Current(ctx).Wallet.DebtLimitVND)
	})

	a.Trips.UseCancelPolicy(func(ctx context.Context) trip.CancelPolicy {
		g := s.Current(ctx).Wallet
		return trip.CancelPolicy{
			FreeWindow: time.Duration(g.FreeCancelWindowSecs) * time.Second,
			FeeVND:     g.CancelFeeVND,
		}
	})

	a.Wallet.UseTaxProvider(func(ctx context.Context) int64 {
		return s.Current(ctx).Wallet.TaxPermille
	})

	a.Location.UseThresholds(func(ctx context.Context) location.Thresholds {
		g := s.Current(ctx).Location
		return location.Thresholds{
			StaleAfter:           time.Duration(g.StaleAfterSeconds) * time.Second,
			MaxPlausibleSpeedMps: g.MaxPlausibleSpeedMps,
			MaxAccuracyM:         g.MaxAccuracyM,
		}
	})

	if a.Settlement != nil {
		a.Settlement.UseMinPayout(func(ctx context.Context) money.VND {
			return money.VND(s.Current(ctx).Wallet.MinPayoutVND)
		})
	}
}

// settingsReloader nạp lại cấu hình khi một pod khác vừa đổi.
//
// Ảnh chụp tự hết hạn sau CacheTTL nên sự kiện chỉ để rút ngắn độ trễ; mất sự
// kiện thì thay đổi vẫn lan tới, chỉ chậm hơn vài giây.
func (a *App) subscribeSettings() {
	if a.Settings == nil {
		return
	}
	a.Bus.Subscribe(settings.TopicChanged, "settings-reload",
		func(ctx context.Context, _ eventbus.Event) error {
			return a.Settings.Reload(ctx)
		})
}
