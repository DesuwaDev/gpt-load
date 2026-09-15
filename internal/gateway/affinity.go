package gateway

import (
	"gpt-load/internal/affinity"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

type requestAffinity struct {
	key                   affinity.Key
	observation           affinity.Observation
	preferredCredentialID uint
	continuityKey         string
	kind                  string
}

func (handler *Handler) resolveRequestAffinity(
	snapshot *state.ConfigSnapshot,
	accessKeyID uint,
	clientProtocol protocol.Protocol,
	prefix []byte,
	allowedCredentialRefs map[uint]state.CredentialRef,
	promptCacheKey string,
) requestAffinity {
	if handler == nil || snapshot == nil {
		return requestAffinity{}
	}
	key := affinity.DeriveKey(
		handler.encryption,
		accessKeyID,
		clientProtocol,
		prefix,
	)
	// 轮换独立于软亲和：它换的是上游分片，不是凭证，所以亲和关闭时也要可用。
	if handler.cacheKeyRotations != nil {
		handler.cacheKeyRotations.Configure(
			snapshot.Revision,
			snapshot.Settings.AffinityCapacity,
			snapshot.Settings.AffinityTTL,
		)
	}
	// 执行层私有 replay scope 仍由提示词派生，不把客户端缓存分组当作会话身份。
	result := requestAffinity{continuityKey: string(key), kind: telemetry.AffinityPromptPrefix}
	if promptCacheKey != "" {
		key = affinity.DerivePromptCacheKey(handler.encryption, accessKeyID, clientProtocol, promptCacheKey)
		result.kind = telemetry.AffinityPromptCacheKey
	}
	if handler.affinityCache == nil ||
		!handler.affinityCache.Configure(
			snapshot.Revision,
			snapshot.Settings.AffinityCapacity,
			snapshot.Settings.AffinityTTL,
		) {
		return result
	}
	if !key.Valid() {
		return result
	}
	result.key = key
	observation := handler.affinityCache.Lookup(key)
	resolved := result
	resolved.observation = observation
	if !observation.Found() {
		return resolved
	}
	target := observation.Target
	group, exists := snapshot.Groups[target.GroupID]
	if !exists || !group.AffinityEnabled {
		return resolved
	}
	ref, allowed := allowedCredentialRefs[target.CredentialID]
	if !allowed || ref.GroupID != target.GroupID ||
		ref.IdentityGeneration != target.IdentityGeneration {
		return resolved
	}
	resolved.preferredCredentialID = target.CredentialID
	return resolved
}

func (handler *Handler) recordAffinitySuccess(
	request requestAffinity,
	selection scheduler.Selection,
	ref state.CredentialRef,
) {
	if handler == nil || handler.affinityCache == nil || !request.key.Valid() ||
		!selection.Group.AffinityEnabled {
		return
	}
	handler.affinityCache.RecordSuccess(
		request.key,
		request.observation,
		affinity.Target{
			GroupID: selection.GroupID, CredentialID: selection.CredentialID,
			IdentityGeneration: ref.IdentityGeneration,
		},
	)
}

// rotatedContinuityKey applies a recorded rotation to the session identity sent
// upstream. The key reaching the provider changes, so the upstream routes the
// conversation to a different cache shard; GPT-Load's own credential selection
// and affinity were already resolved from the unrotated key.
func (handler *Handler) rotatedContinuityKey(selection scheduler.Selection, base string) string {
	if handler == nil || base == "" || !selection.Group.CacheKeyRotationEnabled {
		return base
	}
	// 降智判定认定这个凭证正在以次充好；换分片是本层唯一能做的补救，一个降智
	// 周期内对同一个会话只换一次。
	var episode uint64
	if handler.degradedTargets != nil {
		episode = handler.degradedTargets.Episode(
			selection.CredentialID,
			optionalModelValue(selection.UpstreamModelID),
		)
	}
	return affinity.RotatedContinuityKey(
		base,
		handler.cacheKeyRotations.RotateForEpoch(base, episode),
	)
}

// rotateCacheKeyOnFailure moves a conversation off the shard that just rejected
// it for want of capacity. The next request carrying the same continuity key
// derives a new upstream session identity; any other failure, and any unrotated
// conversation, is left byte-identical.
func (handler *Handler) rotateCacheKeyOnFailure(
	selection scheduler.Selection,
	decision health.Decision,
	base string,
) {
	if handler == nil || base == "" || !selection.Group.CacheKeyRotationEnabled ||
		!decision.IndicatesUpstreamCapacityPressure() {
		return
	}
	handler.cacheKeyRotations.Rotate(base)
}
