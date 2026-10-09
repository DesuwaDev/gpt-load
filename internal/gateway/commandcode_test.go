package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

// TestCommandCodeGatewayEndToEnd 通过完整网关验证 Command Code 渠道：访问密钥鉴权、
// 模型别名、协议转换、流式与非流式输出及用量。上游按 /alpha/generate 的逐行 JSON 返回。
func TestCommandCodeGatewayEndToEnd(t *testing.T) {
	t.Setenv("CC_CLI_VERSION", "9.8.7")
	events := strings.Join([]string{
		`{"type":"start"}`,
		`{"type":"reasoning-delta","text":"think"}`,
		`{"type":"text-delta","text":"Hello"}`,
		`{"type":"text-delta","text":" world"}`,
		`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":21,"outputTokens":5,"totalTokens":26}}`,
	}, "\n") + "\n"
	var mu sync.Mutex
	var models []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/alpha/generate" || request.Header.Get("Authorization") != "Bearer synthetic-cc-key" ||
			request.Header.Get("X-Command-Code-Version") != "9.8.7" {
			t.Errorf("unexpected upstream request %s %v", request.URL.Path, request.Header)
		}
		var body struct {
			Params struct {
				Model string `json:"model"`
			} `json:"params"`
		}
		raw, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		models = append(models, body.Params.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, events)
	}))
	defer upstream.Close()
	params, err := json.Marshal(map[string]string{"base_url": upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		protocol protocol.Protocol
		path     string
		body     string
		want     []string
	}{
		{"chat", protocol.OpenAICompletions, "/v1/chat/completions",
			`{"model":"public-model","messages":[{"role":"user","content":"ping"}]}`,
			[]string{`"object":"chat.completion"`, `"model":"public-model"`, `"content":"Hello world"`, `"prompt_tokens":21`}},
		{"chat stream", protocol.OpenAICompletions, "/v1/chat/completions",
			`{"model":"public-model","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"ping"}]}`,
			[]string{`"model":"public-model"`, `"content":"Hello"`, `"finish_reason":"stop"`, "data: [DONE]"}},
		{"responses", protocol.OpenAIResponses, "/v1/responses",
			`{"model":"public-model","input":"ping"}`,
			[]string{`"object":"response"`, `"model":"public-model"`, "Hello world", `"input_tokens":21`}},
		{"anthropic", protocol.Anthropic, "/v1/messages",
			`{"model":"public-model","max_tokens":64,"messages":[{"role":"user","content":"ping"}]}`,
			[]string{`"type":"message"`, `"model":"public-model"`, `"text":"Hello world"`, `"input_tokens":21`}},
		{"anthropic stream", protocol.Anthropic, "/v1/messages",
			`{"model":"public-model","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"ping"}]}`,
			[]string{"event: message_start", `"text":"Hello"`, "event: message_stop"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			engine, _ := newDialectGatewayEngine(t, testCase.protocol, "public-model",
				dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses(), dialect.NewAnthropic()), dialectGatewayGroup{
					id: 1, name: "Command Code", channelID: channel.CommandCode, params: params,
					apiKeys: []string{"synthetic-cc-key"},
					models:  []state.ModelConfig{{ID: "deepseek/deepseek-v4-pro", Alias: "public-model"}},
				})
			request := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader(testCase.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
			}
			for _, want := range testCase.want {
				if !strings.Contains(recorder.Body.String(), want) {
					t.Fatalf("response lacks %q:\n%s", want, recorder.Body.String())
				}
			}
			if strings.Contains(recorder.Body.String(), "deepseek/deepseek-v4-pro") {
				t.Fatalf("upstream model leaked to client:\n%s", recorder.Body.String())
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(models) != len(cases) {
		t.Fatalf("upstream calls = %d, want %d", len(models), len(cases))
	}
	for _, model := range models {
		if model != "deepseek/deepseek-v4-pro" {
			t.Fatalf("upstream received model %q", model)
		}
	}
}
