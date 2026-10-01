package container

import "gpt-load/internal/state"

type retentionSnapshotProvider struct {
	manager *state.Manager
}

func (provider retentionSnapshotProvider) RequestLogRetentionDays() int {
	snapshot := provider.manager.Current()
	if snapshot == nil {
		return state.DefaultRuntimeSettings().RequestLogRetentionDays
	}
	return snapshot.Settings.RequestLogRetentionDays
}

// accessKeyConcurrencyUsage 让管理面的实时用量读取网关实际执行准入的那份计数，
// 展示值与 429 判定始终同源。
type accessKeyConcurrencyUsage struct {
	manager *state.Manager
}

func (usage accessKeyConcurrencyUsage) InFlight(accessKeyID uint) int64 {
	if usage.manager == nil {
		return 0
	}
	return usage.manager.Concurrency().AccessKeyCurrent(accessKeyID)
}
