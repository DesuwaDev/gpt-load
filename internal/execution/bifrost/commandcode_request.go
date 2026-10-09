package bifrost

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Command Code（commandcode.ai）的标准订阅只开放 /alpha/generate：请求体是官方 CLI 的
// 私有格式，响应恒为逐行 JSON 事件流。协议细节参照 MIT 许可的 commandcode-api-proxy
// 0.4.1（github.com/thaolaptrinh/commandcode-api-proxy），模型元数据亦取自该包。

const (
	commandCodeGeneratePath = "/alpha/generate"
	commandCodeModelsPath   = "/provider/v1/models"
	// 上游会拒绝版本缺失或过旧的 CLI 身份；拉取最新版本失败时退回此版本。
	commandCodeFallbackCLIVersion = "1.79.2"
	commandCodeCLIVersionEnv      = "CC_CLI_VERSION"
	commandCodeNodeVersion        = "v22.20.0"
	commandCodeWorkingDir         = "/workspace/project"
	commandCodeProjectSlug        = "project"
	commandCodeNoToolsInstruction = "CRITICAL: You are running in a chat-only environment. Tool execution is disabled. Do not generate or call any tools (e.g. Build, ReadFile, grep, Search, etc.). Respond only with plain text."
	commandCodeNoToolsUserNote    = "\n\n[System Note: Tool execution is disabled in this environment. Do not output any tool calls (such as Build, Search, ReadFile, grep, etc.). You must answer directly in plain text.]"
)

//go:embed commandcode_models.json
var commandCodeModelJSON []byte

type commandCodeModelCatalog struct {
	ClosedModelOrgs  []string            `json:"closedModelOrgs"`
	Builtin          []string            `json:"builtin"`
	ShortAliases     map[string]string   `json:"shortAliases"`
	ModelNames       map[string]string   `json:"modelNames"`
	ContextWindows   map[string]int      `json:"contextWindows"`
	ReasoningEfforts map[string][]string `json:"reasoningEfforts"`
}

var commandCodeModels = func() commandCodeModelCatalog {
	var catalog commandCodeModelCatalog
	if err := json.Unmarshal(commandCodeModelJSON, &catalog); err != nil || len(catalog.Builtin) == 0 {
		panic(fmt.Sprintf("invalid embedded Command Code model catalog: %v", err))
	}
	return catalog
}()

// commandCodeClosedModel 识别上游列出但不经 /alpha/generate 提供的闭源模型。
func commandCodeClosedModel(id string) bool {
	lower := strings.ToLower(id)
	if strings.HasPrefix(lower, "claude-") || strings.HasPrefix(lower, "gpt-") {
		return true
	}
	org, _, _ := strings.Cut(lower, "/")
	for _, closed := range commandCodeModels.ClosedModelOrgs {
		if org == strings.ToLower(closed) {
			return true
		}
	}
	return false
}

var commandCodeEffortRank = map[string]int{"low": 0, "medium": 1, "high": 2, "xhigh": 3, "max": 4}

// commandCodeReasoningEffort 把请求的思考强度收敛到模型支持的档位。上游对不支持的档位
// 会静默改写，这里按“不超过请求档位的最高档，否则取最低档”就近裁剪以保留调用方意图；
// 未收录的模型原样透传。none 表示不请求思考，由上游使用默认值。
func commandCodeReasoningEffort(model, requested string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	switch requested {
	case "", "none":
		return ""
	case "minimal":
		requested = "low"
	}
	supported := commandCodeModels.ReasoningEfforts[model]
	if len(supported) == 0 {
		return requested
	}
	rank, known := commandCodeEffortRank[requested]
	if !known {
		return requested
	}
	best, lowest := "", ""
	for _, effort := range supported {
		if effort == requested {
			return effort
		}
		value := commandCodeEffortRank[effort]
		if value <= rank && (best == "" || value > commandCodeEffortRank[best]) {
			best = effort
		}
		if lowest == "" || value < commandCodeEffortRank[lowest] {
			lowest = effort
		}
	}
	if best != "" {
		return best
	}
	return lowest
}

// ---------------------------------------------------------------------------
// CLI 版本
// ---------------------------------------------------------------------------

var commandCodeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]{1,64})?$`)

const (
	commandCodeVersionTTL        = 6 * time.Hour
	commandCodeVersionRetry      = 10 * time.Minute
	commandCodeVersionColdWait   = 4 * time.Second
	commandCodeVersionFetchLimit = 8 * time.Second
)

var commandCodeVersionSources = []string{
	"https://registry.npmjs.org/command-code/latest",
	"https://registry.npmmirror.com/command-code/latest",
}

// commandCodeVersionResolver 缓存官方 CLI 的最新版本：首次使用最多等待数秒，之后过期时
// 后台刷新并继续使用旧值；拉取失败不会让每个请求都阻塞重试。
type commandCodeVersionResolver struct {
	mu        sync.Mutex
	version   string
	fetchedAt time.Time
	failedAt  time.Time
	done      chan struct{}
	fetch     func(context.Context) (string, error)
	now       func() time.Time
	coldWait  time.Duration
}

var commandCodeVersions = &commandCodeVersionResolver{
	fetch: fetchCommandCodeCLIVersion, now: time.Now, coldWait: commandCodeVersionColdWait,
}

func (r *commandCodeVersionResolver) Version(ctx context.Context) string {
	if override := strings.TrimSpace(os.Getenv(commandCodeCLIVersionEnv)); commandCodeVersionPattern.MatchString(override) {
		return override
	}
	r.mu.Lock()
	if r.version != "" {
		version := r.version
		if r.now().Sub(r.fetchedAt) >= commandCodeVersionTTL && r.now().Sub(r.failedAt) >= commandCodeVersionRetry {
			r.startRefreshLocked()
		}
		r.mu.Unlock()
		return version
	}
	if !r.failedAt.IsZero() {
		// 只有进程内首次获取会等待；此后失败只在后台重试，请求立即使用兜底版本。
		if r.now().Sub(r.failedAt) >= commandCodeVersionRetry {
			r.startRefreshLocked()
		}
		r.mu.Unlock()
		return commandCodeFallbackCLIVersion
	}
	if r.done != nil {
		// 首次获取已在进行，后续请求同样等待其结果。
		done := r.done
		r.mu.Unlock()
		return r.awaitColdVersion(ctx, done)
	}
	done := r.startRefreshLocked()
	r.mu.Unlock()
	return r.awaitColdVersion(ctx, done)
}

func (r *commandCodeVersionResolver) awaitColdVersion(ctx context.Context, done <-chan struct{}) string {
	timer := time.NewTimer(r.coldWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.version != "" {
		return r.version
	}
	return commandCodeFallbackCLIVersion
}

func (r *commandCodeVersionResolver) startRefreshLocked() <-chan struct{} {
	if r.done != nil {
		return r.done
	}
	done := make(chan struct{})
	r.done = done
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), commandCodeVersionFetchLimit)
		defer cancel()
		version, err := r.fetch(ctx)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.done = nil
		if err != nil || !commandCodeVersionPattern.MatchString(version) {
			r.failedAt = r.now()
			return
		}
		// 镜像可能滞后，绝不退回到比已知版本更旧的版本。
		if commandCodeVersionLess(version, commandCodeFallbackCLIVersion) {
			version = commandCodeFallbackCLIVersion
		}
		r.version, r.fetchedAt, r.failedAt = version, r.now(), time.Time{}
	}()
	return done
}

func fetchCommandCodeCLIVersion(ctx context.Context) (string, error) {
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	var lastErr error
	for _, source := range commandCodeVersionSources {
		version, err := fetchCommandCodeCLIVersionFrom(ctx, client, source)
		if err == nil {
			return version, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func fetchCommandCodeCLIVersionFrom(ctx context.Context, client *http.Client, source string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version source returned %d", response.StatusCode)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&manifest); err != nil {
		return "", err
	}
	if !commandCodeVersionPattern.MatchString(manifest.Version) {
		return "", fmt.Errorf("invalid version %q", manifest.Version)
	}
	return manifest.Version, nil
}

func commandCodeVersionLess(left, right string) bool {
	parse := func(value string) [3]int {
		var parts [3]int
		core, _, _ := strings.Cut(strings.SplitN(value, "+", 2)[0], "-")
		for index, field := range strings.SplitN(core, ".", 3) {
			parts[index], _ = strconv.Atoi(field)
		}
		return parts
	}
	a, b := parse(left), parse(right)
	for index := range a {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 请求翻译：OpenAI Chat → /alpha/generate
// ---------------------------------------------------------------------------

type commandCodeChatRequest struct {
	Messages            []commandCodeChatMessage `json:"messages"`
	Tools               []commandCodeChatTool    `json:"tools"`
	ToolChoice          json.RawMessage          `json:"tool_choice"`
	MaxTokens           json.RawMessage          `json:"max_tokens"`
	MaxCompletionTokens json.RawMessage          `json:"max_completion_tokens"`
	Temperature         json.RawMessage          `json:"temperature"`
	TopP                json.RawMessage          `json:"top_p"`
	Stop                json.RawMessage          `json:"stop"`
	ReasoningEffort     string                   `json:"reasoning_effort"`
	Reasoning           *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

type commandCodeChatMessage struct {
	Role       string                    `json:"role"`
	Content    json.RawMessage           `json:"content"`
	ToolCalls  []commandCodeChatToolCall `json:"tool_calls"`
	ToolCallID string                    `json:"tool_call_id"`
}

type commandCodeChatToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type commandCodeChatTool struct {
	Type     string `json:"type"`
	Function *struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type commandCodeRequest struct {
	Config         commandCodeConfig `json:"config"`
	Memory         string            `json:"memory"`
	Taste          string            `json:"taste"`
	Skills         string            `json:"skills"`
	PermissionMode string            `json:"permissionMode"`
	Params         commandCodeParams `json:"params"`
	ThreadID       string            `json:"threadId"`
}

type commandCodeConfig struct {
	WorkingDir    string   `json:"workingDir"`
	Date          string   `json:"date"`
	Environment   string   `json:"environment"`
	Structure     []string `json:"structure"`
	IsGitRepo     bool     `json:"isGitRepo"`
	CurrentBranch string   `json:"currentBranch"`
	MainBranch    string   `json:"mainBranch"`
	GitStatus     string   `json:"gitStatus"`
	RecentCommits []string `json:"recentCommits"`
}

type commandCodeParams struct {
	Model           string                 `json:"model"`
	Messages        []commandCodeMessage   `json:"messages"`
	Stream          bool                   `json:"stream"`
	MaxTokens       json.RawMessage        `json:"max_tokens,omitempty"`
	Temperature     json.RawMessage        `json:"temperature,omitempty"`
	TopP            json.RawMessage        `json:"top_p,omitempty"`
	Stop            json.RawMessage        `json:"stop,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	Tools           []commandCodeTool      `json:"tools,omitempty"`
	ToolChoice      *commandCodeToolChoice `json:"tool_choice,omitempty"`
	System          string                 `json:"system,omitempty"`
}

