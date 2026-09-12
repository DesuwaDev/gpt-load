package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0014 = "0014_access_key_concurrency_limit"

type accessKeyConcurrency0014 struct {
	ConcurrencyLimit int64 `gorm:"column:concurrency_limit;not null;default:0"`
}

func (accessKeyConcurrency0014) TableName() string { return "access_keys" }

// Up0014 为访问密钥增加并发上限；默认 0 与 rpm_limit 同义，表示不限。
func Up0014(db *gorm.DB) error {
	model := &accessKeyConcurrency0014{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("add access key concurrency limit: table %q is missing", model.TableName())
	}
	if db.Migrator().HasColumn(model, "concurrency_limit") {
		return nil
	}
	if err := db.Migrator().AddColumn(model, "ConcurrencyLimit"); err != nil {
		return fmt.Errorf("add access_keys.concurrency_limit: %w", err)
	}
	return nil
}

// ValidateRecoverable0014 接受原子加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0014(db *gorm.DB) error {
	model := &accessKeyConcurrency0014{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("validate recoverable access key concurrency limit: table %q is missing", model.TableName())
	}
	if !db.Migrator().HasColumn(model, "concurrency_limit") {
		return nil
	}
	return validateAccessKeyConcurrencyColumn0014(db)
}

func Validate0014(db *gorm.DB) error {
	if err := ValidateRecoverable0014(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&accessKeyConcurrency0014{}, "concurrency_limit") {
		return fmt.Errorf("validate access key concurrency limit: column %q is missing", "access_keys.concurrency_limit")
	}
	return validateAccessKeyConcurrencyColumn0014(db)
}

func validateAccessKeyConcurrencyColumn0014(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return fmt.Errorf("inspect access_keys.concurrency_limit: %w", err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), "concurrency_limit") {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("validate access key concurrency limit: column %q is not integer", "access_keys.concurrency_limit")
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf("validate access key concurrency limit: column %q must not be nullable", "access_keys.concurrency_limit")
		}
		return nil
	}
	return fmt.Errorf("validate access key concurrency limit: column %q is missing", "access_keys.concurrency_limit")
}
