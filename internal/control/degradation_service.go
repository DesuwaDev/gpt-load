package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/degradation"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

// Bounds mirror the database CHECK constraints of migration 0019 so an invalid
// value is rejected as a validation error instead of a driver error.
const (
	degradationMinIntervalSeconds    = 300
	degradationMaxIntervalSeconds    = 604_800
	degradationMinConcurrentRuns     = 1
	degradationMaxConcurrentRuns     = 16
	degradationMinTimeoutSeconds     = 30
	degradationMaxTimeoutSeconds     = 1_800
	degradationMaxRetryLimit         = 5
	degradationMaxQuarantineErrors   = 100
	degradationMinRetentionDays      = 1
	degradationMaxRetentionDays      = 365
	degradationMinScanIntervalSecond = 5
	degradationMaxScanIntervalSecond = 3_600
	degradationMaxDebounceSeconds    = 86_400
	degradationMaxKeywords           = 16
	degradationKeywordColumnLimit    = 512
	degradationMaxNoteLength         = 255
	degradationMaxPageSize           = 100
	degradationDefaultPageSize       = 20
	degradationMaxEnrollTargets      = 500
	degradationMaxBatchMonitors      = 500
	degradationRunHistoryLimit       = 50
	probabilityMicrosScale           = 1_000_000
)

var degradationBankIndex = sync.OnceValues(func() (map[string]degradation.BankModel, error) {
	bank, err := degradation.EmbeddedBank()
	if err != nil {
		return nil, err
	}
	index := make(map[string]degradation.BankModel, len(bank.Models))
	for _, model := range bank.Models {
		index[model.ID] = model
	}
	return index, nil
})

func degradationModelName(id string) string {
	index, err := degradationBankIndex()
	if err != nil {
		return id
	}
	if model, ok := index[id]; ok && strings.TrimSpace(model.DisplayName) != "" {
		return model.DisplayName
	}
	return id
}

// DegradationCatalog describes the detector to the UI: the enrolled fingerprints
// an expected model can be chosen from, plus the sampling bounds.
func (s *Service) DegradationCatalog() (DegradationCatalogResponse, error) {
	bank, err := degradation.EmbeddedBank()
	if err != nil {
		return DegradationCatalogResponse{}, fmt.Errorf(
			"load degradation bank: %w", app_errors.ErrInternalServer,
		)
	}
	items := make([]DegradationBankModelResponse, 0, len(bank.Models))
	for _, model := range bank.Models {
		items = append(items, DegradationBankModelResponse{
			ID: model.ID, DisplayName: model.DisplayName,
			Family: model.Family, FamilyName: model.FamilyName,
		})
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].Family == items[right].Family {
			return items[left].ID < items[right].ID
		}
		return items[left].Family < items[right].Family
	})
	return DegradationCatalogResponse{
		Models: items,
		Efforts: []string{
			string(models.DegradationEffortDefault), string(models.DegradationEffortMinimal),
			string(models.DegradationEffortLow), string(models.DegradationEffortMedium),
			string(models.DegradationEffortHigh),
		},
		States: []string{
			string(models.DegradationStateUnknown), string(models.DegradationStateHealthy),
			string(models.DegradationStateDegraded), string(models.DegradationStateInconclusive),
			string(models.DegradationStateError), string(models.DegradationStateQuotaExhausted),
		},
		RecommendedSamples: bank.RecommendedQueries,
		MaxSamples:         degradation.MaxSamplesPerRun,
		CalibratedSamples:  degradation.MaxCalibratedSamples,
		Method:             bank.MethodName(),
		BuiltAt:            bank.BuiltAt,
	}, nil
}

// loadDegradationSettings returns the singleton, seeding it on first use.
func (s *Service) loadDegradationSettings(ctx context.Context) (models.DegradationSettings, error) {
	row, found, err := s.readDegradationSettings(ctx)
	if err != nil {
		return models.DegradationSettings{}, err
	}
	if found {
		return row, nil
	}
	seeded := models.DefaultDegradationSettings()
	if err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seeded).Error
	}); err != nil {
		return models.DegradationSettings{}, fmt.Errorf(
			"seed degradation settings: %w", app_errors.ErrDatabase,
		)
	}
	row, found, err = s.readDegradationSettings(ctx)
	if err != nil {
		return models.DegradationSettings{}, err
	}
	if !found {
		return models.DegradationSettings{}, fmt.Errorf(
			"degradation settings row is missing: %w", app_errors.ErrInternalServer,
		)
	}
	return row, nil
}

func (s *Service) readDegradationSettings(
	ctx context.Context,
) (models.DegradationSettings, bool, error) {
	var rows []models.DegradationSettings
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Where("id = ?", models.DegradationSettingsID).Limit(1).Find(&rows).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return models.DegradationSettings{}, false, parentErr
	}
	if err != nil {
		return models.DegradationSettings{}, false, fmt.Errorf(
			"read degradation settings: %w", app_errors.ErrDatabase,
		)
	}
	if len(rows) == 0 {
		return models.DegradationSettings{}, false, nil
	}
	return rows[0], true, nil
}

// GetDegradationSettings returns the global configuration.
func (s *Service) GetDegradationSettings(
	ctx context.Context,
) (DegradationSettingsResponse, error) {
	row, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationSettingsResponse{}, err
	}
	return projectDegradationSettings(row), nil
}

// GetDegradationOverview returns the settings and the detector catalog in one
// response so the monitor page mounts with a single request.
func (s *Service) GetDegradationOverview(
	ctx context.Context,
) (DegradationOverviewResponse, error) {
	settings, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationOverviewResponse{}, err
	}
	catalog, err := s.DegradationCatalog()
	if err != nil {
		return DegradationOverviewResponse{}, err
	}
	return DegradationOverviewResponse{
		Settings: projectDegradationSettings(settings), Catalog: catalog,
	}, nil
}

