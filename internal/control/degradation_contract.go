package control

import (
	"gpt-load/internal/storage/models"
)

// DegradationSettingsResponse is the single global configuration block. Every
// per-monitor override inherits from it, so the monitor page can present one
// obvious place to change the behavior of every target at once.
type DegradationSettingsResponse struct {
	Enabled                     bool     `json:"enabled"`
	IntervalSeconds             int64    `json:"interval_seconds"`
	CooldownIntervalSeconds     int64    `json:"cooldown_interval_seconds"`
	SampleCount                 int      `json:"sample_count"`
	MinProbabilityMicros        int64    `json:"min_probability_micros"`
	RunWhenDisabled             bool     `json:"run_when_disabled"`
	SkipQuotaExhausted          bool     `json:"skip_quota_exhausted"`
	MaxConcurrentRuns           int      `json:"max_concurrent_runs"`
	RequestTimeoutSeconds       int64    `json:"request_timeout_seconds"`
	RetryLimit                  int      `json:"retry_limit"`
	QuarantineErrorThreshold    int      `json:"quarantine_error_threshold"`
	HistoryRetentionDays        int      `json:"history_retention_days"`
	OverloadTriggerEnabled      bool     `json:"overload_trigger_enabled"`
	OverloadKeywords            []string `json:"overload_keywords"`
	OverloadRetryLimit          int      `json:"overload_retry_limit"`
	OverloadScanIntervalSeconds int64    `json:"overload_scan_interval_seconds"`
	OverloadDebounceSeconds     int64    `json:"overload_debounce_seconds"`
	// 通知开关目前只落库占位，投递尚未接入。
	NotifyTelegramEnabled bool  `json:"notify_telegram_enabled"`
	NotifyEmailEnabled    bool  `json:"notify_email_enabled"`
	NotifyOnDegraded      bool  `json:"notify_on_degraded"`
	NotifyOnRecovered     bool  `json:"notify_on_recovered"`
	NotifyOnError         bool  `json:"notify_on_error"`
	UpdatedAtMS           int64 `json:"updated_at_ms"`
}

// DegradationSettingsRequest is a partial update. Omitted fields keep their
// stored value so adding a knob never invalidates an older client payload.
type DegradationSettingsRequest struct {
	Enabled                     *bool     `json:"enabled"`
	IntervalSeconds             *int64    `json:"interval_seconds"`
	CooldownIntervalSeconds     *int64    `json:"cooldown_interval_seconds"`
	SampleCount                 *int      `json:"sample_count"`
	MinProbabilityMicros        *int64    `json:"min_probability_micros"`
	RunWhenDisabled             *bool     `json:"run_when_disabled"`
	SkipQuotaExhausted          *bool     `json:"skip_quota_exhausted"`
	MaxConcurrentRuns           *int      `json:"max_concurrent_runs"`
	RequestTimeoutSeconds       *int64    `json:"request_timeout_seconds"`
	RetryLimit                  *int      `json:"retry_limit"`
	QuarantineErrorThreshold    *int      `json:"quarantine_error_threshold"`
	HistoryRetentionDays        *int      `json:"history_retention_days"`
	OverloadTriggerEnabled      *bool     `json:"overload_trigger_enabled"`
	OverloadKeywords            *[]string `json:"overload_keywords"`
	OverloadRetryLimit          *int      `json:"overload_retry_limit"`
	OverloadScanIntervalSeconds *int64    `json:"overload_scan_interval_seconds"`
	OverloadDebounceSeconds     *int64    `json:"overload_debounce_seconds"`
	NotifyTelegramEnabled       *bool     `json:"notify_telegram_enabled"`
	NotifyEmailEnabled          *bool     `json:"notify_email_enabled"`
	NotifyOnDegraded            *bool     `json:"notify_on_degraded"`
	NotifyOnRecovered           *bool     `json:"notify_on_recovered"`
	NotifyOnError               *bool     `json:"notify_on_error"`
}

// DegradationBankModelResponse is one enrolled fingerprint from the embedded
// reference bank. The expected model of a monitor must be one of these.
type DegradationBankModelResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Family      string `json:"family"`
	FamilyName  string `json:"family_name"`
}

