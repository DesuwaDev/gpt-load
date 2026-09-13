package codex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func identityTestToken(t *testing.T, claims map[string]string, padded bool) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"https://api.openai.com/auth": claims})
	if err != nil {
		t.Fatal(err)
	}
	encoding := base64.RawURLEncoding
	if padded {
		encoding = base64.URLEncoding
	}
	return "e30." + encoding.EncodeToString(raw) + ".signature"
}

func identityTestJWT(t *testing.T, userID string) string {
	t.Helper()
	return identityTestToken(t, map[string]string{
		"chatgpt_account_id": "team-workspace",
		"user_id":            userID,
	}, false)
}

// 持久化身份串冻结在本仓库发布时的口径：user_id 优先、分隔符 "|"。改动会让既有
// 凭据的 identity_fingerprint 失配并触发重新授权。
func TestCodexIdentityUsesUserClaimsWithoutChangingCanonical(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, idToken, accessToken, want string }{
		{"id token", identityTestToken(t, map[string]string{"user_id": "user-one"}, false), "access", "workspace|user-one"},
		{"access token", "not-a-jwt", identityTestToken(t, map[string]string{"user_id": "user-one"}, false), "workspace|user-one"},
		{"user id claim wins", identityTestToken(t, map[string]string{"chatgpt_user_id": "user-two", "user_id": "user-one"}, false), "access", "workspace|user-one"},
		{"chatgpt user id fallback", identityTestToken(t, map[string]string{"chatgpt_user_id": "user-two"}, false), "access", "workspace|user-two"},
		{"padded encoding", identityTestToken(t, map[string]string{"user_id": "user-three"}, true), "access", "workspace|user-three"},
		{"missing claims", identityTestToken(t, map[string]string{}, false), "access", "workspace"},
		{"invalid token", "e30.!.signature", "access", "workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 固定旧格式的字段顺序，身份推导不得改变 canonical 字节或持久化派生字段。
			raw := []byte(fmt.Sprintf(`{"type":"codex"%s,"access_token":%q,"refresh_token":"refresh","account_id":"workspace","email":"owner@example.com"}`, identityTestIDTokenField(test.idToken), test.accessToken))
			credential, err := newCodexDriver().Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(credential.Canonical()) != string(raw) {
				t.Fatal("identity derivation changed canonical credential bytes")
			}
			if credential.Identity() != test.want {
				t.Fatalf("identity = %q, want %q", credential.Identity(), test.want)
			}
		})
	}
}

func identityTestIDTokenField(token string) string {
	if token == "" {
		return ""
	}
	return fmt.Sprintf(`,"id_token":%q`, token)
}

func TestCodexIdentityRejectsConflictingTokenUsers(t *testing.T) {
	t.Parallel()
	for _, accessClaim := range []string{"chatgpt_user_id", "user_id"} {
		t.Run(accessClaim, func(t *testing.T) {
			value := Credential{Type: "codex", AccountID: "workspace", RefreshToken: "refresh", IDToken: identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one"}, false), AccessToken: identityTestToken(t, map[string]string{accessClaim: "user-two"}, false)}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newCodexDriver().Parse(raw); err == nil {
				t.Fatal("conflicting token identities were accepted")
			}
			if _, err := MarshalCredential(value); err == nil {
				t.Fatal("conflicting token identities were canonicalized")
			}
		})
	}
}

func TestCodexRefreshWithoutNewIDTokenPreservesKnownIdentity(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = targetRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://auth.openai.com/oauth/token" {
			t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
			Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)),
		}, nil
	})
	userToken := identityTestToken(t, map[string]string{"chatgpt_account_id": "workspace", "chatgpt_user_id": "user-one"}, false)
	for _, test := range []struct {
		name, idToken, accessToken string
		allowed                    bool
	}{
		{"retained ID token", userToken, "old-access", true},
		{"last user claim only in replaced access token", "", userToken, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := MarshalCredential(Credential{Type: "codex", AccountID: "workspace", IDToken: test.idToken, AccessToken: test.accessToken, RefreshToken: "old-refresh"})
			if err != nil {
				t.Fatal(err)
			}
			driver := newCodexDriver()
			current, err := driver.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			refreshed, err := driver.Refresh(t.Context(), current)
			if err != nil {
				t.Fatal(err)
			}
			value, err := ParseCredentialJSON(refreshed.Canonical())
			if err != nil {
				t.Fatal(err)
			}
			if value.IDToken != test.idToken || value.AccessToken != "new-access" || value.RefreshToken != "new-refresh" {
				t.Fatal("refresh did not retain the old ID token while replacing access and refresh tokens")
			}
			if allowed := subscriptionruntime.RefreshPreservesIdentity(driver, current, refreshed); allowed != test.allowed {
				t.Fatalf("refresh allowed = %v, want %v", allowed, test.allowed)
			}
		})
	}
}

