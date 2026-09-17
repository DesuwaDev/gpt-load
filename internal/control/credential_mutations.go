package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

// credentialLimitUpdate 描述一次凭据本地限额的可选修改；nil 表示未提交该字段。
type credentialLimitUpdate struct {
	rpm         *int64
	concurrency *int64
}

// credentialMarkUpdate 描述一次凭据人工标记的可选修改；nil 表示未提交该字段。
// 标记与备注同进同出，所以两个指针要么都为 nil，要么都非 nil。
type credentialMarkUpdate struct {
	mark *string
	note *string
}

// credentialUpdatePlan 汇总一次凭据配置更新里已校验的字段，避免返回值继续膨胀。
type credentialUpdatePlan struct {
	status    *state.CredentialStatus
	weight    *int
	weightSet bool
	limits    credentialLimitUpdate
	mark      credentialMarkUpdate
	// codexTurnState 为 nil 表示未提交；空串表示清除注入。
	// codexTurnStateModels 同理，空串表示不限模型。
	codexTurnState       *string
	codexTurnStateModels *string
	proxy                *string
	proxySet             bool
}

func normalizeCredentialUpdate(
	request CredentialUpdateRequest,
	encryptionService encryption.Service,
) (credentialUpdatePlan, error) {
	var plan credentialUpdatePlan
	if !request.Status.Set && !request.WeightManual.Set && !request.RPMLimit.Set &&
		!request.ConcurrencyLimit.Set && !request.Mark.Set && !request.MarkNote.Set &&
		!request.CodexTurnState.Set && !request.CodexTurnStateModels.Set && !request.Proxy.Set {
		return plan, app_errors.ErrBadRequest
	}
	if request.Status.Set {
		if request.Status.Null ||
			(request.Status.Value != state.CredentialStatusActive && request.Status.Value != state.CredentialStatusDisabled) {
			return plan, app_errors.ErrValidation
		}
		value := request.Status.Value
		plan.status = &value
	}
	if request.WeightManual.Set {
		plan.weightSet = true
		if !request.WeightManual.Null {
			if request.WeightManual.Value < 1 || request.WeightManual.Value > state.MaxWeight {
				return plan, app_errors.ErrValidation
			}
			value := request.WeightManual.Value
			plan.weight = &value
		}
	}
	// 限额字段不接受 null：0 即不限，与访问密钥的 rpm_limit 语义一致。
	if request.RPMLimit.Set {
		if request.RPMLimit.Null || request.RPMLimit.Value < 0 {
			return plan, app_errors.ErrValidation
		}
		value := request.RPMLimit.Value
		plan.limits.rpm = &value
	}
	if request.ConcurrencyLimit.Set {
		if request.ConcurrencyLimit.Null || request.ConcurrencyLimit.Value < 0 {
			return plan, app_errors.ErrValidation
		}
		value := request.ConcurrencyLimit.Value
		plan.limits.concurrency = &value
	}
	mark, note, err := normalizeCredentialMark(request)
	if err != nil {
		return plan, err
	}
	plan.mark.mark, plan.mark.note = mark, note
	plan.codexTurnState, err = normalizeCodexTurnState(request.CodexTurnState)
	if err != nil {
		return plan, err
	}
	plan.codexTurnStateModels, err = normalizeCodexTurnStateModels(request.CodexTurnStateModels)
	if err != nil {
		return plan, err
	}
	plan.proxy, plan.proxySet, err = normalizeProxyOverride(request.Proxy, encryptionService)
	if err != nil {
		return plan, err
	}
	return plan, nil
}

// normalizeCredentialMark 校验人工标记；未提交时返回两个 nil。
func normalizeCredentialMark(request CredentialUpdateRequest) (*string, *string, error) {
	if request.Mark.Set != request.MarkNote.Set {
		return nil, nil, app_errors.ErrValidation
	}
	if !request.Mark.Set {
		return nil, nil, nil
	}
	if request.Mark.Null || request.MarkNote.Null {
		return nil, nil, app_errors.ErrValidation
	}
	mark := request.Mark.Value
	switch mark {
	case credentialMarkNone, credentialMarkDegraded, credentialMarkAbnormal, credentialMarkCustom:
	default:
		return nil, nil, app_errors.ErrValidation
	}
	note := strings.TrimSpace(request.MarkNote.Value)
	if !validCredentialMarkNote(note) {
		return nil, nil, app_errors.ErrValidation
	}
	// 未标记时没有可挂备注；自定义标记的备注就是标签本身，必须给出。
	if (mark == credentialMarkNone && note != "") || (mark == credentialMarkCustom && note == "") {
		return nil, nil, app_errors.ErrValidation
	}
	return &mark, &note, nil
}

