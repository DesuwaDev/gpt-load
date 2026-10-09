package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const commandCodeTestKey = "cc-test-key"

type commandCodeCapture struct {
	mu      sync.Mutex
	path    string
	method  string
	headers http.Header
	body    map[string]any
}

func (c *commandCodeCapture) record(request *http.Request, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path, c.method, c.headers = request.URL.Path, request.Method, request.Header.Clone()
	c.body = nil
	if len(body) > 0 {
		_ = json.Unmarshal(body, &c.body)
	}
}

func (c *commandCodeCapture) params(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	params, ok := c.body["params"].(map[string]any)
	if !ok {
		t.Fatalf("upstream body has no params: %v", c.body)
	}
	return params
}

// commandCodeServer 模拟 /alpha/generate：每个事件单独 flush，并故意把一行拆成两次写出，
// 覆盖跨数据块的行重组。
func commandCodeServer(t *testing.T, capture *commandCodeCapture, status int, contentType string, lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		capture.record(request, body)
		writer.Header().Set("Content-Type", contentType)
		writer.WriteHeader(status)
		flusher, _ := writer.(http.Flusher)
		for _, line := range lines {
			half := len(line) / 2
			_, _ = io.WriteString(writer, line[:half])
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = io.WriteString(writer, line[half:])
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

func commandCodeSpec(t *testing.T, baseURL string, clientProtocol protocol.Protocol, body string) execution.AttemptSpec {
	t.Helper()
	spec := openAIChatAttempt(t, channel.CommandCode, baseURL, commandCodeTestKey)
	spec.UpstreamModel = "deepseek-v4-pro"
	spec.ClientProtocol = clientProtocol
	spec.Header.Set("X-Stainless-Lang", "js")
	spec.Header.Set("User-Agent", "OpenAI/JS 6.0")
	switch clientProtocol {
	case protocol.OpenAIResponses:
		spec.Operation = execution.OperationResponsesCreate
		spec.Path = "/v1/responses"
	case protocol.Anthropic:
		spec.Path = "/v1/messages"
	case protocol.Gemini:
		spec.Path = "/v1beta/models/client-model:generateContent"
	}
	if body != "" {
		spec.Body = []byte(body)
	}
	return freezeTestAttempt(spec)
}

func setCommandCodeTestVersion(t *testing.T) {
	t.Helper()
	t.Setenv(commandCodeCLIVersionEnv, "9.8.7")
}

func collectCommandCodeStream(t *testing.T, runtime *testRuntime, spec execution.AttemptSpec) (string, execution.StreamResult, []execution.StreamEvent) {
	t.Helper()
	var output bytes.Buffer
	var events []execution.StreamEvent
	result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		events = append(events, event)
		output.Write(event.Data)
		return nil
	})
	return output.String(), result, events
}

var commandCodeTextEvents = []string{
	`{"type":"start"}` + "\n",
	`data: {"type":"reasoning-delta","id":"r-0","text":"think"}` + "\n",
	`{"type":"text-delta","id":"txt-0","text":"Hello"}` + "\n",
	`{"type":"text-delta","data":{"text":" world"}}` + "\n",
	`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":12,"outputTokens":5,"totalTokens":17,"cachedInputTokens":4,"reasoningTokens":2}}` + "\n",
}

