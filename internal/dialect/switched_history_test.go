package dialect

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func decodeTestJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return object
}

func TestSanitizeMistralChatRequestRemovesForeignFields(t *testing.T) {
	body := []byte(`{"model":"m","max_completion_tokens":100,"seed":7,"user":"u","store":false,"logit_bias":{},
	"verbosity":"low","web_search_options":{},"safety_identifier":"s","thinking":{"type":"enabled"},
	"reasoning":{"effort":"medium"},"stream_options":{"include_usage":true,"include_obfuscation":false},
	"metadata":{"keep":"me"},"parallel_tool_calls":true,
	"messages":[
	 {"role":"developer","content":"be brief","name":"dev"},
	 {"role":"user","name":"alice","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]},
	 {"role":"assistant","name":"bot","content":"","refusal":null,"annotations":[],"reasoning":"r","reasoning_content":"r",
	  "reasoning_details":[{"type":"reasoning.text","text":"r"}],
	  "tool_calls":[{"id":"call_1","type":"function","index":0,"extra_content":{"google":{"thought_signature":"sig"}},
	   "function":{"name":"weather","arguments":"{}","strict":true}}]},
	 {"role":"tool","tool_call_id":"call_1","name":"weather","content":"done","is_error":false},
	 {"role":"user","content":"again"}]}`)
	sanitized, changed, err := SanitizeMistralChatRequest(body)
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	got := decodeTestJSON(t, sanitized)
	want := decodeTestJSON(t, []byte(`{"model":"m","max_tokens":100,"random_seed":7,"reasoning_effort":"high",
	"stream_options":{"include_usage":true},"metadata":{"keep":"me"},"parallel_tool_calls":true,
	"messages":[
	 {"role":"system","content":"be brief"},
	 {"role":"user","content":[{"type":"text","text":"hi"}]},
	 {"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","index":0,"function":{"name":"weather","arguments":"{}"}}]},
	 {"role":"tool","tool_call_id":"call_1","name":"weather","content":"done"},
	 {"role":"user","content":"again"}]}`))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitized = %s", sanitized)
	}
}

func TestSanitizeMistralChatRequestKeepsAcceptedRequests(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","name":"f","content":"ok"}]}`)
	sanitized, changed, err := SanitizeMistralChatRequest(body)
	if err != nil || changed || string(sanitized) != string(body) {
		t.Fatalf("accepted request changed: changed=%t err=%v body=%s", changed, err, sanitized)
	}
}

func TestSanitizeMistralChatRequestPrefersMistralNames(t *testing.T) {
	sanitized, _, err := SanitizeMistralChatRequest([]byte(`{"max_tokens":5,"max_completion_tokens":9,"random_seed":1,"seed":2,"reasoning_effort":"none","reasoning":{"effort":"high"},"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := decodeTestJSON(t, sanitized)
	if got["max_tokens"] != float64(5) || got["random_seed"] != float64(1) || got["reasoning_effort"] != "none" ||
		got["max_completion_tokens"] != nil || got["seed"] != nil || got["reasoning"] != nil {
		t.Fatalf("sanitized = %s", sanitized)
	}
}

func TestSanitizeClaudeThinkingHistory(t *testing.T) {
	const signed = `{"type":"thinking","thinking":"mine","signature":"EqQBsig"}`
	const unsigned = `{"type":"thinking","thinking":"from gemini"}`
	const emptySigned = `{"type":"thinking","thinking":"from gpt","signature":""}`
	const redacted = `{"type":"redacted_thinking"}`
	const toolUse = `{"type":"tool_use","id":"toolu_1","name":"f","input":{}}`
	const toolResult = `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}`
	for _, test := range []struct {
		name, body, want string
		changed          bool
	}{
		{
			name:    "signed thinking is kept",
			body:    `{"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + signed + `,{"type":"text","text":"a"}]}]}`,
			changed: false,
		},
		{
			name:    "unsigned thinking in an earlier turn is removed",
			body:    `{"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + unsigned + `,{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`,
			want:    `{"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`,
			changed: true,
		},
		{
			name:    "assistant turn left empty is removed",
			body:    `{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + emptySigned + `,` + redacted + `]},{"role":"user","content":"next"}]}`,
			want:    `{"messages":[{"role":"user","content":"q"},{"role":"user","content":"next"}]}`,
			changed: true,
		},
		{
			name:    "final tool turn disables thinking",
			body:    `{"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + unsigned + `,` + toolUse + `]},` + toolResult + `]}`,
			want:    `{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + toolUse + `]},` + toolResult + `]}`,
			changed: true,
		},
		{
			name:    "earlier tool turn keeps thinking enabled",
			body:    `{"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + unsigned + `,` + toolUse + `]},` + toolResult + `,{"role":"assistant","content":[` + signed + `,{"type":"text","text":"done"}]},{"role":"user","content":"more"}]}`,
			want:    `{"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[` + toolUse + `]},` + toolResult + `,{"role":"assistant","content":[` + signed + `,{"type":"text","text":"done"}]},{"role":"user","content":"more"}]}`,
			changed: true,
		},
		{
			name:    "string content is untouched",
			body:    `{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"a"}]}`,
			changed: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sanitized, changed, err := SanitizeClaudeThinkingHistory([]byte(test.body))
			if err != nil || changed != test.changed {
				t.Fatalf("changed=%t err=%v", changed, err)
			}
			if !changed {
				if string(sanitized) != test.body {
					t.Fatalf("unchanged body was rewritten: %s", sanitized)
				}
				return
			}
			if got, want := decodeTestJSON(t, sanitized), decodeTestJSON(t, []byte(test.want)); !reflect.DeepEqual(got, want) {
				t.Fatalf("sanitized = %s\nwant      %s", sanitized, test.want)
			}
		})
	}
}