// commandCodeMessage.Content 是字符串或部件数组，与上游 schema 保持一致。
type commandCodeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type commandCodePart map[string]any

type commandCodeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type commandCodeToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// commandCodeRequestPlan 是一次 generate 请求翻译后的线上形态与元数据。
type commandCodeRequestPlan struct {
	body     []byte
	threadID string
	hasTools bool
}

func buildCommandCodeRequest(chatBody []byte, model string, now time.Time) (commandCodeRequestPlan, error) {
	var chat commandCodeChatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		return commandCodeRequestPlan{}, fmt.Errorf("decode Chat request: %w", err)
	}
	tools := make([]commandCodeTool, 0, len(chat.Tools))
	for _, tool := range chat.Tools {
		if tool.Function == nil || strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		tools = append(tools, commandCodeTool{
			Name: tool.Function.Name, Description: tool.Function.Description,
			InputSchema: nonNullJSON(tool.Function.Parameters),
		})
	}
	messages, system, err := commandCodeMessages(chat.Messages)
	if err != nil {
		return commandCodeRequestPlan{}, err
	}
	resolvedModel := resolveCommandCodeModel(model)
	effort := chat.ReasoningEffort
	if effort == "" && chat.Reasoning != nil {
		effort = chat.Reasoning.Effort
	}
	maxTokens := nonNullJSON(chat.MaxTokens)
	if maxTokens == nil {
		maxTokens = nonNullJSON(chat.MaxCompletionTokens)
	}
	threadID, err := randomUUID()
	if err != nil {
		return commandCodeRequestPlan{}, err
	}
	params := commandCodeParams{
		Model: resolvedModel, Messages: messages, Stream: true,
		MaxTokens: maxTokens, Temperature: nonNullJSON(chat.Temperature), TopP: nonNullJSON(chat.TopP),
		Stop:            nonNullJSON(chat.Stop),
		ReasoningEffort: commandCodeReasoningEffort(resolvedModel, effort),
		System:          system,
	}
	hasTools := len(tools) > 0
	if hasTools {
		params.Tools = tools
		params.ToolChoice = commandCodeToolChoiceFor(chat.ToolChoice)
	} else {
		applyCommandCodeNoToolsSafeguard(&params)
	}
	body, err := json.Marshal(commandCodeRequest{
		Config: commandCodeConfig{
			WorkingDir: commandCodeWorkingDir, Date: now.UTC().Format("2006-01-02"),
			Environment: "linux-x64, Node.js " + commandCodeNodeVersion,
			Structure:   []string{}, RecentCommits: []string{},
		},
		PermissionMode: "standard", Params: params, ThreadID: threadID,
	})
	if err != nil {
		return commandCodeRequestPlan{}, err
	}
	return commandCodeRequestPlan{body: body, threadID: threadID, hasTools: hasTools}, nil
}

