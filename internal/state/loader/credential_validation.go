package loader

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type credentialValidationIssue struct {
	index int
	code  string
}

// Credential data failures are isolated to that row. Missing execution bindings
// remain configuration errors; no unvalidated credential becomes schedulable.
func (l *Loader) validatePersistedCredentials(ctx context.Context, input state.CompileInput, entries []state.CredentialEntry) error {
	if l.encryption == nil {
		return nil
	}
	type validationTarget struct {
		channelID      channel.ID
		connectionType string
	}
	targets := make(map[uint]validationTarget, len(input.Groups))
	for _, group := range input.Groups {
		targets[group.ID] = validationTarget{group.ChannelID, group.ConnectionType}
	}
	issues := []credentialValidationIssue{}
	for index, entry := range entries {
		target, exists := targets[entry.GroupID]
		if !exists {
			return fmt.Errorf("validate credential %d for group %d: execution target is missing", entry.ID, entry.GroupID)
		}
		expectedConnection, known := l.channelRegistry.ConnectionType(target.channelID)
		if !known || strings.TrimSpace(target.connectionType) != expectedConnection {
			return fmt.Errorf("validate credential %d for group %d: connection does not match channel", entry.ID, entry.GroupID)
		}
		if expectedConnection != string(models.ConnectionTypeAPIKey) && (expectedConnection != string(models.ConnectionTypeSubscription) || l.subscriptions == nil) {
			return fmt.Errorf("validate credential %d for group %d: subscription driver is unavailable", entry.ID, entry.GroupID)
		}
		plaintext, err := l.encryption.Decrypt(entry.EncryptedValue)
		if err != nil {
			issues = append(issues, credentialValidationIssue{index, models.CredentialStartupDecryptFailed})
			continue
		}
		// The persisted HMAC describes the bytes originally encrypted, not the JSON
		// a newer serializer might produce after adding optional provider defaults.
		storedFingerprint := l.encryption.Hash(plaintext)
		raw := []byte(plaintext)
		plaintext = ""
		var canonical []byte
		if expectedConnection == string(models.ConnectionTypeSubscription) {
			canonical, err = l.subscriptions.CanonicalCredential(target.channelID, raw)
		} else {
			credential, validateErr := l.channelRegistry.ValidateCredential(target.channelID, raw)
			err = validateErr
			if err == nil {
				canonical = credential.CanonicalJSON()
			}
		}
		clear(raw)
		if err != nil {
			clear(canonical)
			issues = append(issues, credentialValidationIssue{index, models.CredentialStartupShapeInvalid})
			continue
		}
		canonicalFingerprint := l.encryption.Hash(string(canonical))
		clear(canonical)
		storedMatches := subtle.ConstantTimeCompare([]byte(storedFingerprint), []byte(entry.Fingerprint))
		canonicalMatches := subtle.ConstantTimeCompare([]byte(canonicalFingerprint), []byte(entry.Fingerprint))
		if storedMatches|canonicalMatches != 1 {
			issues = append(issues, credentialValidationIssue{index, models.CredentialStartupFingerprintMismatch})
		}
	}
	if len(issues) == 0 {
		return nil
	}
	// Keep encrypted data, fingerprints, identity and secret versions intact.
	// A durable auth gate keeps later registry reloads from re-enabling bad rows.
	if err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, issue := range issues {
			entry := entries[issue.index]
			result := tx.Model(&models.Credential{}).
				Where("id = ? AND group_id = ? AND secret_version = ? AND fingerprint = ?", entry.ID, entry.GroupID, entry.Version, entry.Fingerprint).
				Updates(map[string]any{"auth_state": models.CredentialAuthStateReauthorizationRequired, "auth_error_code": issue.code, "updated_at_ms": time.Now().UnixMilli()})
			if result.Error != nil {
				return fmt.Errorf("quarantine credential %d: update failed", entry.ID)
			}
			if result.RowsAffected != 1 {
				// MySQL may report zero changed rows for an already quarantined record.
				var unchanged int64
				err := tx.Model(&models.Credential{}).
					Where("id = ? AND group_id = ? AND secret_version = ? AND fingerprint = ? AND auth_state = ? AND auth_error_code = ?", entry.ID, entry.GroupID, entry.Version, entry.Fingerprint, models.CredentialAuthStateReauthorizationRequired, issue.code).
					Count(&unchanged).Error
				if err != nil || unchanged != 1 {
					return fmt.Errorf("quarantine credential %d: stored version changed", entry.ID)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, issue := range issues {
		entry := &entries[issue.index]
		entry.AuthState = state.CredentialAuthStateReauthorizationRequired
		logrus.WithFields(logrus.Fields{"event": "startup.credential_quarantined", "credential_id": entry.ID, "group_id": entry.GroupID, "error_code": issue.code}).Warn("credential failed startup validation and requires repair")
	}
	return nil
}
