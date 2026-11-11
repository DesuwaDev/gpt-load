package geminicli

import (
	"context"
	"time"
	_ "time/tzdata"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

type LocalQuotaUsage struct {
	Start, Reset time.Time
	Requests     int64
	Known        bool
}
type localQuotaUsageKey struct{}

func WithLocalQuotaUsage(ctx context.Context, windows []LocalQuotaUsage) context.Context {
	return context.WithValue(ctx, localQuotaUsageKey{}, windows)
}
func LocalQuotaUsageWindows(now time.Time) []LocalQuotaUsage {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.FixedZone("PST", -8*3600)
	}
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	minute := now.Truncate(time.Minute)
	return []LocalQuotaUsage{{Start: day, Reset: day.Add(24 * time.Hour)}, {Start: minute, Reset: minute.Add(time.Minute)}}
}
func localQuotaWindows(ctx context.Context, tier string) ([]providerobservation.QuotaWindow, bool) {
	usage, ok := ctx.Value(localQuotaUsageKey{}).([]LocalQuotaUsage)
	if !ok || len(usage) != 2 {
		return []providerobservation.QuotaWindow{}, false
	}
	daily, minute := 1500.0, 120.0
	switch tier {
	case "gcp_enterprise", "google_ai_ultra":
		daily = 2000
	case "google_one_free":
		daily = 1000
		minute = 60
	}
	windows := make([]providerobservation.QuotaWindow, 0, 2)
	complete := true
	for index, local := range usage {
		id, label, limit := "shared_daily", "RPD (local)", daily
		if index == 1 {
			id, label, limit = "shared_minute", "RPM (local)", minute
		}
		seconds, reset := int64(local.Reset.Sub(local.Start)/time.Second), local.Reset.UnixMilli()
		window := providerobservation.QuotaWindow{ID: id, Label: label, Scope: "account", Unit: "requests", Limit: &limit, ResetAtMS: &reset, WindowSeconds: &seconds, State: "unknown", IsPrimary: index == 0}
		if local.Known {
			used := float64(local.Requests)
			remaining := max(0, limit-used)
			utilization := used / limit
			window.Used = &used
			window.Remaining = &remaining
			window.Utilization = &utilization
			window.State = "available"
			if remaining == 0 {
				window.State = "exhausted"
			}
		} else {
			complete = false
		}
		windows = append(windows, window)
	}
	return windows, complete
}