// normalizeCodexTurnState 校验凭据级强制注入的 X-Codex-Turn-State；未提交时返回 nil。
// null 等价于清除注入，与前端“留空即关闭”的输入一致；值只做去空白，不做截断——
// 截断后的 state 上游必然拒收，还不如让操作者自己看见长度超限。
func normalizeCodexTurnState(field optionalField[string]) (*string, error) {
	if !field.Set {
		return nil, nil
	}
	if field.Null {
		value := ""
		return &value, nil
	}
	value := strings.TrimSpace(field.Value)
	if !validCodexTurnState(value) {
		return nil, app_errors.ErrValidation
	}
	return &value, nil
}

// validCodexTurnState 只放行能原样进 HTTP 头的单行 ASCII 可见字符串。
func validCodexTurnState(value string) bool {
	if len(value) > maxCodexTurnStateBytes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

// presentCodexTurnState 过滤直接改库留下的脏值，避免响应里出现无法注入的头值。
func presentCodexTurnState(row models.Credential) string {
	value := strings.TrimSpace(row.CodexTurnState)
	if !validCodexTurnState(value) {
		return ""
	}
	return value
}

// normalizeCodexTurnStateModels 校验并规范化注入的模型名单；未提交时返回 nil。
// null 与空串都表示不限模型。规范化在这里做完，网关那侧就只剩一次朴素的逗号切分。
func normalizeCodexTurnStateModels(field optionalField[string]) (*string, error) {
	if !field.Set {
		return nil, nil
	}
	if field.Null {
		value := ""
		return &value, nil
	}
	value, ok := canonicalCodexTurnStateModels(field.Value)
	if !ok {
		return nil, app_errors.ErrValidation
	}
	return &value, nil
}

// canonicalCodexTurnStateModels 把名单收敛成「去空白、小写、去重、逗号分隔」的单行
// 形式。条目允许以 * 结尾做前缀匹配，除此之外不接受通配符——模型名本身不含 * 。
func canonicalCodexTurnStateModels(raw string) (string, bool) {
	entries := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if !validCodexTurnStateModelEntry(entry) {
			return "", false
		}
		if _, duplicated := seen[entry]; duplicated {
			continue
		}
		seen[entry] = struct{}{}
		entries = append(entries, entry)
	}
	value := strings.Join(entries, ",")
	if len(value) > maxCodexTurnStateModelsBytes {
		return "", false
	}
	return value, true
}

// validCodexTurnStateModelEntry 只放行模型名里真正会出现的字符，外加结尾的 * 。
func validCodexTurnStateModelEntry(entry string) bool {
	entry = strings.TrimSuffix(entry, "*")
	if entry == "" {
		return false
	}
	for _, r := range entry {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':', r == '/':
		default:
			return false
		}
	}
	return true
}

// presentCodexTurnStateModels 过滤直接改库留下的脏值，口径与写入时一致。
func presentCodexTurnStateModels(row models.Credential) string {
	value, ok := canonicalCodexTurnStateModels(row.CodexTurnStateModels)
	if !ok {
		return ""
	}
	return value
}