// UpdateDegradationSettings applies a partial update to the singleton.
func (s *Service) UpdateDegradationSettings(
	ctx context.Context,
	request DegradationSettingsRequest,
) (DegradationSettingsResponse, error) {
	current, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationSettingsResponse{}, err
	}
	updated, err := applyDegradationSettingsRequest(current, request)
	if err != nil {
		return DegradationSettingsResponse{}, err
	}
	rescheduleFrom := current
	if err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.DegradationSettings{}).
			Where("id = ?", models.DegradationSettingsID).
			Select(degradationSettingsColumns()).
			Updates(&updated).Error; err != nil {
			return err
		}
		// 全局节奏变了就把继承全局的监控重新排期，避免旧的 next_run_at 长期停在
		// 一个已经不再有效的间隔上。
		if updated.IntervalSeconds != rescheduleFrom.IntervalSeconds ||
			updated.CooldownIntervalSeconds != rescheduleFrom.CooldownIntervalSeconds {
			return rescheduleInheritedDegradationMonitors(tx, updated, s.currentTime())
		}
		return nil
	}); err != nil {
		return DegradationSettingsResponse{}, fmt.Errorf(
			"update degradation settings: %w", app_errors.ErrDatabase,
		)
	}
	return projectDegradationSettings(updated), nil
}

func degradationSettingsColumns() []string {
	return []string{
		"enabled", "interval_seconds", "cooldown_interval_seconds", "sample_count",
		"min_probability_micros", "run_when_disabled", "skip_quota_exhausted",
		"max_concurrent_runs", "request_timeout_seconds", "retry_limit",
		"quarantine_error_threshold", "history_retention_days",
		"overload_trigger_enabled", "overload_keywords", "overload_retry_limit",
		"overload_scan_interval_seconds", "overload_debounce_seconds",
		"notify_telegram_enabled", "notify_email_enabled", "notify_on_degraded",
		"notify_on_recovered", "notify_on_error", "updated_at_ms",
	}
}

func rescheduleInheritedDegradationMonitors(
	tx *gorm.DB,
	settings models.DegradationSettings,
	now time.Time,
) error {
	var rows []models.DegradationMonitor
	if err := tx.
		Where("enabled = ? AND next_run_at_ms > 0", true).
		Where("interval_seconds = 0 OR cooldown_interval_seconds = 0").
		Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		next := degradationNextRunAt(row, settings, now)
		if next == row.NextRunAtMS {
			continue
		}
		if err := tx.Model(&models.DegradationMonitor{}).
			Where("id = ?", row.ID).
			UpdateColumn("next_run_at_ms", next).Error; err != nil {
			return err
		}
	}
	return nil
}

func applyDegradationSettingsRequest(
	current models.DegradationSettings,
	request DegradationSettingsRequest,
) (models.DegradationSettings, error) {
	updated := current
	updated.ID = models.DegradationSettingsID
	if request.Enabled != nil {
		updated.Enabled = *request.Enabled
	}
	if request.IntervalSeconds != nil {
		updated.IntervalSeconds = *request.IntervalSeconds
	}
	if request.CooldownIntervalSeconds != nil {
		updated.CooldownIntervalSeconds = *request.CooldownIntervalSeconds
	}
	if request.SampleCount != nil {
		updated.SampleCount = *request.SampleCount
	}
	if request.MinProbabilityMicros != nil {
		updated.MinProbabilityMicros = *request.MinProbabilityMicros
	}
	if request.RunWhenDisabled != nil {
		updated.RunWhenDisabled = *request.RunWhenDisabled
	}
	if request.SkipQuotaExhausted != nil {
		updated.SkipQuotaExhausted = *request.SkipQuotaExhausted
	}
	if request.MaxConcurrentRuns != nil {
		updated.MaxConcurrentRuns = *request.MaxConcurrentRuns
	}
	if request.RequestTimeoutSeconds != nil {
		updated.RequestTimeoutSeconds = *request.RequestTimeoutSeconds
	}
	if request.RetryLimit != nil {
		updated.RetryLimit = *request.RetryLimit
	}
	if request.QuarantineErrorThreshold != nil {
		updated.QuarantineErrorThreshold = *request.QuarantineErrorThreshold
	}
	if request.HistoryRetentionDays != nil {
		updated.HistoryRetentionDays = *request.HistoryRetentionDays
	}
	if request.OverloadTriggerEnabled != nil {
		updated.OverloadTriggerEnabled = *request.OverloadTriggerEnabled
	}
	if request.OverloadKeywords != nil {
		joined, err := joinDegradationKeywords(*request.OverloadKeywords)
		if err != nil {
			return models.DegradationSettings{}, err
		}
		updated.OverloadKeywords = joined
	}
	if request.OverloadRetryLimit != nil {
		updated.OverloadRetryLimit = *request.OverloadRetryLimit
	}
	if request.OverloadScanIntervalSeconds != nil {
		updated.OverloadScanIntervalSeconds = *request.OverloadScanIntervalSeconds
	}
	if request.OverloadDebounceSeconds != nil {
		updated.OverloadDebounceSeconds = *request.OverloadDebounceSeconds
	}
	if request.NotifyTelegramEnabled != nil {
		updated.NotifyTelegramEnabled = *request.NotifyTelegramEnabled
	}
	if request.NotifyEmailEnabled != nil {
		updated.NotifyEmailEnabled = *request.NotifyEmailEnabled
	}
	if request.NotifyOnDegraded != nil {
		updated.NotifyOnDegraded = *request.NotifyOnDegraded
	}
	if request.NotifyOnRecovered != nil {
		updated.NotifyOnRecovered = *request.NotifyOnRecovered
	}
	if request.NotifyOnError != nil {
		updated.NotifyOnError = *request.NotifyOnError
	}
	if err := validateDegradationSettings(updated); err != nil {
		return models.DegradationSettings{}, err
	}
	return updated, nil
}