func TestCommandCodeNativeUnaryTranslatesRequestAndResponse(t *testing.T) {
	setCommandCodeTestVersion(t)
	capture := &commandCodeCapture{}
	server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson", commandCodeTextEvents...)
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := commandCodeSpec(t, server.URL, protocol.OpenAICompletions,
		`{"model":"client-model","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"}],"max_completion_tokens":64,"temperature":0.2,"reasoning_effort":"low"}`)
	result := runtime.Execute(t.Context(), spec)
	if result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, error = %+v", result, result.Error)
	}
	if capture.path != "/alpha/generate" || capture.method != http.MethodPost {
		t.Fatalf("upstream endpoint = %s %s", capture.method, capture.path)
	}
	headers := capture.headers
	if headers.Get("Authorization") != "Bearer "+commandCodeTestKey ||
		headers.Get("User-Agent") != "commandcode-cli/9.8.7 Node.js/"+commandCodeNodeVersion ||
		headers.Get("X-Command-Code-Version") != "9.8.7" || headers.Get("X-Cli-Environment") != "production" ||
		headers.Get("Accept-Encoding") != "identity" || headers.Get("X-Session-Id") == "" ||
		!strings.HasPrefix(headers.Get("Traceparent"), "00-") {
		t.Fatalf("upstream headers do not mimic the CLI: %v", headers)
	}
	if headers.Get("X-Stainless-Lang") != "" {
		t.Fatalf("client headers leaked upstream: %v", headers)
	}
	if capture.body["threadId"] != headers.Get("X-Session-Id") || capture.body["permissionMode"] != "standard" {
		t.Fatalf("thread/session mismatch: %v", capture.body)
	}
	params := capture.params(t)
	if params["model"] != "deepseek/deepseek-v4-pro" || params["stream"] != true ||
		params["max_tokens"] != float64(64) || params["temperature"] != 0.2 {
		t.Fatalf("params = %v", params)
	}
	// deepseek-v4-pro 只支持 high/max，low 需就近提升到 high。
	if params["reasoning_effort"] != "high" {
		t.Fatalf("reasoning effort = %v", params["reasoning_effort"])
	}
	system, _ := params["system"].(string)
	if !strings.HasPrefix(system, "be brief\n\n") || !strings.Contains(system, "Tool execution is disabled") {
		t.Fatalf("system prompt = %q", system)
	}
	messages, _ := params["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	if user := messages[0].(map[string]any); user["role"] != "user" || !strings.HasPrefix(user["content"].(string), "hi\n\n[System Note:") {
		t.Fatalf("user message = %v", user)
	}

	var body struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatalf("decode %s: %v", result.Body, err)
	}
	if body.Object != "chat.completion" || body.Model != "client-model" || len(body.Choices) != 1 ||
		body.Choices[0].Message.Content != "Hello world" || body.Choices[0].Message.ReasoningContent != "think" ||
		body.Choices[0].FinishReason != "stop" || body.Usage.PromptTokens != 12 || body.Usage.CompletionTokens != 5 ||
		body.Usage.PromptTokensDetails.CachedTokens != 4 {
		t.Fatalf("completion = %s", result.Body)
	}
	if result.Usage == nil || result.Usage.Normalized.Tokens.Output != 5 ||
		result.Usage.Normalized.Tokens.CacheRead != 4 || result.Usage.Normalized.Tokens.UncachedInput != 8 {
		t.Fatalf("usage evidence = %+v", result.Usage)
	}
	if result.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", result.Header.Get("Content-Type"))
	}
}

func TestCommandCodeNativeStreamEmitsOpenAIChatSSE(t *testing.T) {
	setCommandCodeTestVersion(t)
	capture := &commandCodeCapture{}
	server := commandCodeServer(t, capture, http.StatusOK, "text/event-stream", commandCodeTextEvents...)
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := commandCodeSpec(t, server.URL, protocol.OpenAICompletions,
		`{"model":"client-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	output, result, events := collectCommandCodeStream(t, runtime, spec)
	if result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("stream result = %+v error = %+v\n%s", result, result.Error, output)
	}
	if events[0].Kind != execution.StreamEventReady || events[0].Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("first event = %+v", events[0])
	}
	frames := strings.Split(strings.TrimSpace(output), "\n\n")
	if frames[len(frames)-1] != "data: [DONE]" {
		t.Fatalf("stream must end with [DONE]:\n%s", output)
	}
	var content, reasoning strings.Builder
	finish := ""
	sawUsage := false
	for _, frame := range frames[:len(frames)-1] {
		payload, ok := strings.CutPrefix(frame, "data: ")
		if !ok {
			t.Fatalf("frame is not SSE data: %q", frame)
		}
		var chunk struct {
			Object  string `json:"object"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("decode chunk %q: %v", payload, err)
		}
		if chunk.Object != "chat.completion.chunk" || chunk.Model != "client-model" {
			t.Fatalf("chunk = %s", payload)
		}
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
			reasoning.WriteString(choice.Delta.ReasoningContent)
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
		}
		if chunk.Usage != nil && chunk.Usage.TotalTokens == 17 {
			sawUsage = true
		}
	}
	if content.String() != "Hello world" || reasoning.String() != "think" || finish != "stop" || !sawUsage {
		t.Fatalf("content=%q reasoning=%q finish=%q usage=%v\n%s", content.String(), reasoning.String(), finish, sawUsage, output)
	}
	if result.Usage == nil || result.Usage.Normalized.Tokens.Output != 5 {
		t.Fatalf("stream usage evidence = %+v", result.Usage)
	}
}

