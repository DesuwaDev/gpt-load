package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ID0019 adds the model-degradation ("降智") monitor schema: monitored targets,
// their run history, and the single settings row driving the scheduler.
const ID0019 = "0019_degradation_monitors"

type degradationMonitor0019 struct {
	ID                      uint               `gorm:"primaryKey;autoIncrement"`
	GroupID                 uint               `gorm:"not null;check:chk_degradation_monitor_group,group_id > 0;index:idx_degradation_monitors_group"`
	CredentialID            uint               `gorm:"not null;check:chk_degradation_monitor_credential,credential_id > 0;uniqueIndex:idx_degradation_monitors_target,priority:1"`
	UpstreamModel           string             `gorm:"type:varchar(255);not null;uniqueIndex:idx_degradation_monitors_target,priority:2"`
	ExpectedModel           string             `gorm:"type:varchar(64);not null"`
	ReasoningEffort         string             `gorm:"type:varchar(16);not null;default:'';check:chk_degradation_monitor_effort,reasoning_effort IN ('','minimal','low','medium','high')"`
	SampleCount             int                `gorm:"not null;default:0;check:chk_degradation_monitor_samples,sample_count >= 0 AND sample_count <= 5"`
	MinProbabilityMicros    int64              `gorm:"column:min_probability_micros;not null;default:0;check:chk_degradation_monitor_threshold,min_probability_micros >= 0 AND min_probability_micros <= 1000000"`
	IntervalSeconds         int64              `gorm:"not null;default:0;check:chk_degradation_monitor_interval,interval_seconds >= 0"`
	CooldownIntervalSeconds int64              `gorm:"not null;default:0;check:chk_degradation_monitor_cooldown,cooldown_interval_seconds >= 0"`
	RunWhenDisabled         *bool              `gorm:"column:run_when_disabled"`
	Enabled                 bool               `gorm:"not null;default:true"`
	State                   string             `gorm:"type:varchar(32);not null;default:'unknown';check:chk_degradation_monitor_state,state IN ('unknown','healthy','degraded','inconclusive','error','quota_exhausted')"`
	StateReason             string             `gorm:"type:varchar(64);not null;default:''"`
	StateSinceMS            int64              `gorm:"column:state_since_ms;not null;default:0;check:chk_degradation_monitor_state_since,state_since_ms >= 0"`
	LastRunAtMS             int64              `gorm:"column:last_run_at_ms;not null;default:0;check:chk_degradation_monitor_last_run,last_run_at_ms >= 0"`
	LastSuccessAtMS         int64              `gorm:"column:last_success_at_ms;not null;default:0;check:chk_degradation_monitor_last_success,last_success_at_ms >= 0"`
	NextRunAtMS             int64              `gorm:"column:next_run_at_ms;not null;default:0;check:chk_degradation_monitor_next_run,next_run_at_ms >= 0;index:idx_degradation_monitors_schedule,priority:2"`
	LastProbabilityMicros   int64              `gorm:"column:last_probability_micros;not null;default:0;check:chk_degradation_monitor_last_probability,last_probability_micros >= 0 AND last_probability_micros <= 1000000"`
	LastDetectedModel       string             `gorm:"type:varchar(64);not null;default:''"`
	LastErrorCode           string             `gorm:"type:varchar(64);not null;default:''"`
	ConsecutiveErrors       int                `gorm:"not null;default:0;check:chk_degradation_monitor_errors,consecutive_errors >= 0"`
	Note                    string             `gorm:"type:varchar(255);not null;default:''"`
	Group                   *initialGroup      `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Credential              *initialCredential `gorm:"foreignKey:CredentialID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS             int64              `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_degradation_monitor_created_at,created_at_ms >= 0"`
	UpdatedAtMS             int64              `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_degradation_monitor_updated_at,updated_at_ms >= 0"`
}

func (degradationMonitor0019) TableName() string { return "degradation_monitors" }

