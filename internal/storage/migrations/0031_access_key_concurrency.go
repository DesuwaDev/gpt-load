package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 对应上游 0023_access_key_concurrency；本仓库编号已占用，顺延为 0031。
const ID0031 = "0031_access_key_concurrency"

// 本仓库 0014 已加过同名列（NOT NULL DEFAULT 0，0 表示不限）。转换期间旧列
// 暂存在这个名字下，转换完成后删除。
const legacyAccessKeyConcurrency0031 = "concurrency_limit_0014"

// Up0031 把访问密钥并发上限收敛到上游语义：可空，NULL 继承默认值。
//
// 步骤：旧列改名暂存 → 按上游定义重新加列 → 回填大于 0 的显式值 → 删除暂存列。
// 旧值 0 映射为 NULL：默认值未配置时同样表示不限，存量密钥行为不变。MySQL 的
// DDL 会隐式提交，所以每一步都按当前列状态判定，中断后可从断点续跑。
func Up0031(db *gorm.DB) error {
	if err := ValidateRecoverable0031(db); err != nil {
		return err
	}
	migrator := db.Migrator()
	hasLegacy := migrator.HasColumn("access_keys", legacyAccessKeyConcurrency0031)
	hasCurrent := migrator.HasColumn("access_keys", "concurrency_limit")
	if hasCurrent && !hasLegacy {
		upstreamForm, err := accessKeyConcurrencyNullable0031(db)
		if err != nil {
			return err
		}
		if !upstreamForm {
			if err := db.Exec(
				"ALTER TABLE ? RENAME COLUMN ? TO ?",
				clause.Table{Name: "access_keys"}, clause.Column{Name: "concurrency_limit"},
				clause.Column{Name: legacyAccessKeyConcurrency0031},
			).Error; err != nil {
				return fmt.Errorf("stash legacy access key concurrency: %w", err)
			}
			hasCurrent, hasLegacy = false, true
		}
	}
	if !hasCurrent {
		if err := db.Exec("ALTER TABLE ? ADD COLUMN concurrency_limit BIGINT NULL CONSTRAINT chk_access_key_concurrency CHECK (concurrency_limit >= 0 AND concurrency_limit <= 9007199254740991)", clause.Table{Name: "access_keys"}).Error; err != nil {
			return fmt.Errorf("add access key concurrency: %w", err)
		}
	}
	if hasLegacy {
		if err := db.Exec(
			"UPDATE ? SET concurrency_limit = ? WHERE ? > 0 AND concurrency_limit IS NULL",
			clause.Table{Name: "access_keys"}, clause.Column{Name: legacyAccessKeyConcurrency0031},
			clause.Column{Name: legacyAccessKeyConcurrency0031},
		).Error; err != nil {
			return fmt.Errorf("copy legacy access key concurrency: %w", err)
		}
		if err := db.Exec(
			"ALTER TABLE ? DROP COLUMN ?",
			clause.Table{Name: "access_keys"}, clause.Column{Name: legacyAccessKeyConcurrency0031},
		).Error; err != nil {
			return fmt.Errorf("drop legacy access key concurrency: %w", err)
		}
	}
	return Validate0031(db)
}

// ValidateRecoverable0031 接受转换前、转换中各步之后以及完成后的状态。
func ValidateRecoverable0031(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported concurrency migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("access_keys") {
		return fmt.Errorf("access_keys table is missing")
	}
	if db.Migrator().HasColumn("access_keys", legacyAccessKeyConcurrency0031) ||
		!db.Migrator().HasColumn("access_keys", "concurrency_limit") {
		return nil
	}
	upstreamForm, err := accessKeyConcurrencyNullable0031(db)
	if err != nil || !upstreamForm {
		return err
	}
	return Validate0031(db)
}

func Validate0031(db *gorm.DB) error {
	if db.Migrator().HasColumn("access_keys", legacyAccessKeyConcurrency0031) {
		return fmt.Errorf("legacy access key concurrency column is still present")
	}
	if !db.Migrator().HasConstraint("access_keys", "chk_access_key_concurrency") {
		return fmt.Errorf("concurrency constraint is missing")
	}
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "concurrency_limit" {
			continue
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(kind, "int") {
			return fmt.Errorf("concurrency limit must be integer")
		}
		if nullable, known := column.Nullable(); !known || !nullable {
			return fmt.Errorf("concurrency limit must be nullable")
		}
		var invalid int64
		if err := db.Table("access_keys").Where("concurrency_limit < 0 OR concurrency_limit > 9007199254740991").Count(&invalid).Error; err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("invalid persisted concurrency limits")
		}
		return nil
	}
	return fmt.Errorf("concurrency limit is missing")
}

// ValidateCurrent0014 在启动复核时代替 Validate0014：0031 会把该列转换成可空，
// 冻结的 0014 校验只适用于转换之前。
func ValidateCurrent0014(db *gorm.DB) error {
	if !db.Migrator().HasTable("access_keys") {
		return fmt.Errorf("validate access key concurrency limit: table %q is missing", "access_keys")
	}
	if db.Migrator().HasColumn("access_keys", legacyAccessKeyConcurrency0031) {
		return nil
	}
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
		return nil
	}
	return fmt.Errorf("validate access key concurrency limit: column %q is missing", "access_keys.concurrency_limit")
}

func accessKeyConcurrencyNullable0031(db *gorm.DB) (bool, error) {
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return false, fmt.Errorf("inspect access_keys.concurrency_limit: %w", err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), "concurrency_limit") {
			continue
		}
		nullable, known := column.Nullable()
		if !known {
			return false, fmt.Errorf("inspect access_keys.concurrency_limit: nullability is unknown")
		}
		return nullable, nil
	}
	return false, fmt.Errorf("inspect access_keys.concurrency_limit: column is missing")
}