func validCredentialMarkNote(note string) bool {
	if utf8.RuneCountInString(note) > maxCredentialMarkNoteRunes {
		return false
	}
	for _, r := range note {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// presentCredentialMark 只输出满足契约的标记，避免直接改库留下的脏值污染响应。
func presentCredentialMark(row models.Credential) (string, string) {
	switch row.Mark {
	case credentialMarkDegraded, credentialMarkAbnormal, credentialMarkCustom:
	default:
		return credentialMarkNone, ""
	}
	note := strings.TrimSpace(row.MarkNote)
	if !validCredentialMarkNote(note) {
		note = ""
	}
	if row.Mark == credentialMarkCustom && note == "" {
		return credentialMarkNone, ""
	}
	return row.Mark, note
}

func nextCredentialUpdatedAtMS(now time.Time, previous int64) (int64, error) {
	nowMS, err := epochms.FromTime(now)
	if err != nil {
		return 0, err
	}
	if nowMS < 1 {
		nowMS = 1
	}
	if nowMS <= previous {
		if previous == math.MaxInt64 {
			return 0, fmt.Errorf("credential version exhausted")
		}
		nowMS = previous + 1
	}
	return nowMS, nil
}

func equalOptionalWeight(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func findRuntimeCredential(
	views []state.CredentialRuntimeView,
	credentialID uint,
) (state.CredentialRuntimeView, bool) {
	for _, view := range views {
		if view.ID == credentialID {
			return view, true
		}
	}
	return state.CredentialRuntimeView{}, false
}

func (s *Service) RevealGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialRevealResult, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialRevealResult{}, app_errors.ErrBadRequest
	}
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return CredentialRevealResult{}, err
	}
	if group.ChannelID == "" {
		return CredentialRevealResult{}, app_errors.ErrValidation
	}
	if normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		return CredentialRevealResult{}, app_errors.ErrForbidden
	}
	var row models.Credential
	if err := s.db.WithContext(ctx).Select("id", "group_id", "data", "fingerprint", "identity_fingerprint", "secret_version", "auth_state", "status", "weight_manual", "updated_at_ms").
		Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CredentialRevealResult{}, credentialNotFoundError()
		}
		return CredentialRevealResult{}, app_errors.ParseDBError(err)
	}
	credential, _, err := s.decodeCredential(group, row)
	if err != nil {
		return CredentialRevealResult{}, err
	}
	revealedAtMS, err := safeEpochMilliseconds(s.now())
	if err != nil {
		return CredentialRevealResult{}, app_errors.ErrInternalServer
	}
	return CredentialRevealResult{
		CredentialID: row.ID, Credential: append([]byte(nil), credential...), RevealedAtMS: revealedAtMS,
	}, nil
}

func (s *Service) UpdateGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	request CredentialUpdateRequest,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialItemResponse{}, app_errors.ErrBadRequest
	}
	plan, err := normalizeCredentialUpdate(request, s.encryption)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	var committed models.Credential
	var committedGroup models.Group
	var committedProxy, committedProxyFingerprint string
	committedProxyUpdate := false
	err = s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		if request.Proxy.Set && !request.Proxy.Null &&
			!s.channelRegistry.SupportsOutboundProxy(channel.ID(group.ChannelID)) {
			return app_errors.ErrValidation
		}
		committedGroup = group
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&committed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialNotFoundError()
			}
			return app_errors.ParseDBError(err)
		}
		view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if err := validateCredentialRuntimeRow(group, committed, view, exists); err != nil {
			return err
		}
		updatedAtMS, err := nextCredentialUpdatedAtMS(s.now(), committed.UpdatedAtMS)
		if err != nil {
			return app_errors.ErrInternalServer
		}
		updates := map[string]any{"updated_at_ms": updatedAtMS}
		if plan.status != nil {
			committed.Status = models.CredentialStatus(*plan.status)
			updates["status"] = committed.Status
		}
		if plan.weightSet {
			committed.WeightManual = cloneInt(plan.weight)
			updates["weight_manual"] = committed.WeightManual
		}
		if plan.limits.rpm != nil {
			committed.RPMLimit = *plan.limits.rpm
			updates["rpm_limit"] = committed.RPMLimit
		}
		if plan.limits.concurrency != nil {
			committed.ConcurrencyLimit = *plan.limits.concurrency
			updates["concurrency_limit"] = committed.ConcurrencyLimit
		}
		// 标记只用于呈现，因此不同步到运行时注册表，也不参与 DB↔注册表一致性校验。
		if plan.mark.mark != nil {
			committed.Mark, committed.MarkNote = *plan.mark.mark, *plan.mark.note
			updates["mark"], updates["mark_note"] = committed.Mark, committed.MarkNote
		}
		// 与标记不同，注入值参与运行时转发，所以下面的注册表同步闭包必须带上它。
		if plan.codexTurnState != nil {
			committed.CodexTurnState = *plan.codexTurnState
			updates["codex_turn_state"] = committed.CodexTurnState
		}
		if plan.codexTurnStateModels != nil {
			committed.CodexTurnStateModels = *plan.codexTurnStateModels
			updates["codex_turn_state_models"] = committed.CodexTurnStateModels
		}
		if plan.proxySet {
			committed.ProxyConfig = plan.proxy
			updates["proxy_config"] = plan.proxy
		}
		committed.UpdatedAtMS = updatedAtMS
		committedProxy, committedProxyFingerprint, err = storedProxyIdentity(s.encryption, committed.ProxyConfig)
		if err != nil {
			return err
		}
		if err := tx.Model(&models.Credential{}).Where("id = ? AND group_id = ?", credentialID, groupID).
			Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		committedProxyUpdate = plan.proxySet
		entries, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credentialID})
		if snapshotErr != nil {
			return dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
		}
		entry := entries[0]
		entry.Status = state.CredentialStatus(committed.Status)
		entry.WeightManual = cloneInt(committed.WeightManual)
		entry.RPMLimit = committed.RPMLimit
		entry.ConcurrencyLimit = committed.ConcurrencyLimit
		entry.Version = groupCollectionCredentialVersion(committed.SecretVersion)
		entry.IdentityGeneration = groupCollectionCredentialIdentity(
			committed.IdentityFingerprint,
			committedGroup,
		)
		entry.Fingerprint = committed.Fingerprint
		entry.EncryptedValue = committed.Data
		entry.EncryptedProxy = committedProxy
		entry.ProxyFingerprint = committedProxyFingerprint
		entry.CodexTurnState = committed.CodexTurnState
		entry.CodexTurnStateModels = committed.CodexTurnStateModels
		return s.registry.RestoreGroupCredentialEntriesExact(groupID, []state.CredentialEntry{entry})
	})
	if committedProxyUpdate {
		s.retireCredentialRuntime(credentialID)
	}
	if err != nil {
		return CredentialItemResponse{}, err
	}
	return s.loadCredentialItem(ctx, groupID, credentialID)
}

