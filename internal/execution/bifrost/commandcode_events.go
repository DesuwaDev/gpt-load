package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// commandCodeEvent 是上游事件流中的一行，兼容扁平（{type,text}）与嵌套（{type,data:{}}）两种形态。
type commandCodeEvent struct {
	Type string
	Data map[string]json.RawMessage
}

// parseCommandCodeLine 解析一行事件；空行、心跳、[DONE] 与无法解析的行按心跳忽略，
// 与参考实现一致，返回 ok=false。
func parseCommandCodeLine(line []byte) (commandCodeEvent, bool, error) {
	line = bytes.TrimSpace(line)
	if payload, found := bytes.CutPrefix(line, []byte("data:")); found {
		line = bytes.TrimSpace(payload)
	}
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) || line[0] != '{' {
		return commandCodeEvent{}, false, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(line, &object) != nil || object == nil {
		return commandCodeEvent{}, false, nil
	}
	var kind string
	if json.Unmarshal(object["type"], &kind) != nil || kind == "" {
		return commandCodeEvent{}, false, nil
	}
	data := object
	var nested map[string]json.RawMessage
	if json.Unmarshal(object["data"], &nested) == nil && nested != nil {
		data = nested
	}
	return commandCodeEvent{Type: kind, Data: data}, true, nil
}