func TestCommandCodeToolsRoundTrip(t *testing.T) {
	setCommandCodeTestVersion(t)
	lines := []string{
		`{"type":"tool-call-delta","toolCallId":"call_1","name":"get_weather","arguments":"{\"city\":"}` + "\n",
		`{"type":"tool-call-delta","toolCallId":"call_1","arguments":"\"Paris\"}"}` + "\n",
		`{"type":"tool-call","toolCallId":"call_1","toolName":"get_weather","input":{"city":"Paris"}}` + "\n",
		`{"type":"tool-call","toolCallId":"call_2","toolName":"get_time","input":{"tz":"UTC"}}` + "\n",
		`{"type":"finish","finishReason":"tool-calls","totalUsage":{"inputTokens":3,"outputTokens":4}}` + "\n",
	}
	request := `{"model":"client-model","messages":[
		{"role":"user","content":[{"type":"text","text":"weather?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
		{"role":"assistant","content":null,"tool_calls":[{"id":"old_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}},{"id":"orphan","type":"function","function":{"name":"get_time","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"old_1","content":"sunny"},
		{"role":"user","content":"and Paris?"}],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}},{"type":"function","function":{"name":"get_time","parameters":{"type":"object"}}}],
		"tool_choice":"required"}`
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "unary", true: "stream"}[stream], func(t *testing.T) {
			capture := &commandCodeCapture{}
			server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson", lines...)
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := commandCodeSpec(t, server.URL, protocol.OpenAICompletions, request)
			var body []byte
			if stream {
				output, result, _ := collectCommandCodeStream(t, runtime, spec)
				if result.Error != nil {
					t.Fatalf("stream error = %+v\n%s", result.Error, output)
				}
				body = []byte(output)
			} else {
				result := runtime.Execute(t.Context(), spec)
				if result.Error != nil {
					t.Fatalf("unary error = %+v", result.Error)
				}
				body = result.Body
			}
			params := capture.params(t)
			if choice, _ := params["tool_choice"].(map[string]any); choice["type"] != "any" {
				t.Fatalf("tool_choice = %v", params["tool_choice"])
			}
			tools, _ := params["tools"].([]any)
			if len(tools) != 2 || tools[0].(map[string]any)["input_schema"] == nil {
				t.Fatalf("tools = %v", tools)
			}
			if system, _ := params["system"].(string); strings.Contains(system, "Tool execution is disabled") {
				t.Fatal("no-tools safeguard must not apply when tools are declared")
			}
			messages := params["messages"].([]any)
			if len(messages) != 4 {
				t.Fatalf("messages = %v", messages)
			}
			userParts := messages[0].(map[string]any)["content"].([]any)
			if userParts[1].(map[string]any)["type"] != "image" {
				t.Fatalf("user parts = %v", userParts)
			}
			assistant := messages[1].(map[string]any)["content"].([]any)
			if len(assistant) != 1 || assistant[0].(map[string]any)["toolCallId"] != "old_1" ||
				assistant[0].(map[string]any)["input"].(map[string]any)["city"] != "Rome" {
				t.Fatalf("assistant tool calls must keep only paired calls: %v", assistant)
			}
			result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
			if result["type"] != "tool-result" || result["toolName"] != "get_weather" ||
				result["output"].(map[string]any)["value"] != "sunny" {
				t.Fatalf("tool result = %v", result)
			}
			if !stream {
				var completion struct {
					Choices []struct {
						Message struct {
							ToolCalls []struct {
								ID       string `json:"id"`
								Function struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"message"`
						FinishReason string `json:"finish_reason"`
					} `json:"choices"`
				}
				if err := json.Unmarshal(body, &completion); err != nil {
					t.Fatal(err)
				}
				calls := completion.Choices[0].Message.ToolCalls
				if completion.Choices[0].FinishReason != "tool_calls" || len(calls) != 2 ||
					calls[0].ID != "call_1" || calls[0].Function.Name != "get_weather" ||
					!jsonEquivalent(calls[0].Function.Arguments, `{"city":"Paris"}`) ||
					calls[1].ID != "call_2" || !jsonEquivalent(calls[1].Function.Arguments, `{"tz":"UTC"}`) {
					t.Fatalf("completion = %s", body)
				}
				return
			}
			arguments := map[int]string{}
			names := map[int]string{}
			ids := map[int]string{}
			finish := ""
			for _, frame := range strings.Split(strings.TrimSpace(string(body)), "\n\n") {
				payload := strings.TrimPrefix(frame, "data: ")
				if payload == "[DONE]" {
					continue
				}
				var chunk struct {
					Choices []struct {
						Delta struct {
							ToolCalls []struct {
								Index    int    `json:"index"`
								ID       string `json:"id"`
								Function struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"delta"`
						FinishReason *string `json:"finish_reason"`
					} `json:"choices"`
				}
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					t.Fatal(err)
				}
				for _, choice := range chunk.Choices {
					for _, call := range choice.Delta.ToolCalls {
						arguments[call.Index] += call.Function.Arguments
						if call.Function.Name != "" {
							if names[call.Index] != "" {
								t.Fatalf("tool name repeated for index %d", call.Index)
							}
							names[call.Index] = call.Function.Name
						}
						if call.ID != "" {
							ids[call.Index] = call.ID
						}
					}
					if choice.FinishReason != nil {
						finish = *choice.FinishReason
					}
				}
			}
			if finish != "tool_calls" || ids[0] != "call_1" || names[0] != "get_weather" || arguments[0] != `{"city":"Paris"}` ||
				ids[1] != "call_2" || names[1] != "get_time" || !jsonEquivalent(arguments[1], `{"tz":"UTC"}`) {
				t.Fatalf("stream tool calls ids=%v names=%v args=%v finish=%q\n%s", ids, names, arguments, finish, body)
			}
		})
	}
}