type degradationRun0019 struct {
	ID                        uint                    `gorm:"primaryKey;autoIncrement;index:idx_degradation_runs_monitor,priority:2,sort:desc"`
	MonitorID                 uint                    `gorm:"not null;check:chk_degradation_run_monitor,monitor_id > 0;index:idx_degradation_runs_monitor,priority:1"`
	Trigger                   string                  `gorm:"type:varchar(16);not null;check:chk_degradation_run_trigger,trigger IN ('schedule','manual','overload')"`
	Outcome                   string                  `gorm:"type:varchar(32);not null;check:chk_degradation_run_outcome,outcome IN ('healthy','degraded','inconclusive','error','quota_exhausted')"`
	StartedAtMS               int64                   `gorm:"column:started_at_ms;not null;check:chk_degradation_run_started_at,started_at_ms >= 0"`
	CompletedAtMS             int64                   `gorm:"column:completed_at_ms;not null;check:chk_degradation_run_completed_at,completed_at_ms >= 0;index:idx_degradation_runs_completed,sort:desc"`
	DurationMs                int64                   `gorm:"not null;default:0;check:chk_degradation_run_duration,duration_ms >= 0"`
	ExpectedModel             string                  `gorm:"type:varchar(64);not null;default:''"`
	DetectedModel             string                  `gorm:"type:varchar(64);not null;default:''"`
	ExpectedProbabilityMicros int64                   `gorm:"column:expected_probability_micros;not null;default:0;check:chk_degradation_run_expected_probability,expected_probability_micros >= 0 AND expected_probability_micros <= 1000000"`
	LeadingProbabilityMicros  int64                   `gorm:"column:leading_probability_micros;not null;default:0;check:chk_degradation_run_leading_probability,leading_probability_micros >= 0 AND leading_probability_micros <= 1000000"`
	MinProbabilityMicros      int64                   `gorm:"column:min_probability_micros;not null;default:0;check:chk_degradation_run_threshold,min_probability_micros >= 0 AND min_probability_micros <= 1000000"`
	SampleCount               int                     `gorm:"not null;default:0;check:chk_degradation_run_samples,sample_count >= 0"`
	UsedSamples               int                     `gorm:"not null;default:0;check:chk_degradation_run_used_samples,used_samples >= 0"`
	Attempts                  int                     `gorm:"not null;default:0;check:chk_degradation_run_attempts,attempts >= 0"`
	Reasons                   string                  `gorm:"type:varchar(160);not null;default:''"`
	ErrorCode                 string                  `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary              string                  `gorm:"type:text;not null"`
	Detail                    initialJSON             `gorm:"type:json"`
	Monitor                   *degradationMonitor0019 `gorm:"foreignKey:MonitorID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (degradationRun0019) TableName() string { return "degradation_runs" }

type degradationSettings0019 struct {
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
	NotifyTelegramEnabled       bool   `gorm:"not null;default:false"`
	NotifyEmailEnabled          bool   `gorm:"not null;default:false"`
	NotifyOnDegraded            bool   `gorm:"not null;default:true"`
	NotifyOnRecovered           bool   `gorm:"not null;default:true"`
	NotifyOnError               bool   `gorm:"not null;default:false"`
	UpdatedAtMS                 int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_degradation_settings_updated_at,updated_at_ms >= 0"`
}

func (degradationSettings0019) TableName() string { return "degradation_settings" }

// Up0019 creates the degradation monitor schema. The settings singleton is left
// for the control plane to seed so its defaults live in one place.
func Up0019(db *gorm.DB) error {
	if err := db.AutoMigrate(SchemaModels0019()...); err != nil {
		return fmt.Errorf("create degradation schema: %w", err)
	}
	return nil
}

// SchemaModels0019 returns the migration-local models in deterministic DDL order.
func SchemaModels0019() []any {
	return []any{
		&degradationSettings0019{},
		&degradationMonitor0019{},
		&degradationRun0019{},
	}
}

// TableNames0019 returns the application tables created by this migration.
func TableNames0019() []string {
	return []string{
		"degradation_settings",
		"degradation_monitors",
		"degradation_runs",
	}
}

type schemaDefinition0019 struct {
	model       any
	table       string
	columns     map[string]struct{}
	indexes     []string
	constraints []string
}

func schemaDefinitions0019(db *gorm.DB) ([]schemaDefinition0019, error) {
	definitions := make([]schemaDefinition0019, 0, len(SchemaModels0019()))
	for _, model := range SchemaModels0019() {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return nil, fmt.Errorf("parse degradation schema model: %w", err)
		}
		definition := schemaDefinition0019{
			model: model, table: statement.Schema.Table, columns: make(map[string]struct{}),
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName != "" {
				definition.columns[strings.ToLower(field.DBName)] = struct{}{}
			}
		}
		for _, index := range statement.Schema.ParseIndexes() {
			definition.indexes = append(definition.indexes, index.Name)
		}
		for name := range statement.Schema.ParseCheckConstraints() {
			definition.constraints = append(definition.constraints, name)
		}
		for name := range statement.Schema.ParseUniqueConstraints() {
			definition.constraints = append(definition.constraints, name)
		}
		for _, relationship := range statement.Schema.Relationships.Relations {
			if constraint := relationship.ParseConstraint(); constraint != nil {
				definition.constraints = append(definition.constraints, constraint.Name)
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// ValidateRecoverable0019 rejects unsafe partial MySQL migration state.
func ValidateRecoverable0019(db *gorm.DB) error {
	definitions, err := schemaDefinitions0019(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			continue
		}
		var count int64
		if err := db.Table(definition.table).Count(&count).Error; err != nil {
			return fmt.Errorf("count interrupted degradation table %q: %w", definition.table, err)
		}
		if count != 0 {
			return fmt.Errorf("table %q contains data", definition.table)
		}
		columns, err := db.Migrator().ColumnTypes(definition.table)
		if err != nil {
			return fmt.Errorf("inspect interrupted degradation table %q: %w", definition.table, err)
		}
		for _, column := range columns {
			if _, expected := definition.columns[strings.ToLower(column.Name())]; !expected {
				return fmt.Errorf("table %q contains unexpected column %q", definition.table, column.Name())
			}
		}
	}
	return nil
}

// Validate0019 verifies the tables, columns, indexes, and constraints owned by 0019.
func Validate0019(db *gorm.DB) error {
	definitions, err := schemaDefinitions0019(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate degradation schema: table %q is missing", definition.table)
		}
		for column := range definition.columns {
			if !db.Migrator().HasColumn(definition.model, column) {
				return fmt.Errorf("validate degradation schema: column %q.%q is missing", definition.table, column)
			}
		}
		for _, index := range definition.indexes {
			if !db.Migrator().HasIndex(definition.model, index) {
				return fmt.Errorf("validate degradation schema: index %q on %q is missing", index, definition.table)
			}
		}
		for _, constraint := range definition.constraints {
			if !db.Migrator().HasConstraint(definition.model, constraint) {
				return fmt.Errorf("validate degradation schema: constraint %q on %q is missing", constraint, definition.table)
			}
		}
	}
	return nil
}