// resolveCommandCodeModel 让短别名（如 deepseek-v4）与裸模型名（如 GLM-5.2）命中
// 带组织前缀的完整 ID；已带前缀或未收录的名称原样透传。
func resolveCommandCodeModel(model string) string {
	model = strings.TrimSpace(model)
	if alias, ok := commandCodeModels.ShortAliases[strings.ToLower(model)]; ok {
		return alias
	}
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	for _, id := range commandCodeModels.Builtin {
		_, last, _ := strings.Cut(id, "/")
		if strings.EqualFold(last, model) {
			return id
		}
	}
	return model
}

func commandCodeMessages(source []commandCodeChatMessage) ([]commandCodeMessage, string, error) {
	toolNames := make(map[string]string)
	for _, message := range source {
		for _, call := range message.ToolCalls {
			if call.ID != "" {
				toolNames[call.ID] = call.Function.Name
			}
		}
	}
	var system []string
	messages := make([]commandCodeMessage, 0, len(source))
	for _, message := range source {
		switch message.Role {
		case "system", "developer":
			text, _, err := commandCodeTextContent(message.Content)
			if err != nil {
				return nil, "", err
			}
			if text != "" {
				system = append(system, text)
			}
		case "tool":
			text, allText, err := commandCodeTextContent(message.Content)
			if err != nil {
				return nil, "", err
			}
			if !allText {
				text = string(bytes.TrimSpace(message.Content))
			}
			messages = append(messages, commandCodeMessage{Role: "tool", Content: []commandCodePart{{
				"type": "tool-result", "toolCallId": message.ToolCallID,
				"toolName": toolNames[message.ToolCallID],
				"output":   map[string]string{"type": "text", "value": text},
			}}})
		case "assistant":
			text, _, err := commandCodeTextContent(message.Content)
			if err != nil {
				return nil, "", err
			}
			if len(message.ToolCalls) == 0 {
				if text != "" {
					messages = append(messages, commandCodeMessage{Role: "assistant", Content: text})
				}
				continue
			}
			parts := make([]commandCodePart, 0, len(message.ToolCalls)+1)
			if text != "" {
				parts = append(parts, commandCodePart{"type": "text", "text": text})
			}
			for _, call := range message.ToolCalls {
				parts = append(parts, commandCodePart{
					"type": "tool-call", "toolCallId": call.ID, "toolName": call.Function.Name,
					"input": commandCodeToolInput(call.Function.Arguments),
				})
			}
			messages = append(messages, commandCodeMessage{Role: "assistant", Content: parts})
		case "user":
			content, err := commandCodeUserContent(message.Content)
			if err != nil {
				return nil, "", err
			}
			messages = append(messages, commandCodeMessage{Role: "user", Content: content})
		default:
			return nil, "", fmt.Errorf("unsupported Chat message role %q", message.Role)
		}
	}
	return pruneCommandCodeDanglingTools(messages), strings.Join(system, "\n\n"), nil
}

// commandCodeTextContent 读取字符串或文本部件数组；第二个返回值表示内容是否全部为文本。
func commandCodeTextContent(raw json.RawMessage) (string, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", true, nil
	}
	if raw[0] == '"' {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, true, err
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", false, fmt.Errorf("decode Chat message content: %w", err)
	}
	texts := make([]string, 0, len(parts))
	allText := true
	for _, part := range parts {
		if part.Type != "text" {
			allText = false
			continue
		}
		texts = append(texts, part.Text)
	}
	return strings.Join(texts, "\n"), allText, nil
}

func commandCodeUserContent(raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("decode Chat user content: %w", err)
	}
	result := make([]commandCodePart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			result = append(result, commandCodePart{"type": "text", "text": part.Text})
		case "image_url":
			var url string
			if json.Unmarshal(part.ImageURL, &url) != nil {
				var object struct {
					URL string `json:"url"`
				}
				_ = json.Unmarshal(part.ImageURL, &object)
				url = object.URL
			}
			if url != "" {
				result = append(result, commandCodePart{"type": "image", "image": url})
			}
		}
	}
	if len(result) == 0 {
		return "", nil
	}
	return result, nil
}

