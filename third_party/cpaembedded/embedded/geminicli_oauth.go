package embedded

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/misc"
)

var (
	ErrGeminiCLIInvalidState              = errors.New("gemini cli oauth state mismatch")
	ErrGeminiCLICredentialIdentityChanged = errors.New("refreshed gemini cli credential identity changed")
	// ErrGeminiCLIProjectRequired 表示账号所在档位不提供托管项目，需要自带 GCP 项目。
	ErrGeminiCLIProjectRequired = errors.New("gemini cli account requires a Google Cloud project")
)

// GeminiCLITokenEndpointError 只保留生命周期判定需要的状态码、错误码与重试延迟。
type GeminiCLITokenEndpointError struct {
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (err *GeminiCLITokenEndpointError) Error() string {
	if err == nil {
		return "Gemini CLI token endpoint failed"
	}
	return fmt.Sprintf("Gemini CLI token endpoint returned status %d", err.StatusCode)
}

// GeminiCLIUpstreamHTTPError 保留控制面请求（UserInfo、Code Assist）的安全证据。
type GeminiCLIUpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *GeminiCLIUpstreamHTTPError) Error() string {
	if err == nil {
		return "Gemini CLI upstream request failed"
	}
	return fmt.Sprintf("Gemini CLI %s endpoint returned status %d", err.Operation, err.StatusCode)
}

// GeminiCLIOptions 提供可替换的端点与时间边界，不改变固定的 OAuth client 与回调地址。
type GeminiCLIOptions struct {
	TokenURL           string
	UserInfoURL        string
	LoadCodeAssistURL  string
	OnboardUserURL     string
	ResourceManagerURL string
	HTTPClient         *http.Client
	Now                func() time.Time
}

type geminiCLITokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type geminiCLIUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail *bool  `json:"verified_email"`
}

// GeminiCLIAccountObservation contains persisted, verified account metadata.
type GeminiCLIAccountObservation struct {
	TierID          string
	AccountObserved bool
}

// BeginGeminiCLIBrowserAuthorization 只生成授权链接与 state，不监听端口、不写文件。
func BeginGeminiCLIBrowserAuthorization() (BrowserAuthorization, error) {
	state, err := misc.GenerateRandomState()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate Gemini CLI OAuth state: %w", err)
	}
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return BrowserAuthorization{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challenge := sha256.Sum256([]byte(verifier))
	params := url.Values{}
	params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	params.Set("code_challenge_method", "S256")
	params.Set("include_granted_scopes", "true")
	params.Set("access_type", "offline")
	params.Set("client_id", geminiCLIClientID)
	// 强制同意页，保证每次授权都返回 refresh_token。
	params.Set("prompt", "consent")
	params.Set("redirect_uri", GeminiCLIRedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", strings.Join(geminiCLIScopes, " "))
	params.Set("state", state)
	return BrowserAuthorization{
		AuthorizationURL: geminiCLIAuthEndpoint + "?" + params.Encode(),
		State:            state,
		CodeVerifier:     verifier,
		CodeChallenge:    base64.RawURLEncoding.EncodeToString(challenge[:]),
		ExpiresAt:        time.Now().UTC().Add(geminiCLILoginTimeout),
	}, nil
}

// CompleteGeminiCLIBrowserAuthorization 校验 state、兑换授权码，并补全身份与 Code Assist 项目。
func CompleteGeminiCLIBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
	options GeminiCLIOptions,
) (GeminiCLICredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !constantTimeEqual(completion.ExpectedState, completion.ReturnedState) {
		return GeminiCLICredential{}, ErrGeminiCLIInvalidState
	}
	if strings.TrimSpace(completion.Code) == "" {
		return GeminiCLICredential{}, fmt.Errorf("authorization code is required")
	}
	token, err := requestGeminiCLIToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(completion.Code)},
		"redirect_uri":  {GeminiCLIRedirectURI},
		"code_verifier": {completion.CodeVerifier},
	}, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("exchange Gemini CLI authorization code: %w", err)
	}
	identity, err := fetchGeminiCLIUserInfo(ctx, token.AccessToken, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("fetch Gemini CLI user identity: %w", err)
	}
	projectID, tierID, err := setupGeminiCLIProject(ctx, token.AccessToken, "", options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("set up Gemini CLI project: %w", err)
	}
	now := geminiCLINow(options)
	credential := GeminiCLICredential{
		Type: ProviderGeminiCLI, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		AccountID: identity.ID, Email: identity.Email, ProjectID: projectID,
		OAuthType: "code_assist", TierID: canonicalGeminiCLITier("code_assist", tierID), Scope: token.Scope,
		ExpiresIn: token.ExpiresIn, Timestamp: now.UnixMilli(), LastRefresh: now.Format(time.RFC3339),
		Expire: now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	normalizeGeminiCLICredential(&credential)
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLICredential{}, fmt.Errorf("exchange Gemini CLI authorization code: %w", err)
	}
	return credential, nil
}

