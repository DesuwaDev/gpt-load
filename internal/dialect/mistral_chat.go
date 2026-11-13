package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Mistral's Chat Completions endpoint validates its schema strictly: an
// unknown field anywhere returns 422 extra_forbidden. Conversations that were
// partly answered by other models carry such fields (Gemini thought signatures
// on tool calls, reasoning fields, OpenAI-only parameters), so they are removed
// or mapped to Mistral's names before forwarding.

var mistralMessageFields = map[string]map[string]bool{
	"system":    {"role": true, "content": true},
	"user":      {"role": true, "content": true},
	"assistant": {"role": true, "content": true, "tool_calls": true, "prefix": true},
	"tool":      {"role": true, "content": true, "tool_call_id": true, "name": true},
}

var (
	mistralToolCallFields = map[string]bool{"id": true, "type": true, "function": true, "index": true}
	mistralFunctionFields = map[string]bool{"name": true, "arguments": true}
	// Content chunk fields other clients attach for their own caching or citations.
	mistralForeignChunkFields = []string{"cache_control", "citations", "prompt_cache_breakpoint", "cachePoint"}
	// OpenAI request fields Mistral rejects and has no equivalent for.
	mistralUnsupportedRequestFields = []string{
		"user", "store", "logit_bias", "verbosity", "web_search_options", "safety_identifier", "thinking",
	}
)

// SanitizeMistralChatRequest rewrites an OpenAI-shaped chat request so that
// Mistral accepts it. It reports whether the body changed; unchanged bodies are
// returned as the original bytes.
func SanitizeMistralChatRequest(body []byte) ([]byte, bool, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil || request == nil {
		return nil, false, fmt.Errorf("decode chat request")
	}
	changed := renameMistralRequestField(request, "max_completion_tokens", "max_tokens")
	changed = renameMistralRequestField(request, "seed", "random_seed") || changed
	if raw, exists := request["reasoning"]; exists {
		var reasoning struct {
			Effort string `json:"effort"`
		}
		if _, hasEffort := request["reasoning_effort"]; !hasEffort &&
			json.Unmarshal(raw, &reasoning) == nil && reasoning.Effort != "" {
			// Mistral accepts only "none" and "high".
			effort := "high"
			if reasoning.Effort == "none" {
				effort = "none"
			}
			request["reasoning_effort"], _ = json.Marshal(effort)
		}
		delete(request, "reasoning")
		changed = true
	}
	for _, field := range mistralUnsupportedRequestFields {
		if _, exists := request[field]; exists {
			delete(request, field)
			changed = true
		}
	}
	if raw, exists := request["stream_options"]; exists {
		updated, optionsChanged, err := keepJSONObjectFields(raw, map[string]bool{"include_usage": true})
		if err != nil {
			return nil, false, fmt.Errorf("decode stream_options")
		}
		if optionsChanged {
			request["stream_options"] = updated
			changed = true
		}
	}
	if raw, exists := request["messages"]; exists {
		messages, messagesChanged, err := sanitizeMistralMessages(raw)
		if err != nil {
			return nil, false, err
		}
		if messagesChanged {
			request["messages"] = messages
			changed = true
		}
	}
	if !changed {
		return body, false, nil
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("encode chat request")
	}
	return encoded, true, nil
}

// renameMistralRequestField moves an OpenAI field to Mistral's name unless the
// request already sets the Mistral one, in which case the OpenAI field is dropped.
func renameMistralRequestField(request map[string]json.RawMessage, from, to string) bool {
	raw, exists := request[from]
	if !exists {
		return false
	}
	if _, set := request[to]; !set && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		request[to] = raw
	}
	delete(request, from)
	return true
}

func sanitizeMistralMessages(raw json.RawMessage) (json.RawMessage, bool, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, false, fmt.Errorf("decode chat messages")
	}
	changed := false
	for index, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil || message == nil {
			continue
		}
		messageChanged := false
		var role string
		_ = json.Unmarshal(message["role"], &role)
		if role == "developer" {
			role = "system"
			message["role"] = json.RawMessage(`"system"`)
			messageChanged = true
		}
		if allowed, known := mistralMessageFields[role]; known {
			for key := range message {
				if !allowed[key] {
					delete(message, key)
					messageChanged = true
				}
			}
		}
		if calls, exists := message["tool_calls"]; exists {
			updated, callsChanged, err := sanitizeMistralToolCalls(calls)
			if err != nil {
				return nil, false, err
			}
			if callsChanged {
				message["tool_calls"] = updated
				messageChanged = true
			}
		}
		if content, exists := message["content"]; exists {
			updated, contentChanged := stripMistralChunkFields(content)
			if contentChanged {
				message["content"] = updated
				messageChanged = true
			}
		}
		if !messageChanged {
			continue
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return nil, false, fmt.Errorf("encode chat message")
		}
		messages[index] = encoded
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return nil, false, fmt.Errorf("encode chat messages")
	}
	return encoded, true, nil
}

func sanitizeMistralToolCalls(raw json.RawMessage) (json.RawMessage, bool, error) {
	var calls []json.RawMessage
	if err := json.Unmarshal(raw, &calls); err != nil {
		// Not an array: leave it for the upstream to reject.
		return raw, false, nil
	}
	changed := false
	for index, rawCall := range calls {
		var call map[string]json.RawMessage
		if err := json.Unmarshal(rawCall, &call); err != nil || call == nil {
			continue
		}
		callChanged := false
		for key := range call {
			if !mistralToolCallFields[key] {
				delete(call, key)
				callChanged = true
			}
		}
		if function, exists := call["function"]; exists {
			updated, functionChanged, err := keepJSONObjectFields(function, mistralFunctionFields)
			if err == nil && functionChanged {
				call["function"] = updated
				callChanged = true
			}
		}
		if !callChanged {
			continue
		}
		encoded, err := json.Marshal(call)
		if err != nil {
			return nil, false, fmt.Errorf("encode tool call")
		}
		calls[index] = encoded
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(calls)
	if err != nil {
		return nil, false, fmt.Errorf("encode tool calls")
	}
	return encoded, true, nil
}

// stripMistralChunkFields removes caching and citation fields from content
// chunks; string content is returned unchanged.
func stripMistralChunkFields(raw json.RawMessage) (json.RawMessage, bool) {
	var chunks []json.RawMessage
	if err := json.Unmarshal(raw, &chunks); err != nil {
		return raw, false
	}
	changed := false
	for index, rawChunk := range chunks {
		var chunk map[string]json.RawMessage
		if err := json.Unmarshal(rawChunk, &chunk); err != nil || chunk == nil {
			continue
		}
		chunkChanged := false
		for _, field := range mistralForeignChunkFields {
			if _, exists := chunk[field]; exists {
				delete(chunk, field)
				chunkChanged = true
			}
		}
		if !chunkChanged {
			continue
		}
		encoded, err := json.Marshal(chunk)
		if err != nil {
			continue
		}
		chunks[index] = encoded
		changed = true
	}
	if !changed {
		return raw, false
	}
	encoded, err := json.Marshal(chunks)
	if err != nil {
		return raw, false
	}
	return encoded, true
}

// keepJSONObjectFields drops every key of a JSON object that is not allowed.
func keepJSONObjectFields(raw json.RawMessage, allowed map[string]bool) (json.RawMessage, bool, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, false, fmt.Errorf("decode object")
	}
	changed := false
	for key := range object {
		if !allowed[key] {
			delete(object, key)
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}
