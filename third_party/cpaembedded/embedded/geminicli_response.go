package embedded

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// Like sub2api, non-streaming Code Assist requests use streamGenerateContent
// and collect the response. Keep image/tool/thought parts and terminal metadata.
func collectGeminiCLIResponse(source io.Reader) ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(source, 256<<20))
	if first, err := reader.Peek(1); err == nil && (first[0] == '{' || first[0] == '[') {
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		if inner := gjson.GetBytes(body, "response"); inner.IsObject() {
			return []byte(inner.Raw), nil
		}
		return body, nil
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	result := map[string]any{}
	candidates := map[int]map[string]any{}
	order := []int{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, errors.New("invalid Code Assist SSE response")
		}
		if _, ok := event["error"]; ok {
			return nil, errors.New("Code Assist stream returned an error")
		}
		if inner, ok := event["response"].(map[string]any); ok {
			event = inner
		}
		for key, value := range event {
			if key != "candidates" {
				result[key] = value
			}
		}
		list, _ := event["candidates"].([]any)
		for position, value := range list {
			incoming, ok := value.(map[string]any)
			if !ok {
				continue
			}
			index := position
			if n, ok := incoming["index"].(float64); ok {
				index = int(n)
			}
			candidate, exists := candidates[index]
			if !exists {
				candidate = map[string]any{}
				candidates[index] = candidate
				order = append(order, index)
			}
			for key, value := range incoming {
				if key != "content" {
					candidate[key] = value
				}
			}
			content, ok := incoming["content"].(map[string]any)
			if !ok {
				continue
			}
			current, ok := candidate["content"].(map[string]any)
			if !ok {
				current = map[string]any{}
				candidate["content"] = current
			}
			parts, _ := current["parts"].([]any)
			incomingParts, _ := content["parts"].([]any)
			current["parts"] = append(parts, incomingParts...)
			for key, value := range content {
				if key != "parts" {
					current[key] = value
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	list := make([]any, 0, len(order))
	for _, index := range order {
		list = append(list, candidates[index])
	}
	if len(result) == 0 && len(list) == 0 {
		return nil, errors.New("Code Assist response contains no data")
	}
	result["candidates"] = list
	return json.Marshal(result)
}

func sanitizeGeminiCLIContents(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if contents, ok := payload["contents"].([]any); ok {
		filtered := make([]any, 0, len(contents))
		for _, raw := range contents {
			content, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			parts, ok := content["parts"].([]any)
			if !ok || len(parts) == 0 {
				continue
			}
			for _, rawPart := range parts {
				if part, ok := rawPart.(map[string]any); ok {
					if _, call := part["functionCall"]; call {
						if signature, _ := part["thoughtSignature"].(string); signature == "" {
							part["thoughtSignature"] = "skip_thought_signature_validator"
						}
					}
				}
			}
			filtered = append(filtered, content)
		}
		payload["contents"] = filtered
	}
	return json.Marshal(payload)
}

// Same text approximation as sub2api when AI Studio countTokens is unavailable.
func estimateGeminiCLITokens(body []byte) int {
	count := 0
	add := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		n := utf8.RuneCountInString(text)
		ascii := 0
		for _, r := range text {
			if r <= 127 {
				ascii++
			}
		}
		if float64(ascii)/float64(n) >= 0.8 {
			count += (n + 3) / 4
		} else {
			count += n
		}
	}
	root := gjson.ParseBytes(body)
	root.Get("systemInstruction.parts").ForEach(func(_, part gjson.Result) bool { add(part.Get("text").String()); return true })
	root.Get("contents").ForEach(func(_, content gjson.Result) bool {
		content.Get("parts").ForEach(func(_, part gjson.Result) bool { add(part.Get("text").String()); return true })
		return true
	})
	return count
}
