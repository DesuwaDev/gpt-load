package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// mistralChatFields mirrors the message fields Mistral accepts; anything else
// is rejected with 422 extra_forbidden.
var mistralChatFields = map[string]map[string]bool{
	"system":    {"role": true, "content": true},
	"user":      {"role": true, "content": true},
	"assistant": {"role": true, "content": true, "tool_calls": true, "prefix": true},
	"tool":      {"role": true, "content": true, "tool_call_id": true, "name": true},
}

func assertMistralAcceptsChat(t *testing.T, wire []byte) {
	t.Helper()
	for _, field := range []string{"max_completion_tokens", "seed", "user", "store", "logit_bias", "verbosity", "web_search_options", "safety_identifier", "reasoning", "thinking"} {
		if gjson.GetBytes(wire, field).Exists() {
			t.Errorf("request field %q reaches Mistral: %s", field, wire)
		}
	}
	for index, message := range gjson.GetBytes(wire, "messages").Array() {
		allowed := mistralChatFields[message.Get("role").String()]
		message.ForEach(func(key, _ gjson.Result) bool {
			if !allowed[key.String()] {
				t.Errorf("messages[%d].%s reaches Mistral: %s", index, key.String(), message.Raw)
			}
			return true
		})
		for _, call := range message.Get("tool_calls").Array() {
			call.ForEach(func(key, _ gjson.Result) bool {
				switch key.String() {
				case "id", "type", "function", "index":
				default:
					t.Errorf("tool call field %q reaches Mistral: %s", key.String(), call.Raw)
				}
				return true
			})
		}
	}
}

func TestMistralConvertedResponsesDropFieldsMistralRejects(t *testing.T) {
	for _, stream := range []bool{false, true} {
		captured := make(chan []byte, 1)
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			wire, _ := io.ReadAll(request.Body)
			captured <- wire
			if stream {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, openAIChatFinalStream)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}))
		runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
		target, _ := json.Marshal(map[string]string{"base_url": server.URL + "/v1"})
		spec := openAIResponsesSpec(execution.OperationResponsesCreate, http.MethodPost, "/v1/responses")
		spec.ChannelID = string(channel.Mistral)
		spec.TargetConfig = target
		spec = freezeTestAttempt(spec)
		// History from a conversation that earlier ran on other models.
		spec.Body = []byte(`{"model":"client-model","stream":` + map[bool]string{false: "false", true: "true"}[stream] + `,"max_output_tokens":100,
		 "tools":[{"type":"function","name":"weather","parameters":{"type":"object","properties":{}}}],
		 "input":[{"role":"developer","content":"be brief"},{"role":"user","content":"hi"},
		  {"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Short."}],"content":[{"type":"reasoning_text","text":"Full."}]},
		  {"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checking.","annotations":[]}]},
		  {"type":"function_call","call_id":"call_1","name":"weather","arguments":"{}"},
		  {"type":"function_call_output","call_id":"call_1","output":"done"},
		  {"role":"user","content":"again"}]}`)
		if stream {
			result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
			if result.Error != nil {
				t.Fatalf("stream result error: %+v", result.Error)
			}
		} else if result := runtime.Execute(context.Background(), spec); result.Error != nil {
			t.Fatalf("result error: %+v", result.Error)
		}
		server.Close()
		wire := <-captured
		assertMistralAcceptsChat(t, wire)
		if gjson.GetBytes(wire, "max_tokens").Int() != 100 {
			t.Errorf("stream=%t output cap lost: %s", stream, wire)
		}
		if !gjson.GetBytes(wire, `messages.#(role=="assistant").tool_calls.0`).Exists() {
			t.Errorf("stream=%t tool call history lost: %s", stream, wire)
		}
	}
}

func switchedHistorySpec(channelID string, clientProtocol protocol.Protocol, operation execution.Operation, path, model, body string) execution.AttemptSpec {
	// Body sanitization reads only the routing identity, so the target is not resolved.
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "switch-request", AttemptID: "switch-attempt", Sequence: 1,
		ChannelID: channelID, ClientProtocol: clientProtocol, Operation: operation,
		ClientModel: model, UpstreamModel: model, Method: http.MethodPost, Path: path,
		Query: make(map[string][]string), Header: http.Header{}, Body: []byte(body),
		TargetConfig: json.RawMessage(`{}`), Credential: execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
}

func TestNativePassthroughSanitizesSwitchedHistory(t *testing.T) {
	longID := "call_g_ts_" + strings.Repeat("S", 1200)

	mistral := switchedHistorySpec(string(channel.Mistral), protocol.OpenAICompletions, execution.OperationChatCompletion,
		"/v1/chat/completions", "deepseek-v4-1-flash-1017",
		`{"model":"deepseek-v4-1-flash-1017","user":"astrbot","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"","refusal":null,"tool_calls":[{"id":"call_1","type":"function","extra_content":{"google":{"thought_signature":"c2ln"}},"function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}]}`)
	body, _, err := sanitizeNativePassthroughRequest(mistral, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMistralAcceptsChat(t, body)

	gpt := switchedHistorySpec(string(channel.OpenAI), protocol.OpenAIResponses, execution.OperationResponsesCreate,
		"/v1/responses", "gpt-6-astra",
		`{"model":"gpt-6-astra","input":[{"type":"function_call","call_id":"`+longID+`","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"`+longID+`","output":"ok"}]}`)
	body, _, err = sanitizeNativePassthroughRequest(gpt, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range gjson.GetBytes(body, "input.#.call_id").Array() {
		if id.String() != "call_g" {
			t.Fatalf("call_id not shortened: %s", body)
		}
	}

	claude := switchedHistorySpec(string(channel.Anthropic), protocol.Anthropic, execution.OperationChatCompletion,
		"/v1/messages", "claude-opus-5-5",
		`{"model":"claude-opus-5-5","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"thinking","thinking":"from gemini"},{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`)
	body, _, err = sanitizeNativePassthroughRequest(claude, true)
	if err != nil {
		t.Fatal(err)
	}
	if blocks := gjson.GetBytes(body, "messages.1.content").Array(); len(blocks) != 1 || blocks[0].Get("type").String() != "text" {
		t.Fatalf("unsigned thinking reaches Claude: %s", body)
	}

	// Anthropic-compatible upstreams that are not Claude keep their thinking history.
	deepseek := switchedHistorySpec(string(channel.DeepSeek), protocol.Anthropic, execution.OperationChatCompletion,
		"/v1/messages", "deepseek-v4-1-flash",
		`{"model":"deepseek-v4-1-flash","max_tokens":64,"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"thinking","thinking":"mine"},{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`)
	body, _, err = sanitizeNativePassthroughRequest(deepseek, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(gjson.GetBytes(body, "messages.1.content").Array()) != 2 {
		t.Fatalf("DeepSeek thinking history removed: %s", body)
	}
}
