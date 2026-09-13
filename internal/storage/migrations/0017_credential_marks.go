package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0017 = "0017_credential_marks"

type credentialMarks0017 struct {
	Mark     string `gorm:"column:mark;type:varchar(32);not null;default:''"`
	MarkNote string `gorm:"column:mark_note;type:varchar(160);not null;default:''"`
}

func (credentialMarks0017) TableName() string { return "credentials" }

// Up0017 为凭据增加人工模型状态标记；空串表示未标记，取值集合由控制面校验，
// 不写成 CHECK 以免后续扩标记类型时又要改库。
func Up0017(db *gorm.DB) error {
	model := &credentialMarks0017{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("add credential marks: table %q is missing", model.TableName())
	}
	for _, field := range []struct{ column, field string }{
		{"mark", "Mark"},
		{"mark_note", "MarkNote"},
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

// ValidateRecoverable0017 接受两列各自加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0017(db *gorm.DB) error {
	model := &credentialMarks0017{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("validate credential marks: table %q is missing", model.TableName())
	}
	for _, column := range credentialMarkColumns0017 {
		if !db.Migrator().HasColumn(model, column) {
			continue
		}
		if err := validateCredentialMarkColumn0017(db, column); err != nil {
			return err
		}
	}
	return nil
}

func Validate0017(db *gorm.DB) error {
	if err := ValidateRecoverable0017(db); err != nil {
		return err
	}
	model := &credentialMarks0017{}
	for _, column := range credentialMarkColumns0017 {
		if !db.Migrator().HasColumn(model, column) {
			return fmt.Errorf("validate credential marks: column %q is missing", "credentials."+column)
		}
	}
	return nil
}

var credentialMarkColumns0017 = []string{"mark", "mark_note"}

func validateCredentialMarkColumn0017(db *gorm.DB, name string) error {
	columns, err := db.Migrator().ColumnTypes("credentials")
	if err != nil {
		return fmt.Errorf("inspect credentials.%s: %w", name, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("validate credential marks: column %q is not textual", "credentials."+name)
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf("validate credential marks: column %q must not be nullable", "credentials."+name)
		}
		return nil
	}
	return fmt.Errorf("validate credential marks: column %q is missing", "credentials."+name)
}
