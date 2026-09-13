package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

// degradationCredential is one prepared credential ready for a detection call.
type degradationCredential struct {
	snapshot         execution.CredentialSnapshot
	apiKey           string
	proxy            outboundproxy.Effective
	proxyFingerprint string
}

// degradationTarget is one monitored credential compiled into an executable
// upstream target. The compiled Group is always enabled locally and never
// published to the routing snapshot, so a disabled group can still be probed to
// find out whether it has recovered.
type degradationTarget struct {
	channelID      channel.ID
	resolvedTarget channel.ResolvedTarget
	credential     degradationCredential
	headerRules    state.HeaderRules
	timeouts       state.TimeoutConfig
}

// degradationTargetRows is the persisted state one detection run needs.
type degradationTargetRows struct {
	found      bool
	group      models.Group
	credential models.Credential
	settings   []models.SystemSetting
}

// readDegradationTargetRows loads the group, the monitored credential, and the
// system settings in one read snapshot. Disabled groups and disabled
// credentials are returned as-is; the scheduler decides whether to probe them.
func (s *Service) readDegradationTargetRows(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (degradationTargetRows, error) {
	if groupID == 0 || credentialID == 0 {
		return degradationTargetRows{}, app_errors.ErrValidation
	}
	var rows degradationTargetRows
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var credentials []models.Credential
		if err := tx.
			Where("id = ? AND group_id = ?", credentialID, groupID).
			Limit(1).Find(&credentials).Error; err != nil {
			return err
		}
		if len(credentials) == 0 {
			return nil
		}
		var groups []models.Group
		if err := tx.Where("id = ?", groupID).Limit(1).Find(&groups).Error; err != nil {
			return err
		}
		if len(groups) == 0 {
			return nil
		}
		rows.found = true
		rows.group = cloneGroupRows(groups)[0]
		rows.credential = cloneDiscoveryCredentialRows(credentials)[0]

		var settings []models.SystemSetting
		if err := tx.
			Order(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "key"}}}}).
			Find(&settings).Error; err != nil {
			return err
		}
		rows.settings = append([]models.SystemSetting(nil), settings...)
		return nil
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return degradationTargetRows{}, parentErr
	}
	if err != nil {
		return degradationTargetRows{}, fmt.Errorf(
			"load degradation target snapshot: %w",
			app_errors.ErrInternalServer,
		)
	}
	return rows, nil
}

// mapDegradationTarget compiles one persisted credential into an executable
// target. It mirrors the model-discovery path, except that it prepares exactly
// one credential and forces the compiled Group to be enabled.
func (s *Service) mapDegradationTarget(
	ctx context.Context,
	rows degradationTargetRows,
) (degradationTarget, error) {
	if err := ctx.Err(); err != nil {
		return degradationTarget{}, err
	}
	if !rows.found {
		return degradationTarget{}, app_errors.ErrResourceNotFound
	}
	if s == nil || s.channelRegistry == nil {
		return degradationTarget{}, app_errors.ErrInternalServer
	}

	resolvedTarget, err := s.channelRegistry.Resolve(
		channel.ID(rows.group.ChannelID),
		json.RawMessage(rows.group.Params),
	)
	if err != nil {
		return degradationTarget{}, fmt.Errorf(
			"resolve degradation channel: %w", app_errors.ErrInternalServer,
		)
	}
	overrides := rows.group.Overrides
	if len(bytes.TrimSpace(overrides)) == 0 {
		overrides = models.JSON(`{}`)
	}
	settings := make(config.Settings)
	if err := decodeGroupDiscoveryJSON(overrides, &settings); err != nil {
		return degradationTarget{}, fmt.Errorf(
			"decode degradation overrides: %w", app_errors.ErrInternalServer,
		)
	}
	systemSettings, err := stateloader.MapSystemSettings(rows.settings)
	if err != nil {
		return degradationTarget{}, fmt.Errorf(
			"load degradation settings: %w", app_errors.ErrInternalServer,
		)
	}
	snapshot, err := state.Compile(state.CompileInput{
		SystemSettings: systemSettings, ChannelRegistry: s.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: rows.group.ID, Name: rows.group.Name,
			ChannelID: channel.ID(rows.group.ChannelID), ConnectionType: string(rows.group.ConnectionType),
			Params:   append(json.RawMessage(nil), rows.group.Params...),
			Settings: settings, Enabled: true,
		}},
	})
	if err != nil {
		return degradationTarget{}, fmt.Errorf(
			"compile degradation target: %w", app_errors.ErrInternalServer,
		)
	}
	compiledGroup, ok := snapshot.Groups[rows.group.ID]
	if !ok {
		return degradationTarget{}, fmt.Errorf(
			"compiled degradation Group is missing: %w", app_errors.ErrInternalServer,
		)
	}

	var canonical []byte
	apiKey := ""
	if normalizeGroupConnectionType(rows.group.ConnectionType) == models.ConnectionTypeSubscription {
		credential, prepareErr := s.prepareStoredSubscriptionCredential(ctx, rows.group, rows.credential)
		if prepareErr != nil {
			return degradationTarget{}, prepareErr
		}
		canonical = credential.Canonical()
	} else {
		var decodeErr error
		canonical, apiKey, decodeErr = s.decodeCredential(rows.group, rows.credential)
		if decodeErr != nil {
			return degradationTarget{}, decodeErr
		}
	}
	prepared := degradationCredential{
		snapshot: execution.NewCredentialSnapshot(
			rows.credential.ID,
			groupCollectionCredentialVersion(rows.credential.SecretVersion),
			groupCollectionCredentialIdentity(rows.credential.IdentityFingerprint, rows.group),
			canonical,
		),
		apiKey: apiKey,
	}
	network, networkErr := s.credentialNetworkContext(ctx, s.db, rows.group, rows.credential)
	clear(canonical)
	if networkErr != nil {
		return degradationTarget{}, networkErr
	}
	prepared.proxy = network.Proxy
	prepared.proxyFingerprint = network.Fingerprint

	return degradationTarget{
		channelID:      channel.ID(rows.group.ChannelID),
		resolvedTarget: resolvedTarget,
		credential:     prepared,
		headerRules:    compiledGroup.HeaderRules,
		timeouts:       compiledGroup.Timeouts,
	}, nil
}

// buildDegradationTarget reads and compiles one monitored credential.
func (s *Service) buildDegradationTarget(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (degradationTarget, error) {
	rows, err := s.readDegradationTargetRows(ctx, groupID, credentialID)
	if err != nil {
		return degradationTarget{}, err
	}
	return s.mapDegradationTarget(ctx, rows)
}