func TestCommandCodeDropsUndeclaredToolCalls(t *testing.T) {
	setCommandCodeTestVersion(t)
	capture := &commandCodeCapture{}
	server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson",
		`{"type":"text-delta","text":"ok"}`+"\n",
		`{"type":"tool-call","toolCallId":"x","toolName":"ReadFile","input":{}}`+"\n",
		`{"type":"finish","finishReason":"tool-calls"}`+"\n")
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	result := runtime.Execute(t.Context(), commandCodeSpec(t, server.URL, protocol.OpenAICompletions, ""))
	if result.Error != nil {
		t.Fatalf("error = %+v", result.Error)
	}
	if strings.Contains(string(result.Body), "ReadFile") || !strings.Contains(string(result.Body), `"finish_reason":"stop"`) {
		t.Fatalf("undeclared tool calls must be dropped: %s", result.Body)
	}
}

func TestCommandCodeConvertedProtocols(t *testing.T) {
	setCommandCodeTestVersion(t)
	cases := []struct {
		name     string
		protocol protocol.Protocol
		body     string
		stream   bool
		want     []string
	}{
		{"anthropic unary", protocol.Anthropic, `{"model":"client-model","max_tokens":256,"system":"sys","thinking":{"type":"enabled","budget_tokens":20000},"messages":[{"role":"user","content":"hi"}]}`, false, []string{`"type":"message"`, `"text":"Hello world"`, `"stop_reason":"end_turn"`}},
		{"anthropic stream", protocol.Anthropic, `{"model":"client-model","max_tokens":256,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, true, []string{"event: message_start", "Hello", "event: message_stop"}},
		{"responses unary", protocol.OpenAIResponses, `{"model":"client-model","input":"hi","stream":false}`, false, []string{`"object":"response"`, "Hello world"}},
		{"responses stream", protocol.OpenAIResponses, `{"model":"client-model","input":"hi","stream":true}`, true, []string{"event: response.created", "Hello", "event: response.completed"}},
		{"gemini unary", protocol.Gemini, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, false, []string{"candidates", "Hello world"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			capture := &commandCodeCapture{}
			server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson", commandCodeTextEvents...)
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := commandCodeSpec(t, server.URL, testCase.protocol, testCase.body)
			if spec.RouteMode != execution.RouteMode(channel.RouteConverted) {
				t.Fatalf("route mode = %q", spec.RouteMode)
			}
			var output string
			if testCase.stream {
				var result execution.StreamResult
				output, result, _ = collectCommandCodeStream(t, runtime, spec)
				if result.Error != nil {
					t.Fatalf("stream error = %+v\n%s", result.Error, output)
				}
			} else {
				result := runtime.Execute(t.Context(), spec)
				if result.Error != nil {
					t.Fatalf("unary error = %+v", result.Error)
				}
				output = string(result.Body)
			}
			for _, want := range testCase.want {
				if !strings.Contains(output, want) {
					t.Fatalf("output lacks %q:\n%s", want, output)
				}
			}
			params := capture.params(t)
			messages, _ := params["messages"].([]any)
			if params["model"] != "deepseek/deepseek-v4-pro" || len(messages) == 0 {
				t.Fatalf("upstream params = %v", params)
			}
			if testCase.name == "anthropic unary" {
				if params["reasoning_effort"] != "high" || !strings.HasPrefix(params["system"].(string), "sys") {
					t.Fatalf("anthropic thinking/system not translated: %v", params)
				}
			}
		})
	}
}

func TestCommandCodeUpstreamErrors(t *testing.T) {
	setCommandCodeTestVersion(t)
	t.Run("http unauthorized", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusUnauthorized, "application/json", `{"error":"Invalid API key"}`)
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		result := runtime.Execute(t.Context(), commandCodeSpec(t, server.URL, protocol.OpenAICompletions, ""))
		if result.Error == nil || result.StatusCode != http.StatusUnauthorized ||
			result.Error.Hint != execution.FailureHintInvalidCredential || !strings.Contains(string(result.Body), "Invalid API key") {
			t.Fatalf("result = %+v body=%s", result.Error, result.Body)
		}
		output, stream, _ := collectCommandCodeStream(t, runtime, commandCodeSpec(t, server.URL, protocol.OpenAICompletions, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		if stream.Error == nil || stream.StatusCode != http.StatusUnauthorized || !strings.Contains(output, "Invalid API key") {
			t.Fatalf("stream result = %+v output=%s", stream.Error, output)
		}
	})
	t.Run("http plain text", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusServiceUnavailable, "text/plain", "Proxy use detected")
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		result := runtime.Execute(t.Context(), commandCodeSpec(t, server.URL, protocol.OpenAICompletions, ""))
		if result.Error == nil || result.StatusCode != http.StatusServiceUnavailable ||
			!strings.Contains(string(result.Body), `"message":"Proxy use detected"`) {
			t.Fatalf("result = %+v body=%s", result.Error, result.Body)
		}
	})
	t.Run("event error", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson",
			`{"type":"text-delta","text":"partial"}`+"\n",
			`{"type":"error","error":{"message":"quota exhausted"}}`+"\n")
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		result := runtime.Execute(t.Context(), commandCodeSpec(t, server.URL, protocol.OpenAICompletions, ""))
		if result.Error == nil || result.StatusCode != http.StatusBadGateway || !strings.Contains(string(result.Body), "quota exhausted") {
			t.Fatalf("unary result = %+v body=%s", result.Error, result.Body)
		}
		output, stream, _ := collectCommandCodeStream(t, runtime, commandCodeSpec(t, server.URL, protocol.OpenAICompletions, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		if stream.Error == nil || !strings.Contains(output, "quota exhausted") || !strings.HasSuffix(strings.TrimSpace(output), "data: [DONE]") {
			t.Fatalf("stream result = %+v output=%s", stream.Error, output)
		}
		output, stream, _ = collectCommandCodeStream(t, runtime, commandCodeSpec(t, server.URL, protocol.Anthropic, `{"model":"m","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		if stream.Error == nil || !strings.Contains(output, "event: error") {
			t.Fatalf("converted stream result = %+v output=%s", stream.Error, output)
		}
	})
	t.Run("empty stream", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson")
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		if result := runtime.Execute(t.Context(), commandCodeSpec(t, server.URL, protocol.OpenAICompletions, "")); result.Error == nil {
			t.Fatal("empty generate response must fail")
		}
		if _, stream, _ := collectCommandCodeStream(t, runtime, commandCodeSpec(t, server.URL, protocol.OpenAICompletions, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)); stream.Error == nil {
			t.Fatal("empty generate stream must fail")
		}
	})
	t.Run("missing finish is completed", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson", `{"type":"text-delta","text":"done"}`)
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		output, stream, _ := collectCommandCodeStream(t, runtime, commandCodeSpec(t, server.URL, protocol.OpenAICompletions, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		if stream.Error != nil || !strings.Contains(output, `"finish_reason":"stop"`) || !strings.Contains(output, "done") {
			t.Fatalf("stream result = %+v output=%s", stream.Error, output)
		}
	})
}