func commandCodeToolInput(raw json.RawMessage) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return map[string]any{}
	}
	if raw[0] != '"' {
		return raw
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return map[string]any{}
	}
	if strings.TrimSpace(text) == "" {
		return map[string]any{}
	}
	var decoded json.RawMessage
	if json.Unmarshal([]byte(text), &decoded) == nil {
		return decoded
	}
	return text
}

func commandCodeToolChoiceFor(raw json.RawMessage) *commandCodeToolChoice {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		// 上游只接受对象形态，没有 none；auto 与 none 都交给模型默认行为。
		if mode == "required" {
			return &commandCodeToolChoice{Type: "any"}
		}
		return nil
	}
	var object struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Type == "function" && object.Function.Name != "" {
		return &commandCodeToolChoice{Type: "tool", Name: object.Function.Name}
	}
	return nil
}

// applyCommandCodeNoToolsSafeguard 防止上游按其编码代理身份调用客户端未声明的工具。
func applyCommandCodeNoToolsSafeguard(params *commandCodeParams) {
	if params.System != "" {
		params.System += "\n\n" + commandCodeNoToolsInstruction
	} else {
		params.System = commandCodeNoToolsInstruction
	}
	for index := len(params.Messages) - 1; index >= 0; index-- {
		message := &params.Messages[index]
		if message.Role != "user" {
			continue
		}
		switch content := message.Content.(type) {
		case string:
			message.Content = content + commandCodeNoToolsUserNote
		case []commandCodePart:
			for part := len(content) - 1; part >= 0; part-- {
				if content[part]["type"] == "text" {
					text, _ := content[part]["text"].(string)
					content[part]["text"] = text + commandCodeNoToolsUserNote
					return
				}
			}
			message.Content = append(content, commandCodePart{"type": "text", "text": commandCodeNoToolsUserNote})
		}
		return
	}
}

// pruneCommandCodeDanglingTools 删除没有配对的工具调用或结果：上游要求二者严格成对。
func pruneCommandCodeDanglingTools(messages []commandCodeMessage) []commandCodeMessage {
	calls, results := make(map[string]bool), make(map[string]bool)
	for _, message := range messages {
		parts, _ := message.Content.([]commandCodePart)
		for _, part := range parts {
			id, _ := part["toolCallId"].(string)
			switch part["type"] {
			case "tool-call":
				calls[id] = true
			case "tool-result":
				results[id] = true
			}
		}
	}
	paired := func(id string) bool { return id != "" && calls[id] && results[id] }
	pruned := make([]commandCodeMessage, 0, len(messages))
	for _, message := range messages {
		parts, ok := message.Content.([]commandCodePart)
		if !ok {
			pruned = append(pruned, message)
			continue
		}
		kept := make([]commandCodePart, 0, len(parts))
		for _, part := range parts {
			kind := part["type"]
			id, _ := part["toolCallId"].(string)
			if (kind == "tool-call" || kind == "tool-result") && !paired(id) {
				continue
			}
			kept = append(kept, part)
		}
		if len(kept) > 0 {
			pruned = append(pruned, commandCodeMessage{Role: message.Role, Content: kept})
		}
	}
	return pruned
}

func nonNullJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	return raw
}

// commandCodeHeaders 复刻官方 CLI 的请求头；上游会把缺少这些身份字段的请求判定为代理。
func commandCodeHeaders(version, sessionID string, generate bool) map[string]string {
	headers := map[string]string{
		"Accept":                 "application/json, */*",
		"Accept-Encoding":        "identity",
		"Accept-Language":        "en-US,en;q=0.9",
		"User-Agent":             "commandcode-cli/" + version + " Node.js/" + commandCodeNodeVersion,
		"X-Cli-Environment":      "production",
		"X-Command-Code-Version": version,
	}
	if generate {
		headers["Content-Type"] = "application/json"
		headers["X-Session-Id"] = sessionID
		headers["X-Co-Flag"] = "false"
		headers["X-Taste-Learning"] = "false"
		headers["X-Project-Slug"] = commandCodeProjectSlug
		headers["Traceparent"] = commandCodeTraceparent()
	}
	return headers
}

func commandCodeTraceparent() string {
	var buffer [24]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "00-" + strings.Repeat("0", 31) + "1-" + strings.Repeat("0", 15) + "1-01"
	}
	return "00-" + hex.EncodeToString(buffer[:16]) + "-" + hex.EncodeToString(buffer[16:]) + "-01"
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