func TestIsClaudeModel(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-opus-5-5":                      true,
		"Claude-Sonnet-5":                      true,
		"anthropic/claude-haiku-5-5":           true,
		"us.anthropic.claude-opus-5-5-v1:0":    true,
		"deepseek-v4-1-flash-1017":             false,
		"kimi-k3":                              false,
		"gemini-4-argon":                       false,
		"openrouter/anthropic/claude-opus-5-5": true,
	} {
		if got := IsClaudeModel(model); got != want {
			t.Errorf("IsClaudeModel(%q) = %t, want %t", model, got, want)
		}
	}
}

func TestStripEmbeddedThoughtSignatures(t *testing.T) {
	long := "call_abc_ts_" + strings.Repeat("Q", 1200)
	short := "call_ab_ts_shortsig"
	for _, test := range []struct {
		name, body, want string
	}{
		{
			name: "responses call ids",
			body: `{"input":[{"type":"function_call","id":"fc_1","call_id":"` + long + `","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"` + long + `","output":"ok"},{"type":"function_call","call_id":"` + short + `","name":"f","arguments":"{}"}]}`,
			want: `{"input":[{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"call_abc","output":"ok"},{"type":"function_call","call_id":"` + short + `","name":"f","arguments":"{}"}]}`,
		},
		{
			name: "chat tool calls",
			body: `{"messages":[{"role":"assistant","tool_calls":[{"id":"` + long + `","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"` + long + `","content":"ok"}]}`,
			want: `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_abc","content":"ok"}]}`,
		},
		{
			name: "anthropic tool use",
			body: `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"` + long + `","name":"f","input":{"n":1.50}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + long + `","content":"ok"}]}]}`,
			want: `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_abc","name":"f","input":{"n":1.50}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_abc","content":"ok"}]}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sanitized, changed, err := StripEmbeddedThoughtSignatures([]byte(test.body))
			if err != nil || !changed {
				t.Fatalf("changed=%t err=%v", changed, err)
			}
			if got, want := decodeTestJSON(t, sanitized), decodeTestJSON(t, []byte(test.want)); !reflect.DeepEqual(got, want) {
				t.Fatalf("sanitized = %s\nwant      %s", sanitized, test.want)
			}
		})
	}
	for _, body := range []string{
		`{"input":[{"type":"function_call","call_id":"` + short + `"}]}`,
		`{"input":[{"type":"message","id":"msg_ts_` + strings.Repeat("x", 100) + `","content":"a"}]}`,
		`{"input":[{"type":"function_call","call_id":"` + strings.Repeat("y", 100) + `"}]}`,
	} {
		sanitized, changed, err := StripEmbeddedThoughtSignatures([]byte(body))
		if err != nil || changed || string(sanitized) != body {
			t.Fatalf("body changed: changed=%t err=%v body=%s", changed, err, sanitized)
		}
	}
}