// RefreshGeminiCLICredentialOnce 刷新令牌并保留已验证的账号、项目与档位。
func RefreshGeminiCLICredentialOnce(
	ctx context.Context,
	current GeminiCLICredential,
	options GeminiCLIOptions,
) (GeminiCLICredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateGeminiCLICredential(current); err != nil {
		return GeminiCLICredential{}, err
	}
	token, err := requestGeminiCLIToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
	}, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("refresh Gemini CLI credential: %w", err)
	}
	refreshed := current
	refreshed.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	if token.Scope != "" {
		refreshed.Scope = token.Scope
	}
	refreshed.ExpiresIn = token.ExpiresIn
	now := geminiCLINow(options)
	refreshed.Timestamp = now.UnixMilli()
	refreshed.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	refreshed.LastRefresh = now.Format(time.RFC3339)
	normalizeGeminiCLICredential(&refreshed)
	if err := validateGeminiCLICredential(refreshed); err != nil {
		return GeminiCLICredential{}, fmt.Errorf("refresh Gemini CLI credential: %w", err)
	}
	return refreshed, nil
}

// ImportGeminiCLICredential 接受 GPT-Load 规范凭据、CPA 的 gemini 认证文件（token 嵌套）
// 与 gemini-cli 的 oauth_creds.json。文件里的 project_id 视为自带 GCP 项目。
func ImportGeminiCLICredential(
	ctx context.Context,
	raw []byte,
	options GeminiCLIOptions,
) (GeminiCLICredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	imported, err := parseGeminiCLIImportedCredential(raw)
	if err != nil {
		return GeminiCLICredential{}, err
	}
	now := geminiCLINow(options)
	refresh := func() error {
		token, refreshErr := requestGeminiCLIToken(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {imported.RefreshToken},
		}, options)
		if refreshErr != nil {
			return fmt.Errorf("refresh imported Gemini CLI credential: %w", refreshErr)
		}
		imported.AccessToken = token.AccessToken
		if token.Scope != "" {
			imported.Scope = token.Scope
		}
		if token.RefreshToken != "" {
			imported.RefreshToken = token.RefreshToken
		}
		imported.ExpiresIn = token.ExpiresIn
		imported.Timestamp = now.UnixMilli()
		imported.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
		imported.LastRefresh = now.Format(time.RFC3339)
		return nil
	}
	refreshed := false
	if expiresAt, known := GeminiCLICredentialExpiresAt(imported); imported.AccessToken == "" || !known ||
		!expiresAt.After(now.Add(5*time.Minute)) {
		if err := refresh(); err != nil {
			return GeminiCLICredential{}, err
		}
		refreshed = true
	}
	identity, err := fetchGeminiCLIUserInfo(ctx, imported.AccessToken, options)
	if geminiCLIUnauthorized(err) && !refreshed {
		if err := refresh(); err != nil {
			return GeminiCLICredential{}, err
		}
		identity, err = fetchGeminiCLIUserInfo(ctx, imported.AccessToken, options)
	}
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("verify imported Gemini CLI identity: %w", err)
	}
	if imported.AccountID != "" && imported.AccountID != identity.ID {
		return GeminiCLICredential{}, fmt.Errorf("imported Gemini CLI account does not match userinfo")
	}
	if imported.AccountID == "" && imported.Email != "" && !strings.EqualFold(imported.Email, identity.Email) {
		return GeminiCLICredential{}, fmt.Errorf("imported Gemini CLI email does not match userinfo")
	}
	projectID, tierID := imported.ProjectID, imported.TierID
	if imported.OAuthType == "code_assist" {
		projectID, tierID, err = setupGeminiCLIProject(ctx, imported.AccessToken, imported.ProjectID, options)
	}
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("set up imported Gemini CLI project: %w", err)
	}
	credential := imported
	credential.Type = ProviderGeminiCLI
	credential.AccountID, credential.Email, credential.ProjectID = identity.ID, identity.Email, projectID
	if credential.OAuthType == "" {
		credential.OAuthType = "code_assist"
	}
	if credential.TierID == "" {
		credential.TierID = canonicalGeminiCLITier(credential.OAuthType, tierID)
	}
	if credential.LastRefresh == "" {
		credential.LastRefresh = now.Format(time.RFC3339)
	}
	normalizeGeminiCLICredential(&credential)
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLICredential{}, fmt.Errorf("import Gemini CLI credential: %w", err)
	}
	return credential, nil
}

