package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/degradation"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

// degradationRankingLimit caps how many scored candidates a run keeps. The tail
// of the ranking is numerically irrelevant and would bloat the history table.
const degradationRankingLimit = 6

// Skip reasons recorded when a scheduled run stands down without calling the
// upstream. They are surfaced on the monitor row so the page can explain why a
// target looks idle.
const (
	degradationSkipGroupDisabled      = "group_disabled"
	degradationSkipCredentialDisabled = "credential_disabled"
	degradationSkipQuotaExhausted     = "quota_exhausted"
)

// degradationErrorTargetUnavailable means the monitored target could not be
// compiled at all — a deleted channel, an undecryptable credential, or a group
// whose parameters no longer resolve.
const degradationErrorTargetUnavailable = "target_unavailable"

// degradationRunOutcome is one completed detection, ready to be persisted.
type degradationRunOutcome struct {
	run        models.DegradationRun
	monitor    models.DegradationMonitor
	skipped    bool
	skipReason string
}

// RunDegradationMonitor executes one detection immediately and persists it.
//
// 探测与落库跑在脱离请求的上下文上：一次检测要真实调上游好几次，操作员关掉页面
// 或请求超时都不该把已经发出去的调用作废、连历史都不留。调用方仍然同步等结果，
// 只是取消信号不再传下去。
func (s *Service) RunDegradationMonitor(
	ctx context.Context,
	monitorID uint,
	trigger models.DegradationTrigger,
) (DegradationRunResponse, error) {
	if monitorID == 0 || !trigger.Valid() {
		return DegradationRunResponse{}, app_errors.ErrValidation
	}
	settings, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationRunResponse{}, err
	}
	monitor, err := s.readDegradationMonitor(ctx, monitorID)
	if err != nil {
		return DegradationRunResponse{}, err
	}
	// 与调度扫描、过载反应共用同一份 inflight 登记，同一条监控不会被同时探测两次。
	if !s.degradationRuns.claim(monitorID) {
		return DegradationRunResponse{}, app_errors.ErrDegradationRunInFlight
	}
	defer s.degradationRuns.release(monitorID)

	runCtx := context.WithoutCancel(ctx)
	outcome, err := s.performDegradationRun(runCtx, monitor, settings, trigger)
	if err != nil {
		return DegradationRunResponse{}, err
	}
	if err := s.commitDegradationRun(runCtx, &outcome); err != nil {
		return DegradationRunResponse{}, err
	}
	if outcome.skipped {
		return DegradationRunResponse{
			MonitorID: monitorID, Trigger: string(trigger),
			Outcome: string(outcome.monitor.State), ErrorCode: outcome.skipReason,
			Reasons: []string{}, Ranking: []DegradationRankingEntry{},
			Diagnostics: []DegradationSampleDiagRes{},
			Samples:     []DegradationSampleTextRes{},
		}, nil
	}
	return projectDegradationRun(outcome.run), nil
}

func (s *Service) readDegradationMonitor(
	ctx context.Context,
	monitorID uint,
) (models.DegradationMonitor, error) {
	var rows []models.DegradationMonitor
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Where("id = ?", monitorID).Limit(1).Find(&rows).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return models.DegradationMonitor{}, parentErr
	}
	if err != nil {
		return models.DegradationMonitor{}, fmt.Errorf(
			"read degradation monitor: %w", app_errors.ErrDatabase,
		)
	}
	if len(rows) == 0 {
		return models.DegradationMonitor{}, app_errors.ErrResourceNotFound
	}
	return rows[0], nil
}

