package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Claude verifies every thinking block in the history: a thinking block without
// a signature, or redacted thinking without data, fails the whole request. Such
// blocks appear when a client switches to Claude mid-conversation and replays
// another model's reasoning as Anthropic thinking. They are removed; previous
// turns' thinking is optional for Claude.

// IsClaudeModel reports whether model names a Claude model, whose upstream
// requires signed thinking history.
func IsClaudeModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndex(model, "/"); index >= 0 {
		model = model[index+1:]
	}
	// Bedrock-style ids such as "anthropic.claude-…" or "us.anthropic.claude-…".
	if index := strings.LastIndex(model, "anthropic."); index >= 0 {
		model = model[index+len("anthropic."):]
	}
	return strings.HasPrefix(model, "claude")
}

// SanitizeClaudeThinkingHistory removes unsigned thinking blocks from assistant
// messages of an Anthropic Messages request. When such a block is removed from
// the final assistant message while it carries tool_use, the request's thinking
// is disabled: Claude requires that message to start with its own signed
// thinking whenever thinking is enabled. Unchanged bodies are returned as the
// original bytes.
func SanitizeClaudeThinkingHistory(body []byte) ([]byte, bool, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil || request == nil {
		return nil, false, fmt.Errorf("decode messages request")
	}
	rawMessages, exists := request["messages"]
	if !exists {
		return body, false, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return body, false, nil
	}
	lastAssistant := -1
	roles := make([]string, len(messages))
	for index, rawMessage := range messages {
		var message struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(rawMessage, &message) == nil {
			roles[index] = message.Role
			if message.Role == "assistant" {
				lastAssistant = index
			}
		}
	}

	changed := false
	disableThinking := false
	kept := make([]json.RawMessage, 0, len(messages))
	for index, rawMessage := range messages {
		if roles[index] != "assistant" {
			kept = append(kept, rawMessage)
			continue
		}
		updated, removed, hasToolUse, empty := removeUnsignedThinking(rawMessage)
		if removed == 0 {
			kept = append(kept, rawMessage)
			continue
		}
		changed = true
		if index == lastAssistant && hasToolUse {
			disableThinking = true
		}
		if empty {
			// Claude rejects assistant turns without content; adjacent turns of
			// the same role are combined by the API.
			continue
		}
		kept = append(kept, updated)
	}
	if !changed {
		return body, false, nil
	}
	encodedMessages, err := json.Marshal(kept)
	if err != nil {
		return nil, false, fmt.Errorf("encode messages")
	}
	request["messages"] = encodedMessages
	if disableThinking && thinkingEnabled(request["thinking"]) {
		delete(request, "thinking")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("encode messages request")
	}
	return encoded, true, nil
}

// removeUnsignedThinking drops thinking blocks that Claude cannot verify from
// one assistant message. It returns the updated message, how many blocks were
// removed, whether the message calls tools and whether no content remains.
func removeUnsignedThinking(raw json.RawMessage) (json.RawMessage, int, bool, bool) {
	var message map[string]json.RawMessage
	if err := json.Unmarshal(raw, &message); err != nil || message == nil {
		return raw, 0, false, false
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(message["content"], &blocks); err != nil {
		// String content has no thinking blocks.
		return raw, 0, false, false
	}
	kept := make([]json.RawMessage, 0, len(blocks))
	removed := 0
	hasToolUse := false
	for _, rawBlock := range blocks {
		var block struct {
			Type      string           `json:"type"`
			Signature *json.RawMessage `json:"signature"`
			Data      *json.RawMessage `json:"data"`
		}
		if json.Unmarshal(rawBlock, &block) != nil {
			kept = append(kept, rawBlock)
			continue
		}
		switch block.Type {
		case "thinking":
			if !nonEmptyJSONString(block.Signature) {
				removed++
				continue
			}
		case "redacted_thinking":
			if !nonEmptyJSONString(block.Data) {
				removed++
				continue
			}
		case "tool_use", "server_tool_use":
			hasToolUse = true
		}
		kept = append(kept, rawBlock)
	}
	if removed == 0 {
		return raw, 0, hasToolUse, false
	}
	if len(kept) == 0 {
		return nil, removed, hasToolUse, true
	}
	content, err := json.Marshal(kept)
	if err != nil {
		return raw, 0, hasToolUse, false
	}
	message["content"] = content
	encoded, err := json.Marshal(message)
	if err != nil {
		return raw, 0, hasToolUse, false
	}
	return encoded, removed, hasToolUse, false
}

func nonEmptyJSONString(raw *json.RawMessage) bool {
	if raw == nil {
		return false
	}
	var value string
	return json.Unmarshal(*raw, &value) == nil && strings.TrimSpace(value) != ""
}

// thinkingEnabled reports whether the thinking parameter asks for thinking.
func thinkingEnabled(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	var thinking struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &thinking) != nil {
		return false
	}
	return thinking.Type != "" && thinking.Type != "disabled"
}