func validateDegradationSettings(value models.DegradationSettings) error {
	if !withinRange(value.IntervalSeconds, degradationMinIntervalSeconds, degradationMaxIntervalSeconds) ||
		!withinRange(value.CooldownIntervalSeconds, degradationMinIntervalSeconds, degradationMaxIntervalSeconds) {
		return app_errors.ErrValidation
	}
	if value.SampleCount < 1 || value.SampleCount > degradation.MaxSamplesPerRun {
		return app_errors.ErrValidation
	}
	if value.MinProbabilityMicros < 1 || value.MinProbabilityMicros > probabilityMicrosScale {
		return app_errors.ErrValidation
	}
	if value.MaxConcurrentRuns < degradationMinConcurrentRuns ||
		value.MaxConcurrentRuns > degradationMaxConcurrentRuns {
		return app_errors.ErrValidation
	}
	if !withinRange(value.RequestTimeoutSeconds, degradationMinTimeoutSeconds, degradationMaxTimeoutSeconds) {
		return app_errors.ErrValidation
	}
	if value.RetryLimit < 0 || value.RetryLimit > degradationMaxRetryLimit {
		return app_errors.ErrValidation
	}
	if value.QuarantineErrorThreshold < 0 ||
		value.QuarantineErrorThreshold > degradationMaxQuarantineErrors {
		return app_errors.ErrValidation
	}
	if value.HistoryRetentionDays < degradationMinRetentionDays ||
		value.HistoryRetentionDays > degradationMaxRetentionDays {
		return app_errors.ErrValidation
	}
	if value.OverloadRetryLimit < 0 || value.OverloadRetryLimit > degradationMaxRetryLimit {
		return app_errors.ErrValidation
	}
	if !withinRange(
		value.OverloadScanIntervalSeconds,
		degradationMinScanIntervalSecond,
		degradationMaxScanIntervalSecond,
	) {
		return app_errors.ErrValidation
	}
	if value.OverloadDebounceSeconds < 0 ||
		value.OverloadDebounceSeconds > degradationMaxDebounceSeconds {
		return app_errors.ErrValidation
	}
	if value.OverloadTriggerEnabled && strings.TrimSpace(value.OverloadKeywords) == "" {
		return app_errors.ErrValidation
	}
	return nil
}

func withinRange(value, low, high int64) bool {
	return value >= low && value <= high
}

func splitDegradationKeywords(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func joinDegradationKeywords(values []string) (string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, ",") {
			return "", app_errors.ErrValidation
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) > degradationMaxKeywords {
		return "", app_errors.ErrValidation
	}
	joined := strings.Join(normalized, ",")
	if len(joined) > degradationKeywordColumnLimit {
		return "", app_errors.ErrValidation
	}
	return joined, nil
}

// degradationNextRunAt places the next scheduled detection. Anything that is not
// plainly healthy uses the cooldown cadence so a recovery is noticed quickly.
func degradationNextRunAt(
	monitor models.DegradationMonitor,
	settings models.DegradationSettings,
	now time.Time,
) int64 {
	seconds := monitor.ResolvedIntervalSeconds(settings)
	switch monitor.State {
	case models.DegradationStateHealthy, models.DegradationStateUnknown:
	default:
		seconds = monitor.ResolvedCooldownIntervalSeconds(settings)
	}
	if seconds <= 0 {
		seconds = degradationMinIntervalSeconds
	}
	return now.Add(time.Duration(seconds) * time.Second).UnixMilli()
}

func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

// ---------------------------------------------------------------------------
// Monitor collection
// ---------------------------------------------------------------------------

// degradationMonitorContext is everything the projection needs beyond the
// monitor row itself.
type degradationMonitorContext struct {
	groups      map[uint]models.Group
	credentials map[uint]models.Credential
	masks       map[uint]string
	plans       map[uint]ObservationPlanSummary
}

// ListDegradationMonitors returns the monitor table together with the global
// settings and the state summary, so the page renders from one response.
func (s *Service) ListDegradationMonitors(
	ctx context.Context,
	query DegradationMonitorQuery,
) (DegradationMonitorCollectionResponse, error) {
	settings, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationMonitorCollectionResponse{}, err
	}
	query = normalizeDegradationQuery(query)

	var (
		rows        []models.DegradationMonitor
		total       int64
		summary     DegradationSummaryResponse
		groupRows   []models.Group
		credentials []models.Credential
	)
	err = s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.Group{}).Order("id ASC").Find(&groupRows).Error; err != nil {
			return err
		}
		summaryRows := make([]struct {
			State   string
			Enabled bool
			Count   int64
		}, 0, 12)
		if err := tx.Model(&models.DegradationMonitor{}).
			Select("state as state, enabled as enabled, COUNT(*) as count").
			Group("state").Group("enabled").
			Find(&summaryRows).Error; err != nil {
			return err
		}
		for _, row := range summaryRows {
			summary.Total += int(row.Count)
			if row.Enabled {
				summary.Enabled += int(row.Count)
			}
			switch models.DegradationState(row.State) {
			case models.DegradationStateHealthy:
				summary.Healthy += int(row.Count)
			case models.DegradationStateDegraded:
				summary.Degraded += int(row.Count)
			case models.DegradationStateInconclusive:
				summary.Inconclusive += int(row.Count)
			case models.DegradationStateError:
				summary.Error += int(row.Count)
			case models.DegradationStateQuotaExhausted:
				summary.QuotaExhausted += int(row.Count)
			default:
				summary.Unknown += int(row.Count)
			}
		}

		scope := degradationMonitorScope(tx, query, groupRows)
		if err := scope.Model(&models.DegradationMonitor{}).Count(&total).Error; err != nil {
			return err
		}
		return degradationMonitorScope(tx, query, groupRows).
			Order(clause.OrderBy{Expression: clause.Expr{SQL: degradationStateOrderSQL}}).
			Order("group_id ASC").Order("credential_id ASC").
			Order("upstream_model ASC").Order("id ASC").
			Offset((query.Page - 1) * query.PageSize).
			Limit(query.PageSize).
			Find(&rows).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return DegradationMonitorCollectionResponse{}, parentErr
	}
	if err != nil {
		return DegradationMonitorCollectionResponse{}, fmt.Errorf(
			"list degradation monitors: %w", app_errors.ErrDatabase,
		)
	}

	credentialIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		credentialIDs = append(credentialIDs, row.CredentialID)
	}
	if len(credentialIDs) > 0 {
		if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
			return tx.Where("id IN ?", credentialIDs).Find(&credentials).Error
		}); err != nil {
			return DegradationMonitorCollectionResponse{}, fmt.Errorf(
				"load degradation credentials: %w", app_errors.ErrDatabase,
			)
		}
	}

	monitorContext := s.buildDegradationMonitorContext(groupRows, credentials)
	items := make([]DegradationMonitorResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, projectDegradationMonitor(row, settings, monitorContext))
	}
	observedAtMS, err := safeEpochMilliseconds(s.currentTime())
	if err != nil {
		return DegradationMonitorCollectionResponse{}, err
	}
	return DegradationMonitorCollectionResponse{
		ObservedAtMS: observedAtMS,
		Settings:     projectDegradationSettings(settings),
		Summary:      summary,
		Items:        items,
		Pagination: CredentialPaginationResponse{
			Page: query.Page, PageSize: query.PageSize,
			TotalItems: int(total),
			TotalPages: credentialCollectionTotalPages(int(total), query.PageSize),
		},
		ActiveRuns: s.degradationRuns.active(),
	}, nil
}