// DegradationCatalogResponse describes what the detector can do, so the UI does
// not have to hardcode any of it.
type DegradationCatalogResponse struct {
	Models             []DegradationBankModelResponse `json:"models"`
	Efforts            []string                       `json:"efforts"`
	States             []string                       `json:"states"`
	RecommendedSamples int                            `json:"recommended_samples"`
	MaxSamples         int                            `json:"max_samples"`
	CalibratedSamples  int                            `json:"calibrated_samples"`
	Method             string                         `json:"method"`
	BuiltAt            string                         `json:"built_at"`
}

// DegradationOverviewResponse is the one payload the monitor page needs at
// mount: what the detector can do, and how it is currently configured.
type DegradationOverviewResponse struct {
	Settings DegradationSettingsResponse `json:"settings"`
	Catalog  DegradationCatalogResponse  `json:"catalog"`
}

// DegradationSummaryResponse is the headline strip of the monitor page.
type DegradationSummaryResponse struct {
	Total          int `json:"total"`
	Enabled        int `json:"enabled"`
	Healthy        int `json:"healthy"`
	Degraded       int `json:"degraded"`
	Inconclusive   int `json:"inconclusive"`
	Error          int `json:"error"`
	QuotaExhausted int `json:"quota_exhausted"`
	Unknown        int `json:"unknown"`
}

// DegradationMonitorResponse is one monitored credential/model target.
// Effective* fields are the values after inheritance, so the table can show what
// will actually happen without replaying the inheritance rules in the browser.
type DegradationMonitorResponse struct {
	ID                    uint   `json:"id"`
	GroupID               uint   `json:"group_id"`
	GroupName             string `json:"group_name"`
	GroupEnabled          bool   `json:"group_enabled"`
	ChannelID             string `json:"channel_id"`
	ConnectionType        string `json:"connection_type"`
	CredentialID          uint   `json:"credential_id"`
	CredentialMask        string `json:"credential_mask"`
	CredentialStatus      string `json:"credential_status"`
	UpstreamModel         string `json:"upstream_model"`
	ExpectedModel         string `json:"expected_model"`
	ExpectedModelName     string `json:"expected_model_name"`
	ReasoningEffort       string `json:"reasoning_effort"`
	SampleCount           int    `json:"sample_count"`
	MinProbabilityMicros  int64  `json:"min_probability_micros"`
	IntervalSeconds       int64  `json:"interval_seconds"`
	CooldownSeconds       int64  `json:"cooldown_interval_seconds"`
	RunWhenDisabled       *bool  `json:"run_when_disabled"`
	Enabled               bool   `json:"enabled"`
	State                 string `json:"state"`
	StateReason           string `json:"state_reason"`
	StateSinceMS          int64  `json:"state_since_ms"`
	LastRunAtMS           int64  `json:"last_run_at_ms"`
	LastSuccessAtMS       int64  `json:"last_success_at_ms"`
	NextRunAtMS           int64  `json:"next_run_at_ms"`
	LastProbabilityMicros int64  `json:"last_probability_micros"`
	LastDetectedModel     string `json:"last_detected_model"`
	LastDetectedModelName string `json:"last_detected_model_name"`
	LastErrorCode         string `json:"last_error_code"`
	ConsecutiveErrors     int    `json:"consecutive_errors"`
	Note                  string `json:"note"`

	EffectiveSampleCount          int   `json:"effective_sample_count"`
	EffectiveMinProbabilityMicros int64 `json:"effective_min_probability_micros"`
	EffectiveIntervalSeconds      int64 `json:"effective_interval_seconds"`
	EffectiveCooldownSeconds      int64 `json:"effective_cooldown_interval_seconds"`
	EffectiveRunWhenDisabled      bool  `json:"effective_run_when_disabled"`

	CreatedAtMS int64 `json:"created_at_ms"`
	UpdatedAtMS int64 `json:"updated_at_ms"`
}

// DegradationMonitorCollectionResponse is the paginated monitor table.
type DegradationMonitorCollectionResponse struct {
	ObservedAtMS int64                        `json:"observed_at_ms"`
	Settings     DegradationSettingsResponse  `json:"settings"`
	Summary      DegradationSummaryResponse   `json:"summary"`
	Items        []DegradationMonitorResponse `json:"items"`
	Pagination   CredentialPaginationResponse `json:"pagination"`
}

