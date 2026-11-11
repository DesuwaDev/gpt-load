// Package geminicli owns GPT-Load's Gemini CLI (Code Assist) subscription
// contract and keeps the embedded CPA bridge types inside the provider boundary.
package geminicli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"
	"github.com/sirupsen/logrus"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const (
	Provider    = cpaembedded.ProviderGeminiCLI
	RedirectURI = cpaembedded.GeminiCLIRedirectURI
)

var (
	ErrInvalidState              = errors.New("gemini cli oauth state mismatch")
	ErrCredentialIdentityChanged = errors.New("refreshed gemini cli credential identity changed")
	ErrProjectRequired           = errors.New("gemini cli account requires a Google Cloud project; import a credential file with project_id")
)

// Credential is GPT-Load's encrypted canonical Gemini CLI credential.
type Credential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email"`
	ProjectID    string `json:"project_id"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Timestamp    int64  `json:"timestamp,omitempty"`
	Expire       string `json:"expired"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

func (value Credential) bridge() cpaembedded.GeminiCLICredential {
	return cpaembedded.GeminiCLICredential(value)
}

func (value Credential) SecretValues() []string { return value.bridge().SecretValues() }

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseGeminiCLICredentialJSON(raw)
	return Credential(value), normalizeError(err)
}

func MarshalCredential(value Credential) ([]byte, error) {
	raw, err := cpaembedded.MarshalGeminiCLICredential(value.bridge())
	return raw, normalizeError(err)
}

func CredentialExpiresAt(value Credential) (time.Time, bool) {
	return cpaembedded.GeminiCLICredentialExpiresAt(value.bridge())
}

type TokenEndpointError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (err *TokenEndpointError) Error() string {
	if err == nil {
		return "Gemini CLI token endpoint failed"
	}
	return fmt.Sprintf("Gemini CLI token endpoint returned status %d", err.StatusCode)
}

func (err *TokenEndpointError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

type UpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *UpstreamHTTPError) Error() string {
	if err == nil {
		return "Gemini CLI upstream request failed"
	}
	return fmt.Sprintf("Gemini CLI %s endpoint returned status %d", err.Operation, err.StatusCode)
}

func (err *UpstreamHTTPError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

type QuotaBucket struct {
	ModelID           string
	TokenType         string
	ResetTime         string
	RemainingFraction *float64
}

type AccountObservation struct {
	TierID            string
	Buckets           []QuotaBucket
	AccountObserved   bool
	QuotaObserved     bool
	IncompleteSources []string
}

func IsDefinitiveRefreshRejection(code string) bool {
	return cpaembedded.IsDefinitiveGeminiCLIRefreshRejection(code)
}

func beginBrowserAuthorization() (subscriptionruntime.Authorization, error) {
	value, err := cpaembedded.BeginGeminiCLIBrowserAuthorization()
	if err != nil {
		return subscriptionruntime.Authorization{}, normalizeError(err)
	}
	return subscriptionruntime.Authorization{URL: value.AuthorizationURL, State: value.State, ExpiresAt: value.ExpiresAt}, nil
}

func completeBrowserAuthorization(ctx context.Context, completion subscriptionruntime.AuthorizationCompletion) (Credential, error) {
	options, err := geminiCLIOptions(ctx, "")
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.CompleteGeminiCLIBrowserAuthorization(ctx, cpaembedded.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState, Code: completion.Code,
	}, options)
	err = normalizeError(err)
	if err != nil {
		logAuthorizationFailure(err)
	}
	return Credential(value), err
}

// Log only bounded classifications; OAuth codes, state, tokens and upstream
// response bodies must never reach the diagnostic log.
func logAuthorizationFailure(err error) {
	fields := logrus.Fields{"event": "gemini_cli.authorization_failed", "phase": "authorization", "error_type": fmt.Sprintf("%T", err)}
	var tokenErr *TokenEndpointError
	var upstreamErr *UpstreamHTTPError
	switch {
	case errors.Is(err, ErrProjectRequired):
		fields["phase"], fields["reason"] = "project_setup", "project_required"
	case errors.As(err, &tokenErr):
		fields["phase"], fields["http_status"] = "token_exchange", tokenErr.StatusCode
		switch tokenErr.Code {
		case "invalid_grant", "invalid_client", "invalid_request", "unauthorized_client", "invalid_scope", "access_denied":
			fields["reason"] = tokenErr.Code
		default:
			fields["reason"] = "token_endpoint_error"
		}
	case errors.As(err, &upstreamErr):
		fields["http_status"] = upstreamErr.StatusCode
		switch upstreamErr.Operation {
		case "userinfo", "loadCodeAssist", "onboardUser", "resourceManager":
			fields["phase"] = upstreamErr.Operation
		}
	case errors.Is(err, context.DeadlineExceeded):
		fields["reason"] = "timeout"
	case errors.Is(err, context.Canceled):
		fields["reason"] = "canceled"
	}
	logrus.WithFields(fields).Warn("Gemini CLI authorization failed")
}