func (s *Service) DeleteGroupCredential(ctx context.Context, groupID, credentialID uint) error {
	if groupID == 0 || credentialID == 0 {
		return app_errors.ErrBadRequest
	}
	return s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		var row models.Credential
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialNotFoundError()
			}
			return app_errors.ParseDBError(err)
		}
		view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if err := validateCredentialRuntimeRow(group, row, view, exists); err != nil {
			return err
		}
		if err := tx.Delete(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		if !s.registry.RemoveCredential(credentialID) {
			return dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
		}
		s.stats.Reset(credentialID)
		s.retireCredentialRuntime(credentialID)
		return nil
	})
}

func (s *Service) RestoreGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialItemResponse, error) {
	return s.restoreGroupCredential(ctx, groupID, credentialID, "")
}

func (s *Service) restoreGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	restoreProof string,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialItemResponse{}, app_errors.ErrBadRequest
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	if group.ChannelID == "" {
		return CredentialItemResponse{}, app_errors.ErrValidation
	}
	if restoreProof != "" &&
		normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		return CredentialItemResponse{}, app_errors.ErrForbidden
	}
	var row models.Credential
	if err := s.db.WithContext(ctx).Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CredentialItemResponse{}, credentialNotFoundError()
		}
		return CredentialItemResponse{}, app_errors.ParseDBError(err)
	}
	view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
	if err := validateCredentialRuntimeRow(group, row, view, exists); err != nil {
		return CredentialItemResponse{}, err
	}
	groupView := state.GroupCatalogView{ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual)}
	var (
		observedAt time.Time
		restoreErr error
	)
	restore := func(targetSignature *groupValidationSignature) {
		observedAt = s.now().UTC()
		var testedCredential *credentialProbeCredential
		current, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if !exists {
			restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
			return
		}
		if targetSignature == nil {
			bucket := classifyHealthKey(groupView, current, observedAt)
			if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted && !hasModelCooldown(current.ModelCooldowns, observedAt) {
				restoreErr = app_errors.ErrInvalidCredentialState
				return
			}
		} else {
			entries, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(
				groupID,
				[]uint{credentialID},
			)
			if snapshotErr != nil || len(entries) != 1 {
				restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
				return
			}
			if !s.credentialProbeRestoreProofMatches(entries[0], *targetSignature, restoreProof) {
				restoreErr = app_errors.ErrCredentialVersionConflict
				return
			}
			credential := credentialProbeCredentialFromEntry(entries[0])
			testedCredential = &credential
		}
		if targetSignature == nil {
			if !s.registry.ClearModelCooldowns(credentialID) {
				restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
				return
			}
			bucket := classifyHealthKey(groupView, current, observedAt)
			if (bucket == healthBucketCooldown || bucket == healthBucketBlacklisted) && !s.registry.RestoreRuntimeState(credentialID) {
				restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
				return
			}
		} else {
			if testedCredential == nil || !s.registry.RestoreRuntimeStateIfMatch(
				testedCredential.ref,
				testedCredential.cooldownUntil,
			) {
				restoreErr = app_errors.ErrCredentialVersionConflict
				return
			}
		}
		s.stats.ClearProblemState(credentialID)
	}
	coordinateRestore := func(targetSignature *groupValidationSignature) {
		if s.mutations == nil {
			restore(targetSignature)
		} else {
			s.mutations.Do(credentialID, func() { restore(targetSignature) })
		}
	}
	if restoreProof == "" {
		coordinateRestore(nil)
	} else {
		if s.manager == nil || s.mutations == nil {
			return CredentialItemResponse{}, app_errors.ErrInternalServer
		}
		matched := s.manager.WithCurrentSnapshot(func(snapshot *state.ConfigSnapshot) bool {
			if snapshot == nil {
				return false
			}
			currentGroup, exists := snapshot.Groups[groupID]
			if !exists {
				restoreErr = app_errors.ErrCredentialVersionConflict
				return false
			}
			currentTarget, valid := buildGroupValidationTarget(currentGroup)
			if !valid {
				restoreErr = app_errors.ErrCredentialVersionConflict
				return false
			}
			coordinateRestore(&currentTarget.signature)
			return restoreErr == nil
		})
		if !matched && restoreErr == nil {
			return CredentialItemResponse{}, app_errors.ErrInternalServer
		}
	}
	if restoreErr != nil {
		return CredentialItemResponse{}, restoreErr
	}
	view, exists = findRuntimeCredential(s.registry.Snapshot(), credentialID)
	if !exists {
		return CredentialItemResponse{}, dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
	}
	return s.mapCredentialItem(ctx, row, view, group, s.stats.Snapshot(credentialID, observedAt), observedAt)
}

