package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0037 = "0037_group_priority"

type group0037 struct {
	Priority int32 `gorm:"type:integer;not null;default:0"`
}

func (group0037) TableName() string { return "groups" }

// Up0037 旧分组统一使用 0，保留原有同档调度行为。
func Up0037(db *gorm.DB) error {
	if err := ValidateRecoverable0037(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&group0037{}, "priority") {
		if err := db.Migrator().AddColumn(&group0037{}, "Priority"); err != nil {
			return fmt.Errorf("add group priority: %w", err)
		}
	}
	return Validate0037(db)
}

func ValidateRecoverable0037(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported group priority migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable(&group0037{}) {
		return fmt.Errorf("groups table is missing")
	}
	if db.Migrator().HasColumn(&group0037{}, "priority") {
		return Validate0037(db)
	}
	return nil
}

func Validate0037(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(&group0037{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "priority" {
			continue
		}
		switch strings.ToLower(column.DatabaseTypeName()) {
		case "int", "integer", "int4":
		default:
			return fmt.Errorf("group priority must be an integer")
		}
		if kind, known := column.ColumnType(); known && strings.Contains(strings.ToLower(kind), "unsigned") {
			return fmt.Errorf("group priority must be signed")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("group priority must not be nullable")
		}
		value, known := column.DefaultValue()
		if !known || strings.Trim(strings.SplitN(value, "::", 2)[0], "'\"() ") != "0" {
			return fmt.Errorf("group priority default must be zero")
		}
		return nil
	}
	return fmt.Errorf("group priority is missing")
}
