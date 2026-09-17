package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0021 = "0021_codex_turn_state"

type credentialCodexTurnState0021 struct {
	CodexTurnState string `gorm:"column:codex_turn_state;type:varchar(4096);not null;default:''"`
}

func (credentialCodexTurnState0021) TableName() string { return "credentials" }

type attemptTurnState0021 struct {
	UpstreamTurnState string `gorm:"column:upstream_turn_state;type:varchar(4096);not null;default:''"`
}

func (attemptTurnState0021) TableName() string { return "request_log_attempts" }

// Up0021 增加 X-Codex-Turn-State 的两端存储：凭据侧是强制注入的覆盖值，尝试侧是
// 上游回带的观测值。两列都用 varchar 而非 text——MySQL 不允许 TEXT 带 DEFAULT，
// SQLite 又不允许 ADD COLUMN NOT NULL 不带 DEFAULT，varchar(4096) 两边都成立。
func Up0021(db *gorm.DB) error {
	for _, target := range codexTurnStateColumns0021 {
		if !db.Migrator().HasTable(target.model) {
			return fmt.Errorf("add codex turn state: table %q is missing", target.table)
		}
		if db.Migrator().HasColumn(target.model, target.column) {
			continue
		}
		if err := db.Migrator().AddColumn(target.model, target.field); err != nil {
			return fmt.Errorf("add %s.%s: %w", target.table, target.column, err)
		}
	}
	return nil
}

// ValidateRecoverable0021 接受两列各自加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0021(db *gorm.DB) error {
	for _, target := range codexTurnStateColumns0021 {
		if !db.Migrator().HasTable(target.model) {
			return fmt.Errorf("validate codex turn state: table %q is missing", target.table)
		}
		if !db.Migrator().HasColumn(target.model, target.column) {
			continue
		}
		if err := validateCodexTurnStateColumn0021(db, target.table, target.column, target.sqliteNullableUnknown); err != nil {
			return err
		}
	}
	return nil
}

func Validate0021(db *gorm.DB) error {
	if err := ValidateRecoverable0021(db); err != nil {
		return err
	}
	for _, target := range codexTurnStateColumns0021 {
		if !db.Migrator().HasColumn(target.model, target.column) {
			return fmt.Errorf("validate codex turn state: column %q is missing", target.table+"."+target.column)
		}
	}
	return nil
}

var codexTurnStateColumns0021 = []struct {
	model  any
	table  string
	column string
	field  string
	// sqliteNullableUnknown 标记那些在 SQLite 上读不出 NOT NULL 的表。0006 用手写
	// DDL 重建过 request_log_attempts，驱动此后再也解析不出该表的非空约束，0006
	// 自己的校验同样对 SQLite 放行这一项。
	sqliteNullableUnknown bool
}{
	{&credentialCodexTurnState0021{}, "credentials", "codex_turn_state", "CodexTurnState", false},
	{&attemptTurnState0021{}, "request_log_attempts", "upstream_turn_state", "UpstreamTurnState", true},
}

func validateCodexTurnStateColumn0021(db *gorm.DB, table, name string, sqliteNullableUnknown bool) error {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return fmt.Errorf("inspect %s.%s: %w", table, name, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("validate codex turn state: column %q is not textual", table+"."+name)
		}
		skipNullable := sqliteNullableUnknown && strings.EqualFold(db.Dialector.Name(), "sqlite")
		if nullable, known := column.Nullable(); known && nullable && !skipNullable {
			return fmt.Errorf("validate codex turn state: column %q must not be nullable", table+"."+name)
		}
		return nil
	}
	return fmt.Errorf("validate codex turn state: column %q is missing", table+"."+name)
}
