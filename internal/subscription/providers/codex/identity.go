package codex

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func (*codexDriver) MatchesRefreshIdentity(current, refreshed subscriptionruntime.Credential) bool {
	before, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return false
	}
	after, err := ParseCredentialJSON(refreshed.Canonical())
	if err != nil || before.AccountID != after.AccountID {
		return false
	}
	beforeUser, afterUser := credentialUserID(before, upstreamUserIDClaim), credentialUserID(after, upstreamUserIDClaim)
	// 旧凭据允许补全身份，但不能把已经确认的用户降级为未知用户。
	return beforeUser == "" || beforeUser == afterUser
}

// 身份只从既有令牌派生，不能改变参与持久化内容指纹计算的 canonical JSON。
//
// 与上游有两处刻意偏离：分隔符沿用 "|"，用户声明优先取 user_id。
// identity_fingerprint 是本函数返回值的哈希且已落库，刷新时会重算比对，对不上
// 就把凭据打成 credential_refresh_identity_changed 并要求重新授权（见
// internal/control/credential_stages.go）。本仓库先于上游按 "<account>|<user_id>"
// 发布过，换成上游写法会让既有 Codex 凭据在下次刷新时集体失效。user_id 缺失时
// 回落到 chatgpt_user_id，这样只带 ChatGPT 声明的令牌也能区分 Team 成员——这类
// 凭据在旧版里本来就退化成了 account，没有可保的既有身份。
func credentialIdentity(value Credential) string {
	accountID := strings.TrimSpace(value.AccountID)
	if accountID == "" {
		return ""
	}
	if userID := credentialUserID(value, persistedUserIDClaim); userID != "" {
		return accountID + "|" + userID
	}
	return accountID
}

func credentialUserID(value Credential, pick func(chatgptUserID, userID string) string) string {
	for _, token := range []string{value.IDToken, value.AccessToken} {
		if userID := tokenUserID(token, pick); userID != "" {
			return userID
		}
	}
	return ""
}

// persistedUserIDClaim 用于拼接落库的身份串，口径冻结在本仓库发布时的 user_id 优先。
func persistedUserIDClaim(chatgptUserID, userID string) string {
	if userID != "" {
		return userID
	}
	return chatgptUserID
}

// upstreamUserIDClaim 是上游口径，用于刷新判重与令牌冲突校验。这两处只比较同一
// 口径下的前后取值，不落库，因此跟着上游走即可。
func upstreamUserIDClaim(chatgptUserID, userID string) string {
	if chatgptUserID != "" {
		return chatgptUserID
	}
	return userID
}

// 不能让旧 ID token 掩盖实际用于请求的新 access token 所属用户。
func validateCredentialIdentity(value Credential) error {
	idUser := tokenUserID(value.IDToken, upstreamUserIDClaim)
	accessUser := tokenUserID(value.AccessToken, upstreamUserIDClaim)
	if idUser != "" && accessUser != "" && idUser != accessUser {
		return ErrCredentialIdentityChanged
	}
	return nil
}

// JWT 仅用于读取已持有凭据的身份元数据，不作为签名或登录认证。
func tokenUserID(token string, pick func(chatgptUserID, userID string) string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	defer clear(payload)
	var claims struct {
		Auth struct {
			ChatGPTUserID string `json:"chatgpt_user_id"`
			UserID        string `json:"user_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return pick(strings.TrimSpace(claims.Auth.ChatGPTUserID), strings.TrimSpace(claims.Auth.UserID))
}
