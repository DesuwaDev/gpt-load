package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0035 = "0035_remove_request_log_output_timing"

// Up0035 原地删列，避免 SQLite 重建父表时级联删除请求尝试等关联记录。
func Up0035(db *gorm.DB) error {
	if err := ValidateRecoverable0035(db); err != nil {
		return err
	}
	for _, name := range []string{"first_output", "last_output"} {
		if !db.Migrator().HasColumn("request_logs", name+"_ms") {
			continue
		}
		statement := "ALTER TABLE request_logs DROP COLUMN " + name + "_ms"
		if db.Dialector.Name() == "mysql" {
			// MySQL 8 的单条 ALTER 原子删除约束及其列，中断只可能发生在两列之间。
			statement = "ALTER TABLE request_logs DROP CHECK chk_request_log_" + name + ", DROP COLUMN " + name + "_ms"
		}
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("remove request log %s: %w", name, err)
		}
	}
	return Validate0035(db)
}

func ValidateRecoverable0035(db *gorm.DB) error {
	if err := ValidateRecoverable0032(db); err != nil {
		return err
	}
	first := db.Migrator().HasColumn("request_logs", "first_output_ms")
	last := db.Migrator().HasColumn("request_logs", "last_output_ms")
	if first && !last {
		return fmt.Errorf("request log output timing columns were removed out of order")
	}
	for _, name := range []string{"first_output", "last_output"} {
		if !db.Migrator().HasColumn("request_logs", name+"_ms") && db.Migrator().HasConstraint("request_logs", "chk_request_log_"+name) {
			return fmt.Errorf("orphaned request log %s constraint", name)
		}
	}
	for _, name := range []string{"duration_ms", "first_response_ms", "output_tokens"} {
		if !db.Migrator().HasColumn("request_logs", name) {
			return fmt.Errorf("request log %s is missing", name)
		}
	}
	return nil
}

func Validate0035(db *gorm.DB) error {
	if err := ValidateRecoverable0035(db); err != nil {
		return err
	}
	for _, name := range []string{"first_output_ms", "last_output_ms"} {
		if db.Migrator().HasColumn("request_logs", name) {
			return fmt.Errorf("obsolete request log %s remains", name)
		}
	}
	return nil
}

// ValidateCurrent0032 仅凭已登记的清理迁移接受缺列；没有升级证据仍执行冻结的原校验。
func ValidateCurrent0032(db *gorm.DB) error {
	var ids []string
	if err := db.Table("schema_migrations").Where("id IN ?", []string{ID0035, ID0035 + "#building"}).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if id == ID0035 {
			return Validate0035(db)
		}
		if id == ID0035+"#building" && db.Dialector.Name() == "mysql" {
			return ValidateRecoverable0035(db)
		}
	}
	return Validate0032(db)
}