// degradationStateOrderSQL sorts the states that need attention to the top. The
// CASE form is portable across SQLite, MySQL, and PostgreSQL.
const degradationStateOrderSQL = "CASE state " +
	"WHEN 'degraded' THEN 0 WHEN 'error' THEN 1 WHEN 'inconclusive' THEN 2 " +
	"WHEN 'quota_exhausted' THEN 3 WHEN 'unknown' THEN 4 ELSE 5 END"

func normalizeDegradationQuery(query DegradationMonitorQuery) DegradationMonitorQuery {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = degradationDefaultPageSize
	}
	if query.PageSize > degradationMaxPageSize {
		query.PageSize = degradationMaxPageSize
	}
	query.Query = strings.TrimSpace(query.Query)
	query.State = strings.TrimSpace(query.State)
	return query
}

func degradationMonitorScope(
	tx *gorm.DB,
	query DegradationMonitorQuery,
	groups []models.Group,
) *gorm.DB {
	scope := tx.Model(&models.DegradationMonitor{})
	if query.GroupID > 0 {
		scope = scope.Where("group_id = ?", query.GroupID)
	}
	if query.State != "" {
		scope = scope.Where("state = ?", query.State)
	}
	if query.Enabled != nil {
		scope = scope.Where("enabled = ?", *query.Enabled)
	}
	if query.Query == "" {
		return scope
	}
	needle := strings.ToLower(query.Query)
	matchedGroups := make([]uint, 0, len(groups))
	for _, group := range groups {
		if strings.Contains(strings.ToLower(group.Name), needle) {
			matchedGroups = append(matchedGroups, group.ID)
		}
	}
	pattern := "%" + needle + "%"
	condition := tx.Session(&gorm.Session{NewDB: true}).
		Where("LOWER(upstream_model) LIKE ?", pattern).
		Or("LOWER(expected_model) LIKE ?", pattern)
	if len(matchedGroups) > 0 {
		condition = condition.Or("group_id IN ?", matchedGroups)
	}
	if credentialID, err := strconv.ParseUint(query.Query, 10, strconv.IntSize); err == nil &&
		credentialID > 0 {
		condition = condition.Or("credential_id = ?", uint(credentialID))
	}
	return scope.Where(condition)
}

func (s *Service) buildDegradationMonitorContext(
	groups []models.Group,
	credentials []models.Credential,
) degradationMonitorContext {
	result := degradationMonitorContext{
		groups:      make(map[uint]models.Group, len(groups)),
		credentials: make(map[uint]models.Credential, len(credentials)),
		masks:       make(map[uint]string, len(credentials)),
		plans:       s.loadDegradationPlans(credentials),
	}
	for _, group := range groups {
		result.groups[group.ID] = group
	}
	for _, credential := range credentials {
		result.credentials[credential.ID] = credential
		group, ok := result.groups[credential.GroupID]
		if !ok {
			result.masks[credential.ID] = fmt.Sprintf("#%d", credential.ID)
			continue
		}
		result.masks[credential.ID] = s.degradationCredentialMask(group, credential)
	}
	return result
}

// loadDegradationPlans reads the plan label of every monitored credential from
// its observation snapshot, which is the same source the group page renders.
// 标记只是附加信息：观测表还没建、读失败或快照解不出来时返回已取到的部分，
// 监控表照常显示，不会因为少一个徽标就整页报错。
func (s *Service) loadDegradationPlans(
	credentials []models.Credential,
) map[uint]ObservationPlanSummary {
	plans := make(map[uint]ObservationPlanSummary, len(credentials))
	if s == nil || s.db == nil || len(credentials) == 0 {
		return plans
	}
	if !s.db.Migrator().HasTable(&models.CredentialObservation{}) {
		return plans
	}
	credentialIDs := make([]uint, 0, len(credentials))
	for _, credential := range credentials {
		credentialIDs = append(credentialIDs, credential.ID)
	}
	var observations []models.CredentialObservation
	if err := s.db.Select("credential_id", "snapshot_json").
		Where("credential_id IN ?", credentialIDs).
		Find(&observations).Error; err != nil {
		return plans
	}
	for _, observation := range observations {
		var snapshot CredentialObservationSnapshot
		if err := json.Unmarshal(observation.SnapshotJSON, &snapshot); err != nil {
			continue
		}
		if strings.TrimSpace(snapshot.Plan.Name) == "" {
			continue
		}
		plans[observation.CredentialID] = snapshot.Plan
	}
	return plans
}

// degradationCredentialMask never fails the listing: an undecryptable row still
// has to appear in the table so the operator can remove its monitor.
func (s *Service) degradationCredentialMask(
	group models.Group,
	row models.Credential,
) string {
	fallback := fmt.Sprintf("#%d", row.ID)
	if s == nil || s.encryption == nil {
		return fallback
	}
	canonical, identity, err := s.decodeCredential(group, row)
	if err != nil {
		return fallback
	}
	mask, _, err := s.credentialPresentation(group, row, canonical, identity)
	clear(canonical)
	if err != nil || strings.TrimSpace(mask) == "" {
		return fallback
	}
	return mask
}