// performDegradationRun runs one detection without writing anything. Upstream
// and configuration failures become a recorded outcome; only context
// cancellation is returned as an error, so a scheduled sweep always leaves a
// trace of what happened.
func (s *Service) performDegradationRun(
	ctx context.Context,
	monitor models.DegradationMonitor,
	settings models.DegradationSettings,
	trigger models.DegradationTrigger,
) (degradationRunOutcome, error) {
	started := s.currentTime()
	rows, err := s.readDegradationTargetRows(ctx, monitor.GroupID, monitor.CredentialID)
	if err != nil {
		if ctx.Err() != nil {
			return degradationRunOutcome{}, ctx.Err()
		}
		return s.finishDegradationRun(monitor, settings, trigger, started, degradationJudgement{
			state:        models.DegradationStateError,
			errorCode:    degradationErrorTargetUnavailable,
			errorSummary: "monitored target could not be loaded",
		}), nil
	}
	if !rows.found {
		return s.finishDegradationRun(monitor, settings, trigger, started, degradationJudgement{
			state:        models.DegradationStateError,
			errorCode:    degradationErrorTargetUnavailable,
			errorSummary: "monitored credential no longer exists",
		}), nil
	}
	if reason := degradationSkipReason(monitor, rows, settings, trigger); reason != "" {
		return s.skipDegradationRun(monitor, settings, reason), nil
	}

	sampleCount := monitor.ResolvedSampleCount(settings)
	challenges, err := degradation.GenerateChallenges(sampleCount)
	if err != nil {
		return degradationRunOutcome{}, fmt.Errorf(
			"generate degradation challenges: %w", app_errors.ErrInternalServer,
		)
	}
	target, err := s.mapDegradationTarget(ctx, rows)
	if err != nil {
		if ctx.Err() != nil {
			return degradationRunOutcome{}, ctx.Err()
		}
		return s.finishDegradationRun(monitor, settings, trigger, started, degradationJudgement{
			state:        models.DegradationStateError,
			errorCode:    degradationErrorTargetUnavailable,
			errorSummary: boundDegradationSummary(err.Error()),
			sampleCount:  sampleCount,
		}), nil
	}

	retryLimit := settings.RetryLimit
	if trigger == models.DegradationTriggerOverload {
		retryLimit = settings.OverloadRetryLimit
	}
	probe, err := s.executeDegradationProbe(ctx, target, degradationProbeRequest{
		upstreamModel:  monitor.UpstreamModel,
		effort:         monitor.ReasoningEffort,
		challenges:     challenges,
		retryLimit:     retryLimit,
		requestTimeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second,
	})
	if err != nil {
		if ctx.Err() != nil {
			return degradationRunOutcome{}, ctx.Err()
		}
		return s.finishDegradationRun(monitor, settings, trigger, started, degradationJudgement{
			state:        models.DegradationStateError,
			errorCode:    degradationErrorInternal,
			errorSummary: boundDegradationSummary(err.Error()),
			sampleCount:  sampleCount,
		}), nil
	}

	judgement := judgeDegradationProbe(monitor, settings, probe)
	judgement.sampleCount = sampleCount
	return s.finishDegradationRun(monitor, settings, trigger, started, judgement), nil
}

// degradationSkipReason reports why a scheduled run should stand down. A manual
// run ignores every skip: the operator explicitly asked for the call.
func degradationSkipReason(
	monitor models.DegradationMonitor,
	rows degradationTargetRows,
	settings models.DegradationSettings,
	trigger models.DegradationTrigger,
) string {
	if trigger == models.DegradationTriggerManual {
		return ""
	}
	if monitor.State == models.DegradationStateQuotaExhausted && settings.SkipQuotaExhausted {
		return degradationSkipQuotaExhausted
	}
	if monitor.ResolvedRunWhenDisabled(settings) {
		return ""
	}
	if !rows.group.Enabled {
		return degradationSkipGroupDisabled
	}
	if rows.credential.Status == models.CredentialStatusDisabled {
		return degradationSkipCredentialDisabled
	}
	return ""
}

