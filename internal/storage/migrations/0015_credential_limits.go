package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0015 = "0015_credential_limits"

type credentialLimits0015 struct {
	RPMLimit         int64 `gorm:"column:rpm_limit;not null;default:0"`
	ConcurrencyLimit int64 `gorm:"column:concurrency_limit;not null;default:0"`
}

func (credentialLimits0015) TableName() string { return "credentials" }

// Up0015 为凭据增加本地 RPM 与并发上限；默认 0 表示不限，存量行为不变。
func Up0015(db *gorm.DB) error {
	model := &credentialLimits0015{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("add credential limits: table %q is missing", model.TableName())
	}
	for _, field := range []struct{ column, field string }{
		{"rpm_limit", "RPMLimit"}, {"concurrency_limit", "ConcurrencyLimit"},
	} {
		if db.Migrator().HasColumn(model, field.column) {
			continue
		}
		if err := db.Migrator().AddColumn(model, field.field); err != nil {
			return fmt.Errorf("add credentials.%s: %w", field.column, err)
		}
	}
	return nil
}

// ValidateRecoverable0015 接受两列各自加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0015(db *gorm.DB) error {
	model := &credentialLimits0015{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("validate recoverable credential limits: table %q is missing", model.TableName())
	}
	for _, column := range []string{"rpm_limit", "concurrency_limit"} {
		if !db.Migrator().HasColumn(model, column) {
			continue
		}
		if err := validateCredentialLimitColumn0015(db, column); err != nil {
			return err
		}
	}
	return nil
}

func Validate0015(db *gorm.DB) error {
	if err := ValidateRecoverable0015(db); err != nil {
		return err
	}
	for _, column := range []string{"rpm_limit", "concurrency_limit"} {
		if !db.Migrator().HasColumn(&credentialLimits0015{}, column) {
			return fmt.Errorf("validate credential limits: column %q is missing", "credentials."+column)
		}
	}
	return nil
}

func validateCredentialLimitColumn0015(db *gorm.DB, name string) error {
	columns, err := db.Migrator().ColumnTypes("credentials")
	if err != nil {
		return fmt.Errorf("inspect credentials.%s: %w", name, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("validate credential limits: column %q is not integer", "credentials."+name)
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf("validate credential limits: column %q must not be nullable", "credentials."+name)
		}
		return nil
	}
	return fmt.Errorf("validate credential limits: column %q is missing", "credentials."+name)
}