func projectDegradationMonitor(
	row models.DegradationMonitor,
	settings models.DegradationSettings,
	monitorContext degradationMonitorContext,
) DegradationMonitorResponse {
	group := monitorContext.groups[row.GroupID]
	credential := monitorContext.credentials[row.CredentialID]
	mask := monitorContext.masks[row.CredentialID]
	if mask == "" {
		mask = fmt.Sprintf("#%d", row.CredentialID)
	}
	response := DegradationMonitorResponse{
		ID: row.ID, GroupID: row.GroupID, GroupName: group.Name, GroupEnabled: group.Enabled,
		ChannelID:      group.ChannelID,
		ConnectionType: string(normalizeGroupConnectionType(group.ConnectionType)),
		CredentialID:   row.CredentialID, CredentialMask: mask,
		CredentialStatus:      string(credential.Status),
		UpstreamModel:         row.UpstreamModel,
		ExpectedModel:         row.ExpectedModel,
		ExpectedModelName:     degradationModelName(row.ExpectedModel),
		ReasoningEffort:       string(row.ReasoningEffort),
		SampleCount:           row.SampleCount,
		MinProbabilityMicros:  row.MinProbabilityMicros,
		IntervalSeconds:       row.IntervalSeconds,
		CooldownSeconds:       row.CooldownIntervalSeconds,
		RunWhenDisabled:       cloneBool(row.RunWhenDisabled),
		Enabled:               row.Enabled,
		State:                 string(row.State),
		StateReason:           row.StateReason,
		StateSinceMS:          row.StateSinceMS,
		LastRunAtMS:           row.LastRunAtMS,
		LastSuccessAtMS:       row.LastSuccessAtMS,
		NextRunAtMS:           row.NextRunAtMS,
		LastProbabilityMicros: row.LastProbabilityMicros,
		LastDetectedModel:     row.LastDetectedModel,
		LastErrorCode:         row.LastErrorCode,
		ConsecutiveErrors:     row.ConsecutiveErrors,
		Note:                  row.Note,

		EffectiveSampleCount:          row.ResolvedSampleCount(settings),
		EffectiveMinProbabilityMicros: row.ResolvedMinProbabilityMicros(settings),
		EffectiveIntervalSeconds:      row.ResolvedIntervalSeconds(settings),
		EffectiveCooldownSeconds:      row.ResolvedCooldownIntervalSeconds(settings),
		EffectiveRunWhenDisabled:      row.ResolvedRunWhenDisabled(settings),

		CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
	}
	if row.LastDetectedModel != "" {
		response.LastDetectedModelName = degradationModelName(row.LastDetectedModel)
	}
	if plan, ok := monitorContext.plans[row.CredentialID]; ok {
		response.PlanName = plan.Name
		response.PlanLevel = plan.Level
	}
	return response
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// ---------------------------------------------------------------------------
// Enrollment
// ---------------------------------------------------------------------------

// EnrollDegradationMonitors adds monitors for whole groups or single
// credentials in one call.
func (s *Service) EnrollDegradationMonitors(
	ctx context.Context,
	request DegradationEnrollRequest,
) (DegradationEnrollResult, error) {
	settings, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationEnrollResult{}, err
	}
	template, err := buildDegradationMonitorTemplate(request)
	if err != nil {
		return DegradationEnrollResult{}, err
	}
	if len(request.Targets) == 0 || len(request.Targets) > degradationMaxEnrollTargets {
		return DegradationEnrollResult{}, app_errors.ErrValidation
	}
	skipExisting := true
	if request.SkipExisting != nil {
		skipExisting = *request.SkipExisting
	}

	result := DegradationEnrollResult{Rejected: make([]DegradationRejectedTarget, 0)}
	var (
		persisted   []models.DegradationMonitor
		groupRows   []models.Group
		credentials []models.Credential
	)
	now := s.currentTime()
	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		result.Created, result.Updated, result.Skipped = 0, 0, 0
		result.Rejected = result.Rejected[:0]
		persisted = persisted[:0]

		expanded, rejected, err := expandDegradationTargets(tx, request.Targets)
		if err != nil {
			return err
		}
		result.Rejected = append(result.Rejected, rejected...)
		if len(expanded) > degradationMaxEnrollTargets {
			return app_errors.ErrValidation
		}
		for _, target := range expanded {
			monitor := template
			monitor.GroupID = target.GroupID
			monitor.CredentialID = target.CredentialID
			monitor.State = models.DegradationStateUnknown
			monitor.StateSinceMS = now.UnixMilli()
			monitor.NextRunAtMS = degradationNextRunAt(monitor, settings, now)

			var existing []models.DegradationMonitor
			if err := tx.
				Where("credential_id = ? AND upstream_model = ?", target.CredentialID, monitor.UpstreamModel).
				Limit(1).Find(&existing).Error; err != nil {
				return err
			}
			if len(existing) == 1 {
				if skipExisting {
					result.Skipped++
					persisted = append(persisted, existing[0])
					continue
				}
				merged := existing[0]
				merged.GroupID = monitor.GroupID
				merged.ExpectedModel = monitor.ExpectedModel
				merged.ReasoningEffort = monitor.ReasoningEffort
				merged.SampleCount = monitor.SampleCount
				merged.MinProbabilityMicros = monitor.MinProbabilityMicros
				merged.IntervalSeconds = monitor.IntervalSeconds
				merged.CooldownIntervalSeconds = monitor.CooldownIntervalSeconds
				merged.RunWhenDisabled = cloneBool(monitor.RunWhenDisabled)
				merged.Note = monitor.Note
				merged.Enabled = true
				merged.NextRunAtMS = degradationNextRunAt(merged, settings, now)
				if err := tx.Model(&models.DegradationMonitor{}).
					Where("id = ?", merged.ID).
					Select(degradationMonitorConfigColumns()).
					Updates(&merged).Error; err != nil {
					return err
				}
				result.Updated++
				persisted = append(persisted, merged)
				continue
			}
			if err := tx.Create(&monitor).Error; err != nil {
				return err
			}
			result.Created++
			persisted = append(persisted, monitor)
		}

		if err := tx.Model(&models.Group{}).Order("id ASC").Find(&groupRows).Error; err != nil {
			return err
		}
		credentialIDs := make([]uint, 0, len(persisted))
		for _, row := range persisted {
			credentialIDs = append(credentialIDs, row.CredentialID)
		}
		if len(credentialIDs) == 0 {
			return nil
		}
		return tx.Where("id IN ?", credentialIDs).Find(&credentials).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return DegradationEnrollResult{}, parentErr
	}
	if err != nil {
		return DegradationEnrollResult{}, err
	}

	monitorContext := s.buildDegradationMonitorContext(groupRows, credentials)
	result.Items = make([]DegradationMonitorResponse, 0, len(persisted))
	for _, row := range persisted {
		result.Items = append(result.Items, projectDegradationMonitor(row, settings, monitorContext))
	}
	return result, nil
}

