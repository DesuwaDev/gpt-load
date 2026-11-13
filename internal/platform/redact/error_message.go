package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// ExtractErrorMessage returns the human-readable message from an upstream
// error body. It deliberately returns no raw JSON or markup when a
// known message field is unavailable, so callers can fall back to a safe
// generic message instead.
func ExtractErrorMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !utf8.Valid(body) || containsUnsafeTextControl(body) {
		return ""
	}
	if message, isJSON := extractJSONErrorMessage(body); isJSON {
		return safeErrorMessageText(message)
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || looksLikeMarkup(text) {
		return ""
	}
	return safeErrorMessageText(text)
}

// SanitizeErrorMessageText 校验已提取的诊断文本，保留方括号前缀并拒绝原始 JSON、标记和控制字符。
func SanitizeErrorMessageText(value string) string {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || strings.HasPrefix(value, "{") || looksLikeMarkup(value) ||
		(strings.HasPrefix(value, "[") && json.Valid([]byte(value))) {
		return ""
	}
	return safeErrorMessageText(value)
}

func extractJSONErrorMessage(body []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return "", false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", false
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		if message := firstErrorMessage(nested); message != "" {
			return message, true
		}
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if nested, ok := response["error"].(map[string]any); ok {
			if message := firstErrorMessage(nested); message != "" {
				return message, true
			}
		}
	}
	if message := firstErrorMessage(payload); message != "" {
		return message, true
	}
	if message, ok := payload["error"].(string); ok {
		return strings.TrimSpace(message), true
	}
	return "", true
}

func firstErrorMessage(payload map[string]any) string {
	for _, field := range []string{"message", "detail", "title"} {
		if value, ok := payload[field].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	if message := validationErrorMessage(payload["detail"]); message != "" {
		return message
	}
	// Mistral wraps validation errors as {"message":{"detail":[...]}}.
	if nested, ok := payload["message"].(map[string]any); ok {
		return validationErrorMessage(nested["detail"])
	}
	return ""
}

// validationErrorMessage summarizes a FastAPI/Pydantic validation error list
// as "location: message" entries. The rejected input is never included, since
// it echoes request content.
func validationErrorMessage(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	const maxEntries = 5
	entries := make([]string, 0, min(len(items), maxEntries))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		message, _ := item["msg"].(string)
		if message = strings.TrimSpace(message); message == "" {
			continue
		}
		location := make([]string, 0, 8)
		if path, ok := item["loc"].([]any); ok {
			for _, segment := range path {
				switch typed := segment.(type) {
				case string:
					location = append(location, typed)
				case json.Number:
					location = append(location, typed.String())
				}
			}
		}
		if len(location) > 0 {
			message = strings.Join(location, ".") + ": " + message
		}
		entries = append(entries, message)
		if len(entries) == maxEntries {
			break
		}
	}
	return strings.Join(entries, "; ")
}

func safeErrorMessageText(value string) string {
	for _, runeValue := range value {
		if runeValue == 0 || (runeValue < 0x20 && runeValue != '\t' && runeValue != '\n' && runeValue != '\r') ||
			(runeValue >= 0x7f && runeValue <= 0x9f) {
			return ""
		}
	}
	return strings.TrimSpace(value)
}

func containsUnsafeTextControl(body []byte) bool {
	for _, value := range body {
		if value == 0 || (value < 0x20 && value != '\t' && value != '\n' && value != '\r') {
			return true
		}
	}
	return false
}

func looksLikeMarkup(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "<")
}