// ObserveGeminiCLIAccount returns stored account metadata. Code Assist quota
// windows are built from the tier policy and local request statistics, as in sub2api.
func ObserveGeminiCLIAccount(ctx context.Context, credential GeminiCLICredential, _ GeminiCLIOptions) (GeminiCLIAccountObservation, error) {
	if ctx != nil && ctx.Err() != nil {
		return GeminiCLIAccountObservation{}, ctx.Err()
	}
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLIAccountObservation{}, err
	}
	return GeminiCLIAccountObservation{TierID: canonicalGeminiCLITier(credential.OAuthType, credential.TierID), AccountObserved: true}, nil
}

func IsDefinitiveGeminiCLIRefreshRejection(code string) bool {
	return IsDefinitiveAntigravityRefreshRejection(code)
}

func geminiCLINow(options GeminiCLIOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

func requestGeminiCLIToken(
	ctx context.Context,
	values url.Values,
	options GeminiCLIOptions,
) (geminiCLITokenResponse, error) {
	values = cloneURLValues(values)
	values.Set("client_id", geminiCLIClientID)
	values.Set("client_secret", geminiCLIClientSecret)
	endpoint := strings.TrimSpace(options.TokenURL)
	if endpoint == "" {
		endpoint = geminiCLITokenEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return geminiCLITokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := clientWithoutRedirects(options.HTTPClient).Do(request)
	if err != nil {
		return geminiCLITokenResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readClaudeOAuthBody(response)
	if err != nil {
		return geminiCLITokenResponse{}, err
	}
	defer clear(body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return geminiCLITokenResponse{}, &GeminiCLITokenEndpointError{
			StatusCode: response.StatusCode,
			Code:       antigravityTokenEndpointErrorCode(body),
			RetryAfter: boundedOAuthRetryAfter(response.Header, geminiCLINow(options)),
		}
	}
	var token geminiCLITokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return geminiCLITokenResponse{}, fmt.Errorf("decode Gemini CLI token response: %w", err)
	}
	token.AccessToken = strings.TrimSpace(token.AccessToken)
	token.RefreshToken = strings.TrimSpace(token.RefreshToken)
	if token.AccessToken == "" || token.ExpiresIn <= 0 {
		return geminiCLITokenResponse{}, fmt.Errorf("Gemini CLI token response is incomplete")
	}
	if values.Get("grant_type") == "authorization_code" && token.RefreshToken == "" {
		return geminiCLITokenResponse{}, fmt.Errorf("Gemini CLI token response is incomplete")
	}
	return token, nil
}

func fetchGeminiCLIUserInfo(
	ctx context.Context,
	accessToken string,
	options GeminiCLIOptions,
) (geminiCLIUserInfo, error) {
	endpoint := strings.TrimSpace(options.UserInfoURL)
	if endpoint == "" {
		endpoint = geminiCLIUserInfoEndpoint
	}
	body, err := fetchGeminiCLIJSON(ctx, endpoint, accessToken, nil, "userinfo", options)
	if err != nil {
		return geminiCLIUserInfo{}, err
	}
	defer clear(body)
	var identity geminiCLIUserInfo
	if err := json.Unmarshal(body, &identity); err != nil {
		return geminiCLIUserInfo{}, fmt.Errorf("decode Gemini CLI userinfo response: %w", err)
	}
	identity.ID = strings.TrimSpace(identity.ID)
	identity.Email = strings.TrimSpace(identity.Email)
	if identity.ID == "" || identity.Email == "" || identity.VerifiedEmail == nil || !*identity.VerifiedEmail {
		return geminiCLIUserInfo{}, fmt.Errorf("Gemini CLI userinfo response is incomplete")
	}
	return identity, nil
}

func fetchGeminiCLIJSON(
	ctx context.Context,
	endpoint string,
	accessToken string,
	payload []byte,
	operation string,
	options GeminiCLIOptions,
) ([]byte, error) {
	method := http.MethodGet
	if payload != nil {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Accept", "application/json")
	if operation != "userinfo" {
		request.Header.Set("User-Agent", geminiCLIUserAgent(""))
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := clientWithoutRedirects(options.HTTPClient).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readClaudeOAuthBody(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		clear(body)
		return nil, &GeminiCLIUpstreamHTTPError{Operation: operation, StatusCode: response.StatusCode}
	}
	return body, nil
}

func loadGeminiCLICodeAssist(
	ctx context.Context,
	accessToken string,
	projectHint string,
	options GeminiCLIOptions,
) (map[string]any, error) {
	endpoint := strings.TrimSpace(options.LoadCodeAssistURL)
	if endpoint == "" {
		endpoint = geminiCLIExecutionBase + "/" + geminiCLIAPIVersion + ":loadCodeAssist"
	}
	request := map[string]any{"metadata": geminiCLIMetadata(projectHint)}
	if projectHint != "" {
		request["cloudaicompanionProject"] = projectHint
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	response, err := fetchGeminiCLIJSON(ctx, endpoint, accessToken, body, "loadCodeAssist", options)
	if err != nil {
		return nil, err
	}
	defer clear(response)
	var payload map[string]any
	if err := json.Unmarshal(response, &payload); err != nil || payload == nil {
		return nil, fmt.Errorf("decode Gemini CLI loadCodeAssist response")
	}
	return payload, nil
}

// setupGeminiCLIProject 复刻 gemini-cli 的 setupUser：已开通则沿用返回的项目，否则按
// 默认档位开通。免费档使用托管项目，携带自带项目会被上游拒绝。
func setupGeminiCLIProject(
	ctx context.Context,
	accessToken string,
	projectHint string,
	options GeminiCLIOptions,
) (string, string, error) {
	projectHint = strings.TrimSpace(projectHint)
	load, loadErr := loadGeminiCLICodeAssist(ctx, accessToken, "", options)
	tierID := geminiCLITierID(load)
	if tierID == "" {
		tierID = geminiCLIOnboardTierID(load)
	}
	if projectHint != "" {
		return projectHint, tierID, nil
	}
	if projectID := antigravityProjectID(load); projectID != "" {
		return projectID, tierID, nil
	}
	if geminiCLITierID(load) != "" {
		if projectHint != "" {
			return projectHint, tierID, nil
		}
		// Registered accounts may have no managed project. Match sub2api's
		// fallback to an existing active GCP project instead of re-onboarding.
		projectID, err := discoverGeminiCLIProject(ctx, accessToken, options)
		return projectID, tierID, err
	}
	tierID = geminiCLIOnboardTierID(load)
	request := map[string]any{"tierId": tierID}
	if tierID == "free-tier" {
		request["metadata"] = geminiCLIMetadata("")
	} else {
		request["metadata"] = geminiCLIMetadata(projectHint)
		if projectHint != "" {
			request["cloudaicompanionProject"] = projectHint
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", "", err
	}
	defer clear(payload)
	endpoint := strings.TrimSpace(options.OnboardUserURL)
	if endpoint == "" {
		endpoint = geminiCLIExecutionBase + "/" + geminiCLIAPIVersion + ":onboardUser"
	}
	for attempt := 0; attempt < 5; attempt++ {
		body, err := fetchGeminiCLIJSON(ctx, endpoint, accessToken, payload, "onboardUser", options)
		if err != nil {
			if projectID, fallbackErr := discoverGeminiCLIProject(ctx, accessToken, options); fallbackErr == nil {
				return projectID, tierID, nil
			}
			return "", tierID, err
		}
		var result struct {
			Done     bool           `json:"done"`
			Response map[string]any `json:"response"`
		}
		err = json.Unmarshal(body, &result)
		clear(body)
		if err != nil {
			return "", "", fmt.Errorf("decode Gemini CLI onboardUser response")
		}
		if result.Done {
			if projectID := antigravityProjectID(result.Response); projectID != "" {
				return projectID, tierID, nil
			}
			if projectHint != "" {
				return projectHint, tierID, nil
			}
			projectID, err := discoverGeminiCLIProject(ctx, accessToken, options)
			return projectID, tierID, err
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", "", ctx.Err()
		case <-timer.C:
		}
	}
	if projectID, fallbackErr := discoverGeminiCLIProject(ctx, accessToken, options); fallbackErr == nil {
		return projectID, tierID, nil
	}
	if loadErr != nil {
		return "", tierID, loadErr
	}
	return "", tierID, fmt.Errorf("Gemini CLI onboarding did not complete")
}

// discoverGeminiCLIProject only lists existing projects. It never creates a
// project, enables an API or changes billing. Prefer companion, then default
// projects as sub2api does; use a stable ID tie-break because list order varies.
func discoverGeminiCLIProject(ctx context.Context, accessToken string, options GeminiCLIOptions) (string, error) {
	endpoint := strings.TrimSpace(options.ResourceManagerURL)
	if endpoint == "" {
		endpoint = "https://cloudresourcemanager.googleapis.com/v1/projects"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.New("invalid Gemini CLI Resource Manager endpoint")
	}
	selected, bestRank, pageToken := "", 3, ""
	seen := make(map[string]bool)
	for page := 0; page < 10; page++ {
		query := parsed.Query()
		query.Set("filter", "lifecycleState:ACTIVE")
		query.Set("pageSize", "100")
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		parsed.RawQuery = query.Encode()
		body, err := fetchGeminiCLIJSON(ctx, parsed.String(), accessToken, nil, "resourceManager", options)
		if err != nil {
			return "", err
		}
		var result struct {
			Projects []struct {
				ID    string `json:"projectId"`
				Name  string `json:"name"`
				State string `json:"lifecycleState"`
			} `json:"projects"`
			NextPageToken string `json:"nextPageToken"`
		}
		err = json.Unmarshal(body, &result)
		clear(body)
		if err != nil {
			return "", errors.New("invalid Gemini CLI Resource Manager response")
		}
		for _, project := range result.Projects {
			id := strings.TrimSpace(project.ID)
			if id == "" || project.State != "ACTIVE" {
				continue
			}
			name := strings.ToLower(id + " " + project.Name)
			rank := 2
			if strings.Contains(name, "cloud-ai-companion") || strings.Contains(name, "cloud ai companion") || strings.Contains(name, "code assist") {
				rank = 0
			} else if strings.Contains(name, "default") {
				rank = 1
			}
			if rank < bestRank || rank == bestRank && id < selected {
				selected, bestRank = id, rank
			}
		}
		pageToken = result.NextPageToken
		if pageToken == "" {
			if selected == "" {
				return "", ErrGeminiCLIProjectRequired
			}
			return selected, nil
		}
		if seen[pageToken] || len(pageToken) > 4096 {
			return "", errors.New("invalid Gemini CLI Resource Manager pagination")
		}
		seen[pageToken] = true
	}
	return "", errors.New("Gemini CLI Resource Manager pagination limit exceeded")
}

func geminiCLIMetadata(projectHint string) map[string]string {
	metadata := make(map[string]string, len(geminiCLIClientMetadata)+1)
	for key, value := range geminiCLIClientMetadata {
		metadata[key] = value
	}
	if projectHint != "" {
		metadata["duetProject"] = projectHint
	}
	return metadata
}

// geminiCLITierID 优先取付费档（Google One AI Pro/Ultra），其次当前档。
func geminiCLITierID(load map[string]any) string {
	for _, key := range []string{"paidTier", "currentTier"} {
		switch tier := load[key].(type) {
		case string:
			if strings.TrimSpace(tier) != "" {
				return strings.TrimSpace(tier)
			}
		case map[string]any:
			if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	return ""
}

func geminiCLIOnboardTierID(load map[string]any) string {
	tiers, _ := load["allowedTiers"].([]any)
	first := ""
	for _, raw := range tiers {
		tier, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := tier["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if first == "" {
			first = id
		}
		if tier["isDefault"] == true {
			return id
		}
	}
	if first != "" {
		return first
	}
	return "LEGACY"
}

func geminiCLIUnauthorized(err error) bool {
	var upstream *GeminiCLIUpstreamHTTPError
	return errors.As(err, &upstream) && upstream != nil && upstream.StatusCode == http.StatusUnauthorized
}

// parseGeminiCLIImportedCredential 宽松读取三种已知文件格式，只提取令牌、过期时间与身份提示。
func parseGeminiCLIImportedCredential(raw []byte) (GeminiCLICredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return GeminiCLICredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return GeminiCLICredential{}, fmt.Errorf("decode imported Gemini CLI credential")
	}
	kind := strings.ToLower(geminiCLIImportString(root, "type"))
	if kind != "" && kind != ProviderGeminiCLI && kind != "gemini" && kind != "gemini-cli" {
		return GeminiCLICredential{}, fmt.Errorf("imported credential is not a Gemini CLI credential")
	}
	sources := []map[string]any{root}
	if credentials, ok := root["credentials"].(map[string]any); ok {
		sources = append(sources, credentials)
	}
	if token, ok := root["token"].(map[string]any); ok {
		sources = append(sources, token)
	}
	pick := func(key string) string {
		for _, source := range sources {
			if value := geminiCLIImportString(source, key); value != "" {
				return value
			}
		}
		return ""
	}
	if client := pick("client_id"); client != "" && client != geminiCLIClientID {
		return GeminiCLICredential{}, fmt.Errorf("imported Gemini CLI credential uses an unsupported OAuth client")
	}
	credential := GeminiCLICredential{
		Type: ProviderGeminiCLI, AccessToken: pick("access_token"), RefreshToken: pick("refresh_token"),
		OAuthType: pick("oauth_type"), TierID: pick("tier_id"), Scope: pick("scope"),
		AccountID: pick("account_id"), Email: pick("email"),
		ProjectID: pick("project_id"), LastRefresh: pick("last_refresh"),
	}
	credential.OAuthType = strings.ToLower(strings.TrimSpace(credential.OAuthType))
	if credential.OAuthType == "" {
		credential.OAuthType = "code_assist"
	}
	if credential.OAuthType != "code_assist" && credential.OAuthType != "google_one" {
		return GeminiCLICredential{}, fmt.Errorf("Gemini CLI import supports code_assist and google_one; AI Studio uses a separate OAuth client")
	}
	for _, value := range []string{credential.AccessToken, credential.RefreshToken, credential.AccountID, credential.Email, credential.ProjectID} {
		if len(value) > 16*1024 || strings.ContainsAny(value, "\r\n\x00") {
			return GeminiCLICredential{}, fmt.Errorf("imported Gemini CLI credential is invalid")
		}
	}
	if credential.RefreshToken == "" {
		return GeminiCLICredential{}, fmt.Errorf("imported Gemini CLI credential has no refresh token")
	}
	if validateTimestamp("last_refresh", credential.LastRefresh) != nil {
		credential.LastRefresh = ""
	}
	// 过期时间缺失或无法解析时留空，导入流程会先刷新一次令牌。
	if expiry := pick("expired"); expiry != "" {
		credential.Expire = geminiCLIRFC3339(expiry)
	} else if expiry := pick("expires_at"); expiry != "" {
		if seconds, err := strconv.ParseInt(expiry, 10, 64); err == nil && seconds > 0 {
			importedExpiry := time.Unix(seconds, 0)
			credential.Expire = importedExpiry.UTC().Format(time.RFC3339)
		} else {
			credential.Expire = geminiCLIRFC3339(expiry)
		}
	} else if expiry := pick("expiry"); expiry != "" {
		credential.Expire = geminiCLIRFC3339(expiry)
	} else {
		for _, source := range sources {
			if number, ok := source["expiry_date"].(json.Number); ok {
				if milliseconds, err := number.Int64(); err == nil && milliseconds > 0 {
					credential.Expire = time.UnixMilli(milliseconds).UTC().Format(time.RFC3339)
					break
				}
			}
		}
	}
	return credential, nil
}

func geminiCLIImportString(source map[string]any, key string) string {
	switch value := source[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func geminiCLIRFC3339(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}
