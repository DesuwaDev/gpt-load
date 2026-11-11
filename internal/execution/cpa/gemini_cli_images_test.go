package cpa

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/geminicli"
	"gpt-load/internal/usage"
)

type geminiCLIImagesExecutor struct {
	geminicli.Executor
	request  geminicli.ExecuteRequest
	response []byte
	calls    int
}

func (executor *geminiCLIImagesExecutor) Execute(_ context.Context, _ string, _ geminicli.Credential, request geminicli.ExecuteRequest) (geminicli.ExecuteResponse, error) {
	executor.calls++
	executor.request = request
	return geminicli.ExecuteResponse{Payload: executor.response}, nil
}

func TestGeminiCLIImagesAdapter(t *testing.T) {
	canonical, err := geminicli.MarshalCredential(geminicli.Credential{
		Type: geminicli.Provider, AccessToken: "fixture-access", RefreshToken: "fixture-refresh",
		AccountID: "fixture-account", Email: "fixture@example.test", ProjectID: "fixture-project", Expire: "2030-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, _, credentials, keyService, row := newSubscriptionAdapterFixture(t, string(channel.GeminiCLI), canonical, "fixture-account")
	ref, ok := credentials.CredentialRef(row.ID)
	if !ok {
		t.Fatal("credential is unavailable")
	}
	executor := &geminiCLIImagesExecutor{response: []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]}}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125}}`)}
	adapter.providers[channel.ProviderGeminiCLI].(*geminiCLIProviderBridge).executor = executor
	spec := validSpec(t, row, keyService)
	spec.ChannelID, spec.Credential.IdentityGeneration = string(channel.GeminiCLI), ref.IdentityGeneration
	spec.ClientProtocol, spec.Operation, spec.RouteMode = protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteConverted
	spec.ClientModel, spec.UpstreamModel = "public-image", "gemini-2.5-flash-image"
	spec.Path, spec.Header = "/v1/images/generations", http.Header{"Content-Type": {"application/json"}}
	spec.Body = []byte(`{"model":"public-image","prompt":"draw a cat","response_format":"b64_json"}`)

	result := adapter.Execute(t.Context(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || executor.calls != 1 {
		t.Fatalf("result = %+v, validation = %v, calls = %d", result, err, executor.calls)
	}
	request := executor.request
	if request.Format != "gemini" || request.Model != spec.UpstreamModel ||
		gjson.GetBytes(request.Payload, "contents.0.parts.0.text").String() != "draw a cat" ||
		gjson.GetBytes(request.Payload, "generationConfig.responseModalities").Raw != `["TEXT","IMAGE"]` ||
		!bytes.Equal(request.Payload, request.OriginalRequest) {
		t.Fatalf("upstream request = %+v, payload = %s", request, request.Payload)
	}
	if gjson.GetBytes(result.Body, "data.0.b64_json").String() != "aW1hZ2U=" || result.Usage == nil ||
		result.Usage.Normalized.Tokens != (usage.Tokens{UncachedInput: 60, CacheRead: 40, Output: 25}) {
		t.Fatalf("image response = %s, usage = %+v", result.Body, result.Usage)
	}

	executor.response = []byte(`{"candidates":[{"content":{"parts":[{"text":"no image"}]}}]}`)
	result = adapter.Execute(t.Context(), spec)
	if result.StatusCode != http.StatusBadGateway || result.Error == nil || result.Error.Code != "invalid_image_response" {
		t.Fatalf("invalid image result = %+v", result)
	}

	spec.Body = []byte(`{"model":"public-image","prompt":"draw a cat","n":2}`)
	result = adapter.Execute(t.Context(), spec)
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil || executor.calls != 2 {
		t.Fatalf("unsupported input result = %+v, calls = %d", result, executor.calls)
	}
}