// skipDegradationRun re-places the next attempt without recording a run.
func (s *Service) skipDegradationRun(
	monitor models.DegradationMonitor,
	settings models.DegradationSettings,
	reason string,
) degradationRunOutcome {
	updated := monitor
	updated.StateReason = reason
	if reason == degradationSkipQuotaExhausted {
		// 额度耗尽时不再自动排期，等操作员确认后用「复位」重新启动。
		updated.NextRunAtMS = 0
	} else {
		updated.NextRunAtMS = degradationNextRunAt(updated, settings, s.currentTime())
	}
	return degradationRunOutcome{monitor: updated, skipped: true, skipReason: reason}
}

// degradationJudgement is the scored result of one detection, before it is
// turned into a persisted run and a monitor state.
type degradationJudgement struct {
	state        models.DegradationState
	reasons      []string
	errorCode    string
	errorSummary string
	sampleCount  int
	usedSamples  int
	attempts     int
	detected     string
	expectedProb float64
	leadingProb  float64
	minProb      float64
	ranking      []DegradationRankingEntry
	diagnostics  []DegradationSampleDiagRes
	samples      []DegradationSampleTextRes
}

// judgeDegradationProbe turns collected answers into a verdict.
func judgeDegradationProbe(
	monitor models.DegradationMonitor,
	settings models.DegradationSettings,
	probe degradationProbeResult,
) degradationJudgement {
	minProbability := microsToProbability(monitor.ResolvedMinProbabilityMicros(settings))
	judgement := degradationJudgement{
		attempts: probe.attempts,
		minProb:  minProbability,
		// 原文在判定之前就留存：失败或无法判定的那几次最需要人工看上游到底回了什么。
		samples: projectDegradationSamples(probe.samples),
	}
	if probe.errorCode != "" {
		judgement.errorCode = probe.errorCode
		judgement.errorSummary = probe.errorSummary
		if probe.errorCode == degradationErrorQuotaExhausted {
			judgement.state = models.DegradationStateQuotaExhausted
			return judgement
		}
		judgement.state = models.DegradationStateError
		return judgement
	}
	bank, err := degradation.EmbeddedBank()
	if err != nil {
		judgement.state = models.DegradationStateError
		judgement.errorCode = degradationErrorInternal
		judgement.errorSummary = "degradation reference bank is unavailable"
		return judgement
	}
	attribution, analyzeErr := bank.Analyze(probe.samples)
	judgement.usedSamples = attribution.UsedSamples
	judgement.diagnostics = projectDegradationDiagnostics(attribution.Diagnostics)
	if analyzeErr != nil {
		judgement.state = models.DegradationStateInconclusive
		judgement.errorCode = degradationErrorEmptyResponse
		judgement.errorSummary = boundDegradationSummary(analyzeErr.Error())
		if errors.Is(analyzeErr, degradation.ErrNoUsableSample) {
			judgement.errorSummary = "no response contained enough numbers to score"
		}
		return judgement
	}

	verdict := degradation.Evaluate(attribution, degradation.Rule{
		ExpectedModel: monitor.ExpectedModel, MinProbability: minProbability,
	})
	judgement.detected = attribution.Prediction
	judgement.expectedProb = verdict.ExpectedProbability
	judgement.leadingProb = verdict.LeadingProbability
	judgement.ranking = projectDegradationRanking(attribution.Results)
	judgement.reasons = make([]string, 0, len(verdict.Reasons))
	for _, reason := range verdict.Reasons {
		judgement.reasons = append(judgement.reasons, string(reason))
	}
	switch {
	case !verdict.Conclusive:
		judgement.state = models.DegradationStateInconclusive
	case verdict.Degraded:
		judgement.state = models.DegradationStateDegraded
	default:
		judgement.state = models.DegradationStateHealthy
	}
	return judgement
}

func projectDegradationRanking(scores []degradation.ModelScore) []DegradationRankingEntry {
	limit := min(len(scores), degradationRankingLimit)
	ranking := make([]DegradationRankingEntry, 0, limit)
	for _, score := range scores[:limit] {
		ranking = append(ranking, DegradationRankingEntry{
			Model: score.Model, DisplayName: score.DisplayName, Family: score.Family,
			ProbabilityMicros: probabilityToMicros(score.Probability),
		})
	}
	return ranking
}

