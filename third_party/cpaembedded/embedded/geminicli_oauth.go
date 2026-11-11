package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
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
	TokenURL             string
	UserInfoURL          string
	LoadCodeAssistURL    string
	OnboardUserURL       string
	RetrieveUserQuotaURL string
	ResourceManagerURL   string
	HTTPClient           *http.Client
	Now                  func() time.Time
}

type geminiCLITokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type geminiCLIUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail *bool  `json:"verified_email"`
}

// GeminiCLIQuotaBucket 是 retrieveUserQuota 返回的单个模型额度桶。
type GeminiCLIQuotaBucket struct {
	ModelID           string
	TokenType         string
	ResetTime         string
	RemainingFraction *float64
}

// GeminiCLIAccountObservation 汇总账号档位与模型额度，任一来源失败时记入 IncompleteSources。
type GeminiCLIAccountObservation struct {
	TierID            string
	Buckets           []GeminiCLIQuotaBucket
	AccountObserved   bool
	QuotaObserved     bool
	IncompleteSources []string
}

// BeginGeminiCLIBrowserAuthorization 只生成授权链接与 state，不监听端口、不写文件。
func BeginGeminiCLIBrowserAuthorization() (BrowserAuthorization, error) {
	state, err := misc.GenerateRandomState()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate Gemini CLI OAuth state: %w", err)
	}
	params := url.Values{}
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
		"grant_type":   {"authorization_code"},
		"code":         {strings.TrimSpace(completion.Code)},
		"redirect_uri": {GeminiCLIRedirectURI},
	}, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("exchange Gemini CLI authorization code: %w", err)
	}
	identity, err := fetchGeminiCLIUserInfo(ctx, token.AccessToken, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("fetch Gemini CLI user identity: %w", err)
	}
	projectID, _, err := setupGeminiCLIProject(ctx, token.AccessToken, "", options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("set up Gemini CLI project: %w", err)
	}
	now := geminiCLINow(options)
	credential := GeminiCLICredential{
		Type: ProviderGeminiCLI, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		AccountID: identity.ID, Email: identity.Email, ProjectID: projectID,
		ExpiresIn: token.ExpiresIn, Timestamp: now.UnixMilli(), LastRefresh: now.Format(time.RFC3339),
		Expire: now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	normalizeGeminiCLICredential(&credential)
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLICredential{}, fmt.Errorf("exchange Gemini CLI authorization code: %w", err)
	}
	return credential, nil
}

// RefreshGeminiCLICredentialOnce 在调用方上下文内只刷新一次，并确认 Google 仍识别为同一账号。
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
	identity, err := fetchGeminiCLIUserInfo(ctx, token.AccessToken, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("verify refreshed Gemini CLI identity: %w", err)
	}
	if identity.ID != current.AccountID {
		return GeminiCLICredential{}, ErrGeminiCLICredentialIdentityChanged
	}
	refreshed := current
	refreshed.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	refreshed.Email = identity.Email
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
	projectID, _, err := setupGeminiCLIProject(ctx, imported.AccessToken, imported.ProjectID, options)
	if err != nil {
		return GeminiCLICredential{}, fmt.Errorf("set up imported Gemini CLI project: %w", err)
	}
	credential := imported
	credential.Type = ProviderGeminiCLI
	credential.AccountID, credential.Email, credential.ProjectID = identity.ID, identity.Email, projectID
	if credential.LastRefresh == "" {
		credential.LastRefresh = now.Format(time.RFC3339)
	}
	normalizeGeminiCLICredential(&credential)
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLICredential{}, fmt.Errorf("import Gemini CLI credential: %w", err)
	}
	return credential, nil
}

// ObserveGeminiCLIAccount 读取账号档位与各模型剩余额度。
func ObserveGeminiCLIAccount(
	ctx context.Context,
	credential GeminiCLICredential,
	options GeminiCLIOptions,
) (GeminiCLIAccountObservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLIAccountObservation{}, err
	}
	var result GeminiCLIAccountObservation
	// 只为读取档位，不带项目提示：免费档的托管项目不应作为自带项目回传。
	load, loadErr := loadGeminiCLICodeAssist(ctx, credential.AccessToken, "", options)
	if loadErr == nil {
		result.TierID = geminiCLITierID(load)
		result.AccountObserved = result.TierID != ""
	} else {
		result.IncompleteSources = append(result.IncompleteSources, "loadCodeAssist")
	}
	buckets, quotaErr := retrieveGeminiCLIQuota(ctx, credential, options)
	if quotaErr == nil {
		result.Buckets = buckets
		result.QuotaObserved = len(buckets) > 0
	} else {
		result.IncompleteSources = append(result.IncompleteSources, "retrieveUserQuota")
	}
	if loadErr != nil && quotaErr != nil {
		return GeminiCLIAccountObservation{}, loadErr
	}
	return result, nil
}

