package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0023 = "0023_codex_turn_state_set_at"

type credentialTurnStateSetAt0023 struct {
	CodexTurnStateSetAtMS int64 `gorm:"column:codex_turn_state_set_at_ms;not null;default:0"`
}

func (credentialTurnStateSetAt0023) TableName() string { return "credentials" }

// Up0023 记录注入值最后一次被写入的时刻，界面据此给出 X-Codex-Turn-State 的时效
// 倒计时。存量行留在 0：历史注入值的写入时间无从追溯，谎报一个时刻比承认未知更糟。
func Up0023(db *gorm.DB) error {
	model := &credentialTurnStateSetAt0023{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("add codex turn state set at: table %q is missing", model.TableName())
	}
	if db.Migrator().HasColumn(model, "codex_turn_state_set_at_ms") {
		return nil
	}
	if err := db.Migrator().AddColumn(model, "CodexTurnStateSetAtMS"); err != nil {
		return fmt.Errorf("add credentials.codex_turn_state_set_at_ms: %w", err)
	}
	return nil
}

// ValidateRecoverable0023 接受加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0023(db *gorm.DB) error {
	model := &credentialTurnStateSetAt0023{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("validate codex turn state set at: table %q is missing", model.TableName())
	}
	if !db.Migrator().HasColumn(model, "codex_turn_state_set_at_ms") {
		return nil
	}
	return validateCodexTurnStateSetAtColumn0023(db)
}

func Validate0023(db *gorm.DB) error {
	if err := ValidateRecoverable0023(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&credentialTurnStateSetAt0023{}, "codex_turn_state_set_at_ms") {
		return fmt.Errorf("validate codex turn state set at: column %q is missing", "credentials.codex_turn_state_set_at_ms")
	}
	return nil
}

func validateCodexTurnStateSetAtColumn0023(db *gorm.DB) error {
	const name = "codex_turn_state_set_at_ms"
	columns, err := db.Migrator().ColumnTypes("credentials")
	if err != nil {
		return fmt.Errorf("inspect credentials.%s: %w", name, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("validate codex turn state set at: column %q is not integer", "credentials."+name)
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf("validate codex turn state set at: column %q must not be nullable", "credentials."+name)
		}
		return nil
	}
	return fmt.Errorf("validate codex turn state set at: column %q is missing", "credentials."+name)
}
