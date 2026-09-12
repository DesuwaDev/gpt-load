package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type codexDriver struct{}

func newCodexDriver() *codexDriver { return &codexDriver{} }

// Implementations returns the concrete Codex subscription behavior assembled by
// the application composition root.
func Implementations() subscriptionruntime.Implementations {
	driver := newCodexDriver()
	return subscriptionruntime.Implementations{
		Drivers:            []subscriptionruntime.Driver{driver},
		ModelDiscoveries:   []subscriptionruntime.ModelDiscovery{driver.modelDiscovery()},
		QuotaObservations:  []subscriptionruntime.QuotaObservation{driver.quotaObservation()},
		ResetCreditActions: []subscriptionruntime.ResetCreditAction{driver.resetCreditAction()},
	}
}

func (*codexDriver) ID() spec.SubscriptionDriverID { return modules.CodexSubscriptionDriver }

func (*codexDriver) Parse(raw []byte) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return codexRuntimeCredential(value, canonical), nil
}

func (*codexDriver) Refresh(ctx context.Context, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
	value, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	refreshed, err := RefreshCredentialOnce(ctx, value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(refreshed)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return codexRuntimeCredential(refreshed, canonical), nil
}

func (*codexDriver) ClassifyRefreshFailure(err error) subscriptionruntime.RefreshFailureDecision {
	var tokenErr *TokenEndpointError
	if errors.Is(err, ErrCredentialIdentityChanged) {
		return subscriptionruntime.RefreshFailureDecision{Kind: subscriptionruntime.RefreshFailureIdentityChanged}
	}
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

type codexAuthorizationState struct {
	Verifier string `json:"verifier"`
}

func (*codexDriver) BeginAuthorization() (subscriptionruntime.Authorization, error) {
	value, err := BeginBrowserAuthorization()
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	state, err := json.Marshal(codexAuthorizationState{Verifier: value.CodeVerifier})
	if err != nil {
		return subscriptionruntime.Authorization{}, err
	}
	return subscriptionruntime.Authorization{
		URL: value.AuthorizationURL, State: value.State, DriverState: state,
		ExpiresAt: value.ExpiresAt,
	}, nil
}

func (*codexDriver) LocalCallback() (subscriptionruntime.LocalCallbackSpec, bool) {
	return subscriptionruntime.LocalCallbackSpec{RedirectURI: "http://localhost:1455/auth/callback"}, true
}

func (*codexDriver) CompleteAuthorization(ctx context.Context, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
	var state codexAuthorizationState
	if json.Unmarshal(completion.DriverState, &state) != nil || strings.TrimSpace(state.Verifier) == "" {
		return subscriptionruntime.Credential{}, ErrInvalidState
	}
	value, err := CompleteBrowserAuthorization(ctx, BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState,
		Code: completion.Code, CodeVerifier: state.Verifier,
	})
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	return codexRuntimeCredential(value, canonical), nil
}

func (*codexDriver) AuthorizationFailureDefinitive(err error) bool {
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(tokenErr.Code)) {
	case "invalid_grant", "access_denied":
		return true
	default:
		return false
	}
}

// DiscoverModels lists the models visible to a Codex subscription credential
// through the resolved official or custom target.
func (*codexDriver) DiscoverModels(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) ([]string, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return nil, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return nil, err
	}
	models, err := ListModels(ctx, value, baseURL)
	if err != nil {
		var upstream *UpstreamHTTPError
		if errors.As(err, &upstream) {
			return nil, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return nil, err
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, model.ID)
	}
	return cpaembedded.MergeModelCatalog(cpaembedded.ProviderCodex, result), nil
}

// Observe retrieves Codex usage and reset-credit details from the resolved
// target and normalizes them into the provider-neutral quota contract.
func (*codexDriver) Observe(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return subscriptionruntime.Observation{}, err
	}
	observed, err := ObserveAccount(ctx, value, baseURL)
	if err != nil {
		var upstream *UpstreamHTTPError
		if errors.As(err, &upstream) {
			return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return subscriptionruntime.Observation{}, err
	}
	var detailsPayload []byte
	details, detailErr := ObserveResetCredits(ctx, value, baseURL)
	if detailErr == nil {
		detailsPayload = details.Payload
	}
	normalized, err := NormalizeQuota(observed.Payload, detailsPayload)
	if err != nil {
		return subscriptionruntime.Observation{}, fmt.Errorf("%w: %v", subscriptionruntime.ErrObservationPayloadInvalid, err)
	}
	return subscriptionruntime.Observation{Payload: normalized, Header: observed.Header.Clone(), QuotaObserved: true}, nil
}

