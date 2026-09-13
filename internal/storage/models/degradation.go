package models

// DegradationState is the judged standing of one monitored target. It is
// deliberately separate from Credential.Mark: the monitor page owns this value
// and never writes the operator's manual group-level mark.
type DegradationState string

const (
	// DegradationStateUnknown means the target has not produced a judgement yet.
	DegradationStateUnknown DegradationState = "unknown"
	// DegradationStateHealthy means the last run attributed the expected model.
	DegradationStateHealthy DegradationState = "healthy"
	// DegradationStateDegraded means the last run attributed something else or
	// fell below the confidence floor.
	DegradationStateDegraded DegradationState = "degraded"
	// DegradationStateInconclusive means the responses could not be scored.
	DegradationStateInconclusive DegradationState = "inconclusive"
	// DegradationStateError means the detection call itself failed.
	DegradationStateError DegradationState = "error"
	// DegradationStateQuotaExhausted means the credential reported no remaining
	// quota, so scheduled runs stand down until an operator revisits it.
	DegradationStateQuotaExhausted DegradationState = "quota_exhausted"
)

// Valid reports whether the state is a known value.
func (s DegradationState) Valid() bool {
	switch s {
	case DegradationStateUnknown, DegradationStateHealthy, DegradationStateDegraded,
		DegradationStateInconclusive, DegradationStateError, DegradationStateQuotaExhausted:
		return true
	}
	return false
}

// DegradationTrigger records what caused a run.
type DegradationTrigger string

const (
	// DegradationTriggerSchedule is the periodic sweep.
	DegradationTriggerSchedule DegradationTrigger = "schedule"
	// DegradationTriggerManual is an operator-initiated run.
	DegradationTriggerManual DegradationTrigger = "manual"
	// DegradationTriggerOverload is a run kicked off by an upstream overload error.
	DegradationTriggerOverload DegradationTrigger = "overload"
)

// Valid reports whether the trigger is a known value.
func (t DegradationTrigger) Valid() bool {
	switch t {
	case DegradationTriggerSchedule, DegradationTriggerManual, DegradationTriggerOverload:
		return true
	}
	return false
}

// DegradationReasoningEffort is the thinking budget requested for a detection.
// The empty value leaves the upstream default in place.
type DegradationReasoningEffort string

const (
	DegradationEffortDefault DegradationReasoningEffort = ""
	DegradationEffortMinimal DegradationReasoningEffort = "minimal"
	DegradationEffortLow     DegradationReasoningEffort = "low"
	DegradationEffortMedium  DegradationReasoningEffort = "medium"
	DegradationEffortHigh    DegradationReasoningEffort = "high"
)

// Valid reports whether the effort is a known value.
func (e DegradationReasoningEffort) Valid() bool {
	switch e {
	case DegradationEffortDefault, DegradationEffortMinimal, DegradationEffortLow,
		DegradationEffortMedium, DegradationEffortHigh:
		return true
	}
	return false
}