func validateCredentialRuntimeRow(
	group models.Group,
	row models.Credential,
	view state.CredentialRuntimeView,
	exists bool,
) error {
	groupID := group.ID
	if !exists {
		return dbRegistryMismatch(mismatchMissingRegistry, groupID, row.ID)
	}
	if view.GroupID != groupID {
		return dbRegistryMismatch(mismatchGroupID, groupID, row.ID)
	}
	if view.Status != state.CredentialStatus(row.Status) {
		return dbRegistryMismatch(mismatchStatus, groupID, row.ID)
	}
	if view.AuthState != normalizeRuntimeCredentialAuthState(row.AuthState) {
		return dbRegistryMismatch(mismatchStatus, groupID, row.ID)
	}
	if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
		return dbRegistryMismatch(mismatchWeightManual, groupID, row.ID)
	}
	if view.Version != groupCollectionCredentialVersion(row.SecretVersion) ||
		view.IdentityGeneration != groupCollectionCredentialIdentity(row.IdentityFingerprint, group) {
		return dbRegistryMismatch(mismatchIdentity, groupID, row.ID)
	}
	return nil
}

func (s *Service) loadCredentialItem(ctx context.Context, groupID, credentialID uint) (CredentialItemResponse, error) {
	capture, err := s.captureCredentials(ctx, groupID)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	observation, err := validateCredentialCapture(capture)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	for _, row := range observation.rows {
		if row.ID == credentialID {
			return s.mapCredentialItem(ctx, row, observation.runtime[credentialID], observation.group,
				s.stats.Snapshot(credentialID, observation.observedAt), observation.observedAt)
		}
	}
	return CredentialItemResponse{}, credentialNotFoundError()
}

