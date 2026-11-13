package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The Gemini conversion carries a function call's thought signature inside its
// call ID as "<baseID>_ts_<signature>", so the signature survives clients that
// only echo IDs back. Such IDs run to thousands of characters, while OpenAI
// caps call IDs at 64. Stripping is deterministic, so a call and its output
// still match after a mid-conversation switch.

const (
	embeddedThoughtSignatureSeparator = "_ts_"
	maxPortableToolCallIDLength       = 64
)

// StripEmbeddedThoughtSignatures shortens over-long tool call IDs that embed a
// thought signature, in Responses, Chat Completions and Anthropic Messages
// requests. Unchanged bodies are returned as the original bytes.
func StripEmbeddedThoughtSignatures(body []byte) ([]byte, bool, error) {
	if !bytes.Contains(body, []byte(embeddedThoughtSignatureSeparator)) {
		return body, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request any
	if err := decoder.Decode(&request); err != nil {
		return nil, false, fmt.Errorf("decode request")
	}
	if !stripToolCallIDs(request) {
		return body, false, nil
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("encode request")
	}
	return encoded, true, nil
}

// stripToolCallIDs rewrites tool call references anywhere in the request:
// call_id, tool_call_id and tool_use_id fields, and the id of tool call
// objects.
func stripToolCallIDs(value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "call_id", "tool_call_id", "tool_use_id":
				if stripped, ok := strippedToolCallID(child); ok {
					typed[key] = stripped
					changed = true
					continue
				}
			case "id":
				if isToolCallObject(typed) {
					if stripped, ok := strippedToolCallID(child); ok {
						typed[key] = stripped
						changed = true
						continue
					}
				}
			}
			changed = stripToolCallIDs(child) || changed
		}
	case []any:
		for _, child := range typed {
			changed = stripToolCallIDs(child) || changed
		}
	}
	return changed
}

// isToolCallObject reports whether an object is a tool call whose id is the
// call ID: a Chat Completions tool call or an Anthropic tool_use block.
func isToolCallObject(object map[string]any) bool {
	if _, hasFunction := object["function"]; hasFunction {
		return true
	}
	kind, _ := object["type"].(string)
	switch kind {
	case "tool_use", "server_tool_use", "function_call", "custom_tool_call":
		return true
	default:
		return false
	}
}

func strippedToolCallID(value any) (string, bool) {
	id, ok := value.(string)
	if !ok || len(id) <= maxPortableToolCallIDLength {
		return "", false
	}
	base, _, found := strings.Cut(id, embeddedThoughtSignatureSeparator)
	if !found || base == "" {
		return "", false
	}
	return base, true
}