// DegradationMonitor is one monitored credential/model target. Zero-valued
// override columns inherit from DegradationSettings, so changing a global knob
// moves every target that has not been individually pinned.
type DegradationMonitor struct {
	ID                      uint                       `gorm:"primaryKey;autoIncrement"`
	GroupID                 uint                       `gorm:"not null;check:chk_degradation_monitor_group,group_id > 0;index:idx_degradation_monitors_group"`
	CredentialID            uint                       `gorm:"not null;check:chk_degradation_monitor_credential,credential_id > 0;uniqueIndex:idx_degradation_monitors_target,priority:1"`
	UpstreamModel           string                     `gorm:"type:varchar(255);not null;uniqueIndex:idx_degradation_monitors_target,priority:2"`
	ExpectedModel           string                     `gorm:"type:varchar(64);not null"`
	ReasoningEffort         DegradationReasoningEffort `gorm:"type:varchar(16);not null;default:'';check:chk_degradation_monitor_effort,reasoning_effort IN ('','minimal','low','medium','high')"`
	SampleCount             int                        `gorm:"not null;default:0;check:chk_degradation_monitor_samples,sample_count >= 0 AND sample_count <= 5"`
	MinProbabilityMicros    int64                      `gorm:"column:min_probability_micros;not null;default:0;check:chk_degradation_monitor_threshold,min_probability_micros >= 0 AND min_probability_micros <= 1000000"`
	IntervalSeconds         int64                      `gorm:"not null;default:0;check:chk_degradation_monitor_interval,interval_seconds >= 0"`
	CooldownIntervalSeconds int64                      `gorm:"not null;default:0;check:chk_degradation_monitor_cooldown,cooldown_interval_seconds >= 0"`
	// RunWhenDisabled 为空表示跟随全局；单独设置可以让某个已停用的分组继续被探测，
	// 方便判断它是否已经恢复。
	RunWhenDisabled       *bool            `gorm:"column:run_when_disabled"`
	Enabled               bool             `gorm:"not null;default:true"`
	State                 DegradationState `gorm:"type:varchar(32);not null;default:'unknown';check:chk_degradation_monitor_state,state IN ('unknown','healthy','degraded','inconclusive','error','quota_exhausted')"`
	StateReason           string           `gorm:"type:varchar(64);not null;default:''"`
	StateSinceMS          int64            `gorm:"column:state_since_ms;not null;default:0;check:chk_degradation_monitor_state_since,state_since_ms >= 0"`
	LastRunAtMS           int64            `gorm:"column:last_run_at_ms;not null;default:0;check:chk_degradation_monitor_last_run,last_run_at_ms >= 0"`
	LastSuccessAtMS       int64            `gorm:"column:last_success_at_ms;not null;default:0;check:chk_degradation_monitor_last_success,last_success_at_ms >= 0"`
	NextRunAtMS           int64            `gorm:"column:next_run_at_ms;not null;default:0;check:chk_degradation_monitor_next_run,next_run_at_ms >= 0;index:idx_degradation_monitors_schedule,priority:2"`
	LastProbabilityMicros int64            `gorm:"column:last_probability_micros;not null;default:0;check:chk_degradation_monitor_last_probability,last_probability_micros >= 0 AND last_probability_micros <= 1000000"`
	LastDetectedModel     string           `gorm:"type:varchar(64);not null;default:''"`
	LastErrorCode         string           `gorm:"type:varchar(64);not null;default:''"`
	ConsecutiveErrors     int              `gorm:"not null;default:0;check:chk_degradation_monitor_errors,consecutive_errors >= 0"`
	Note                  string           `gorm:"type:varchar(255);not null;default:''"`
	Group                 *Group           `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Credential            *Credential      `gorm:"foreignKey:CredentialID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS           int64            `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_degradation_monitor_created_at,created_at_ms >= 0"`
	UpdatedAtMS           int64            `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_degradation_monitor_updated_at,updated_at_ms >= 0"`
}

func (DegradationMonitor) TableName() string { return "degradation_monitors" }