func (s *Service) mapCredentialItem(
	ctx context.Context,
	row models.Credential,
	view state.CredentialRuntimeView,
	group models.Group,
	stats health.CredentialStats,
	observedAt time.Time,
) (CredentialItemResponse, error) {
	canonical, identity, err := s.decodeCredential(group, row)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	mask, account, err := s.credentialPresentation(group, row, canonical, identity)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	bucket := classifyHealthKey(state.GroupCatalogView{ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual)}, view, observedAt)
	rpmUsed, concurrencyUsed := s.credentialLiveUsage(row.ID)
	item, err := mapCredentialRuntimeItem(mask, row.ID, view, bucket, stats, observedAt,
		credentialLimitContext{
			groupRPMLimit:         group.CredentialRPMLimit,
			groupConcurrencyLimit: group.CredentialConcurrencyLimit,
			rpmUsed:               rpmUsed,
			concurrencyUsed:       concurrencyUsed,
		},
	)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	item.ConnectionType = string(normalizeGroupConnectionType(group.ConnectionType))
	item.SecretVersion = row.SecretVersion
	item.AuthState = string(row.AuthState)
	item.Mark, item.MarkNote = presentCredentialMark(row)
	item.CodexTurnState = presentCodexTurnState(row)
	item.CodexTurnStateModels = presentCodexTurnStateModels(row)
	item.Account = account
	proxyViews, err := s.credentialProxyViews(ctx, s.db, group, []models.Credential{row})
	if err != nil {
		return CredentialItemResponse{}, err
	}
	item.Proxy = proxyViews[row.ID]
	if normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		var observation models.CredentialObservation
		result := s.db.WithContext(ctx).Take(&observation, "credential_id = ?", row.ID)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return CredentialItemResponse{}, app_errors.ParseDBError(result.Error)
		}
		item.Observation = presentCredentialObservation(observation, row.IdentityFingerprint)
	}
	return item, nil
}