func TestCommandCodeProbe(t *testing.T) {
	setCommandCodeTestVersion(t)
	capture := &commandCodeCapture{}
	server := commandCodeServer(t, capture, http.StatusOK, "application/x-ndjson", commandCodeTextEvents...)
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	for _, value := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini, protocol.OpenAIResponses} {
		spec := openAIChatAttempt(t, channel.CommandCode, server.URL, commandCodeTestKey)
		spec.UpstreamModel = "zai-org/GLM-5.2"
		spec.ClientProtocol = value
		spec.Operation = execution.OperationProbe
		spec.Method, spec.Path, spec.Body = "", "", nil
		result := runtime.Execute(t.Context(), freezeTestAttempt(spec))
		if result.Error != nil || result.StatusCode != http.StatusOK {
			t.Fatalf("%s probe = %+v", value, result.Error)
		}
		if params := capture.params(t); params["model"] != "zai-org/GLM-5.2" || params["max_tokens"] != float64(32) {
			t.Fatalf("probe params = %v", params)
		}
	}
}

func TestCommandCodeListModels(t *testing.T) {
	setCommandCodeTestVersion(t)
	listSpec := func(t *testing.T, baseURL string, value protocol.Protocol) execution.AttemptSpec {
		spec := openAIChatAttempt(t, channel.CommandCode, baseURL, commandCodeTestKey)
		spec.ClientProtocol = value
		spec.Operation = execution.OperationListModels
		spec.Method, spec.Path, spec.Body = http.MethodGet, "/v1/models", nil
		spec.UpstreamModel, spec.ClientModel = "", ""
		if value == protocol.Gemini {
			spec.Path = "/v1beta/models"
		}
		return freezeTestAttempt(spec)
	}
	t.Run("merges upstream catalog", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusOK, "application/json",
			`{"data":[{"id":"deepseek/deepseek-v4-pro","name":"DS Pro Live","context_length":2000},{"id":"newlab/brand-new","name":"Brand New"},{"id":"claude-opus-5"},{"id":"google/gemini-3.7-flash"}]}`)
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		result := runtime.Execute(t.Context(), listSpec(t, server.URL, protocol.OpenAICompletions))
		if result.Error != nil || result.StatusCode != http.StatusOK {
			t.Fatalf("list = %+v", result.Error)
		}
		if capture.path != "/provider/v1/models" || capture.headers.Get("Authorization") != "Bearer "+commandCodeTestKey {
			t.Fatalf("list request = %s %v", capture.path, capture.headers)
		}
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(result.Body, &list); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(list.Data))
		for _, model := range list.Data {
			ids = append(ids, model.ID)
		}
		joined := strings.Join(ids, ",")
		if ids[0] != "deepseek/deepseek-v4-pro" || !strings.Contains(joined, "newlab/brand-new") ||
			strings.Contains(joined, "claude-opus-5") || strings.Contains(joined, "gemini-3.7") ||
			len(ids) != len(commandCodeModels.Builtin)+1 {
			t.Fatalf("model ids = %v", ids)
		}
	})
	t.Run("falls back when catalog is forbidden", func(t *testing.T) {
		capture := &commandCodeCapture{}
		server := commandCodeServer(t, capture, http.StatusForbidden, "application/json", `{"error":"provider tier required"}`)
		defer server.Close()
		runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
		for _, value := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini} {
			result := runtime.Execute(t.Context(), listSpec(t, server.URL, value))
			if result.Error != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("%s fallback list = %+v body=%s", value, result.Error, result.Body)
			}
			// 每种协议都必须保留带组织前缀的完整 ID，否则发现的模型无法回查。
			for _, id := range []string{"deepseek/deepseek-v4-pro", "xai/grok-4.6", "zai-org/GLM-5.3"} {
				if !strings.Contains(string(result.Body), `"`+id+`"`) && !strings.Contains(string(result.Body), `"models/`+id+`"`) {
					t.Fatalf("%s list lost full model id %q: %s", value, id, result.Body)
				}
			}
		}
	})
	t.Run("discovery prefers native OpenAI list", func(t *testing.T) {
		target, err := channel.NewRegistry().ResolveExecutionTarget(channel.CommandCode, json.RawMessage(`{"base_url":"https://api.commandcode.ai"}`))
		if err != nil {
			t.Fatal(err)
		}
		clientProtocol, mode, ok := target.PreferredRoute(execution.OperationListModels, "")
		if !ok || clientProtocol != protocol.OpenAICompletions || mode != channel.RouteNative {
			t.Fatalf("preferred list route = %s %s %v", clientProtocol, mode, ok)
		}
	})
}