// 同一个 ChatGPT Team 工作区下的两个成员共享 chatgpt_account_id，只有 user id
// 能区分它们；身份必须分开，否则第二个成员会被判成重复导入并跳过。
func TestCodexTeamMembersSharingAccountIDGetDistinctIdentities(t *testing.T) {
	driver := newCodexDriver()
	parse := func(userID, email string) string {
		raw, err := json.Marshal(map[string]string{
			"type":          "codex",
			"id_token":      identityTestJWT(t, userID),
			"access_token":  "access-secret",
			"refresh_token": "refresh-" + userID,
			"account_id":    "team-workspace",
			"email":         email,
		})
		if err != nil {
			t.Fatal(err)
		}
		credential, parseErr := driver.Parse(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return credential.Identity()
	}
	first := parse("user-one", "first@example.com")
	second := parse("user-two", "second@example.com")
	if first == second {
		t.Fatalf("team members share identity %q", first)
	}
	if first != "team-workspace|user-one" || second != "team-workspace|user-two" {
		t.Fatalf("identities = %q, %q", first, second)
	}
}

// 凭据里没有可解析的 user id 时身份退回 account id，保持既有行为不变。
func TestCodexIdentityFallsBackToAccountID(t *testing.T) {
	for name, value := range map[string]Credential{
		"opaque tokens": {AccountID: "account-one", AccessToken: "access-secret"},
		"jwt without uid": {
			AccountID: "account-one",
			IDToken:   identityTestJWT(t, ""),
		},
		"malformed jwt": {AccountID: "account-one", IDToken: "not.a.jwt"},
	} {
		if got := credentialIdentity(value); got != "account-one" {
			t.Fatalf("%s: credentialIdentity() = %q, want account-one", name, got)
		}
	}
}

// account id 缺失时身份为空，调用方据此跳过判重；补出一个只含 user id 的身份
// 会让既有凭据的指纹失配。
func TestCodexIdentityWithoutAccountIDIsEmpty(t *testing.T) {
	value := Credential{AccessToken: identityTestJWT(t, "user-one")}
	if got := credentialIdentity(value); got != "" {
		t.Fatalf("credentialIdentity() = %q, want empty", got)
	}
}

// id_token 缺失时从 access_token 取回同一身份，两者携带相同的 auth 声明。
func TestCodexIdentityReadsAccessTokenWhenIDTokenMissing(t *testing.T) {
	value := Credential{AccountID: "team-workspace", AccessToken: identityTestJWT(t, "user-one")}
	if got := credentialIdentity(value); got != "team-workspace|user-one" {
		t.Fatalf("credentialIdentity() = %q", got)
	}
}

// 身份必须在 canonical 往返后保持不变，否则入组比对会把凭据判成身份变更。
func TestCodexIdentitySurvivesCanonicalRoundTrip(t *testing.T) {
	driver := newCodexDriver()
	raw, err := json.Marshal(map[string]string{
		"type":          "codex",
		"id_token":      identityTestJWT(t, "user-one"),
		"access_token":  "access-secret",
		"refresh_token": "refresh-secret",
		"account_id":    "team-workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := driver.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := driver.Parse(credential.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Identity() != credential.Identity() || reparsed.Identity() != "team-workspace|user-one" {
		t.Fatalf("identity drifted: %q -> %q", credential.Identity(), reparsed.Identity())
	}
}
