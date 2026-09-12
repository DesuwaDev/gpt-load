package codex

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func identityTestJWT(t *testing.T, userID string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "team-workspace",
			"user_id":            userID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".c2ln"
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
		if got := codexIdentity(value); got != "account-one" {
			t.Fatalf("%s: codexIdentity() = %q, want account-one", name, got)
		}
	}
}

// id_token 缺失时从 access_token 取回同一身份，两者携带相同的 auth 声明。
func TestCodexIdentityReadsAccessTokenWhenIDTokenMissing(t *testing.T) {
	value := Credential{AccountID: "team-workspace", AccessToken: identityTestJWT(t, "user-one")}
	if got := codexIdentity(value); got != "team-workspace|user-one" {
		t.Fatalf("codexIdentity() = %q", got)
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
