package geminicli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	providerobservation "gpt-load/internal/subscription/providers/observation"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const quotaScopeAccount = "account"

type driver struct{}

// Implementations returns the Gemini CLI subscription behavior assembled by
// the application composition root.
func Implementations() subscriptionruntime.Implementations {
	value := &driver{}
	return subscriptionruntime.Implementations{
		Drivers:           []subscriptionruntime.Driver{value},
		ModelDiscoveries:  []subscriptionruntime.ModelDiscovery{modelDiscovery{value}},
		QuotaObservations: []subscriptionruntime.QuotaObservation{quotaObservation{value}},
	}
}

func (*driver) ID() spec.SubscriptionDriverID { return modules.GeminiCLISubscriptionDriver }

func (*driver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*driver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	refreshed, err := refreshCredentialOnce(ctx, value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(refreshed)
}

func (*driver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	if errors.Is(err, ErrCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
	var tokenErr *TokenEndpointError
	if errors.As(err, &tokenErr) {
		decision := subscriptionruntime.RefreshFailureDecision{
			Kind: subscriptionruntime.RefreshFailureOutcomeUnknown, StatusCode: tokenErr.StatusCode,
			OAuthCode: strings.TrimSpace(tokenErr.Code), RetryAfter: tokenErr.RetryAfter,
		}
		if subscriptionruntime.TokenEndpointFailureRetryable(tokenErr.StatusCode, tokenErr.Code) {
			decision.Kind = subscriptionruntime.RefreshFailureRetryable
		} else if IsDefinitiveRefreshRejection(tokenErr.Code) {
			decision.Kind = subscriptionruntime.RefreshFailureReauthorizationRequired
		}
		return decision
	}
	return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureOutcomeUnknown}
}

func (*driver) BeginAuthorization() (subscriptionruntime.Authorization, error) {
	return beginBrowserAuthorization()
}

func (*driver) LocalCallback() (subscriptionruntime.LocalCallbackSpec, bool) {
	return subscriptionruntime.LocalCallbackSpec{RedirectURI: RedirectURI, ManualOnly: true}, true
}

func (*driver) CompleteAuthorization(ctx context.Context, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
	value, err := completeBrowserAuthorization(ctx, completion)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

func (*driver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *TokenEndpointError
	return errors.Is(err, ErrProjectRequired) || errors.As(err, &tokenErr) && IsDefinitiveRefreshRejection(tokenErr.Code)
}

func (*driver) AuthorizationFailureCode(err error) string {
	if errors.Is(err, ErrProjectRequired) {
		return "authorization_project_required"
	}
	return ""
}

func (*driver) ImportCredential(ctx context.Context, raw []byte) (subscriptionruntime.Credential, error) {
	value, err := importCredential(ctx, raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return runtimeCredential(value)
}

// DiscoverModels 返回 Code Assist 的内置可选目录，不依赖额度接口。
func (*driver) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) ([]string, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return nil, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return nil, err
	}
	models, err := listModels(ctx, value, baseURL)
	return models, runtimeUpstreamError(err)
}

// Observe 根据已保存档位与本地成功请求统计生成共享的 RPD/RPM 展示窗口。
func (*driver) Observe(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	observed, err := observeAccount(ctx, value, baseURL)
	if err != nil {
		return subscriptionruntime.Observation{}, runtimeUpstreamError(err)
	}
	if !observed.AccountObserved {
		return subscriptionruntime.Observation{}, fmt.Errorf("%w: Gemini CLI account observation has no usable fields", subscriptionruntime.ErrObservationPayloadInvalid)
	}
	windows, complete := localQuotaWindows(ctx, observed.TierID)
	snapshot := providerobservation.Snapshot{Plan: geminiCLIPlan(observed.TierID), Account: &providerobservation.AccountSummary{Email: value.Email}, QuotaWindows: windows}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	result := subscriptionruntime.Observation{Payload: payload, AccountObserved: true, QuotaObserved: complete, Partial: len(windows) > 0 && !complete}
	if len(windows) > 0 {
		result.ObservedQuotaScopes = []string{quotaScopeAccount}
	}
	return result, nil
}

func runtimeUpstreamError(err error) error {
	var upstream *UpstreamHTTPError
	if errors.As(err, &upstream) {
		return &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
	}
	return err
}

func geminiCLIPlan(tierID string) providerobservation.PlanSummary {
	switch strings.ToLower(strings.TrimSpace(tierID)) {
	case "":
		return providerobservation.PlanSummary{}
	case "free-tier", "google_one_free":
		return providerobservation.PlanSummary{Name: "Free", Level: providerobservation.PlanLevelFree}
	case "g1-pro-tier", "google_ai_pro":
		return providerobservation.PlanSummary{Name: "Google AI Pro", Level: providerobservation.PlanLevelStandard}
	case "g1-ultra-tier", "google_ai_ultra":
		return providerobservation.PlanSummary{Name: "Google AI Ultra", Level: providerobservation.PlanLevelPremium}
	case "standard-tier", "gcp_standard":
		return providerobservation.PlanSummary{Name: "Code Assist Standard", Level: providerobservation.PlanLevelStandard}
	case "enterprise-tier", "gcp_enterprise":
		return providerobservation.PlanSummary{Name: "Code Assist Enterprise", Level: providerobservation.PlanLevelPremium}
	default:
		return providerobservation.PlanSummary{Name: providerobservation.DisplayName(tierID)}
	}
}

func runtimeCredential(value Credential) (subscriptionruntime.Credential, error) {
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	expiresAt, expires := CredentialExpiresAt(value)
	account := subscriptionruntime.Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(canonical, strings.TrimSpace(value.AccountID), account, expiresAt, expires, value.SecretValues()), nil
}

type modelDiscovery struct{ *driver }
type quotaObservation struct{ *driver }

func (modelDiscovery) ID() spec.UtilityID   { return modules.GeminiCLIModelDiscovery }
func (quotaObservation) ID() spec.UtilityID { return modules.GeminiCLIQuotaObservation }

var _ subscriptionruntime.BrowserAuthorizationDriver = (*driver)(nil)
var _ subscriptionruntime.CredentialFileImporter = (*driver)(nil)
