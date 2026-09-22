package control

import (
	"encoding/json"
	"errors"
	"testing"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
)

func TestUpdateCodexCredentialCustomBaseURL(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)

	stage := mustImportSubscriptionStage(t, fixture, "codex-custom-gateway-acc", "gateway-user@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("codex-gateway-group"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.3-codex"}}},
		StagedCredentialIDs: []string{stage.StageID},
		ConfirmSameTarget:   true,
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}

	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatalf("take credential: %v", err)
	}

	// 1. Initially, credential has no custom BaseURL
	initial, err := fixture.service.loadCredentialItem(t.Context(), created.GroupID, row.ID)
	if err != nil {
		t.Fatalf("loadCredentialItem() error = %v", err)
	}
	if initial.Account.BaseURL != "" {
		t.Fatalf("initial BaseURL = %q, want empty", initial.Account.BaseURL)
	}

	// 2. Reject insecure HTTP BaseURL
	_, err = fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, CredentialUpdateRequest{
		BaseURL: optionalField[string]{Set: true, Value: "http://insecure-gateway.example/backend-api/codex"},
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("expected ErrValidation for http URL, got %v", err)
	}

	// 3. Reject BaseURL with query parameters
	_, err = fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, CredentialUpdateRequest{
		BaseURL: optionalField[string]{Set: true, Value: "https://custom-gateway.example/backend-api/codex?token=123"},
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("expected ErrValidation for URL with query, got %v", err)
	}

	// 4. Update to custom HTTPS BaseURL
	customURL := "https://custom-gateway.example/backend-api/codex"
	updated, err := fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, CredentialUpdateRequest{
		BaseURL: optionalField[string]{Set: true, Value: customURL},
	})
	if err != nil {
		t.Fatalf("UpdateGroupCredential() error = %v", err)
	}
	if updated.Account.BaseURL != customURL {
		t.Fatalf("updated BaseURL = %q, want %q", updated.Account.BaseURL, customURL)
	}

	// Verify decrypted ciphertext in DB contains BaseURL
	var committed models.Credential
	if err := fixture.db.Take(&committed, row.ID).Error; err != nil {
		t.Fatalf("take committed credential: %v", err)
	}
	decrypted, err := fixture.encryption.Decrypt(committed.Data)
	if err != nil {
		t.Fatalf("decrypt committed data: %v", err)
	}
	parsed, err := codex.ParseCredentialJSON([]byte(decrypted))
	if err != nil {
		t.Fatalf("parse decrypted credential: %v", err)
	}
	if parsed.BaseURL != customURL {
		t.Fatalf("persisted BaseURL = %q, want %q", parsed.BaseURL, customURL)
	}

	// 5. Clear BaseURL back to empty
	cleared, err := fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, CredentialUpdateRequest{
		BaseURL: optionalField[string]{Set: true, Value: ""},
	})
	if err != nil {
		t.Fatalf("UpdateGroupCredential() clear error = %v", err)
	}
	if cleared.Account.BaseURL != "" {
		t.Fatalf("cleared BaseURL = %q, want empty", cleared.Account.BaseURL)
	}

	// Verify DB is also cleared
	if err := fixture.db.Take(&committed, row.ID).Error; err != nil {
		t.Fatalf("take committed credential after clear: %v", err)
	}
	decryptedCleared, err := fixture.encryption.Decrypt(committed.Data)
	if err != nil {
		t.Fatalf("decrypt committed data after clear: %v", err)
	}
	parsedCleared, err := codex.ParseCredentialJSON([]byte(decryptedCleared))
	if err != nil {
		t.Fatalf("parse decrypted credential after clear: %v", err)
	}
	if parsedCleared.BaseURL != "" {
		t.Fatalf("persisted BaseURL after clear = %q, want empty", parsedCleared.BaseURL)
	}
}

func TestUpdateNonCodexCredentialCustomBaseURLRejects(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)

	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("openai-key-group"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "sk-test-openai-key-secret", ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}

	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatalf("take credential: %v", err)
	}

	_, err = fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, CredentialUpdateRequest{
		BaseURL: optionalField[string]{Set: true, Value: "https://custom-gateway.example/backend-api/codex"},
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("expected ErrValidation when updating BaseURL on non-codex group, got %v", err)
	}
}