// DiscoverGeminiCLIModels 以账号额度桶里出现的模型为准；接口未列出任何模型时回退到
// CPA 的 Gemini 静态目录（去掉 Code Assist 不提供的图像、嵌入与别名模型）。
func DiscoverGeminiCLIModels(
	ctx context.Context,
	credential GeminiCLICredential,
	options GeminiCLIOptions,
) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateGeminiCLICredential(credential); err != nil {
		return nil, err
	}
	buckets, err := retrieveGeminiCLIQuota(ctx, credential, options)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(buckets))
	for _, bucket := range buckets {
		models = append(models, bucket.ModelID)
	}
	models = normalizeModelIDs(models)
	if len(models) == 0 {
		for _, model := range registry.GetStaticModelDefinitionsByChannel("gemini") {
			if model == nil {
				continue
			}
			id := strings.TrimSpace(model.ID)
			if !strings.HasPrefix(id, "gemini-") || strings.Contains(id, "image") ||
				strings.Contains(id, "embedding") || strings.HasSuffix(id, "-latest") {
				continue
			}
			models = append(models, id)
		}
		models = normalizeModelIDs(models)
	}
	sort.Strings(models)
	return models, nil
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
	load, err := loadGeminiCLICodeAssist(ctx, accessToken, projectHint, options)
	if err != nil {
		return "", "", err
	}
	tierID := geminiCLITierID(load)
	if projectID := antigravityProjectID(load); projectID != "" {
		return projectID, tierID, nil
	}
	if load["currentTier"] != nil || load["paidTier"] != nil {
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
	for attempt := 0; attempt < 6; attempt++ {
		body, err := fetchGeminiCLIJSON(ctx, endpoint, accessToken, payload, "onboardUser", options)
		if err != nil {
			return "", "", err
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
	return "", "", fmt.Errorf("Gemini CLI onboarding did not complete")
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

func retrieveGeminiCLIQuota(
	ctx context.Context,
	credential GeminiCLICredential,
	options GeminiCLIOptions,
) ([]GeminiCLIQuotaBucket, error) {
	endpoint := strings.TrimSpace(options.RetrieveUserQuotaURL)
	if endpoint == "" {
		endpoint = geminiCLIExecutionBase + "/" + geminiCLIAPIVersion + ":retrieveUserQuota"
	}
	body, err := json.Marshal(map[string]string{"project": credential.ProjectID})
	if err != nil {
		return nil, err
	}
	defer clear(body)
	response, err := fetchGeminiCLIJSON(ctx, endpoint, credential.AccessToken, body, "retrieveUserQuota", options)
	if err != nil {
		return nil, err
	}
	defer clear(response)
	var payload struct {
		Buckets []struct {
			ModelID           string          `json:"modelId"`
			TokenType         string          `json:"tokenType"`
			ResetTime         string          `json:"resetTime"`
			RemainingFraction json.RawMessage `json:"remainingFraction"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return nil, fmt.Errorf("decode Gemini CLI retrieveUserQuota response")
	}
	result := make([]GeminiCLIQuotaBucket, 0, len(payload.Buckets))
	for _, raw := range payload.Buckets {
		modelID := boundedAntigravityErrorScalar(raw.ModelID)
		if modelID == "" {
			continue
		}
		bucket := GeminiCLIQuotaBucket{
			ModelID: modelID, TokenType: boundedAntigravityErrorScalar(raw.TokenType),
			ResetTime: boundedAntigravityErrorScalar(raw.ResetTime),
		}
		if fraction, ok := geminiCLIFraction(raw.RemainingFraction); ok {
			bucket.RemainingFraction = &fraction
		}
		result = append(result, bucket)
	}
	return result, nil
}

func geminiCLIFraction(raw json.RawMessage) (float64, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
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
		if tier, ok := load[key].(map[string]any); ok {
			if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	return ""
}

func geminiCLIOnboardTierID(load map[string]any) string {
	if tiers, ok := load["allowedTiers"].([]any); ok {
		for _, raw := range tiers {
			tier, ok := raw.(map[string]any)
			if !ok || tier["isDefault"] != true {
				continue
			}
			if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	return "legacy-tier"
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
		AccountID: geminiCLIImportString(root, "account_id"), Email: geminiCLIImportString(root, "email"),
		ProjectID: geminiCLIImportString(root, "project_id"), LastRefresh: geminiCLIImportString(root, "last_refresh"),
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
	value, _ := source[key].(string)
	return strings.TrimSpace(value)
}

func geminiCLIRFC3339(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}