// Consume redeems one Codex reset credit against the resolved target using the
// caller's durable request identity.
func (*codexDriver) Consume(ctx context.Context, credential subscriptionruntime.Credential, target subscriptionruntime.Target, requestID string) (subscriptionruntime.ResetCreditResult, error) {
	value, err := ParseCredentialJSON(credential.Canonical())
	if err != nil {
		return subscriptionruntime.ResetCreditResult{}, err
	}
	baseURL, err := target.BaseURL()
	if err != nil {
		return subscriptionruntime.ResetCreditResult{}, err
	}
	result, err := ConsumeResetCredit(ctx, value, baseURL, requestID)
	if err != nil {
		var upstream *UpstreamHTTPError
		if errors.As(err, &upstream) {
			return subscriptionruntime.ResetCreditResult{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
		}
		return subscriptionruntime.ResetCreditResult{}, err
	}
	return NormalizeResetCreditResult(result.Payload)
}

type codexResetCreditPayload struct {
	Code         string `json:"code"`
	WindowsReset int    `json:"windows_reset"`
	Credit       *struct {
		RedeemedAt string `json:"redeemed_at"`
	} `json:"credit"`
}

// NormalizeResetCreditResult converts the Codex mutation payload into the
// provider-neutral action result persisted by the control plane.
func NormalizeResetCreditResult(raw []byte) (subscriptionruntime.ResetCreditResult, error) {
	var payload codexResetCreditPayload
	if json.Unmarshal(raw, &payload) != nil || !strings.EqualFold(strings.TrimSpace(payload.Code), "reset") || payload.WindowsReset < 0 {
		return subscriptionruntime.ResetCreditResult{}, errors.New("invalid subscription reset-credit response")
	}
	result := subscriptionruntime.ResetCreditResult{Status: "succeeded", WindowsReset: payload.WindowsReset}
	if payload.Credit != nil && strings.TrimSpace(payload.Credit.RedeemedAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, payload.Credit.RedeemedAt); err == nil {
			value := parsed.UnixMilli()
			result.RedeemedAtMS = &value
		}
	}
	return result, nil
}

// Go cannot overload ID across the narrow capability interfaces, so wrappers
// expose each typed ID while sharing the implementation below.
type codexModelDiscovery struct{ *codexDriver }
type codexQuotaObservation struct{ *codexDriver }
type codexResetCreditAction struct{ *codexDriver }

func (driver *codexDriver) modelDiscovery() subscriptionruntime.ModelDiscovery {
	return codexModelDiscovery{driver}
}
func (driver *codexDriver) quotaObservation() subscriptionruntime.QuotaObservation {
	return codexQuotaObservation{driver}
}
func (driver *codexDriver) resetCreditAction() subscriptionruntime.ResetCreditAction {
	return codexResetCreditAction{driver}
}

func (codexModelDiscovery) ID() spec.UtilityID   { return modules.CodexModelDiscovery }
func (codexQuotaObservation) ID() spec.UtilityID { return modules.CodexQuotaObservation }
func (codexResetCreditAction) ID() spec.ActionID { return modules.CodexResetCreditAction }

func codexRuntimeCredential(value Credential, canonical []byte) subscriptionruntime.Credential {
	expiresAt, expires := CredentialExpiresAt(value)
	account := subscriptionruntime.Account{Email: strings.TrimSpace(value.Email), ExpiresAt: expiresAt, ExpiresAtKnown: expires}
	if refreshed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.LastRefresh)); err == nil {
		account.LastRefresh, account.LastRefreshKnown = refreshed, true
	}
	return subscriptionruntime.NewCredential(canonical, codexIdentity(value), account, expiresAt, expires, value.SecretValues())
}

// codexIdentity 把凭据身份定为 chatgpt_account_id 与 chatgpt_user_id 的组合。
// account id 只标识 ChatGPT 工作区，同一 Team 下的多个成员共享同一个值，仅凭它
// 去重会把不同成员判成同一个账号。user id 从凭据自带的 JWT 中解析，token 响应
// 省略身份声明时桥接层会沿用原 token，因此该身份在刷新前后保持稳定。解析不出
// user id 时退回 account id，与历史行为一致。
func codexIdentity(value Credential) string {
	accountID := strings.TrimSpace(value.AccountID)
	userID := codexClaimUserID(value)
	if accountID == "" || userID == "" {
		return accountID
	}
	return accountID + "|" + userID
}

// JWT claims 仅用于取回身份元数据，不作为认证或签名验证。
func codexClaimUserID(value Credential) string {
	for _, token := range []string{value.IDToken, value.AccessToken} {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims struct {
			Auth struct {
				UserID string `json:"user_id"`
			} `json:"https://api.openai.com/auth"`
		}
		err = json.Unmarshal(payload, &claims)
		clear(payload)
		if err != nil {
			continue
		}
		if userID := strings.TrimSpace(claims.Auth.UserID); userID != "" {
			return userID
		}
	}
	return ""
}

var _ subscriptionruntime.BrowserAuthorizationDriver = (*codexDriver)(nil)
