package embedded

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestGeminiCLIModelDiscoveryDoesNotRequireQuota(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()
	credential := GeminiCLICredential{
		Type: ProviderGeminiCLI, AccessToken: "fixture-access", RefreshToken: "fixture-refresh",
		AccountID: "fixture-account", Email: "fixture@example.test", ProjectID: "fixture-project",
		Expire: "2030-01-01T00:00:00Z",
	}
	models, err := DiscoverGeminiCLIModels(t.Context(), credential, GeminiCLIOptions{
		LoadCodeAssistURL: upstream.URL + "/load", HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("model selection made %d quota requests", requests)
	}
	for _, id := range []string{"gemini-3.5-flash", "gemini-3.1-flash-image-preview", "gemini-3-pro-image-preview"} {
		if !slices.Contains(models, id) {
			t.Errorf("selection catalog is missing %s", id)
		}
	}
}
