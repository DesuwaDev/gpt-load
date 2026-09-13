package control

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/utils"
	"gpt-load/internal/storage/models"
)

const (
	// degradationTickInterval is the scheduler heartbeat. Every cadence the
	// operator configures is a multiple of it, so one cheap indexed query per
	// tick is enough to drive the whole feature.
	degradationTickInterval = 15 * time.Second
	// degradationRetentionInterval is how often expired history is swept.
	degradationRetentionInterval = time.Hour
	// degradationDueBatchLimit bounds one sweep. A larger backlog simply carries
	// over to the next tick instead of being loaded at once.
	degradationDueBatchLimit = 64
	// degradationOverloadLagMS keeps the overload cursor behind the present so a
	// row committed slightly late is not skipped over.
	degradationOverloadLagMS = 10_000
	// degradationOverloadScanLimit bounds one overload scan.
	degradationOverloadScanLimit = 500
)

// degradationScheduler owns the periodic detection sweep, the overload reaction
// and the history retention. All of its state is process-local: the durable
// schedule lives in degradation_monitors.next_run_at_ms.
type degradationScheduler struct {
	service *Service

	sweeping atomic.Bool
	scanning atomic.Bool

	inflightMu sync.Mutex
	inflight   map[uint]struct{}

	overloadMu   sync.Mutex
	overloadSeen map[uint]int64

	lastOverloadScanMS int64
	lastRetentionMS    int64

	workers sync.WaitGroup
}

// RunDegradationMonitors drives scheduled degradation detection until the
// application context ends.
func (s *Service) RunDegradationMonitors(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	scheduler := &degradationScheduler{
		service:      s,
		inflight:     make(map[uint]struct{}),
		overloadSeen: make(map[uint]int64),
	}
	scheduler.run(ctx)
}

func (scheduler *degradationScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(degradationTickInterval)
	defer ticker.Stop()
	defer scheduler.workers.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			scheduler.tick(ctx)
		}
	}
}

func (scheduler *degradationScheduler) tick(ctx context.Context) {
	settings, err := scheduler.service.loadDegradationSettings(ctx)
	if err != nil {
		if ctx.Err() == nil {
			degradationLog(logrus.WarnLevel, logrus.Fields{
				"event": "control.degradation_settings_unavailable",
			}, "Degradation settings could not be loaded")
		}
		return
	}
	nowMS := scheduler.service.currentTime().UnixMilli()
	// 历史清理与开关无关：即使暂停检测，过期记录也应该继续回收。
	if nowMS-scheduler.lastRetentionMS >= degradationRetentionInterval.Milliseconds() {
		scheduler.lastRetentionMS = nowMS
		scheduler.pruneHistory(ctx, settings, nowMS)
	}
	if !settings.Enabled {
		return
	}
	scheduler.startSweep(ctx, settings)
	if !settings.OverloadTriggerEnabled {
		return
	}
	if nowMS-scheduler.lastOverloadScanMS < settings.OverloadScanIntervalSeconds*1000 {
		return
	}
	scheduler.lastOverloadScanMS = nowMS
	scheduler.startOverloadScan(ctx, settings)
}

// startSweep runs one due-monitor sweep in the background. A sweep that is
// still running simply skips the tick, so a slow upstream never queues sweeps.
func (scheduler *degradationScheduler) startSweep(
	ctx context.Context,
	settings models.DegradationSettings,
) {
	if !scheduler.sweeping.CompareAndSwap(false, true) {
		return
	}
	scheduler.workers.Add(1)
	go func() {
		defer scheduler.workers.Done()
		defer scheduler.sweeping.Store(false)
		scheduler.sweepDue(ctx, settings)
	}()
}

func (scheduler *degradationScheduler) sweepDue(
	ctx context.Context,
	settings models.DegradationSettings,
) {
	nowMS := scheduler.service.currentTime().UnixMilli()
	var rows []models.DegradationMonitor
	err := scheduler.service.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.
			Where("enabled = ? AND next_run_at_ms > 0 AND next_run_at_ms <= ?", true, nowMS).
			Order("next_run_at_ms ASC").Limit(degradationDueBatchLimit).
			Find(&rows).Error
	})
	if err != nil || len(rows) == 0 {
		if err != nil && ctx.Err() == nil {
			degradationLog(logrus.WarnLevel, logrus.Fields{
				"event": "control.degradation_due_query_failed",
			}, "Degradation due monitors could not be listed")
		}
		return
	}
	scheduler.fanOut(ctx, rows, settings, models.DegradationTriggerSchedule)
}