// DegradationMonitorQuery filters the monitor table.
type DegradationMonitorQuery struct {
	Query    string
	State    string
	GroupID  uint
	Enabled  *bool
	Page     int
	PageSize int
}

// DegradationTargetRequest names one target to enroll. A credential-only entry
// enrolls that credential; a group-only entry enrolls every credential of the
// group, which is what the batch-add drawer sends.
type DegradationTargetRequest struct {
	GroupID      uint `json:"group_id"`
	CredentialID uint `json:"credential_id"`
}

// DegradationEnrollRequest adds monitors in bulk. The detection parameters are
// shared by every target in the call; per-target tuning happens afterwards.
type DegradationEnrollRequest struct {
	Targets                 []DegradationTargetRequest `json:"targets"`
	UpstreamModel           string                     `json:"upstream_model"`
	ExpectedModel           string                     `json:"expected_model"`
	ReasoningEffort         string                     `json:"reasoning_effort"`
	SampleCount             int                        `json:"sample_count"`
	MinProbabilityMicros    int64                      `json:"min_probability_micros"`
	IntervalSeconds         int64                      `json:"interval_seconds"`
	CooldownIntervalSeconds int64                      `json:"cooldown_interval_seconds"`
	RunWhenDisabled         *bool                      `json:"run_when_disabled"`
	Note                    string                     `json:"note"`
	// SkipExisting 默认 true：重复加入时保留已有配置，而不是悄悄覆盖。
	SkipExisting *bool `json:"skip_existing"`
}

// DegradationEnrollResult reports what an enrollment actually changed.
type DegradationEnrollResult struct {
	Created  int                          `json:"created"`
	Updated  int                          `json:"updated"`
	Skipped  int                          `json:"skipped"`
	Rejected []DegradationRejectedTarget  `json:"rejected"`
	Items    []DegradationMonitorResponse `json:"items"`
}

// DegradationRejectedTarget explains one target that could not be enrolled.
type DegradationRejectedTarget struct {
	GroupID      uint   `json:"group_id"`
	CredentialID uint   `json:"credential_id"`
	Reason       string `json:"reason"`
}

// DegradationMonitorUpdateRequest edits one monitor. Omitted fields keep their
// stored value; a zero override value means "inherit the global setting".
type DegradationMonitorUpdateRequest struct {
	UpstreamModel           *string `json:"upstream_model"`
	ExpectedModel           *string `json:"expected_model"`
	ReasoningEffort         *string `json:"reasoning_effort"`
	SampleCount             *int    `json:"sample_count"`
	MinProbabilityMicros    *int64  `json:"min_probability_micros"`
	IntervalSeconds         *int64  `json:"interval_seconds"`
	CooldownIntervalSeconds *int64  `json:"cooldown_interval_seconds"`
	RunWhenDisabled         *bool   `json:"run_when_disabled"`
	Enabled                 *bool   `json:"enabled"`
	Note                    *string `json:"note"`
}

// DegradationBatchAction is a bulk operation on selected monitors.
type DegradationBatchAction string

const (
	DegradationBatchEnable  DegradationBatchAction = "enable"
	DegradationBatchDisable DegradationBatchAction = "disable"
	DegradationBatchDelete  DegradationBatchAction = "delete"
	// DegradationBatchClear 把状态与错误计数复位为 unknown，并立即安排下一次检测。
	DegradationBatchClear DegradationBatchAction = "clear"
)

// Valid reports whether the batch action is recognized.
func (a DegradationBatchAction) Valid() bool {
	switch a {
	case DegradationBatchEnable, DegradationBatchDisable,
		DegradationBatchDelete, DegradationBatchClear:
		return true
	}
	return false
}

// DegradationBatchRequest applies one action to a monitor selection.
type DegradationBatchRequest struct {
	Action     DegradationBatchAction `json:"action"`
	MonitorIDs []uint                 `json:"monitor_ids"`
}

// DegradationBatchResult reports how many monitors the action touched.
type DegradationBatchResult struct {
	Action   DegradationBatchAction `json:"action"`
	Affected int                    `json:"affected"`
}