func normalizeCredentialBatchRequest(request CredentialBatchRequest) ([]uint, bool, error) {
	if request.Action != CredentialBatchEnable && request.Action != CredentialBatchDisable &&
		request.Action != CredentialBatchDelete && request.Action != CredentialBatchRestore {
		return nil, false, app_errors.ErrValidation
	}
	if request.Scope == CredentialBatchScopeAll {
		if request.Action == CredentialBatchDelete || len(request.CredentialIDs) != 0 {
			return nil, false, app_errors.ErrValidation
		}
		return nil, true, nil
	}
	if request.Scope != "" || request.Action == CredentialBatchRestore {
		return nil, false, app_errors.ErrValidation
	}
	if len(request.CredentialIDs) < 1 || len(request.CredentialIDs) > 100 {
		return nil, false, app_errors.ErrValidation
	}
	ids := append([]uint(nil), request.CredentialIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for index, id := range ids {
		if id == 0 || index > 0 && id == ids[index-1] {
			return nil, false, app_errors.ErrValidation
		}
	}
	return ids, false, nil
}

func (s *Service) BatchGroupCredentials(
	ctx context.Context,
	groupID uint,
	request CredentialBatchRequest,
) (CredentialBatchResponse, error) {
	if groupID == 0 {
		return CredentialBatchResponse{}, app_errors.ErrBadRequest
	}
	ids, all, err := normalizeCredentialBatchRequest(request)
	if err != nil {
		return CredentialBatchResponse{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return CredentialBatchResponse{}, err
	}
	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return CredentialBatchResponse{}, err
	}
	if group.ChannelID == "" {
		return CredentialBatchResponse{}, app_errors.ErrValidation
	}
	var rows []models.Credential
	rowsQuery := s.db.WithContext(ctx)
	if all {
		rowsQuery = rowsQuery.Where("group_id = ?", groupID).Order("id ASC")
	} else {
		rowsQuery = rowsQuery.Where("id IN ?", ids)
	}
	if err := rowsQuery.Find(&rows).Error; err != nil {
		return CredentialBatchResponse{}, app_errors.ParseDBError(err)
	}
	if all {
		ids = make([]uint, len(rows))
		for index, row := range rows {
			ids[index] = row.ID
		}
		if len(ids) == 0 {
			return CredentialBatchResponse{
				AffectedCredentialIDs: []uint{},
				Summary:               summarizeGroupRuntimeCredentials(group, s.registry.Snapshot(), s.now().UTC()),
			}, nil
		}
	}
	if len(rows) != len(ids) {
		return CredentialBatchResponse{}, credentialNotFoundError()
	}
	rowByID := make(map[uint]models.Credential, len(rows))
	viewByID := make(map[uint]state.CredentialRuntimeView)
	for _, view := range s.registry.Snapshot() {
		viewByID[view.ID] = view
	}
	for _, row := range rows {
		if row.GroupID != groupID {
			return CredentialBatchResponse{}, credentialNotFoundError()
		}
		view, exists := viewByID[row.ID]
		if err := validateCredentialRuntimeRow(group, row, view, exists); err != nil {
			return CredentialBatchResponse{}, err
		}
		rowByID[row.ID] = row
	}
	coordinator, ok := s.mutations.(interface{ DoMany([]uint, func()) })
	if !ok {
		return CredentialBatchResponse{}, fmt.Errorf("batch mutation coordinator unavailable: %w", app_errors.ErrInternalServer)
	}
	var mutationErr error
	coordinator.DoMany(ids, func() {
		before, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(groupID, ids)
		if snapshotErr != nil {
			mutationErr = fmt.Errorf("snapshot credential registry entries: %w", app_errors.ErrInternalServer)
			return
		}
		if request.Action == CredentialBatchRestore {
			ids, mutationErr = s.restoreCredentialBatchRuntime(group, before)
			return
		}
		desired := make([]state.CredentialEntry, len(before))
		for index, entry := range before {
			desired[index] = entry
			if request.Action == CredentialBatchEnable {
				desired[index].Status = state.CredentialStatusActive
			} else if request.Action == CredentialBatchDisable {
				desired[index].Status = state.CredentialStatusDisabled
			}
		}
		persist := func() error {
			return s.withControlTransaction(ctx, func(tx *gorm.DB) error {
				query := tx.Where("group_id = ?", groupID)
				if !all {
					query = query.Where("id IN ?", ids)
				}
				var result *gorm.DB
				switch request.Action {
				case CredentialBatchEnable:
					result = query.Model(&models.Credential{}).Updates(map[string]any{
						"status": models.CredentialStatusActive,
					})
				case CredentialBatchDisable:
					result = query.Model(&models.Credential{}).Updates(map[string]any{
						"status": models.CredentialStatusDisabled,
					})
				case CredentialBatchDelete:
					result = query.Delete(&models.Credential{})
				}
				if result.Error != nil {
					return app_errors.ParseDBError(result.Error)
				}
				if result.RowsAffected != int64(len(ids)) {
					return fmt.Errorf("batch credential rows affected = %d, want %d: %w", result.RowsAffected, len(ids), app_errors.ErrDatabase)
				}
				return nil
			})
		}
		if s.applyBatchRegistryMutation == nil {
			mutationErr = app_errors.ErrInternalServer
			return
		}
		if request.Action == CredentialBatchEnable {
			// Enabling expands data-plane authority. Persist it before making
			// the credentials routable; disable/delete intentionally keep the
			// safer runtime-first ordering below.
			if mutationErr = persist(); mutationErr != nil {
				return
			}
			if applyErr := s.applyBatchRegistryMutation(groupID, ids, request.Action); applyErr != nil {
				operationErr := withControlOperationContext(
					newControlOperationError(stageApplyCommittedRegistryMutation),
					groupID,
					0,
				)
				mutationErr = joinCommittedRuntimeRecovery(
					errors.Join(operationErr, applyErr),
					s.recoverCommittedCredentialRegistryGroup(ctx, groupID),
				)
				return
			}
			if restoreErr := s.registry.RestoreGroupCredentialEntriesExact(groupID, desired); restoreErr != nil {
				operationErr := withControlOperationContext(
					newControlOperationError(stageApplyCommittedRegistryMutation),
					groupID,
					0,
				)
				mutationErr = joinCommittedRuntimeRecovery(
					errors.Join(operationErr, restoreErr),
					s.recoverCommittedCredentialRegistryGroup(ctx, groupID),
				)
			}
			return
		}
		if applyErr := s.applyBatchRegistryMutation(groupID, ids, request.Action); applyErr != nil {
			mutationErr = withControlOperationContext(newControlOperationError(stageApplyCommittedRegistryMutation), groupID, 0)
			return
		}
		if request.Action != CredentialBatchDelete {
			if restoreErr := s.registry.RestoreGroupCredentialEntriesExact(groupID, desired); restoreErr != nil {
				mutationErr = compensateCredentialBatchRegistry(s, groupID, before, restoreErr)
				return
			}
		}
		mutationErr = persist()
		if mutationErr != nil {
			mutationErr = compensateCredentialBatchRegistry(s, groupID, before, mutationErr)
			return
		}
		if request.Action == CredentialBatchDelete {
			for _, id := range ids {
				s.stats.Reset(id)
				s.retireCredentialRuntime(id)
			}
		}
	})
	if mutationErr != nil {
		return CredentialBatchResponse{}, mutationErr
	}
	return CredentialBatchResponse{
		AffectedCredentialIDs: ids,
		Summary:               summarizeGroupRuntimeCredentials(group, s.registry.Snapshot(), s.now().UTC()),
	}, nil
}

func (s *Service) restoreCredentialBatchRuntime(group models.Group, entries []state.CredentialEntry) ([]uint, error) {
	groupView := state.GroupCatalogView{ID: group.ID, Enabled: group.Enabled, WeightManual: group.WeightManual}
	now := s.now().UTC()
	restored := make([]uint, 0, len(entries))
	for _, entry := range entries {
		view := state.CredentialRuntimeView{
			Status: entry.Status, AuthState: entry.AuthState, WeightManual: entry.WeightManual,
			CooldownUntil: entry.CooldownUntil, Blacklisted: entry.Blacklisted,
		}
		bucket := classifyHealthKey(groupView, view, now)
		if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted && !hasModelCooldown(entry.ModelCooldowns, now) {
			continue
		}
		if !s.registry.ClearModelCooldowns(entry.ID) {
			return nil, dbRegistryMismatch(mismatchMissingRegistry, group.ID, entry.ID)
		}
		// 只修改健康字段，保留并发发布的订阅额度与授权状态。
		if (bucket == healthBucketCooldown || bucket == healthBucketBlacklisted) && !s.registry.RestoreRuntimeState(entry.ID) {
			return nil, dbRegistryMismatch(mismatchMissingRegistry, group.ID, entry.ID)
		}
		s.stats.ClearProblemState(entry.ID)
		restored = append(restored, entry.ID)
	}
	return restored, nil
}

func (s *Service) applyCredentialBatchRegistryMutation(
	groupID uint,
	credentialIDs []uint,
	action CredentialBatchAction,
) error {
	switch action {
	case CredentialBatchEnable:
		return s.registry.UpdateGroupCredentialStatuses(
			groupID, credentialIDs, state.CredentialStatusActive,
		)
	case CredentialBatchDisable:
		return s.registry.UpdateGroupCredentialStatuses(
			groupID, credentialIDs, state.CredentialStatusDisabled,
		)
	case CredentialBatchDelete:
		return s.registry.RemoveGroupCredentials(groupID, credentialIDs)
	default:
		return fmt.Errorf("unsupported batch credential action %q", action)
	}
}

func compensateCredentialBatchRegistry(
	s *Service,
	groupID uint,
	before []state.CredentialEntry,
	cause error,
) error {
	if s.restoreBatchRegistryEntries == nil {
		return errors.Join(
			cause,
			fmt.Errorf("compensate batch credential Registry mutation: %w", app_errors.ErrInternalServer),
		)
	}
	if err := s.restoreBatchRegistryEntries(groupID, before); err != nil {
		return errors.Join(
			cause,
			fmt.Errorf("compensate batch credential Registry mutation: %w", err),
		)
	}
	return cause
}

func summarizeGroupRuntimeCredentials(
	group models.Group,
	views []state.CredentialRuntimeView,
	observedAt time.Time,
) CredentialSummaryResponse {
	summary := CredentialSummaryResponse{}
	groupView := state.GroupCatalogView{
		ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual),
	}
	for _, view := range views {
		if view.GroupID != group.ID {
			continue
		}
		summary.Total++
		switch classifyHealthKey(groupView, view, observedAt) {
		case healthBucketAvailable:
			summary.Available++
		case healthBucketCooldown:
			summary.Cooldown++
		case healthBucketBlacklisted:
			summary.Blacklisted++
		case healthBucketDisabled:
			summary.Disabled++
		}
	}
	return summary
}