func degradationMonitorConfigColumns() []string {
	return []string{
		"group_id", "expected_model", "reasoning_effort", "sample_count",
		"min_probability_micros", "interval_seconds", "cooldown_interval_seconds",
		"run_when_disabled", "enabled", "note", "next_run_at_ms", "updated_at_ms",
	}
}

// expandDegradationTargets turns a mixed group/credential selection into the
// concrete credential list, reporting whatever could not be resolved.
func expandDegradationTargets(
	tx *gorm.DB,
	targets []DegradationTargetRequest,
) ([]DegradationTargetRequest, []DegradationRejectedTarget, error) {
	expanded := make([]DegradationTargetRequest, 0, len(targets))
	rejected := make([]DegradationRejectedTarget, 0)
	seen := make(map[uint]struct{}, len(targets))
	for _, target := range targets {
		switch {
		case target.CredentialID > 0:
			var rows []models.Credential
			query := tx.Where("id = ?", target.CredentialID)
			if target.GroupID > 0 {
				query = query.Where("group_id = ?", target.GroupID)
			}
			if err := query.Limit(1).Find(&rows).Error; err != nil {
				return nil, nil, err
			}
			if len(rows) == 0 {
				rejected = append(rejected, DegradationRejectedTarget{
					GroupID: target.GroupID, CredentialID: target.CredentialID,
					Reason: "credential_not_found",
				})
				continue
			}
			if _, duplicate := seen[rows[0].ID]; duplicate {
				continue
			}
			seen[rows[0].ID] = struct{}{}
			expanded = append(expanded, DegradationTargetRequest{
				GroupID: rows[0].GroupID, CredentialID: rows[0].ID,
			})
		case target.GroupID > 0:
			var rows []models.Credential
			if err := tx.Where("group_id = ?", target.GroupID).
				Order("id ASC").Find(&rows).Error; err != nil {
				return nil, nil, err
			}
			if len(rows) == 0 {
				rejected = append(rejected, DegradationRejectedTarget{
					GroupID: target.GroupID, Reason: "group_has_no_credential",
				})
				continue
			}
			for _, row := range rows {
				if _, duplicate := seen[row.ID]; duplicate {
					continue
				}
				seen[row.ID] = struct{}{}
				expanded = append(expanded, DegradationTargetRequest{
					GroupID: row.GroupID, CredentialID: row.ID,
				})
			}
		default:
			rejected = append(rejected, DegradationRejectedTarget{Reason: "target_is_empty"})
		}
	}
	return expanded, rejected, nil
}

func buildDegradationMonitorTemplate(
	request DegradationEnrollRequest,
) (models.DegradationMonitor, error) {
	upstreamModel := strings.TrimSpace(request.UpstreamModel)
	expectedModel := strings.TrimSpace(request.ExpectedModel)
	if upstreamModel == "" || len(upstreamModel) > 255 || expectedModel == "" {
		return models.DegradationMonitor{}, app_errors.ErrValidation
	}
	index, err := degradationBankIndex()
	if err != nil {
		return models.DegradationMonitor{}, fmt.Errorf(
			"load degradation bank: %w", app_errors.ErrInternalServer,
		)
	}
	if _, enrolled := index[expectedModel]; !enrolled {
		return models.DegradationMonitor{}, app_errors.ErrValidation
	}
	effort := models.DegradationReasoningEffort(strings.TrimSpace(request.ReasoningEffort))
	if !effort.Valid() {
		return models.DegradationMonitor{}, app_errors.ErrValidation
	}
	monitor := models.DegradationMonitor{
		UpstreamModel: upstreamModel, ExpectedModel: expectedModel,
		ReasoningEffort: effort, SampleCount: request.SampleCount,
		MinProbabilityMicros:    request.MinProbabilityMicros,
		IntervalSeconds:         request.IntervalSeconds,
		CooldownIntervalSeconds: request.CooldownIntervalSeconds,
		RunWhenDisabled:         cloneBool(request.RunWhenDisabled),
		Enabled:                 true,
		Note:                    strings.TrimSpace(request.Note),
	}
	if err := validateDegradationMonitorOverrides(monitor); err != nil {
		return models.DegradationMonitor{}, err
	}
	return monitor, nil
}

// validateDegradationMonitorOverrides checks the per-monitor overrides. Zero
// always means "inherit the global setting".
func validateDegradationMonitorOverrides(monitor models.DegradationMonitor) error {
	if monitor.SampleCount < 0 || monitor.SampleCount > degradation.MaxSamplesPerRun {
		return app_errors.ErrValidation
	}
	if monitor.MinProbabilityMicros < 0 || monitor.MinProbabilityMicros > probabilityMicrosScale {
		return app_errors.ErrValidation
	}
	for _, seconds := range []int64{monitor.IntervalSeconds, monitor.CooldownIntervalSeconds} {
		if seconds == 0 {
			continue
		}
		if !withinRange(seconds, degradationMinIntervalSeconds, degradationMaxIntervalSeconds) {
			return app_errors.ErrValidation
		}
	}
	if len(monitor.Note) > degradationMaxNoteLength {
		return app_errors.ErrValidation
	}
	return nil
}

// ---------------------------------------------------------------------------
// Single monitor mutations
// ---------------------------------------------------------------------------

