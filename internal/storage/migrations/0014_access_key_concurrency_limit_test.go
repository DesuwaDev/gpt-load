package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyConcurrencyMigrationAddsDefaultZeroAndPreservesRows(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	accessKey := models.AccessKey{
		Name: "legacy", KeyValue: "ciphertext", KeyHash: "legacy-hash",
		KeySuffix: "cafe", Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Omit("ExpiresAtMS", "PriceMultiplierMicros", "KeyPrefix", "ConcurrencyLimit").
		Create(&accessKey).Error; err != nil {
		t.Fatalf("create legacy access key: %v", err)
	}

	if err := migrations.Up0014(db); err != nil {
		t.Fatalf("Up0014() error = %v", err)
	}
	if err := migrations.Validate0014(db); err != nil {
		t.Fatalf("Validate0014() error = %v", err)
	}

	var limit int64 = -1
	if err := db.Table("access_keys").Select("concurrency_limit").Where("id = ?", accessKey.ID).
		Scan(&limit).Error; err != nil {
		t.Fatalf("read preserved concurrency limit: %v", err)
	}
	if limit != 0 {
		t.Fatalf("legacy concurrency_limit = %d, want 0", limit)
	}
	if err := migrations.Up0014(db); err != nil {
		t.Fatalf("repeated Up0014() error = %v", err)
	}
}

func TestAccessKeyConcurrencyMigrationValidationRejectsMissingColumn(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ValidateRecoverable0014(db); err != nil {
		t.Fatalf("ValidateRecoverable0014() before column = %v", err)
	}
	if err := migrations.Validate0014(db); err == nil {
		t.Fatal("Validate0014() error = nil, want missing column error")
	}
}
