package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/storage/models"
)

func TestQuarantinedCredentialRemainsManageableWithoutParsingItsSecret(t *testing.T) {
	t.Parallel()
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	damaged, err := fixture.encryption.Encrypt(`{"type":"codex","access_token":"must-not-leak-damaged-secret"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", credentialID).Update("data", damaged).Error; err != nil {
		t.Fatal(err)
	}
	markStoredCredentialAuthState(t, fixture, credentialID, models.CredentialAuthStateReauthorizationRequired, models.CredentialStartupShapeInvalid)
	collection, err := fixture.service.ListGroupCredentials(t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.Items[0].Mask != fmt.Sprintf("Credential #%d", credentialID) || collection.Items[0].AuthErrorCode != models.CredentialStartupShapeInvalid {
		t.Fatal("quarantined credential was not listed with a safe repair state")
	}
	detail, err := fixture.service.GetCredentialDetail(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Credential.AuthErrorCode != models.CredentialStartupShapeInvalid {
		t.Fatal("credential details omitted the repair reason")
	}
	options, err := fixture.service.ListModernCredentialOptions(t.Context())
	if err != nil || len(options) != 0 {
		t.Fatal("identity selector tried to parse or select an invalid credential")
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "must-not-leak") || strings.Contains(string(raw), damaged) {
		t.Fatal("quarantined credential presentation exposed secret data")
	}
}