func refreshCredentialOnce(ctx context.Context, current Credential) (Credential, error) {
	options, err := geminiCLIOptions(ctx, "")
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.RefreshGeminiCLICredentialOnce(ctx, current.bridge(), options)
	return Credential(value), normalizeError(err)
}

func importCredential(ctx context.Context, raw []byte) (Credential, error) {
	options, err := geminiCLIOptions(ctx, "")
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.ImportGeminiCLICredential(ctx, raw, options)
	return Credential(value), normalizeError(err)
}

func listModels(ctx context.Context, credential Credential, apiRoot string) ([]string, error) {
	options, err := geminiCLIOptions(ctx, apiRoot)
	if err != nil {
		return nil, err
	}
	models, err := cpaembedded.DiscoverGeminiCLIModels(ctx, credential.bridge(), options)
	return models, normalizeError(err)
}

func observeAccount(ctx context.Context, credential Credential, apiRoot string) (AccountObservation, error) {
	options, err := geminiCLIOptions(ctx, apiRoot)
	if err != nil {
		return AccountObservation{}, err
	}
	value, err := cpaembedded.ObserveGeminiCLIAccount(ctx, credential.bridge(), options)
	if err != nil {
		return AccountObservation{}, normalizeError(err)
	}
	result := AccountObservation{
		TierID: value.TierID, AccountObserved: value.AccountObserved, QuotaObserved: value.QuotaObserved,
		IncompleteSources: append([]string(nil), value.IncompleteSources...),
	}
	for _, bucket := range value.Buckets {
		mapped := QuotaBucket{ModelID: bucket.ModelID, TokenType: bucket.TokenType, ResetTime: bucket.ResetTime}
		if bucket.RemainingFraction != nil {
			fraction := *bucket.RemainingFraction
			mapped.RemainingFraction = &fraction
		}
		result.Buckets = append(result.Buckets, mapped)
	}
	return result, nil
}

// geminiCLIOptions 为控制面请求设置 HTTP 客户端；apiRoot 非空时 Code Assist 端点映射到代理根地址。
func geminiCLIOptions(ctx context.Context, apiRoot string) (cpaembedded.GeminiCLIOptions, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	if err != nil {
		return cpaembedded.GeminiCLIOptions{}, err
	}
	options := cpaembedded.GeminiCLIOptions{HTTPClient: client}
	if strings.TrimSpace(apiRoot) == "" {
		return options, nil
	}
	endpoints, err := cpaembedded.ResolveGeminiCLIAPIEndpoints(apiRoot)
	if err != nil {
		return cpaembedded.GeminiCLIOptions{}, err
	}
	options.LoadCodeAssistURL = endpoints.LoadCodeAssistURL
	options.OnboardUserURL = endpoints.OnboardUserURL
	options.RetrieveUserQuotaURL = endpoints.RetrieveUserQuotaURL
	return options, nil
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, cpaembedded.ErrGeminiCLIInvalidState):
		return ErrInvalidState
	case errors.Is(err, cpaembedded.ErrGeminiCLICredentialIdentityChanged):
		return ErrCredentialIdentityChanged
	case errors.Is(err, cpaembedded.ErrGeminiCLIProjectRequired):
		return ErrProjectRequired
	}
	var tokenErr *cpaembedded.GeminiCLITokenEndpointError
	if errors.As(err, &tokenErr) {
		return &TokenEndpointError{
			StatusCode: tokenErr.StatusCode, Code: strings.TrimSpace(tokenErr.Code), RetryAfter: tokenErr.RetryAfter,
		}
	}
	var upstream *cpaembedded.GeminiCLIUpstreamHTTPError
	if errors.As(err, &upstream) {
		return &UpstreamHTTPError{Operation: strings.TrimSpace(upstream.Operation), StatusCode: upstream.StatusCode}
	}
	return err
}