// ---------------------------------------------------------------------------
// 纯函数单元测试
// ---------------------------------------------------------------------------

func TestCommandCodeReasoningEffortClipping(t *testing.T) {
	cases := []struct{ model, requested, want string }{
		{"deepseek/deepseek-v4-pro", "low", "high"},
		{"deepseek/deepseek-v4-pro", "max", "max"},
		{"deepseek/deepseek-v4-pro", "xhigh", "high"},
		{"xai/grok-4.6", "max", "high"},
		{"xai/grok-4.6", "minimal", "low"},
		{"unknown/model", "medium", "medium"},
		{"deepseek/deepseek-v4-pro", "none", ""},
		{"deepseek/deepseek-v4-pro", "", ""},
	}
	for _, testCase := range cases {
		if got := commandCodeReasoningEffort(testCase.model, testCase.requested); got != testCase.want {
			t.Fatalf("effort(%s, %s) = %q, want %q", testCase.model, testCase.requested, got, testCase.want)
		}
	}
}

func TestCommandCodeModelResolution(t *testing.T) {
	cases := map[string]string{
		"deepseek-v4":                "deepseek/deepseek-v4-pro",
		"GLM-5.2":                    "zai-org/GLM-5.2",
		"kimi-k3":                    "moonshotai/Kimi-K3",
		"vendor/custom-model":        "vendor/custom-model",
		"nemotron-3-ultra-550b-a55b": "nvidia/nemotron-3-ultra-550b-a55b",
		"not-catalogued":             "not-catalogued",
	}
	for input, want := range cases {
		if got := resolveCommandCodeModel(input); got != want {
			t.Fatalf("resolve(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCommandCodeToolArgumentSuffix(t *testing.T) {
	cases := []struct{ emitted, canonical, want string }{
		{"", `{"a":1}`, `{"a":1}`},
		{`{"a":`, `{"a":1}`, `1}`},
		{`{"a": 1}`, `{"a":1}`, ""},
		{`{ "a" :`, `{"a":1}`, `1}`},
	}
	for _, testCase := range cases {
		got, err := commandCodeToolArgumentSuffix(testCase.emitted, testCase.canonical)
		if err != nil || got != testCase.want {
			t.Fatalf("suffix(%q, %q) = %q, %v; want %q", testCase.emitted, testCase.canonical, got, err, testCase.want)
		}
	}
	if _, err := commandCodeToolArgumentSuffix(`{"a":2`, `{"a":1}`); err == nil {
		t.Fatal("conflicting arguments must fail")
	}
}

func TestCommandCodeUsageShapes(t *testing.T) {
	cases := map[string]commandCodeUsage{
		`{"type":"finish","totalUsage":{"inputTokens":10,"outputTokens":3}}`:                                                        {PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
		`{"type":"finish","usage":{"prompt_tokens":"7","completion_tokens":2,"total_tokens":9}}`:                                    {PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9},
		`{"type":"finish","data":{"totalUsage":{"promptTokens":4,"completionTokens":1,"inputTokenDetails":{"cacheReadTokens":2}}}}`: {PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5, PromptTokensDetails: &commandCodeCachedTokens{CachedTokens: 2}},
	}
	for line, want := range cases {
		event, ok, err := parseCommandCodeLine([]byte(line))
		if err != nil || !ok {
			t.Fatalf("parse %s: %v", line, err)
		}
		got := commandCodeUsageFrom(event)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("usage(%s) = %s, want %s", line, gotJSON, wantJSON)
		}
	}
	if event, _, _ := parseCommandCodeLine([]byte(`{"type":"finish"}`)); commandCodeUsageFrom(event) != nil {
		t.Fatal("finish without usage must report nil")
	}
	for _, line := range []string{"", ": keepalive", "data: [DONE]", "{broken", `{"no":"type"}`} {
		if _, ok, err := parseCommandCodeLine([]byte(line)); ok || err != nil {
			t.Fatalf("line %q must be ignored, ok=%v err=%v", line, ok, err)
		}
	}
}

func TestCommandCodeVersionResolver(t *testing.T) {
	t.Setenv(commandCodeCLIVersionEnv, "")
	now := time.Unix(1_000_000, 0)
	calls := 0
	resolver := &commandCodeVersionResolver{
		now:      func() time.Time { return now },
		coldWait: time.Second,
		fetch: func(context.Context) (string, error) {
			calls++
			return "99.1.0", nil
		},
	}
	if got := resolver.Version(context.Background()); got != "99.1.0" || calls != 1 {
		t.Fatalf("cold version = %q calls=%d", got, calls)
	}
	if got := resolver.Version(context.Background()); got != "99.1.0" || calls != 1 {
		t.Fatalf("cached version = %q calls=%d", got, calls)
	}

	failing := &commandCodeVersionResolver{
		now:      func() time.Time { return now },
		coldWait: time.Second,
		fetch:    func(context.Context) (string, error) { return "", errors.New("offline") },
	}
	if got := failing.Version(context.Background()); got != commandCodeFallbackCLIVersion {
		t.Fatalf("offline version = %q", got)
	}
	started := time.Now()
	if got := failing.Version(context.Background()); got != commandCodeFallbackCLIVersion || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("offline retry must not block: %q after %s", got, time.Since(started))
	}

	stale := &commandCodeVersionResolver{
		now:      func() time.Time { return now },
		coldWait: time.Second,
		fetch:    func(context.Context) (string, error) { return "0.1.0", nil },
	}
	if got := stale.Version(context.Background()); got != commandCodeFallbackCLIVersion {
		t.Fatalf("an older mirror version must not replace the known version, got %q", got)
	}

	t.Setenv(commandCodeCLIVersionEnv, "2.0.0")
	if got := failing.Version(context.Background()); got != "2.0.0" {
		t.Fatalf("env override = %q", got)
	}
	t.Setenv(commandCodeCLIVersionEnv, "bad\r\nheader")
	if got := failing.Version(context.Background()); got != commandCodeFallbackCLIVersion {
		t.Fatalf("invalid override must be ignored, got %q", got)
	}
}
