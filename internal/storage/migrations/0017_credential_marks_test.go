package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

// 标记加列必须对存量行落到空串（未标记），并且可重复执行。
func TestCredentialMarkMigrationDefaultsToEmpty(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	group := models.Group{
		Name: "marks", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription,
		Params: models.JSON(`{}`), Models: models.JSON(`[]`), Enabled: true,
	}
	if err := db.Omit("ProxyConfig", "PriceMultiplierMicros", "ValidationProtocol",
		"CredentialRPMLimit", "CredentialConcurrencyLimit").Create(&group).Error; err != nil {
		t.Fatalf("create legacy group: %v", err)
	}
	credential := models.Credential{
		GroupID: group.ID, Data: "cipher", Fingerprint: "fingerprint",
		IdentityFingerprint: "identity", Status: models.CredentialStatusActive,
	}
	if err := db.Omit("ProxyConfig", "RPMLimit", "ConcurrencyLimit", "Mark", "MarkNote").
		Create(&credential).Error; err != nil {
		t.Fatalf("create legacy credential: %v", err)
	}

	if err := migrations.Up0017(db); err != nil {
		t.Fatalf("apply mark migration: %v", err)
	}
	// 重复执行不得报错，覆盖 MySQL 的 DDL 中断恢复路径。
	if err := migrations.Up0017(db); err != nil {
		t.Fatalf("repeat mark migration: %v", err)
	}
	if err := migrations.Validate0017(db); err != nil {
		t.Fatalf("Validate0017() error = %v", err)
	}

	for _, column := range []string{"mark", "mark_note"} {
		value := "unset"
		if err := db.Table("credentials").Select(column).
			Where("id = ?", credential.ID).Scan(&value).Error; err != nil {
			t.Fatalf("read credentials.%s: %v", column, err)
		}
		if value != "" {
			t.Fatalf("credentials.%s = %q, want empty", column, value)
		}
	}
}

func TestMarkMigrationValidationRejectsMissingColumns(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ValidateRecoverable0017(db); err != nil {
		t.Fatalf("ValidateRecoverable0017() before column = %v", err)
	}
	if err := migrations.Validate0017(db); err == nil {
		t.Fatal("Validate0017() error = nil, want missing column error")
	}
}