// UpdateDegradationMonitor edits one monitor and re-places its next run.
func (s *Service) UpdateDegradationMonitor(
	ctx context.Context,
	monitorID uint,
	request DegradationMonitorUpdateRequest,
) (DegradationMonitorResponse, error) {
	if monitorID == 0 {
		return DegradationMonitorResponse{}, app_errors.ErrValidation
	}
	settings, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationMonitorResponse{}, err
	}
	now := s.currentTime()
	var (
		updated     models.DegradationMonitor
		groupRows   []models.Group
		credentials []models.Credential
	)
	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var rows []models.DegradationMonitor
		if err := tx.Where("id = ?", monitorID).Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return app_errors.ErrResourceNotFound
		}
		merged, err := applyDegradationMonitorRequest(rows[0], request)
		if err != nil {
			return err
		}
		if merged.Enabled {
			merged.NextRunAtMS = degradationNextRunAt(merged, settings, now)
		} else {
			merged.NextRunAtMS = 0
		}
		if err := tx.Model(&models.DegradationMonitor{}).
			Where("id = ?", monitorID).
			Select(append(degradationMonitorConfigColumns(), "upstream_model")).
			Updates(&merged).Error; err != nil {
			return err
		}
		updated = merged
		if err := tx.Model(&models.Group{}).
			Where("id = ?", merged.GroupID).Find(&groupRows).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", merged.CredentialID).Find(&credentials).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return DegradationMonitorResponse{}, parentErr
	}
	if err != nil {
		return DegradationMonitorResponse{}, err
	}
	monitorContext := s.buildDegradationMonitorContext(groupRows, credentials)
	return projectDegradationMonitor(updated, settings, monitorContext), nil
}

func applyDegradationMonitorRequest(
	current models.DegradationMonitor,
	request DegradationMonitorUpdateRequest,
) (models.DegradationMonitor, error) {
	merged := current
	if request.UpstreamModel != nil {
		merged.UpstreamModel = strings.TrimSpace(*request.UpstreamModel)
		if merged.UpstreamModel == "" || len(merged.UpstreamModel) > 255 {
			return models.DegradationMonitor{}, app_errors.ErrValidation
		}
	}
	if request.ExpectedModel != nil {
		expected := strings.TrimSpace(*request.ExpectedModel)
		index, err := degradationBankIndex()
		if err != nil {
			return models.DegradationMonitor{}, fmt.Errorf(
				"load degradation bank: %w", app_errors.ErrInternalServer,
			)
		}
		if _, enrolled := index[expected]; !enrolled {
			return models.DegradationMonitor{}, app_errors.ErrValidation
		}
		merged.ExpectedModel = expected
	}
	if request.ReasoningEffort != nil {
		effort := models.DegradationReasoningEffort(strings.TrimSpace(*request.ReasoningEffort))
		if !effort.Valid() {
			return models.DegradationMonitor{}, app_errors.ErrValidation
		}
		merged.ReasoningEffort = effort
	}
	if request.SampleCount != nil {
		merged.SampleCount = *request.SampleCount
	}
	if request.MinProbabilityMicros != nil {
		merged.MinProbabilityMicros = *request.MinProbabilityMicros
	}
	if request.IntervalSeconds != nil {
		merged.IntervalSeconds = *request.IntervalSeconds
	}
	if request.CooldownIntervalSeconds != nil {
		merged.CooldownIntervalSeconds = *request.CooldownIntervalSeconds
	}
	if request.RunWhenDisabled != nil {
		merged.RunWhenDisabled = cloneBool(request.RunWhenDisabled)
	}
	if request.Enabled != nil {
		merged.Enabled = *request.Enabled
	}
	if request.Note != nil {
		merged.Note = strings.TrimSpace(*request.Note)
	}
	if err := validateDegradationMonitorOverrides(merged); err != nil {
		return models.DegradationMonitor{}, err
	}
	return merged, nil
}

// DeleteDegradationMonitor removes one monitor and its history.
func (s *Service) DeleteDegradationMonitor(ctx context.Context, monitorID uint) error {
	if monitorID == 0 {
		return app_errors.ErrValidation
	}
	return s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("id = ?", monitorID).Delete(&models.DegradationMonitor{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return app_errors.ErrResourceNotFound
		}
		return tx.Where("monitor_id = ?", monitorID).Delete(&models.DegradationRun{}).Error
	})
}

// BatchDegradationMonitors applies one action to a monitor selection.
func (s *Service) BatchDegradationMonitors(
	ctx context.Context,
	request DegradationBatchRequest,
) (DegradationBatchResult, error) {
	if !request.Action.Valid() {
		return DegradationBatchResult{}, app_errors.ErrValidation
	}
	ids := normalizeDegradationIDs(request.MonitorIDs)
	if len(ids) == 0 || len(ids) > degradationMaxBatchMonitors {
		return DegradationBatchResult{}, app_errors.ErrValidation
	}
	if request.Action == DegradationBatchRun {
		return s.queueDegradationManualRuns(ctx, ids)
	}
	settings, err := s.loadDegradationSettings(ctx)
	if err != nil {
		return DegradationBatchResult{}, err
	}
	now := s.currentTime()
	result := DegradationBatchResult{Action: request.Action}
	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		result.Affected = 0
		switch request.Action {
		case DegradationBatchDelete:
			deleted := tx.Where("id IN ?", ids).Delete(&models.DegradationMonitor{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.Affected = int(deleted.RowsAffected)
			return tx.Where("monitor_id IN ?", ids).Delete(&models.DegradationRun{}).Error
		case DegradationBatchDisable:
			disabled := tx.Model(&models.DegradationMonitor{}).Where("id IN ?", ids).
				Updates(map[string]any{
					"enabled": false, "next_run_at_ms": 0, "updated_at_ms": now.UnixMilli(),
				})
			if disabled.Error != nil {
				return disabled.Error
			}
			result.Affected = int(disabled.RowsAffected)
			return nil
		}

		var rows []models.DegradationMonitor
		if err := tx.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			update := map[string]any{"enabled": true, "updated_at_ms": now.UnixMilli()}
			scheduled := row
			scheduled.Enabled = true
			if request.Action == DegradationBatchClear {
				scheduled.State = models.DegradationStateUnknown
				update["state"] = models.DegradationStateUnknown
				update["state_reason"] = ""
				update["state_since_ms"] = now.UnixMilli()
				update["last_error_code"] = ""
				update["consecutive_errors"] = 0
			}
			update["next_run_at_ms"] = degradationNextRunAt(scheduled, settings, now)
			if err := tx.Model(&models.DegradationMonitor{}).
				Where("id = ?", row.ID).Updates(update).Error; err != nil {
				return err
			}
			result.Affected++
		}
		return nil
	})
	if err != nil {
		return DegradationBatchResult{}, err
	}
	return result, nil
}

