package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0022 = "0022_codex_turn_state_scope"

type credentialTurnStateModels0022 struct {
	CodexTurnStateModels string `gorm:"column:codex_turn_state_models;type:varchar(1024);not null;default:''"`
}

func (credentialTurnStateModels0022) TableName() string { return "credentials" }

type attemptInjectedTurnState0022 struct {
	InjectedTurnState string `gorm:"column:injected_turn_state;type:varchar(4096);not null;default:''"`
}

func (attemptInjectedTurnState0022) TableName() string { return "request_log_attempts" }

// Up0022 给 0021 的两端各补一列：凭据侧限定注入生效的模型名单，尝试侧记录本次
// 实际注入的值，请求日志据此能看出注入有没有发生。列类型仍用 varchar，理由同
// 0021——MySQL 不允许 TEXT 带 DEFAULT，SQLite 不允许 ADD COLUMN NOT NULL 不带
// DEFAULT。
func Up0022(db *gorm.DB) error {
	for _, target := range codexTurnStateScopeColumns0022 {
		if !db.Migrator().HasTable(target.model) {
			return fmt.Errorf("add codex turn state scope: table %q is missing", target.table)
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

// ValidateRecoverable0022 接受两列各自加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0022(db *gorm.DB) error {
	for _, target := range codexTurnStateScopeColumns0022 {
		if !db.Migrator().HasTable(target.model) {
			return fmt.Errorf("validate codex turn state scope: table %q is missing", target.table)
		}
		if !db.Migrator().HasColumn(target.model, target.column) {
			continue
		}
		if err := validateCodexTurnStateScopeColumn0022(db, target.table, target.column, target.sqliteNullableUnknown); err != nil {
			return err
		}
	}
	return nil
}

func Validate0022(db *gorm.DB) error {
	if err := ValidateRecoverable0022(db); err != nil {
		return err
	}
	for _, target := range codexTurnStateScopeColumns0022 {
		if !db.Migrator().HasColumn(target.model, target.column) {
			return fmt.Errorf("validate codex turn state scope: column %q is missing", target.table+"."+target.column)
		}
	}
	return nil
}

var codexTurnStateScopeColumns0022 = []struct {
	model  any
	table  string
	column string
	field  string
	// sqliteNullableUnknown 的理由见 0021：0006 用手写 DDL 重建过
	// request_log_attempts，驱动此后读不出该表的非空约束。
	sqliteNullableUnknown bool
}{
	{&credentialTurnStateModels0022{}, "credentials", "codex_turn_state_models", "CodexTurnStateModels", false},
	{&attemptInjectedTurnState0022{}, "request_log_attempts", "injected_turn_state", "InjectedTurnState", true},
}

func validateCodexTurnStateScopeColumn0022(db *gorm.DB, table, name string, sqliteNullableUnknown bool) error {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return fmt.Errorf("inspect %s.%s: %w", table, name, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("validate codex turn state scope: column %q is not textual", table+"."+name)
		}
		skipNullable := sqliteNullableUnknown && strings.EqualFold(db.Dialector.Name(), "sqlite")
		if nullable, known := column.Nullable(); known && nullable && !skipNullable {
			return fmt.Errorf("validate codex turn state scope: column %q must not be nullable", table+"."+name)
		}
		return nil
	}
	return fmt.Errorf("validate codex turn state scope: column %q is missing", table+"."+name)
}