// degradationSampleTextLimit bounds how much of one upstream answer is kept on
// the run row. The scoring only needs the numbers, so the stored text exists for
// human review; a runaway response must not be able to bloat the history table.
const degradationSampleTextLimit = 4000

// projectDegradationSamples keeps the raw answers of a run, bounded per sample.
func projectDegradationSamples(samples []degradation.Sample) []DegradationSampleTextRes {
	result := make([]DegradationSampleTextRes, 0, len(samples))
	for index, sample := range samples {
		text := sample.Text
		truncated := false
		if runes := []rune(text); len(runes) > degradationSampleTextLimit {
			text = string(runes[:degradationSampleTextLimit])
			truncated = true
		}
		result = append(result, DegradationSampleTextRes{
			Index: index, ExpectedCount: sample.ExpectedCount,
			Text: text, Truncated: truncated,
		})
	}
	return result
}

func projectDegradationDiagnostics(
	diagnostics []degradation.SampleDiagnostic,
) []DegradationSampleDiagRes {
	result := make([]DegradationSampleDiagRes, 0, len(diagnostics))
	for _, item := range diagnostics {
		result = append(result, DegradationSampleDiagRes{
			Index: item.Index, ParsedNumbers: item.ParsedNumbers,
			MinimumNumbers: item.MinimumNumbers, Accepted: item.Accepted,
		})
	}
	return result
}

// finishDegradationRun assembles the run row and the monitor state that the
// judgement implies.
func (s *Service) finishDegradationRun(
	monitor models.DegradationMonitor,
	settings models.DegradationSettings,
	trigger models.DegradationTrigger,
	started time.Time,
	judgement degradationJudgement,
) degradationRunOutcome {
	completed := s.currentTime()
	if completed.Before(started) {
		completed = started
	}
	detail, err := json.Marshal(degradationRunDetail{
		Ranking: judgement.ranking, Diagnostics: judgement.diagnostics,
		Samples: judgement.samples,
	})
	if err != nil {
		detail = []byte(`{}`)
	}
	run := models.DegradationRun{
		MonitorID:     monitor.ID,
		Trigger:       trigger,
		Outcome:       judgement.state,
		StartedAtMS:   started.UnixMilli(),
		CompletedAtMS: completed.UnixMilli(),
		DurationMs:    completed.Sub(started).Milliseconds(),
		ExpectedModel: monitor.ExpectedModel,
		DetectedModel: judgement.detected,

		ExpectedProbabilityMicros: probabilityToMicros(judgement.expectedProb),
		LeadingProbabilityMicros:  probabilityToMicros(judgement.leadingProb),
		MinProbabilityMicros:      probabilityToMicros(judgement.minProb),
		SampleCount:               judgement.sampleCount,
		UsedSamples:               judgement.usedSamples,
		Attempts:                  judgement.attempts,
		Reasons:                   boundDegradationReasons(judgement.reasons),
		ErrorCode:                 judgement.errorCode,
		ErrorSummary:              judgement.errorSummary,
		Detail:                    models.JSON(detail),
	}

	updated := monitor
	updated.State = judgement.state
	updated.StateReason = run.Reasons
	if updated.StateReason == "" {
		updated.StateReason = judgement.errorCode
	}
	if monitor.State != judgement.state {
		updated.StateSinceMS = completed.UnixMilli()
	}
	updated.LastRunAtMS = completed.UnixMilli()
	updated.LastErrorCode = judgement.errorCode
	updated.LastDetectedModel = judgement.detected
	updated.LastProbabilityMicros = run.ExpectedProbabilityMicros

	parked := false
	switch judgement.state {
	case models.DegradationStateError:
		updated.ConsecutiveErrors = monitor.ConsecutiveErrors + 1
		if settings.QuarantineErrorThreshold > 0 &&
			updated.ConsecutiveErrors >= settings.QuarantineErrorThreshold {
			parked = true
		}
	case models.DegradationStateQuotaExhausted:
		updated.ConsecutiveErrors = 0
		parked = settings.SkipQuotaExhausted
	default:
		// 调用成功即视为可达，错误计数归零；是否降智由 state 单独表达。
		updated.ConsecutiveErrors = 0
		updated.LastSuccessAtMS = completed.UnixMilli()
	}

	switch {
	case !updated.Enabled, parked:
		updated.NextRunAtMS = 0
	default:
		updated.NextRunAtMS = degradationNextRunAt(updated, settings, completed)
	}
	return degradationRunOutcome{run: run, monitor: updated}
}

