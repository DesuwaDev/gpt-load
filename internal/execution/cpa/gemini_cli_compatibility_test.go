package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/state"
	"gpt-load/internal/subscription/providers/geminicli"
	"gpt-load/internal/testutil/encryptiontest"
)

func TestGeminiCLIHTTPCompatibility(t *testing.T) {
	// Exercise real gateway selection, CPA translation and Code Assist wrapping;
	// the only replacement is Google's HTTP endpoint, with no real credentials.
	calls := make(chan []byte, 32)
	finishReason := "STOP"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		calls <- body
		if strings.HasSuffix(r.URL.Path, ":countTokens") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"totalTokens":12}`)
			return
		}
		parts := `[{"text":"Remember fixture-summary and continue the task."}]`
		if gjson.GetBytes(body, "request.generationConfig.responseModalities").Exists() {
			parts = `[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]`
		}
		response := `{"responseId":"fixture","modelVersion":"gemini-2.5-flash","candidates":[{"index":0,"content":{"role":"model","parts":` + parts + `},"finishReason":"` + finishReason + `","groundingMetadata":{"webSearchQueries":["fixture"],"groundingChunks":[{"web":{"uri":"https://example.test/source","title":"Fixture source"}}],"groundingSupports":[{"segment":{"startIndex":0,"endIndex":8,"text":"Remember"},"groundingChunkIndices":[0]}]}}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125}}`
		if r.URL.Path == "/v1internal:streamGenerateContent" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"response\":"+response+"}\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"response":`+response+`}`)
		}
	}))
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != server.Listener.Addr().String() {
			return nil, errors.New("unexpected external test destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport; transport.CloseIdleConnections() })

	canonical, err := geminicli.MarshalCredential(geminicli.Credential{Type: geminicli.Provider, AccessToken: "fixture-access", RefreshToken: "fixture-refresh", AccountID: "fixture-account", Email: "fixture@example.test", ProjectID: "fixture-project", Expire: "2030-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	adapter, _, credentials, cipher, row := newSubscriptionAdapterFixture(t, string(channel.GeminiCLI), canonical, "fixture-account")
	bridge := adapter.providers[channel.ProviderGeminiCLI].(*geminiCLIProviderBridge)
	bridge.compactionCipher = cipher
	ref, _ := credentials.CredentialRef(row.ID)
	params, _ := json.Marshal(map[string]string{"base_url": server.URL})
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups:          []state.GroupConfig{{ID: row.GroupID, Name: "gemini-cli", ChannelID: channel.GeminiCLI, ConnectionType: "subscription", Params: params, Models: []state.ModelConfig{{ID: "gemini-2.5-flash", Alias: "public-model"}}, Enabled: true}},
		Credentials:     []state.CredentialConfig{{ID: row.ID, GroupID: row.GroupID, Status: state.CredentialStatusActive, Version: ref.Version, IdentityGeneration: ref.IdentityGeneration, Fingerprint: ref.Fingerprint}},
		AccessKeys:      []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: cipher.Hash("gl-fixture"), Status: state.AccessKeyStatusActive}},
	}); err != nil {
		t.Fatal(err)
	}
	handler := gateway.NewHandler(manager, credentials, cipher, gateway.NewExecutionForwarder(adapter), dialect.NewSet(dialect.NewOpenAIImages(), dialect.NewOpenAIResponses(), dialect.NewOpenAI(), dialect.NewAnthropic(), dialect.NewGemini()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, nil, nil)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatal(err)
	}
	post := func(path, contentType, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer gl-fixture")
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != status {
			t.Fatalf("%s = %d: %s", path, recorder.Code, recorder.Body.String())
		}
		return recorder
	}
	take := func() []byte {
		t.Helper()
		select {
		case body := <-calls:
			return body
		default:
			t.Fatal("upstream was not called")
			return nil
		}
	}

	// Each registered protocol and count endpoint must actually reach Code Assist.
	for _, test := range []struct{ path, body, expected string }{
		{"/v1/chat/completions", `{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`, "fixture-summary"},
		{"/v1/messages", `{"model":"public-model","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`, "fixture-summary"},
		{"/v1/messages/count_tokens", `{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`, `"input_tokens":12`},
		{"/v1beta/models/public-model:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"googleSearch":{}}]}`, "groundingMetadata"},
		{"/v1beta/models/public-model:countTokens", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, `"totalTokens":12`},
		{"/v1/images/generations", `{"model":"public-model","prompt":"draw"}`, `"b64_json":"aW1hZ2U="`},
		{"/v1/images/edits", `{"model":"public-model","prompt":"edit","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}]}`, `"b64_json":"aW1hZ2U="`},
	} {
		response := post(test.path, "application/json", test.body, http.StatusOK)
		if !strings.Contains(response.Body.String(), test.expected) {
			t.Fatalf("%s body = %s", test.path, response.Body.String())
		}
		sent := take()
		if test.path == "/v1/images/edits" && gjson.GetBytes(sent, "request.contents.0.parts.1.inlineData.data").String() != "aW1hZ2U=" {
			t.Fatalf("edit lost image: %s", sent)
		}
		if strings.HasSuffix(test.path, ":generateContent") && !gjson.GetBytes(sent, "request.tools.0.googleSearch").Exists() {
			t.Fatalf("native search lost: %s", sent)
		}
	}
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	_ = writer.WriteField("model", "public-model")
	_ = writer.WriteField("prompt", "edit both")
	for _, image := range []string{"first", "second"} {
		header := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image[]"; filename="image.png"`}, "Content-Type": {"image/png"}}
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(part, image)
	}
	_ = writer.Close()
	post("/v1/images/edits", writer.FormDataContentType(), multipartBody.String(), http.StatusOK)
	if sent := take(); len(gjson.GetBytes(sent, "request.contents.0.parts").Array()) != 3 || gjson.GetBytes(sent, "request.contents.0.parts.2.inlineData.data").String() != "c2Vjb25k" {
		t.Fatalf("multipart images lost: %s", sent)
	}

	for _, stream := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"model": "public-model", "input": "search fixture", "tools": []any{map[string]string{"type": "web_search"}}, "stream": stream})
		response := post("/v1/responses", "application/json", string(body), http.StatusOK)
		if sent := take(); !gjson.GetBytes(sent, "request.tools.0.googleSearch").Exists() {
			t.Fatalf("search not forwarded: %s", sent)
		}
		if !strings.Contains(response.Body.String(), "web_search_call") || !strings.Contains(response.Body.String(), "https://example.test/source") {
			t.Fatalf("search evidence lost: %s", response.Body.String())
		}
	}

	compact := post("/v1/responses/compact", "application/json", `{"model":"public-model","input":[{"role":"user","content":"keep goal"},{"type":"function_call","name":"read_file","call_id":"c1","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"fixture tool result"},{"role":"assistant","content":"old long answer"}]}`, http.StatusOK)
	sent := take()
	if !strings.Contains(string(sent), "fixture tool result") {
		t.Fatalf("compact lost tool history: %s", sent)
	}
	if gjson.Get(compact.Body.String(), "object").String() != "response.compaction" || gjson.Get(compact.Body.String(), "usage.input_tokens").Int() != 100 || gjson.Get(compact.Body.String(), "usage.output_tokens").Int() != 25 {
		t.Fatalf("compact response = %s", compact.Body.String())
	}
	output := gjson.Get(compact.Body.String(), "output")
	if len(output.Array()) != 2 || output.Get("0.content.0.text").String() != "keep goal" || !strings.HasPrefix(output.Get("1.encrypted_content").String(), geminiCLICompactionPrefix) || strings.Contains(output.Raw, "fixture-summary") {
		t.Fatalf("compact output = %s", output.Raw)
	}
	// A fresh bridge/key-service instance must read the envelope after a restart.
	bridge = newGeminiCLIProviderBridge()
	bridge.compactionCipher = encryptiontest.Service(t, "cpa-adapter-test-encryption-key-material")
	adapter.providers[channel.ProviderGeminiCLI] = bridge
	input := strings.TrimSuffix(output.Raw, "]") + `,{"role":"user","content":"continue"}]`
	followup := `{"model":"public-model","input":` + input + `}`
	for _, path := range []string{"/v1/responses", "/v1/responses/input_tokens", "/v1/responses/compact"} {
		post(path, "application/json", followup, http.StatusOK)
		if sent := take(); !strings.Contains(string(sent), "fixture-summary") || strings.Contains(string(sent), geminiCLICompactionPrefix) {
			t.Fatalf("%s did not expand envelope: %s", path, sent)
		}
	}
	streaming, _ := sjson.Set(followup, "stream", true)
	response := post("/v1/responses", "application/json", streaming, http.StatusOK)
	if sent := take(); !strings.Contains(string(sent), "fixture-summary") || !strings.Contains(response.Body.String(), "response.completed") {
		t.Fatalf("stream round trip failed: %s / %s", sent, response.Body.String())
	}

	invalid, _ := sjson.Set(followup, "input.1.encrypted_content", "glgc1.tampered")
	response = post("/v1/responses", "application/json", invalid, http.StatusBadRequest)
	if len(calls) != 0 {
		t.Fatal("tampered envelope was dispatched")
	}
	finishReason = "MAX_TOKENS"
	post("/v1/responses/compact", "application/json", followup, http.StatusBadGateway)
	take()
}