// fanOut executes monitors concurrently up to the configured cap.
func (scheduler *degradationScheduler) fanOut(
	ctx context.Context,
	rows []models.DegradationMonitor,
	settings models.DegradationSettings,
	trigger models.DegradationTrigger,
) {
	concurrency := settings.MaxConcurrentRuns
	if concurrency < 1 {
		concurrency = 1
	}
	slots := make(chan struct{}, concurrency)
	var group sync.WaitGroup
	for _, row := range rows {
		if ctx.Err() != nil {
			break
		}
		if !scheduler.claim(row.ID) {
			continue
		}
		slots <- struct{}{}
		group.Add(1)
		go func(monitor models.DegradationMonitor) {
			defer group.Done()
			defer func() { <-slots }()
			defer scheduler.release(monitor.ID)
			scheduler.execute(ctx, monitor, settings, trigger)
		}(row)
	}
	group.Wait()
}

func (scheduler *degradationScheduler) execute(
	ctx context.Context,
	monitor models.DegradationMonitor,
	settings models.DegradationSettings,
	trigger models.DegradationTrigger,
) {
	outcome, err := scheduler.service.performDegradationRun(ctx, monitor, settings, trigger)
	if err != nil {
		return
	}
	if err := scheduler.service.commitDegradationRun(ctx, &outcome); err != nil && ctx.Err() == nil {
		degradationLog(logrus.WarnLevel, logrus.Fields{
			"event":      "control.degradation_run_not_recorded",
			"monitor_id": monitor.ID,
		}, "Degradation run could not be recorded")
	}
}

func (scheduler *degradationScheduler) claim(monitorID uint) bool {
	scheduler.inflightMu.Lock()
	defer scheduler.inflightMu.Unlock()
	if _, running := scheduler.inflight[monitorID]; running {
		return false
	}
	scheduler.inflight[monitorID] = struct{}{}
	return true
}

func (scheduler *degradationScheduler) release(monitorID uint) {
	scheduler.inflightMu.Lock()
	defer scheduler.inflightMu.Unlock()
	delete(scheduler.inflight, monitorID)
}

// ---------------------------------------------------------------------------
// Overload reaction
// ---------------------------------------------------------------------------

// startOverloadScan reacts to upstream overload errors recorded on request
// attempts by probing the credential immediately, which is what turns a
// transient "servers are currently overloaded" into an actionable verdict.
func (scheduler *degradationScheduler) startOverloadScan(
	ctx context.Context,
	settings models.DegradationSettings,
) {
	if !scheduler.scanning.CompareAndSwap(false, true) {
		return
	}
	scheduler.workers.Add(1)
	go func() {
		defer scheduler.workers.Done()
		defer scheduler.scanning.Store(false)
		scheduler.scanOverload(ctx, settings)
	}()
}

// degradationOverloadAttempt is the minimal projection of a failed attempt.
type degradationOverloadAttempt struct {
	CompletedAtMS int64
	CredentialID  uint
	ErrorCode     string
	ErrorSummary  string
}

