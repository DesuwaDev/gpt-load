package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

// 限额加列必须对存量行落到 0（不限/继承），并且可重复执行。
func TestCredentialAndGroupLimitMigrationsDefaultToZero(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	group := models.Group{
		Name: "limits", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription,
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
	if err := db.Omit("ProxyConfig", "RPMLimit", "ConcurrencyLimit").
		Create(&credential).Error; err != nil {
		t.Fatalf("create legacy credential: %v", err)
	}

	for _, apply := range []func() error{
		func() error { return migrations.Up0015(db) },
		func() error { return migrations.Up0016(db) },
	} {
		if err := apply(); err != nil {
			t.Fatalf("apply limit migration: %v", err)
		}
		// 重复执行不得报错，覆盖 MySQL 的 DDL 中断恢复路径。
		if err := apply(); err != nil {
			t.Fatalf("repeat limit migration: %v", err)
		}
	}
	if err := migrations.Validate0015(db); err != nil {
		t.Fatalf("Validate0015() error = %v", err)
	}
	if err := migrations.Validate0016(db); err != nil {
		t.Fatalf("Validate0016() error = %v", err)
	}

	for _, check := range []struct {
		table  string
		column string
		id     uint
	}{
		{"credentials", "rpm_limit", credential.ID},
		{"credentials", "concurrency_limit", credential.ID},
		{"groups", "credential_rpm_limit", group.ID},
		{"groups", "credential_concurrency_limit", group.ID},
	} {
		var value int64 = -1
		if err := db.Table(check.table).Select(check.column).
			Where("id = ?", check.id).Scan(&value).Error; err != nil {
			t.Fatalf("read %s.%s: %v", check.table, check.column, err)
		}
		if value != 0 {
			t.Fatalf("%s.%s = %d, want 0", check.table, check.column, value)
		}
	}
}

func TestLimitMigrationValidationRejectsMissingColumns(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ValidateRecoverable0015(db); err != nil {
		t.Fatalf("ValidateRecoverable0015() before column = %v", err)
	}
	if err := migrations.ValidateRecoverable0016(db); err != nil {
		t.Fatalf("ValidateRecoverable0016() before column = %v", err)
	}
	if err := migrations.Validate0015(db); err == nil {
		t.Fatal("Validate0015() error = nil, want missing column error")
	}
	if err := migrations.Validate0016(db); err == nil {
		t.Fatal("Validate0016() error = nil, want missing column error")
	}
}