func (e commandCodeEvent) str(keys ...string) string {
	for _, key := range keys {
		var value string
		if json.Unmarshal(e.Data[key], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func (e commandCodeEvent) index() (int, bool) {
	var value int
	if raw, ok := e.Data["index"]; ok && json.Unmarshal(raw, &value) == nil && value >= 0 {
		return value, true
	}
	return 0, false
}

// arguments 返回工具参数文本：字符串原样返回，对象等 JSON 值按紧凑形式序列化。
func (e commandCodeEvent) arguments(keys ...string) string {
	for _, key := range keys {
		raw := bytes.TrimSpace(e.Data[key])
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil {
			return compact.String()
		}
	}
	return ""
}

func (e commandCodeEvent) errorMessage() string {
	if message := e.str("message", "errorText"); message != "" {
		return message
	}
	var nested struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(e.Data["error"], &nested) == nil && nested.Message != "" {
		return nested.Message
	}
	var text string
	if json.Unmarshal(e.Data["error"], &text) == nil && text != "" {
		return text
	}
	return "Command Code generation failed"
}

type commandCodeUsage struct {
	PromptTokens            int64                     `json:"prompt_tokens"`
	CompletionTokens        int64                     `json:"completion_tokens"`
	TotalTokens             int64                     `json:"total_tokens"`
	PromptTokensDetails     *commandCodeCachedTokens  `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *commandCodeReasonedUsage `json:"completion_tokens_details,omitempty"`
}

type commandCodeCachedTokens struct {
	CachedTokens int64 `json:"cached_tokens"`
}

type commandCodeReasonedUsage struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// commandCodeUsageFrom 读取 finish 事件的用量：上游用 AI SDK 的 totalUsage（驼峰），
// 部分网关用 usage（下划线），两种都接受。没有可用数字时返回 nil。
func commandCodeUsageFrom(event commandCodeEvent) *commandCodeUsage {
	raw := event.Data["totalUsage"]
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = event.Data["usage"]
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil
	}
	number := func(object map[string]json.RawMessage, keys ...string) (int64, bool) {
		for _, key := range keys {
			value := bytes.TrimSpace(object[key])
			if len(value) == 0 {
				continue
			}
			var text string
			if json.Unmarshal(value, &text) == nil {
				value = []byte(strings.TrimSpace(text))
			}
			if parsed, err := strconv.ParseFloat(string(value), 64); err == nil && parsed >= 0 {
				return int64(parsed), true
			}
		}
		return 0, false
	}
	nested := func(key string) map[string]json.RawMessage {
		var object map[string]json.RawMessage
		_ = json.Unmarshal(fields[key], &object)
		return object
	}
	prompt, hasPrompt := number(fields, "promptTokens", "inputTokens", "prompt_tokens")
	completion, hasCompletion := number(fields, "completionTokens", "outputTokens", "completion_tokens")
	total, hasTotal := number(fields, "totalTokens", "total_tokens")
	if !hasPrompt && !hasCompletion && !hasTotal {
		return nil
	}
	if !hasTotal {
		total = prompt + completion
	}
	usage := &commandCodeUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total}
	cached, hasCached := number(fields, "cachedInputTokens")
	if !hasCached {
		cached, hasCached = number(nested("inputTokenDetails"), "cacheReadTokens")
	}
	if !hasCached {
		cached, hasCached = number(nested("promptTokensDetails"), "cachedTokens")
	}
	if !hasCached {
		cached, hasCached = number(nested("prompt_tokens_details"), "cached_tokens")
	}
	if hasCached {
		usage.PromptTokensDetails = &commandCodeCachedTokens{CachedTokens: cached}
	}
	reasoningTokens, hasReasoning := number(fields, "reasoningTokens")
	if !hasReasoning {
		reasoningTokens, hasReasoning = number(nested("outputTokenDetails"), "reasoningTokens")
	}
	if !hasReasoning {
		reasoningTokens, hasReasoning = number(nested("completion_tokens_details"), "reasoning_tokens")
	}
	if hasReasoning {
		usage.CompletionTokensDetails = &commandCodeReasonedUsage{ReasoningTokens: reasoningTokens}
	}
	return usage
}

var commandCodeFinishReasons = map[string]string{
	"stop": "stop", "length": "length", "content_filtered": "content_filter", "content-filter": "content_filter",
	"tool-call": "tool_calls", "tool-calls": "tool_calls", "tool_call": "tool_calls", "tool_calls": "tool_calls",
}

type commandCodeToolState struct {
	id, name  string
	arguments string
}

// commandCodeTranslator 把上游事件翻译为 OpenAI Chat：流式产出 chunk，同时累积非流式结果。
type commandCodeTranslator struct {
	id, model     string
	created       int64
	allowTools    bool
	started       bool
	finished      bool
	finishReason  string
	usage         *commandCodeUsage
	failure       string
	content       strings.Builder
	reasoning     strings.Builder
	tools         []*commandCodeToolState
	toolIndexByID map[string]int
	// toolIndexByUpstream 让缺少 toolCallId 的后续增量按上游序号归并到同一调用。
	toolIndexByUpstream map[int]int
}

func newCommandCodeTranslator(id, model string, created int64, allowTools bool) *commandCodeTranslator {
	return &commandCodeTranslator{
		id: id, model: model, created: created, allowTools: allowTools,
		toolIndexByID: make(map[string]int), toolIndexByUpstream: make(map[int]int),
	}
}

type commandCodeChunkDelta map[string]any

func (t *commandCodeTranslator) chunk(delta commandCodeChunkDelta, finishReason any) map[string]any {
	return map[string]any{
		"id": t.id, "object": "chat.completion.chunk", "created": t.created, "model": t.model,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finishReason}},
	}
}

// begin 在首个输出前补发角色块：上游不保证发送 start 事件。
func (t *commandCodeTranslator) begin() []map[string]any {
	if t.started {
		return nil
	}
	t.started = true
	return []map[string]any{t.chunk(commandCodeChunkDelta{"role": "assistant", "content": ""}, nil)}
}

// toolIndex 为工具调用分配从 0 连续递增的序号；同一 toolCallId 或同一上游序号的增量与
// 终态必须落在同一序号上，否则客户端会把一个调用拆成多个。
func (t *commandCodeTranslator) toolIndex(id string, upstream int, hasUpstream bool) int {
	index, known := -1, false
	if id != "" {
		index, known = t.toolIndexByID[id]
	}
	if !known && hasUpstream {
		index, known = t.toolIndexByUpstream[upstream]
	}
	if !known {
		index = len(t.tools)
		t.tools = append(t.tools, &commandCodeToolState{})
	}
	if id != "" {
		if _, mapped := t.toolIndexByID[id]; !mapped {
			t.toolIndexByID[id] = index
		}
	}
	if hasUpstream {
		if _, mapped := t.toolIndexByUpstream[upstream]; !mapped {
			t.toolIndexByUpstream[upstream] = index
		}
	}
	return index
}

// toolIdentity 首次出现时补发工具 ID 与名称；同一序号上的元数据前后不一致视为协议错误。
func (t *commandCodeTranslator) toolIdentity(index int, id, name string) (map[string]any, string, error) {
	state := t.tools[index]
	identity := make(map[string]any)
	newName := ""
	if id != "" {
		if state.id != "" && state.id != id {
			return nil, "", fmt.Errorf("inconsistent Command Code tool call id")
		}
		if state.id == "" {
			state.id = id
			identity["id"], identity["type"] = id, "function"
		}
	}
	if name != "" {
		if state.name != "" && state.name != name {
			return nil, "", fmt.Errorf("inconsistent Command Code tool call name")
		}
		if state.name == "" {
			state.name = name
			newName = name
		}
	}
	return identity, newName, nil
}

func (t *commandCodeTranslator) toolChunk(index int, identity map[string]any, name, arguments string) map[string]any {
	call := map[string]any{"index": index}
	for key, value := range identity {
		call[key] = value
	}
	function := map[string]any{"arguments": arguments}
	if name != "" {
		function["name"] = name
	}
	call["function"] = function
	return t.chunk(commandCodeChunkDelta{"tool_calls": []map[string]any{call}}, nil)
}

// translate 处理一个上游事件，返回需要按序发送的 OpenAI Chat chunk。
func (t *commandCodeTranslator) translate(event commandCodeEvent) ([]map[string]any, error) {
	if t.finished {
		return nil, nil
	}
	switch event.Type {
	case "start":
		return t.begin(), nil
	case "text-delta":
		text := event.str("text", "delta", "textDelta")
		if text == "" {
			return nil, nil
		}
		t.content.WriteString(text)
		return append(t.begin(), t.chunk(commandCodeChunkDelta{"content": text}, nil)), nil
	case "reasoning-delta":
		text := event.str("text", "delta", "textDelta")
		if text == "" {
			return nil, nil
		}
		t.reasoning.WriteString(text)
		return append(t.begin(), t.chunk(commandCodeChunkDelta{"reasoning_content": text, "reasoning": text}, nil)), nil
	case "tool-call-delta":
		if !t.allowTools {
			return nil, nil
		}
		upstream, hasUpstream := event.index()
		id := event.str("toolCallId")
		index := t.toolIndex(id, upstream, hasUpstream)
		identity, name, err := t.toolIdentity(index, id, event.str("name", "toolName"))
		if err != nil {
			return nil, err
		}
		arguments := event.arguments("arguments", "argsTextDelta", "inputTextDelta")
		t.tools[index].arguments += arguments
		return append(t.begin(), t.toolChunk(index, identity, name, arguments)), nil
	case "tool-call":
		if !t.allowTools {
			return nil, nil
		}
		upstream, hasUpstream := event.index()
		id := event.str("toolCallId")
		index := t.toolIndex(id, upstream, hasUpstream)
		identity, name, err := t.toolIdentity(index, id, event.str("toolName", "name"))
		if err != nil {
			return nil, err
		}
		canonical := event.arguments("input", "arguments", "args")
		suffix, err := commandCodeToolArgumentSuffix(t.tools[index].arguments, canonical)
		if err != nil {
			return nil, err
		}
		t.tools[index].arguments += suffix
		return append(t.begin(), t.toolChunk(index, identity, name, suffix)), nil
	case "finish":
		t.finished = true
		reason := commandCodeFinishReasons[event.str("finishReason", "finish_reason")]
		if reason == "" {
			reason = "stop"
		}
		if reason == "tool_calls" && len(t.tools) == 0 {
			reason = "stop"
		}
		t.finishReason = reason
		t.usage = commandCodeUsageFrom(event)
		chunks := append(t.begin(), t.chunk(commandCodeChunkDelta{}, reason))
		if t.usage != nil {
			usageChunk := map[string]any{
				"id": t.id, "object": "chat.completion.chunk", "created": t.created, "model": t.model,
				"choices": []map[string]any{}, "usage": t.usage,
			}
			chunks = append(chunks, usageChunk)
		}
		return chunks, nil
	case "error":
		t.finished = true
		t.failure = event.errorMessage()
		return nil, nil
	default:
		return nil, nil
	}
}

// finishMissing 在上游正常结束却没有 finish 事件时补发终止块，避免客户端看到截断的流。
func (t *commandCodeTranslator) finishMissing() []map[string]any {
	if t.finished {
		return nil
	}
	t.finished = true
	t.finishReason = "stop"
	if len(t.tools) > 0 {
		t.finishReason = "tool_calls"
	}
	return append(t.begin(), t.chunk(commandCodeChunkDelta{}, t.finishReason))
}

// completion 返回累积的非流式 Chat Completions 响应。
func (t *commandCodeTranslator) completion() ([]byte, error) {
	message := map[string]any{"role": "assistant"}
	if reasoning := t.reasoning.String(); reasoning != "" {
		message["reasoning_content"], message["reasoning"] = reasoning, reasoning
	}
	if len(t.tools) > 0 {
		calls := make([]map[string]any, 0, len(t.tools))
		for _, tool := range t.tools {
			calls = append(calls, map[string]any{
				"id": tool.id, "type": "function",
				"function": map[string]any{"name": tool.name, "arguments": tool.arguments},
			})
		}
		message["tool_calls"] = calls
	}
	if content := t.content.String(); content != "" || len(t.tools) == 0 {
		message["content"] = content
	} else {
		message["content"] = nil
	}
	reason := t.finishReason
	if reason == "" {
		reason = "stop"
	}
	response := map[string]any{
		"id": t.id, "object": "chat.completion", "created": t.created, "model": t.model,
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": reason}},
	}
	if t.usage != nil {
		response["usage"] = t.usage
	}
	return json.Marshal(response)
}

// commandCodeToolArgumentSuffix 计算终态参数相对已发送增量的剩余部分：已发送字节不可撤回，
// 终态可能只在空白或键序上与增量不同。二者无法对齐时视为上游协议错误。
func commandCodeToolArgumentSuffix(emitted, canonical string) (string, error) {
	if strings.HasPrefix(canonical, emitted) {
		return canonical[len(emitted):], nil
	}
	if jsonEquivalent(emitted, canonical) {
		return "", nil
	}
	offset := 0
	inString, escaped := false, false
	for _, char := range emitted {
		if !inString {
			if strings.ContainsRune(" \t\r\n", char) {
				continue
			}
			for offset < len(canonical) && strings.ContainsRune(" \t\r\n", rune(canonical[offset])) {
				offset++
			}
		}
		encoded := string(char)
		if !strings.HasPrefix(canonical[offset:], encoded) {
			return "", fmt.Errorf("inconsistent Command Code tool arguments")
		}
		offset += len(encoded)
		switch {
		case escaped:
			escaped = false
		case inString && char == '\\':
			escaped = true
		case char == '"':
			inString = !inString
		}
	}
	suffix := canonical[offset:]
	if jsonEquivalent(emitted+suffix, canonical) {
		return suffix, nil
	}
	return "", fmt.Errorf("inconsistent Command Code tool arguments")
}

func jsonEquivalent(left, right string) bool {
	var a, b any
	if json.Unmarshal([]byte(left), &a) != nil || json.Unmarshal([]byte(right), &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// marshalCommandCodeSSE 把 chunk 编码为 OpenAI Chat SSE 帧。
func marshalCommandCodeSSE(chunks []map[string]any) ([]byte, error) {
	var output bytes.Buffer
	for _, chunk := range chunks {
		body, err := json.Marshal(chunk)
		if err != nil {
			return nil, err
		}
		output.WriteString("data: ")
		output.Write(body)
		output.WriteString("\n\n")
	}
	return output.Bytes(), nil
}