// DegradationRunResponse is one recorded detection.
type DegradationRunResponse struct {
	ID                        uint                       `json:"id"`
	MonitorID                 uint                       `json:"monitor_id"`
	Trigger                   string                     `json:"trigger"`
	Outcome                   string                     `json:"outcome"`
	StartedAtMS               int64                      `json:"started_at_ms"`
	CompletedAtMS             int64                      `json:"completed_at_ms"`
	DurationMs                int64                      `json:"duration_ms"`
	ExpectedModel             string                     `json:"expected_model"`
	DetectedModel             string                     `json:"detected_model"`
	DetectedModelName         string                     `json:"detected_model_name"`
	ExpectedProbabilityMicros int64                      `json:"expected_probability_micros"`
	LeadingProbabilityMicros  int64                      `json:"leading_probability_micros"`
	MinProbabilityMicros      int64                      `json:"min_probability_micros"`
	SampleCount               int                        `json:"sample_count"`
	UsedSamples               int                        `json:"used_samples"`
	Attempts                  int                        `json:"attempts"`
	Reasons                   []string                   `json:"reasons"`
	ErrorCode                 string                     `json:"error_code"`
	ErrorSummary              string                     `json:"error_summary"`
	Ranking                   []DegradationRankingEntry  `json:"ranking"`
	Diagnostics               []DegradationSampleDiagRes `json:"diagnostics"`
}

// DegradationRankingEntry is one scored candidate model of a run.
type DegradationRankingEntry struct {
	Model             string `json:"model"`
	DisplayName       string `json:"display_name"`
	Family            string `json:"family"`
	ProbabilityMicros int64  `json:"probability_micros"`
}

// DegradationSampleDiagRes explains why one sample was accepted or dropped.
type DegradationSampleDiagRes struct {
	Index          int  `json:"index"`
	ParsedNumbers  int  `json:"parsed_numbers"`
	MinimumNumbers int  `json:"minimum_numbers"`
	Accepted       bool `json:"accepted"`
}

// DegradationRunCollectionResponse is one monitor's detection history.
type DegradationRunCollectionResponse struct {
	MonitorID uint                     `json:"monitor_id"`
	Items     []DegradationRunResponse `json:"items"`
}

// degradationRunDetail is the JSON persisted on a run row. It keeps the scoring
// evidence and never the upstream text.
type degradationRunDetail struct {
	Ranking     []DegradationRankingEntry  `json:"ranking"`
	Diagnostics []DegradationSampleDiagRes `json:"diagnostics"`
}

func projectDegradationSettings(row models.DegradationSettings) DegradationSettingsResponse {
	return DegradationSettingsResponse{
		Enabled:                     row.Enabled,
		IntervalSeconds:             row.IntervalSeconds,
		CooldownIntervalSeconds:     row.CooldownIntervalSeconds,
		SampleCount:                 row.SampleCount,
		MinProbabilityMicros:        row.MinProbabilityMicros,
		RunWhenDisabled:             row.RunWhenDisabled,
		SkipQuotaExhausted:          row.SkipQuotaExhausted,
		MaxConcurrentRuns:           row.MaxConcurrentRuns,
		RequestTimeoutSeconds:       row.RequestTimeoutSeconds,
		RetryLimit:                  row.RetryLimit,
		QuarantineErrorThreshold:    row.QuarantineErrorThreshold,
		HistoryRetentionDays:        row.HistoryRetentionDays,
		OverloadTriggerEnabled:      row.OverloadTriggerEnabled,
		OverloadKeywords:            splitDegradationKeywords(row.OverloadKeywords),
		OverloadRetryLimit:          row.OverloadRetryLimit,
		OverloadScanIntervalSeconds: row.OverloadScanIntervalSeconds,
		OverloadDebounceSeconds:     row.OverloadDebounceSeconds,
		NotifyTelegramEnabled:       row.NotifyTelegramEnabled,
		NotifyEmailEnabled:          row.NotifyEmailEnabled,
		NotifyOnDegraded:            row.NotifyOnDegraded,
		NotifyOnRecovered:           row.NotifyOnRecovered,
		NotifyOnError:               row.NotifyOnError,
		UpdatedAtMS:                 row.UpdatedAtMS,
	}
}