// DegradationRun is one recorded detection attempt.
type DegradationRun struct {
	ID                        uint               `gorm:"primaryKey;autoIncrement;index:idx_degradation_runs_monitor,priority:2,sort:desc"`
	MonitorID                 uint               `gorm:"not null;check:chk_degradation_run_monitor,monitor_id > 0;index:idx_degradation_runs_monitor,priority:1"`
	Trigger                   DegradationTrigger `gorm:"type:varchar(16);not null;check:chk_degradation_run_trigger,trigger IN ('schedule','manual','overload')"`
	Outcome                   DegradationState   `gorm:"type:varchar(32);not null;check:chk_degradation_run_outcome,outcome IN ('healthy','degraded','inconclusive','error','quota_exhausted')"`
	StartedAtMS               int64              `gorm:"column:started_at_ms;not null;check:chk_degradation_run_started_at,started_at_ms >= 0"`
	CompletedAtMS             int64              `gorm:"column:completed_at_ms;not null;check:chk_degradation_run_completed_at,completed_at_ms >= 0;index:idx_degradation_runs_completed,sort:desc"`
	DurationMs                int64              `gorm:"not null;default:0;check:chk_degradation_run_duration,duration_ms >= 0"`
	ExpectedModel             string             `gorm:"type:varchar(64);not null;default:''"`
	DetectedModel             string             `gorm:"type:varchar(64);not null;default:''"`
	ExpectedProbabilityMicros int64              `gorm:"column:expected_probability_micros;not null;default:0;check:chk_degradation_run_expected_probability,expected_probability_micros >= 0 AND expected_probability_micros <= 1000000"`
	LeadingProbabilityMicros  int64              `gorm:"column:leading_probability_micros;not null;default:0;check:chk_degradation_run_leading_probability,leading_probability_micros >= 0 AND leading_probability_micros <= 1000000"`
	MinProbabilityMicros      int64              `gorm:"column:min_probability_micros;not null;default:0;check:chk_degradation_run_threshold,min_probability_micros >= 0 AND min_probability_micros <= 1000000"`
	SampleCount               int                `gorm:"not null;default:0;check:chk_degradation_run_samples,sample_count >= 0"`
	UsedSamples               int                `gorm:"not null;default:0;check:chk_degradation_run_used_samples,used_samples >= 0"`
	Attempts                  int                `gorm:"not null;default:0;check:chk_degradation_run_attempts,attempts >= 0"`
	Reasons                   string             `gorm:"type:varchar(160);not null;default:''"`
	ErrorCode                 string             `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary              string             `gorm:"type:text;not null"`
	// Detail 只保存归因排名与采样诊断，不保存上游原文，避免把模型输出长期留存。
	Detail  JSON                `gorm:"type:json"`
	Monitor *DegradationMonitor `gorm:"foreignKey:MonitorID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (DegradationRun) TableName() string { return "degradation_runs" }

// DegradationSettingsID is the primary key of the settings singleton.
const DegradationSettingsID uint = 1

// DegradationSettings is the single row holding every global knob. Column
// defaults are the product defaults; the control plane seeds the row on first
// read so there is exactly one source of truth.
type DegradationSettings struct {
	ID                          uint   `gorm:"primaryKey;check:chk_degradation_settings_singleton,id = 1"`
	Enabled                     bool   `gorm:"not null;default:false"`
	IntervalSeconds             int64  `gorm:"not null;default:21600;check:chk_degradation_settings_interval,interval_seconds >= 300 AND interval_seconds <= 604800"`
	CooldownIntervalSeconds     int64  `gorm:"not null;default:3600;check:chk_degradation_settings_cooldown,cooldown_interval_seconds >= 300 AND cooldown_interval_seconds <= 604800"`
	SampleCount                 int    `gorm:"not null;default:1;check:chk_degradation_settings_samples,sample_count >= 1 AND sample_count <= 5"`
	MinProbabilityMicros        int64  `gorm:"column:min_probability_micros;not null;default:750000;check:chk_degradation_settings_threshold,min_probability_micros >= 1 AND min_probability_micros <= 1000000"`
	RunWhenDisabled             bool   `gorm:"column:run_when_disabled;not null;default:true"`
	SkipQuotaExhausted          bool   `gorm:"not null;default:true"`
	MaxConcurrentRuns           int    `gorm:"not null;default:2;check:chk_degradation_settings_concurrency,max_concurrent_runs >= 1 AND max_concurrent_runs <= 16"`
	RequestTimeoutSeconds       int64  `gorm:"not null;default:300;check:chk_degradation_settings_timeout,request_timeout_seconds >= 30 AND request_timeout_seconds <= 1800"`
	RetryLimit                  int    `gorm:"not null;default:1;check:chk_degradation_settings_retry,retry_limit >= 0 AND retry_limit <= 5"`
	QuarantineErrorThreshold    int    `gorm:"not null;default:5;check:chk_degradation_settings_quarantine,quarantine_error_threshold >= 0 AND quarantine_error_threshold <= 100"`
	HistoryRetentionDays        int    `gorm:"not null;default:30;check:chk_degradation_settings_retention,history_retention_days >= 1 AND history_retention_days <= 365"`
	OverloadTriggerEnabled      bool   `gorm:"not null;default:false"`
	OverloadKeywords            string `gorm:"type:varchar(512);not null;default:'overload'"`
	OverloadRetryLimit          int    `gorm:"not null;default:2;check:chk_degradation_settings_overload_retry,overload_retry_limit >= 0 AND overload_retry_limit <= 5"`
	OverloadScanIntervalSeconds int64  `gorm:"not null;default:30;check:chk_degradation_settings_overload_scan,overload_scan_interval_seconds >= 5 AND overload_scan_interval_seconds <= 3600"`
	OverloadDebounceSeconds     int64  `gorm:"not null;default:600;check:chk_degradation_settings_overload_debounce,overload_debounce_seconds >= 0 AND overload_debounce_seconds <= 86400"`
	OverloadCursorMS            int64  `gorm:"column:overload_cursor_ms;not null;default:0;check:chk_degradation_settings_overload_cursor,overload_cursor_ms >= 0"`
	// 通知通道目前只占位：开关与事件选择可以保存，实际投递尚未接入。
	NotifyTelegramEnabled bool  `gorm:"not null;default:false"`
	NotifyEmailEnabled    bool  `gorm:"not null;default:false"`
	NotifyOnDegraded      bool  `gorm:"not null;default:true"`
	NotifyOnRecovered     bool  `gorm:"not null;default:true"`
	NotifyOnError         bool  `gorm:"not null;default:false"`
	UpdatedAtMS           int64 `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_degradation_settings_updated_at,updated_at_ms >= 0"`
}

func (DegradationSettings) TableName() string { return "degradation_settings" }

// DefaultDegradationSettings returns the seeded singleton.
func DefaultDegradationSettings() DegradationSettings {
	return DegradationSettings{
		ID:                          DegradationSettingsID,
		Enabled:                     false,
		IntervalSeconds:             21600,
		CooldownIntervalSeconds:     3600,
		SampleCount:                 1,
		MinProbabilityMicros:        750000,
		RunWhenDisabled:             true,
		SkipQuotaExhausted:          true,
		MaxConcurrentRuns:           2,
		RequestTimeoutSeconds:       300,
		RetryLimit:                  1,
		QuarantineErrorThreshold:    5,
		HistoryRetentionDays:        30,
		OverloadTriggerEnabled:      false,
		OverloadKeywords:            "overload",
		OverloadRetryLimit:          2,
		OverloadScanIntervalSeconds: 30,
		OverloadDebounceSeconds:     600,
		NotifyOnDegraded:            true,
		NotifyOnRecovered:           true,
	}
}

// ResolvedSampleCount resolves the per-monitor override against the global value.
func (m DegradationMonitor) ResolvedSampleCount(settings DegradationSettings) int {
	if m.SampleCount > 0 {
		return m.SampleCount
	}
	return settings.SampleCount
}

// ResolvedMinProbabilityMicros resolves the confidence floor.
func (m DegradationMonitor) ResolvedMinProbabilityMicros(settings DegradationSettings) int64 {
	if m.MinProbabilityMicros > 0 {
		return m.MinProbabilityMicros
	}
	return settings.MinProbabilityMicros
}

// ResolvedIntervalSeconds resolves the healthy-state cadence.
func (m DegradationMonitor) ResolvedIntervalSeconds(settings DegradationSettings) int64 {
	if m.IntervalSeconds > 0 {
		return m.IntervalSeconds
	}
	return settings.IntervalSeconds
}

// ResolvedCooldownIntervalSeconds resolves the cadence used while a target is
// degraded or its group is disabled, so recovery is noticed sooner than a full
// healthy interval would allow.
func (m DegradationMonitor) ResolvedCooldownIntervalSeconds(settings DegradationSettings) int64 {
	if m.CooldownIntervalSeconds > 0 {
		return m.CooldownIntervalSeconds
	}
	return settings.CooldownIntervalSeconds
}

// ResolvedRunWhenDisabled resolves whether a disabled target is still probed.
func (m DegradationMonitor) ResolvedRunWhenDisabled(settings DegradationSettings) bool {
	if m.RunWhenDisabled != nil {
		return *m.RunWhenDisabled
	}
	return settings.RunWhenDisabled
}
