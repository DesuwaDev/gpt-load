package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestConcurrencyMigrationContract(t *testing.T) {
	testConcurrencyMigration(t, openInternalMigrationTestDatabase)
}
func TestExternalConcurrencyMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testConcurrencyMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}
func testConcurrencyMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if len(migrations) < 31 {
				t.Fatal("concurrency migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:30]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("access_keys").Create(map[string]any{"id": 1, "name": "existing", "key_value": "test-cipher", "key_hash": "test-hash", "key_suffix": "cafe", "status": "active", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[30]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after concurrency DDL")
				}
				if err := applyMigrationRegistry(db, append(append([]migration(nil), migrations[:30]...), entry)); err == nil {
					t.Fatal("interruption succeeded")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("access_keys", "concurrency_limit") {
				t.Fatal("concurrency limit is missing")
			}
			if scenario != "fresh" {
				var row struct {
					Name             string
					ConcurrencyLimit *int64
				}
				if err := db.Table("access_keys").Where("id = ?", 1).Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.Name != "existing" || row.ConcurrencyLimit != nil {
					t.Fatalf("existing data changed: %+v", row)
				}
				for _, limit := range []any{int64(0), int64(7), nil} {
					if err := db.Table("access_keys").Where("id = ?", 1).Update("concurrency_limit", limit).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Table("access_keys").Where("id = ?", 1).Update("concurrency_limit", -1).Error; err == nil {
					t.Fatal("negative limit accepted")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 本仓库 0014 先加过 NOT NULL DEFAULT 0 的同名列；0031 必须把它转换成上游语义，
// 并能从转换的任意一步中断处续跑。
func TestConcurrencyMigrationConvertsLegacyColumn(t *testing.T) {
	testLegacyConcurrencyConversion(t, openInternalMigrationTestDatabase)
}
func TestExternalConcurrencyMigrationConvertsLegacyColumn(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testLegacyConcurrencyConversion(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}
func testLegacyConcurrencyConversion(t *testing.T, open func(*testing.T) *gorm.DB) {
	steps := map[string][]string{
		"not started": nil,
		"after stash": {
			"ALTER TABLE access_keys RENAME COLUMN concurrency_limit TO concurrency_limit_0014",
		},
		"after add": {
			"ALTER TABLE access_keys RENAME COLUMN concurrency_limit TO concurrency_limit_0014",
			"ALTER TABLE access_keys ADD COLUMN concurrency_limit BIGINT NULL CONSTRAINT chk_access_key_concurrency CHECK (concurrency_limit >= 0 AND concurrency_limit <= 9007199254740991)",
		},
		"after copy": {
			"ALTER TABLE access_keys RENAME COLUMN concurrency_limit TO concurrency_limit_0014",
			"ALTER TABLE access_keys ADD COLUMN concurrency_limit BIGINT NULL CONSTRAINT chk_access_key_concurrency CHECK (concurrency_limit >= 0 AND concurrency_limit <= 9007199254740991)",
			"UPDATE access_keys SET concurrency_limit = concurrency_limit_0014 WHERE concurrency_limit_0014 > 0",
		},
	}
	for name, statements := range steps {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			if err := applyMigrationRegistry(db, migrations[:30]); err != nil {
				t.Fatal(err)
			}
			for id, limit := range map[int]int64{1: 0, 2: 5} {
				if err := db.Table("access_keys").Create(map[string]any{"id": id, "name": fmt.Sprintf("existing-%d", id), "key_value": "test-cipher", "key_hash": fmt.Sprintf("test-hash-%d", id), "key_suffix": "cafe", "status": "active", "concurrency_limit": limit, "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, statement := range statements {
				if err := db.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			if db.Migrator().HasColumn("access_keys", "concurrency_limit_0014") {
				t.Fatal("legacy column was not dropped")
			}
			var rows []struct {
				ID               uint
				ConcurrencyLimit *int64
			}
			if err := db.Table("access_keys").Order("id").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].ConcurrencyLimit != nil || rows[1].ConcurrencyLimit == nil || *rows[1].ConcurrencyLimit != 5 {
				t.Fatalf("converted limits = %+v, want [nil 5]", rows)
			}
			if err := db.Table("access_keys").Where("id = ?", 1).Update("concurrency_limit", -1).Error; err == nil {
				t.Fatal("negative limit accepted")
			}
		})
	}
}
