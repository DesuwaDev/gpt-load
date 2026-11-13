package cpa

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestBridgeRequestSanitizesSwitchedHistory(t *testing.T) {
	longID := "call_g_ts_" + strings.Repeat("S", 1200)
	codex := execution.AttemptSpec{
		ChannelID: string(channel.Codex), ClientProtocol: protocol.OpenAIResponses,
		Operation: execution.OperationResponsesCreate, Method: http.MethodPost, Path: "/v1/responses",
		ClientModel: "gpt-6-astra", UpstreamModel: "gpt-6-astra",
		Body: []byte(`{"model":"gpt-6-astra","input":[{"type":"function_call","call_id":"` + longID + `","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"` + longID + `","output":"ok"}]}`),
	}
	request, err := bridgeRequest(codex, cpaProxySettings{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{request.Payload, request.OriginalRequest} {
		for _, id := range gjson.GetBytes(payload, "input.#.call_id").Array() {
			if id.String() != "call_g" {
				t.Fatalf("Codex receives the long call_id: %s", payload)
			}
		}
	}

	claude := execution.AttemptSpec{
		ChannelID: string(channel.Claude), ClientProtocol: protocol.Anthropic,
		Operation: execution.OperationChatCompletion, Method: http.MethodPost, Path: "/v1/messages",
		ClientModel: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5",
		Body: []byte(`{"model":"claude-opus-5-5","max_tokens":64,"messages":[{"role":"user","content":"q"},{"role":"assistant","content":[{"type":"thinking","thinking":"from gemini"},{"type":"text","text":"a"}]},{"role":"user","content":"next"}]}`),
	}
	request, err = bridgeRequest(claude, cpaProxySettings{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if blocks := gjson.GetBytes(request.Payload, "messages.1.content").Array(); len(blocks) != 1 || blocks[0].Get("type").String() != "text" {
		t.Fatalf("Claude receives unsigned thinking: %s", request.Payload)
	}

	// Other subscription channels receive the history unchanged.
	antigravity := codex
	antigravity.ChannelID = string(channel.Antigravity)
	request, err = bridgeRequest(antigravity, cpaProxySettings{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(request.Payload) != string(codex.Body) {
		t.Fatalf("Antigravity history changed: %s", request.Payload)
	}
}