func (scheduler *degradationScheduler) scanOverload(
	ctx context.Context,
	settings models.DegradationSettings,
) {
	keywords := splitDegradationKeywords(strings.ToLower(settings.OverloadKeywords))
	if len(keywords) == 0 {
		return
	}
	nowMS := scheduler.service.currentTime().UnixMilli()
	ceiling := nowMS - degradationOverloadLagMS
	cursor := settings.OverloadCursorMS
	if cursor <= 0 {
		// 首次启用时不回溯历史，只从当前时间点开始观察。
		scheduler.persistOverloadCursor(ctx, ceiling)
		return
	}
	if ceiling <= cursor {
		return
	}

	var attempts []degradationOverloadAttempt
	err := scheduler.service.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Model(&models.RequestLogAttempt{}).
			Select("completed_at_ms", "credential_id", "error_code", "error_summary").
			Where("completed_at_ms > ? AND completed_at_ms <= ?", cursor, ceiling).
			Where("failure_category <> ?", "ok").
			Order("completed_at_ms ASC").Limit(degradationOverloadScanLimit).
			Find(&attempts).Error
	})
	if err != nil {
		if ctx.Err() == nil {
			degradationLog(logrus.WarnLevel, logrus.Fields{
				"event": "control.degradation_overload_scan_failed",
			}, "Degradation overload scan could not read request attempts")
		}
		return
	}

	next := ceiling
	if len(attempts) == degradationOverloadScanLimit {
		// 达到批量上限说明还有更早的行没处理完，游标只推进到已读到的位置。
		next = attempts[len(attempts)-1].CompletedAtMS
	}
	triggered := scheduler.collectOverloadCredentials(attempts, keywords, settings, nowMS)
	scheduler.persistOverloadCursor(ctx, next)
	if len(triggered) == 0 || ctx.Err() != nil {
		return
	}

	var rows []models.DegradationMonitor
	if err := scheduler.service.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Where("enabled = ? AND credential_id IN ?", true, triggered).
			Order("id ASC").Find(&rows).Error
	}); err != nil {
		return
	}
	if settings.SkipQuotaExhausted {
		kept := rows[:0]
		for _, row := range rows {
			if row.State != models.DegradationStateQuotaExhausted {
				kept = append(kept, row)
			}
		}
		rows = kept
	}
	scheduler.fanOut(ctx, rows, settings, models.DegradationTriggerOverload)
}

// collectOverloadCredentials applies the keyword filter and the per-credential
// debounce, so one overloaded minute produces one detection, not hundreds.
func (scheduler *degradationScheduler) collectOverloadCredentials(
	attempts []degradationOverloadAttempt,
	keywords []string,
	settings models.DegradationSettings,
	nowMS int64,
) []uint {
	debounceMS := settings.OverloadDebounceSeconds * 1000
	scheduler.overloadMu.Lock()
	defer scheduler.overloadMu.Unlock()
	for credentialID, seenMS := range scheduler.overloadSeen {
		if nowMS-seenMS > debounceMS {
			delete(scheduler.overloadSeen, credentialID)
		}
	}
	triggered := make([]uint, 0, 4)
	for _, attempt := range attempts {
		if attempt.CredentialID == 0 {
			continue
		}
		haystack := strings.ToLower(attempt.ErrorCode + " " + attempt.ErrorSummary)
		if !containsAnyKeyword(haystack, keywords) {
			continue
		}
		if seenMS, seen := scheduler.overloadSeen[attempt.CredentialID]; seen &&
			nowMS-seenMS <= debounceMS {
			continue
		}
		scheduler.overloadSeen[attempt.CredentialID] = nowMS
		triggered = append(triggered, attempt.CredentialID)
	}
	return triggered
}

func containsAnyKeyword(haystack string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(haystack, keyword) {
			return true
		}
	}
	return false
}

func (scheduler *degradationScheduler) persistOverloadCursor(ctx context.Context, cursor int64) {
	if cursor <= 0 {
		return
	}
	if err := scheduler.service.withControlTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&models.DegradationSettings{}).
			Where("id = ? AND overload_cursor_ms < ?", models.DegradationSettingsID, cursor).
			UpdateColumn("overload_cursor_ms", cursor).Error
	}); err != nil && ctx.Err() == nil {
		degradationLog(logrus.WarnLevel, logrus.Fields{
			"event": "control.degradation_overload_cursor_failed",
		}, "Degradation overload cursor could not be advanced")
	}
}

// ---------------------------------------------------------------------------
// Retention
// ---------------------------------------------------------------------------

func (scheduler *degradationScheduler) pruneHistory(
	ctx context.Context,
	settings models.DegradationSettings,
	nowMS int64,
) {
	days := settings.HistoryRetentionDays
	if days < degradationMinRetentionDays {
		days = degradationMinRetentionDays
	}
	cutoff := nowMS - int64(days)*24*time.Hour.Milliseconds()
	if cutoff <= 0 {
		return
	}
	if err := scheduler.service.withControlTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("completed_at_ms < ?", cutoff).
			Delete(&models.DegradationRun{}).Error
	}); err != nil && ctx.Err() == nil {
		degradationLog(logrus.WarnLevel, logrus.Fields{
			"event": "control.degradation_history_prune_failed",
		}, "Degradation history could not be pruned")
	}
}

func degradationLog(level logrus.Level, fields logrus.Fields, message string) {
	utils.LogPlaneBestEffort(
		logrus.StandardLogger(), level, utils.LogPlaneControl, fields, message,
	)
}
