package control

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/storage/models"
)

// degradedTarget is one monitored credential and upstream model.
type degradedTarget struct {
	credentialID  uint
	upstreamModel string
}

// DegradationRuntime publishes which monitored targets the detector currently
// judges degraded, so the request path can read the verdict without touching
// the database.
//
// 每个目标带一个 episode 号：只有"健康转降智"才发新号，降智持续期间号保持不变。
// 消费方据此对同一个会话只反应一次，而不是降智期间每个请求都反应一次。
type DegradationRuntime struct {
	mu       sync.RWMutex
	episodes map[degradedTarget]uint64
	nextID   uint64
}

func NewDegradationRuntime() *DegradationRuntime {
	return &DegradationRuntime{episodes: make(map[degradedTarget]uint64)}
}

// Episode reports the degraded episode currently running for one target. Zero
// means the detector does not judge that target degraded.
func (runtime *DegradationRuntime) Episode(credentialID uint, upstreamModel string) uint64 {
	if runtime == nil || credentialID == 0 {
		return 0
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.episodes[degradedTarget{
		credentialID: credentialID, upstreamModel: upstreamModel,
	}]
}

// publish replaces the degraded set. A target that was already degraded keeps
// its episode number, so one monitor changing state does not make conversations
// on every other degraded target react a second time.
func (runtime *DegradationRuntime) publish(targets map[degradedTarget]struct{}) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	episodes := make(map[degradedTarget]uint64, len(targets))
	for target := range targets {
		if running, ongoing := runtime.episodes[target]; ongoing {
			episodes[target] = running
			continue
		}
		runtime.nextID++
		episodes[target] = runtime.nextID
	}
	runtime.episodes = episodes
}

// refreshDegradedTargets republishes the targets the detector currently judges
// degraded. The set is one row per monitored credential and model, so reloading
// it whole keeps it exactly consistent with the table instead of trying to
// patch it from every place a monitor can change.
func (s *Service) refreshDegradedTargets(ctx context.Context) {
	if s == nil || s.db == nil || s.degradationRuntime == nil {
		return
	}
	var rows []models.DegradationMonitor
	err := s.db.WithContext(ctx).
		Model(&models.DegradationMonitor{}).
		Select("credential_id", "upstream_model").
		Where("enabled = ? AND state = ?", true, models.DegradationStateDegraded).
		Find(&rows).Error
	if err != nil {
		logrus.WithError(err).
			WithField("event", "degradation.degraded_targets_refresh_failed").
			Warn("degraded targets were not republished")
		return
	}
	targets := make(map[degradedTarget]struct{}, len(rows))
	for _, row := range rows {
		targets[degradedTarget{
			credentialID: row.CredentialID, upstreamModel: row.UpstreamModel,
		}] = struct{}{}
	}
	s.degradationRuntime.publish(targets)
}

// DegradationRuntime exposes the published verdicts to the request path.
func (s *Service) DegradationRuntime() *DegradationRuntime {
	if s == nil {
		return nil
	}
	return s.degradationRuntime
}
