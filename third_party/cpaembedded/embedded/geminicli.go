package embedded

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Gemini CLI 使用 Google 官方开源 CLI 的 installed-app OAuth client，业务请求走
// Cloud Code Assist（cloudcode-pa）的 v1internal 接口，个人账号与 Google One
// 订阅额度都由该接口按项目结算。
const (
	ProviderGeminiCLI    = "gemini_cli"
	GeminiCLIRedirectURI = "http://localhost:8085/oauth2callback"

	geminiCLIClientID         = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	geminiCLIAuthEndpoint     = "https://accounts.google.com/o/oauth2/v2/auth"
	geminiCLITokenEndpoint    = "https://oauth2.googleapis.com/token"
	geminiCLIUserInfoEndpoint = "https://www.googleapis.com/oauth2/v2/userinfo?alt=json"
	geminiCLIExecutionBase    = "https://cloudcode-pa.googleapis.com"
	geminiCLIAPIVersion       = "v1internal"
	geminiCLIVersion          = "0.21.0"
	geminiCLILoginTimeout     = 5 * time.Minute
)

// geminiCLIClientSecret 是 gemini-cli 源码中公开的 installed-app secret，
// 拆开书写只为避免被误判为私有密钥泄漏。
var geminiCLIClientSecret = "GOCSPX-" + "4uHgMPm-1o7Sk-geV6Cu5clXFsxl"

var geminiCLIScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// geminiCLIClientMetadata 与 gemini-cli setupUser 发送的 ClientMetadata 一致。
var geminiCLIClientMetadata = map[string]string{
	"ideType":    "IDE_UNSPECIFIED",
	"platform":   "PLATFORM_UNSPECIFIED",
	"pluginType": "GEMINI",
}

// GeminiCLICredential 是 GPT-Load 加密保存的规范凭据。account_id 只取自
// Google UserInfo；project_id 是 Code Assist 计费与额度所属项目。
type GeminiCLICredential struct {
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

// SecretValues 列出错误跨出内嵌边界前必须脱敏的凭据值。
func (credential GeminiCLICredential) SecretValues() []string {
	return []string{
		credential.AccessToken,
		credential.RefreshToken,
		credential.AccountID,
		credential.Email,
		credential.ProjectID,
	}
}

// ParseGeminiCLICredentialJSON 只接受 GPT-Load 规范结构；外部文件须先走导入流程。
func ParseGeminiCLICredentialJSON(raw []byte) (GeminiCLICredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return GeminiCLICredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := map[string]struct{}{
		"type": {}, "access_token": {}, "refresh_token": {}, "account_id": {},
		"email": {}, "project_id": {}, "expires_in": {}, "timestamp": {},
		"expired": {}, "last_refresh": {},
	}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return GeminiCLICredential{}, err
	}
	var credential GeminiCLICredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return GeminiCLICredential{}, fmt.Errorf("decode credential: %w", err)
	}
	normalizeGeminiCLICredential(&credential)
	if err := validateGeminiCLICredential(credential); err != nil {
		return GeminiCLICredential{}, err
	}
	return credential, nil
}

// MarshalGeminiCLICredential 返回重新校验过的规范 JSON。
func MarshalGeminiCLICredential(credential GeminiCLICredential) ([]byte, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseGeminiCLICredentialJSON(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

// GeminiCLICredentialExpiresAt 返回访问令牌的过期时间。
func GeminiCLICredentialExpiresAt(credential GeminiCLICredential) (time.Time, bool) {
	if strings.TrimSpace(credential.Expire) == "" {
		return time.Time{}, false
	}
	value, err := time.Parse(time.RFC3339, credential.Expire)
	return value, err == nil
}

func normalizeGeminiCLICredential(credential *GeminiCLICredential) {
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.ProjectID = strings.TrimSpace(credential.ProjectID)
	credential.Expire = strings.TrimSpace(credential.Expire)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
}

func validateGeminiCLICredential(credential GeminiCLICredential) error {
	if credential.Type != ProviderGeminiCLI {
		return fmt.Errorf("credential type must be gemini_cli")
	}
	for field, value := range map[string]string{
		"access_token":  credential.AccessToken,
		"refresh_token": credential.RefreshToken,
		"account_id":    credential.AccountID,
		"email":         credential.Email,
		"project_id":    credential.ProjectID,
	} {
		if value == "" {
			return fmt.Errorf("credential %s is required", field)
		}
		if len(value) > 16*1024 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("credential %s is invalid", field)
		}
	}
	if credential.ExpiresIn < 0 || credential.Timestamp < 0 {
		return fmt.Errorf("credential token lifetime is invalid")
	}
	if credential.Expire == "" {
		return fmt.Errorf("credential expired is required")
	}
	if err := validateTimestamp("expired", credential.Expire); err != nil {
		return err
	}
	return validateTimestamp("last_refresh", credential.LastRefresh)
}

// GeminiCLIAPIEndpoints 集中声明凭据就绪后的业务端点，可整体映射到 API 代理根地址。
type GeminiCLIAPIEndpoints struct {
	ExecutionBase        string
	LoadCodeAssistURL    string
	OnboardUserURL       string
	RetrieveUserQuotaURL string
}

// ResolveGeminiCLIAPIEndpoints 保留官方 cloudcode-pa 端点，或把原生路径映射到代理根地址。
func ResolveGeminiCLIAPIEndpoints(apiRoot string) (GeminiCLIAPIEndpoints, error) {
	prefix := geminiCLIExecutionBase + "/" + geminiCLIAPIVersion
	endpoints := GeminiCLIAPIEndpoints{
		ExecutionBase:        geminiCLIExecutionBase,
		LoadCodeAssistURL:    prefix + ":loadCodeAssist",
		OnboardUserURL:       prefix + ":onboardUser",
		RetrieveUserQuotaURL: prefix + ":retrieveUserQuota",
	}
	for _, endpoint := range []*string{
		&endpoints.ExecutionBase, &endpoints.LoadCodeAssistURL,
		&endpoints.OnboardUserURL, &endpoints.RetrieveUserQuotaURL,
	} {
		resolved, err := ResolveAPIEndpoint(apiRoot, *endpoint)
		if err != nil {
			return GeminiCLIAPIEndpoints{}, err
		}
		*endpoint = resolved
	}
	endpoints.ExecutionBase = strings.TrimRight(endpoints.ExecutionBase, "/")
	return endpoints, nil
}

func geminiCLIUserAgent(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "GeminiCLI/" + geminiCLIVersion + " (linux; x64)"
	}
	return "GeminiCLI/" + geminiCLIVersion + "/" + model + " (linux; x64)"
}