// commitDegradationRun writes the run and the monitor state in one transaction.
func (s *Service) commitDegradationRun(
	ctx context.Context,
	outcome *degradationRunOutcome,
) error {
	monitor := outcome.monitor
	update := map[string]any{
		"state":                   monitor.State,
		"state_reason":            monitor.StateReason,
		"state_since_ms":          monitor.StateSinceMS,
		"next_run_at_ms":          monitor.NextRunAtMS,
		"updated_at_ms":           s.currentTime().UnixMilli(),
		"last_error_code":         monitor.LastErrorCode,
		"consecutive_errors":      monitor.ConsecutiveErrors,
		"last_run_at_ms":          monitor.LastRunAtMS,
		"last_success_at_ms":      monitor.LastSuccessAtMS,
		"last_detected_model":     monitor.LastDetectedModel,
		"last_probability_micros": monitor.LastProbabilityMicros,
	}
	if outcome.skipped {
		update = map[string]any{
			"state_reason":   monitor.StateReason,
			"next_run_at_ms": monitor.NextRunAtMS,
			"updated_at_ms":  s.currentTime().UnixMilli(),
		}
	}
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&models.DegradationMonitor{}).
			Where("id = ?", monitor.ID).Updates(update)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// 监控项在检测过程中被删除了，丢弃这次结果即可。
			return nil
		}
		if outcome.skipped {
			return nil
		}
		if err := tx.Create(&outcome.run).Error; err != nil {
			return err
		}
		return pruneDegradationRuns(tx, monitor.ID)
	})
	if err != nil {
		return fmt.Errorf("record degradation run: %w", app_errors.ErrDatabase)
	}
	return nil
}

// pruneDegradationRuns keeps the per-monitor history bounded. Age-based
// retention is handled by the sweeper; this bound is what keeps a monitor with
// a short interval from dominating the table between sweeps.
func pruneDegradationRuns(tx *gorm.DB, monitorID uint) error {
	var keep []uint
	if err := tx.Model(&models.DegradationRun{}).
		Where("monitor_id = ?", monitorID).
		Order("id DESC").Limit(degradationRunHistoryLimit).
		Pluck("id", &keep).Error; err != nil {
		return err
	}
	if len(keep) < degradationRunHistoryLimit {
		return nil
	}
	oldest := keep[len(keep)-1]
	return tx.Where("monitor_id = ? AND id < ?", monitorID, oldest).
		Delete(&models.DegradationRun{}).Error
}

func boundDegradationReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	sorted := append([]string(nil), reasons...)
	sort.Strings(sorted)
	joined := strings.Join(sorted, ",")
	const reasonColumnLimit = 160
	if len(joined) <= reasonColumnLimit {
		return joined
	}
	return joined[:reasonColumnLimit]
}

// probabilityToMicros clamps a fraction into the persisted micro scale so a
// NaN or an out-of-range value can never violate the column CHECK.
func probabilityToMicros(value float64) int64 {
	if math.IsNaN(value) || value <= 0 {
		return 0
	}
	if value >= 1 {
		return probabilityMicrosScale
	}
	return int64(math.Round(value * probabilityMicrosScale))
}

func microsToProbability(value int64) float64 {
	if value <= 0 {
		return 0
	}
	if value >= probabilityMicrosScale {
		return 1
	}
	return float64(value) / probabilityMicrosScale
}
