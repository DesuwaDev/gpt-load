package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ID0027 = "0027_auto_decision_attribution"

const (
	autoDecisionUsageTable0027       = "auto_decision_usage_stats"
	autoDecisionUsageBuildTable0027  = "auto_decision_usage_stats_0027"
	autoDecisionUsageBackupTable0027 = "auto_decision_usage_stats_0027_old"
)

var autoDecisionLogColumns0027 = []string{"DecisionGroupID", "DecisionChannelID", "DecisionCredentialID"}

type autoLog0027 struct {
	DecisionGroupID      uint   `gorm:"not null;default:0"`
	DecisionChannelID    string `gorm:"type:varchar(64);not null;default:''"`
	DecisionCredentialID uint   `gorm:"not null;default:0"`
}

func (autoLog0027) TableName() string { return "request_logs" }

type autoUsage0027 struct {
	BucketStartMS        int64  `gorm:"primaryKey;not null"`
	AccessKeyID          uint   `gorm:"primaryKey;not null"`
	GroupID              uint   `gorm:"primaryKey;not null;default:0"`
	ChannelID            string `gorm:"type:varchar(64);primaryKey;not null;default:''"`
	CredentialID         uint   `gorm:"primaryKey;not null;default:0"`
	Model                string `gorm:"type:varchar(512);primaryKey;not null"`
	EstimatedCostNanoUSD int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_0027_cost,estimated_cost_nano_usd >= 0"`
	UnpricedRequestCount int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_0027_unpriced,unpriced_request_count >= 0"`
	PricingPartialCount  int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_0027_partial,pricing_partial_count >= 0"`
}

func (autoUsage0027) TableName() string { return "auto_decision_usage_stats" }

type autoUsageBuild0027 autoUsage0027

func (autoUsageBuild0027) TableName() string { return autoDecisionUsageBuildTable0027 }