// queueDegradationManualRuns registers an immediate probe for every selected
// monitor that still exists, and reports how many entries were queued.
//
// 批量检测每条都要打一次真实上游，同步执行会把 HTTP 请求挂到超时，所以这里只
// 登记意向后立即返回，由调度器按全局并发上限逐条消化，结果照常写进运行历史。
func (s *Service) queueDegradationManualRuns(
	ctx context.Context,
	ids []uint,
) (DegradationBatchResult, error) {
	var existing []uint
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Model(&models.DegradationMonitor{}).
			Where("id IN ?", ids).Order("id ASC").Pluck("id", &existing).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return DegradationBatchResult{}, parentErr
	}
	if err != nil {
		return DegradationBatchResult{}, fmt.Errorf(
			"queue degradation runs: %w", app_errors.ErrDatabase,
		)
	}
	if len(existing) == 0 {
		return DegradationBatchResult{}, app_errors.ErrResourceNotFound
	}
	return DegradationBatchResult{
		Action:   DegradationBatchRun,
		Affected: s.degradationRuns.enqueueManual(existing),
	}, nil
}

func normalizeDegradationIDs(values []uint) []uint {
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

// ---------------------------------------------------------------------------
// Run history
// ---------------------------------------------------------------------------

// ListDegradationRuns returns the newest detections of one monitor.
func (s *Service) ListDegradationRuns(
	ctx context.Context,
	monitorID uint,
	limit int,
) (DegradationRunCollectionResponse, error) {
	if monitorID == 0 {
		return DegradationRunCollectionResponse{}, app_errors.ErrValidation
	}
	if limit < 1 || limit > degradationRunHistoryLimit {
		limit = degradationRunHistoryLimit
	}
	var rows []models.DegradationRun
	var statRows []degradationRunStatRow
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.DegradationRun{}).
			Select("outcome", "trigger").
			Where("monitor_id = ?", monitorID).
			Find(&statRows).Error; err != nil {
			return err
		}
		return tx.Where("monitor_id = ?", monitorID).
			Order("id DESC").Limit(limit).Find(&rows).Error
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return DegradationRunCollectionResponse{}, parentErr
	}
	if err != nil {
		return DegradationRunCollectionResponse{}, fmt.Errorf(
			"list degradation runs: %w", app_errors.ErrDatabase,
		)
	}
	items := make([]DegradationRunResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, projectDegradationRun(row))
	}
	return DegradationRunCollectionResponse{
		MonitorID: monitorID, Items: items, Stats: summarizeDegradationRuns(statRows),
	}, nil
}

// degradationRunStatRow is the two columns the history totals need. 历史条数本身
// 受 degradationRunHistoryLimit 约束，所以整段历史扫一遍也只是几十行。
type degradationRunStatRow struct {
	Outcome string
	Trigger string
}

func summarizeDegradationRuns(rows []degradationRunStatRow) DegradationRunStatsResponse {
	stats := DegradationRunStatsResponse{Total: len(rows)}
	for _, row := range rows {
		switch models.DegradationState(row.Outcome) {
		case models.DegradationStateHealthy:
			stats.Healthy++
		case models.DegradationStateDegraded:
			stats.Degraded++
		case models.DegradationStateInconclusive:
			stats.Inconclusive++
		case models.DegradationStateError:
			stats.Error++
		case models.DegradationStateQuotaExhausted:
			stats.QuotaExhausted++
		default:
			stats.Unknown++
		}
		switch models.DegradationTrigger(row.Trigger) {
		case models.DegradationTriggerSchedule:
			stats.Schedule++
		case models.DegradationTriggerManual:
			stats.Manual++
		case models.DegradationTriggerOverload:
			stats.Overload++
		}
	}
	return stats
}

func projectDegradationRun(row models.DegradationRun) DegradationRunResponse {
	response := DegradationRunResponse{
		ID: row.ID, MonitorID: row.MonitorID,
		Trigger: string(row.Trigger), Outcome: string(row.Outcome),
		StartedAtMS: row.StartedAtMS, CompletedAtMS: row.CompletedAtMS,
		DurationMs:    row.DurationMs,
		ExpectedModel: row.ExpectedModel, DetectedModel: row.DetectedModel,
		ExpectedProbabilityMicros: row.ExpectedProbabilityMicros,
		LeadingProbabilityMicros:  row.LeadingProbabilityMicros,
		MinProbabilityMicros:      row.MinProbabilityMicros,
		SampleCount:               row.SampleCount,
		UsedSamples:               row.UsedSamples,
		Attempts:                  row.Attempts,
		Reasons:                   splitDegradationKeywords(row.Reasons),
		ErrorCode:                 row.ErrorCode,
		ErrorSummary:              row.ErrorSummary,
		Ranking:                   make([]DegradationRankingEntry, 0),
		Diagnostics:               make([]DegradationSampleDiagRes, 0),
		Samples:                   make([]DegradationSampleTextRes, 0),
	}
	if row.DetectedModel != "" {
		response.DetectedModelName = degradationModelName(row.DetectedModel)
	}
	if len(row.Detail) > 0 {
		var detail degradationRunDetail
		if err := json.Unmarshal(row.Detail, &detail); err == nil {
			if detail.Ranking != nil {
				response.Ranking = detail.Ranking
			}
			if detail.Diagnostics != nil {
				response.Diagnostics = detail.Diagnostics
			}
			if detail.Samples != nil {
				response.Samples = detail.Samples
			}
		}
	}
	return response
}
