package embedded

import "testing"

func TestResolveCodexAPIEndpoints(t *testing.T) {
	for _, test := range []struct {
		name        string
		apiRoot     string
		wantExec    string
		wantAccount string
	}{
		{
			name:        "official default",
			apiRoot:     "",
			wantExec:    defaultCodexBaseURL,
			wantAccount: defaultCodexAPIBase,
		},
		{
			name:        "custom gateway root domain",
			apiRoot:     "https://relay.example",
			wantExec:    "https://relay.example/backend-api/codex",
			wantAccount: "https://relay.example/backend-api",
		},
		{
			name:        "custom gateway with openai_base_url full path",
			apiRoot:     "https://relay.example/backend-api/codex",
			wantExec:    "https://relay.example/backend-api/codex",
			wantAccount: "https://relay.example/backend-api",
		},
		{
			name:        "custom gateway with trailing slash",
			apiRoot:     "https://relay.example/backend-api/codex/",
			wantExec:    "https://relay.example/backend-api/codex",
			wantAccount: "https://relay.example/backend-api",
		},
		{
			name:        "custom gateway with backend-api path",
			apiRoot:     "https://relay.example/backend-api",
			wantExec:    "https://relay.example/backend-api/codex",
			wantAccount: "https://relay.example/backend-api",
		},
		{
			name:        "custom gateway with tenant prefix and full codex path",
			apiRoot:     "https://relay.example/tenant-a/backend-api/codex",
			wantExec:    "https://relay.example/tenant-a/backend-api/codex",
			wantAccount: "https://relay.example/tenant-a/backend-api",
		},
		{
			name:        "custom gateway with tenant prefix root",
			apiRoot:     "https://relay.example/tenant-a",
			wantExec:    "https://relay.example/tenant-a/backend-api/codex",
			wantAccount: "https://relay.example/tenant-a/backend-api",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveCodexAPIEndpoints(test.apiRoot)
			if err != nil {
				t.Fatalf("ResolveCodexAPIEndpoints(%q) error = %v", test.apiRoot, err)
			}
			if got.ExecutionBase != test.wantExec {
				t.Errorf("ExecutionBase = %q, want %q", got.ExecutionBase, test.wantExec)
			}
			if got.AccountBase != test.wantAccount {
				t.Errorf("AccountBase = %q, want %q", got.AccountBase, test.wantAccount)
			}
		})
	}
}

func TestParseCodexCredentialWithCustomBaseURL(t *testing.T) {
	for _, test := range []struct {
		name    string
		raw     string
		wantURL string
		wantErr bool
	}{
		{
			name:    "with base_url",
			raw:     `{"type":"codex","access_token":"at","refresh_token":"rt","account_id":"acc","base_url":"https://custom-gateway.example/backend-api/codex"}`,
			wantURL: "https://custom-gateway.example/backend-api/codex",
		},
		{
			name:    "with openai_base_url",
			raw:     `{"type":"codex","access_token":"at","refresh_token":"rt","account_id":"acc","openai_base_url":"https://custom-gateway.example/backend-api/codex/"}`,
			wantURL: "https://custom-gateway.example/backend-api/codex",
		},
		{
			name:    "without base_url",
			raw:     `{"type":"codex","access_token":"at","refresh_token":"rt","account_id":"acc"}`,
			wantURL: "",
		},
		{
			name:    "invalid http base_url",
			raw:     `{"type":"codex","access_token":"at","refresh_token":"rt","account_id":"acc","base_url":"http://insecure.example"}`,
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cred, err := ParseCodexCredentialJSON([]byte(test.raw))
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cred.BaseURL != test.wantURL {
				t.Errorf("BaseURL = %q, want %q", cred.BaseURL, test.wantURL)
			}
		})
	}
}