func Up0027(db *gorm.DB) error {
	if err := ValidateRecoverable0027(db); err != nil {
		return err
	}
	for _, name := range autoDecisionLogColumns0027 {
		if !db.Migrator().HasColumn(&autoLog0027{}, name) {
			if err := db.Migrator().AddColumn(&autoLog0027{}, name); err != nil {
				return fmt.Errorf("add automatic decision attribution column %s: %w", name, err)
			}
		}
	}

	hasCurrent := db.Migrator().HasTable(&autoUsage0027{})
	hasBuild := db.Migrator().HasTable(&autoUsageBuild0027{})
	hasBackup := db.Migrator().HasTable(autoDecisionUsageBackupTable0027)
	if hasCurrent && autoDecisionUsageHasAttribution0027(db) {
		if hasBuild {
			if err := dropAutoDecisionUsageTable0027(db, autoDecisionUsageBuildTable0027, &autoUsageBuild0027{}); err != nil {
				return fmt.Errorf("drop stale automatic decision attribution table: %w", err)
			}
		}
		if hasBackup {
			if err := dropAutoDecisionUsageTable0027(db, autoDecisionUsageBackupTable0027, autoDecisionUsageBackupTable0027); err != nil {
				return fmt.Errorf("drop automatic decision attribution swap backup: %w", err)
			}
		}
		return Validate0027(db)
	}
	if !hasCurrent && hasBuild {
		if err := db.Migrator().RenameTable(autoDecisionUsageBuildTable0027, autoDecisionUsageTable0027); err != nil {
			return fmt.Errorf("finish automatic decision attribution table rename: %w", err)
		}
		if hasBackup {
			if err := dropAutoDecisionUsageTable0027(db, autoDecisionUsageBackupTable0027, autoDecisionUsageBackupTable0027); err != nil {
				return fmt.Errorf("drop automatic decision attribution recovery backup: %w", err)
			}
		}
		return Validate0027(db)
	}
	if !hasCurrent {
		return fmt.Errorf("automatic decision attribution migration requires auto_decision_usage_stats")
	}
	if hasBackup {
		return fmt.Errorf("automatic decision attribution migration found an unexpected swap backup")
	}
	if hasBuild {
		if err := dropAutoDecisionUsageTable0027(db, autoDecisionUsageBuildTable0027, &autoUsageBuild0027{}); err != nil {
			return fmt.Errorf("reset automatic decision attribution build table: %w", err)
		}
	}
	if err := db.Migrator().CreateTable(&autoUsageBuild0027{}); err != nil {
		return fmt.Errorf("create automatic decision attribution build table: %w", err)
	}
	if err := db.Exec(`INSERT INTO auto_decision_usage_stats_0027
		(bucket_start_ms, access_key_id, group_id, channel_id, credential_id, model,
		 estimated_cost_nano_usd, unpriced_request_count, pricing_partial_count)
		SELECT bucket_start_ms, access_key_id, 0, '', 0, model,
		       estimated_cost_nano_usd, unpriced_request_count, pricing_partial_count
		FROM auto_decision_usage_stats`).Error; err != nil {
		return fmt.Errorf("copy automatic decision usage history: %w", err)
	}
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		if err := db.Exec(
			"RENAME TABLE ? TO ?, ? TO ?",
			clause.Table{Name: autoDecisionUsageTable0027},
			clause.Table{Name: autoDecisionUsageBackupTable0027},
			clause.Table{Name: autoDecisionUsageBuildTable0027},
			clause.Table{Name: autoDecisionUsageTable0027},
		).Error; err != nil {
			return fmt.Errorf("swap automatic decision attribution table: %w", err)
		}
		if err := dropAutoDecisionUsageTable0027(db, autoDecisionUsageBackupTable0027, autoDecisionUsageBackupTable0027); err != nil {
			return fmt.Errorf("drop old automatic decision usage table: %w", err)
		}
	} else {
		if err := dropAutoDecisionUsageTable0027(db, autoDecisionUsageTable0027, &autoUsage0027{}); err != nil {
			return fmt.Errorf("drop old automatic decision usage table: %w", err)
		}
		if err := db.Migrator().RenameTable(autoDecisionUsageBuildTable0027, autoDecisionUsageTable0027); err != nil {
			return fmt.Errorf("rename automatic decision attribution table: %w", err)
		}
	}
	return Validate0027(db)
}

func dropAutoDecisionUsageTable0027(db *gorm.DB, table string, model any) error {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return db.Exec("DROP TABLE IF EXISTS ?", clause.Table{Name: table}).Error
	}
	return db.Migrator().DropTable(model)
}

func ValidateRecoverable0027(db *gorm.DB) error {
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("automatic decision attribution migration requires request_logs")
	}
	if !db.Migrator().HasTable("auto_decision_usage_stats") &&
		!db.Migrator().HasTable(autoDecisionUsageBuildTable0027) {
		return fmt.Errorf("automatic decision attribution migration requires decision usage history")
	}
	return nil
}

func Validate0027(db *gorm.DB) error {
	for _, name := range autoDecisionLogColumns0027 {
		if !db.Migrator().HasColumn(&autoLog0027{}, name) {
			return fmt.Errorf("automatic decision attribution log column %s missing", name)
		}
	}
	if !db.Migrator().HasTable(&autoUsage0027{}) || !autoDecisionUsageHasAttribution0027(db) {
		return fmt.Errorf("automatic decision usage attribution table is incomplete")
	}
	return nil
}

func autoDecisionUsageHasAttribution0027(db *gorm.DB) bool {
	for _, name := range []string{
		"BucketStartMS", "AccessKeyID", "GroupID", "ChannelID", "CredentialID", "Model",
		"EstimatedCostNanoUSD", "UnpricedRequestCount", "PricingPartialCount",
	} {
		if !db.Migrator().HasColumn(&autoUsage0027{}, name) {
			return false
		}
	}
	return true
}
