package embedded

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeminiCLIAuthorizationDiscoversExistingProject(t *testing.T) {
	for _, test := range []struct {
		name, load, projects, next, want string
		projectRequired                  bool
	}{
		{name: "registered current tier", load: `{"currentTier":{"id":"standard-tier"}}`, projects: `{"projects":[{"projectId":"other-project","name":"Other","lifecycleState":"ACTIVE"},{"projectId":"default-project","name":"Default","lifecycleState":"ACTIVE"},{"projectId":"deleted-companion","name":"Code Assist","lifecycleState":"DELETE_REQUESTED"}],"nextPageToken":"page-two"}`, next: `{"projects":[{"projectId":"companion-project","name":"Code Assist","lifecycleState":"ACTIVE"}]}`, want: "companion-project"},
		{name: "paid tier only", load: `{"paidTier":{"id":"premium-tier"}}`, projects: `{"projects":[{"projectId":"default-project","name":"Default","lifecycleState":"ACTIVE"}]}`, want: "default-project"},
		{name: "managed project skips discovery", load: `{"currentTier":{"id":"free-tier"},"cloudaicompanionProject":"managed-project"}`, want: "managed-project"},
		{name: "no active projects", load: `{"currentTier":{"id":"standard-tier"}}`, projects: `{"projects":[{"projectId":"deleted-project","lifecycleState":"DELETE_REQUESTED"}]}`, projectRequired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			projectCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/token":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Form.Get("code") != "fresh-code" || r.Form.Get("redirect_uri") != GeminiCLIRedirectURI {
						t.Errorf("token exchange fields were lost")
					}
					_, _ = w.Write([]byte(`{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`))
				case "/userinfo":
					_, _ = w.Write([]byte(`{"id":"fixture-account","email":"fixture@example.test","verified_email":true}`))
				case "/load":
					_, _ = w.Write([]byte(test.load))
				case "/projects":
					projectCalls++
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-access" || r.URL.Query().Get("filter") != "lifecycleState:ACTIVE" {
						t.Error("invalid project discovery request")
					}
					if projectCalls == 1 {
						_, _ = w.Write([]byte(test.projects))
					} else {
						if r.URL.Query().Get("pageToken") != "page-two" || projectCalls > 2 {
							t.Error("invalid pagination")
						}
						_, _ = w.Write([]byte(test.next))
					}
				default:
					t.Errorf("unexpected endpoint: %s; registered accounts must not be onboarded again", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			credential, err := CompleteGeminiCLIBrowserAuthorization(t.Context(), BrowserAuthorizationCompletion{ExpectedState: "fixture-state", ReturnedState: "fixture-state", Code: "fresh-code"}, GeminiCLIOptions{TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo", LoadCodeAssistURL: server.URL + "/load", OnboardUserURL: server.URL + "/onboard", ResourceManagerURL: server.URL + "/projects", HTTPClient: server.Client()})
			if test.projectRequired {
				if !errors.Is(err, ErrGeminiCLIProjectRequired) {
					t.Fatalf("expected project-required error, got %v", err)
				}
				return
			}
			if err != nil || credential.ProjectID != test.want || credential.AccountID != "fixture-account" {
				t.Fatalf("credential project = %s, error = %v", credential.ProjectID, err)
			}
			wantCalls := 1
			if test.projects == "" {
				wantCalls = 0
			} else if test.next != "" {
				wantCalls = 2
			}
			if projectCalls != wantCalls {
				t.Fatalf("project calls = %d, want %d", projectCalls, wantCalls)
			}
		})
	}
}
