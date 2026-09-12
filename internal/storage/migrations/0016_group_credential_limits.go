package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0016 = "0016_group_credential_limits"

type groupCredentialLimits0016 struct {
	CredentialRPMLimit         int64 `gorm:"column:credential_rpm_limit;not null;default:0"`
	CredentialConcurrencyLimit int64 `gorm:"column:credential_concurrency_limit;not null;default:0"`
}

func (groupCredentialLimits0016) TableName() string { return "groups" }

// Up0016 为分组增加凭据限额默认值；默认 0 表示不限，凭据侧同样以 0 表示继承。
func Up0016(db *gorm.DB) error {
	model := &groupCredentialLimits0016{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("add group credential limits: table %q is missing", model.TableName())
	}
	for _, field := range []struct{ column, field string }{
		{"credential_rpm_limit", "CredentialRPMLimit"},
		{"credential_concurrency_limit", "CredentialConcurrencyLimit"},
	} {
		if db.Migrator().HasColumn(model, field.column) {
			continue
		}
		if err := db.Migrator().AddColumn(model, field.field); err != nil {
			return fmt.Errorf("add groups.%s: %w", field.column, err)
		}
	}
	return nil
}

// ValidateRecoverable0016 接受两列各自加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0016(db *gorm.DB) error {
	model := &groupCredentialLimits0016{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("validate recoverable group credential limits: table %q is missing", model.TableName())
	}
	for _, column := range []string{"credential_rpm_limit", "credential_concurrency_limit"} {
		if !db.Migrator().HasColumn(model, column) {
			continue
		}
		if err := validateGroupCredentialLimitColumn0016(db, column); err != nil {
			return err
		}
	}
	return nil
}

func Validate0016(db *gorm.DB) error {
	if err := ValidateRecoverable0016(db); err != nil {
		return err
	}
	for _, column := range []string{"credential_rpm_limit", "credential_concurrency_limit"} {
		if !db.Migrator().HasColumn(&groupCredentialLimits0016{}, column) {
			return fmt.Errorf("validate group credential limits: column %q is missing", "groups."+column)
		}
	}
	return nil
}

func validateGroupCredentialLimitColumn0016(db *gorm.DB, name string) error {
	columns, err := db.Migrator().ColumnTypes("groups")
	if err != nil {
		return fmt.Errorf("inspect groups.%s: %w", name, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("validate group credential limits: column %q is not integer", "groups."+name)
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf("validate group credential limits: column %q must not be nullable", "groups."+name)
		}
		return nil
	}
	return fmt.Errorf("validate group credential limits: column %q is missing", "groups."+name)
}
