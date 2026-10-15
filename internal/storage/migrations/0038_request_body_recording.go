package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const ID0038 = "0038_request_body_recording"

type accessKey0038 struct {
	RecordRequestBody bool `gorm:"not null;default:false"`
}

func (accessKey0038) TableName() string { return "access_keys" }

type requestLogParent0038 struct {
	ID string `gorm:"type:varchar(36);primaryKey;not null"`
}

func (requestLogParent0038) TableName() string { return "request_logs" }

type requestLogBodyText0038 string

func (requestLogBodyText0038) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "longtext"
	}
	return "text"
}

type requestLogBody0038 struct {
	Request       requestLogParent0038    `gorm:"foreignKey:RequestID;references:ID;constraint:OnDelete:CASCADE"`
	RequestID     string                  `gorm:"type:varchar(36);primaryKey;not null"`
	State         string                  `gorm:"type:varchar(32);not null;check:chk_request_log_body_state,state IN ('original','truncated','non_json','too_large','queue_full')"`
	OriginalBytes int64                   `gorm:"not null;check:chk_request_log_body_original_bytes,original_bytes >= 0"`
	Body          *requestLogBodyText0038 `gorm:"check:chk_request_log_body_content,(state IN ('original','truncated') AND body IS NOT NULL) OR (state IN ('non_json','too_large','queue_full') AND body IS NULL)"`
}

func (requestLogBody0038) TableName() string { return "request_log_bodies" }

var requestLogBodyChecks0038 = []string{
	"chk_request_log_body_state",
	"chk_request_log_body_original_bytes",
	"chk_request_log_body_content",
}

// Up0038 增加访问密钥的请求内容记录开关（默认关闭）和随请求日志级联删除的原始请求体表。
func Up0038(db *gorm.DB) error {
	if err := ValidateRecoverable0038(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&accessKey0038{}, "record_request_body") {
		if err := db.Migrator().AddColumn(&accessKey0038{}, "RecordRequestBody"); err != nil {
			return fmt.Errorf("add access key request body recording: %w", err)
		}
	}
	if !db.Migrator().HasTable(&requestLogBody0038{}) {
		if err := db.Migrator().CreateTable(&requestLogBody0038{}); err != nil {
			return fmt.Errorf("create request log bodies: %w", err)
		}
	}
	if !db.Migrator().HasConstraint(&requestLogBody0038{}, "Request") {
		if err := db.Migrator().CreateConstraint(&requestLogBody0038{}, "Request"); err != nil {
			return fmt.Errorf("create request log body foreign key: %w", err)
		}
	}
	return Validate0038(db)
}

func ValidateRecoverable0038(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported request body recording migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("access_keys") || !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("request body recording requires access_keys and request_logs")
	}
	if db.Migrator().HasColumn(&accessKey0038{}, "record_request_body") {
		if err := validateRecordColumn0038(db); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&requestLogBody0038{}) {
		return validateBodyTable0038(db, false)
	}
	return nil
}

func Validate0038(db *gorm.DB) error {
	if err := validateRecordColumn0038(db); err != nil {
		return err
	}
	return validateBodyTable0038(db, true)
}

func validateRecordColumn0038(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(&accessKey0038{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "record_request_body" {
			continue
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if kind != "bool" && kind != "boolean" && kind != "numeric" && !strings.Contains(kind, "int") {
			return fmt.Errorf("access key request body recording must be boolean")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("access key request body recording must not be nullable")
		}
		value, known := column.DefaultValue()
		switch strings.ToLower(strings.Trim(strings.SplitN(value, "::", 2)[0], "'\"() ")) {
		case "0", "false":
		default:
			known = false
		}
		if !known {
			return fmt.Errorf("access key request body recording must default to false")
		}
		return nil
	}
	return fmt.Errorf("access key request body recording is missing")
}

// validateBodyTable0038 与 0021 的子表校验一致，只核对列与约束是否存在，
// 不额外读取列类型，避免 MySQL 每次启动多一次元数据查询。
func validateBodyTable0038(db *gorm.DB, requireForeignKey bool) error {
	model := &requestLogBody0038{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("request log bodies table is missing")
	}
	for _, name := range []string{"RequestID", "State", "OriginalBytes", "Body"} {
		if !db.Migrator().HasColumn(model, name) {
			return fmt.Errorf("request log body column %s is missing", name)
		}
	}
	for _, name := range requestLogBodyChecks0038 {
		if !db.Migrator().HasConstraint(model, name) {
			return fmt.Errorf("request log body constraint %s is missing", name)
		}
	}
	if requireForeignKey && !db.Migrator().HasConstraint(model, "Request") {
		return fmt.Errorf("request log body foreign key is missing")
	}
	return nil
}
