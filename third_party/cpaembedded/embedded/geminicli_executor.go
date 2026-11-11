package embedded

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	geminiresponses "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/gemini/openai/responses"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// GeminiCLIExecutionError 是允许跨出内嵌边界的有界错误；上游响应体在本地解析后丢弃。
type GeminiCLIExecutionError struct {
	status        int
	typeValue     string
	codeValue     string
	summary       string
	retryAfter    time.Duration
	requestScoped bool
}

func (err *GeminiCLIExecutionError) Error() string {
	if err == nil || err.summary == "" {
		return "Gemini CLI upstream request failed"
	}
	return err.summary
}

func (err *GeminiCLIExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *GeminiCLIExecutionError) ErrorType() string {
	if err == nil {
		return ""
	}
	return err.typeValue
}

func (err *GeminiCLIExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.codeValue
}

func (err *GeminiCLIExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

func (err *GeminiCLIExecutionError) IsRequestScoped() bool {
	return err != nil && err.requestScoped
}

// GeminiCLIHTTPExecutor 是 GPT-Load 使用的 Gemini CLI 执行面。
//
// 协议转换、思考配置与用量统计全部复用 CPA 的 Gemini API 执行器；传输层把它发往
// generativelanguage 的请求改写成 Code Assist 的 v1internal 信封，并把响应解包回
// Gemini API 形态，因此 CPA 内部不感知 Code Assist。
type GeminiCLIHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, GeminiCLICredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, string, GeminiCLICredential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, GeminiCLICredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type geminiCLIHTTPExecutor struct {
	inner *internalexecutor.GeminiExecutor
}

// NewGeminiCLIHTTPExecutor 创建生产执行器。RequestRetry=0 保证一次 GPT-Load 尝试内 CPA 不重试。
func NewGeminiCLIHTTPExecutor() GeminiCLIHTTPExecutor {
	return &geminiCLIHTTPExecutor{inner: internalexecutor.NewGeminiExecutor(&internalconfig.Config{RequestRetry: 0})}
}

func (executor *geminiCLIHTTPExecutor) prepare(
	ctx context.Context,
	credentialID string,
	credential GeminiCLICredential,
	request ExecuteRequest,
	observation *executionObservation,
) (context.Context, error) {
	if err := validateGeminiCLICredential(credential); err != nil {
		return nil, err
	}
	endpoints, err := ResolveGeminiCLIAPIEndpoints(request.BaseURL)
	if err != nil {
		return nil, err
	}
	scope := strings.TrimSpace(credentialID)
	if scope == "" {
		scope = credential.AccountID
	}
	key := antigravityExecutionTransportKey{
		credential: ProviderGeminiCLI + "\x00" + scope,
		baseURL:    endpoints.ExecutionBase,
		proxyURL:   strings.TrimSpace(request.ProxyURL),
	}
	transport, err := antigravityExecutionTransports.Get(key, func() (*http.Transport, error) {
		return newHTTP1ExecutionTransport(request.ProxyURL)
	})
	if err != nil {
		return nil, fmt.Errorf("prepare Gemini CLI execution transport: %w", err)
	}
	return context.WithValue(antigravityExecutionContext{Context: ctx}, "cliproxy.roundtripper", noRedirectRoundTripper{
		base: geminiCLICodeAssistRoundTripper{
			base: transport, executionBase: endpoints.ExecutionBase,
			accessToken: credential.AccessToken, projectID: credential.ProjectID,
		},
		observation: observation,
	}), nil
}

func geminiCLIExecutorOptions(request ExecuteRequest, format sdktranslator.Format, stream bool) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{
		Stream: stream, Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	}
}

// geminiCLIAuth 只承载 provider 标识；令牌与项目由传输层注入，CPA 不会刷新或持久化它。
func geminiCLIAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{Provider: "gemini"}
}

func (executor *geminiCLIHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential GeminiCLICredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	observation := newProviderExecutionObservation(request, "gemini")
	executionCtx, err := executor.prepare(ctx, credentialID, credential, request, observation)
	if err != nil {
		return ExecuteResponse{}, err
	}
	format := sdktranslator.FromString(request.Format)
	response, err := executor.inner.Execute(executionCtx, geminiCLIAuth(), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, geminiCLIExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort(),
			AppliedReasoningMode:         observation.reasoningMode(),
			AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens()}, normalizeGeminiCLIExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       observation.reasoningEffort(),
		AppliedReasoningMode:         observation.reasoningMode(),
		AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens(),
	}, nil
}

func (executor *geminiCLIHTTPExecutor) CountTokensCanonical(
	ctx context.Context,
	credentialID string,
	credential GeminiCLICredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	executionCtx, err := executor.prepare(ctx, credentialID, credential, request, nil)
	if err != nil {
		return ExecuteResponse{}, err
	}
	format := sdktranslator.FromString(request.Format)
	response, err := executor.inner.CountTokens(executionCtx, geminiCLIAuth(), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, geminiCLIExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{}, normalizeGeminiCLIExecutionError(err)
	}
	return ExecuteResponse{
		Payload: normalizeAntigravityCountTokens(request.Format, response.Payload), Headers: response.Headers.Clone(),
	}, nil
}

func (executor *geminiCLIHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential GeminiCLICredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	observation := newProviderExecutionObservation(request, "gemini")
	executionCtx, err := executor.prepare(ctx, credentialID, credential, request, observation)
	if err != nil {
		return nil, err
	}
	format := sdktranslator.FromString(request.Format)
	response, err := executor.inner.ExecuteStream(executionCtx, geminiCLIAuth(), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, geminiCLIExecutorOptions(request, format, true))
	if err != nil {
		return &ExecuteStreamResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort(),
			AppliedReasoningMode:         observation.reasoningMode(),
			AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens()}, normalizeGeminiCLIExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...)}
			if chunk.Err != nil {
				converted.Err = normalizeGeminiCLIExecutionError(chunk.Err)
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks, AppliedReasoningEffort: observation.reasoningEffort(),
		AppliedReasoningMode:         observation.reasoningMode(),
		AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens(),
	}, nil
}

// geminiCLICodeAssistRoundTripper 把 CPA 生成的 Gemini API 请求改写成 Code Assist 请求。
// 出站请求头全部重建，客户端头与 CPA 默认头都不会带到上游。
type geminiCLICodeAssistRoundTripper struct {
	base          http.RoundTripper
	executionBase string
	accessToken   string
	projectID     string
}

func (transport geminiCLICodeAssistRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("Gemini CLI request is invalid")
	}
	model, action, err := geminiCLIModelAction(request.URL.Path)
	if err != nil {
		return nil, err
	}
	var body []byte
	if request.Body != nil {
		body, err = io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	wrapped, err := wrapGeminiCLIRequest(action, model, transport.projectID, body)
	clear(body)
	if err != nil {
		return nil, err
	}
	target := transport.executionBase + "/" + geminiCLIAPIVersion + ":" + action
	if action == "streamGenerateContent" {
		target += "?alt=sse"
	}
	outbound, err := http.NewRequestWithContext(request.Context(), http.MethodPost, target, bytes.NewReader(wrapped))
	if err != nil {
		return nil, err
	}
	outbound.Header.Set("Content-Type", "application/json")
	outbound.Header.Set("Authorization", "Bearer "+transport.accessToken)
	outbound.Header.Set("User-Agent", geminiCLIUserAgent(model))
	response, err := transport.base.RoundTrip(outbound)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || response.Body == nil {
		return response, nil
	}
	switch action {
	case "streamGenerateContent":
		response.Body = &geminiCLIStreamUnwrapper{source: response.Body, reader: bufio.NewReaderSize(response.Body, 64*1024)}
		response.ContentLength = -1
		response.Header.Del("Content-Length")
	case "generateContent":
		data, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if inner := gjson.GetBytes(data, "response"); inner.IsObject() {
			data = []byte(inner.Raw)
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		response.ContentLength = int64(len(data))
		response.Header.Del("Content-Length")
	}
	return response, nil
}

// geminiCLIModelAction 解析 CPA 生成的 /v1beta/models/{model}:{action} 路径。
func geminiCLIModelAction(path string) (string, string, error) {
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", errors.New("Gemini CLI request path is unsupported")
	}
	rest := path[len(prefix):]
	separator := strings.LastIndex(rest, ":")
	if separator <= 0 || strings.Contains(rest[:separator], "/") {
		return "", "", errors.New("Gemini CLI request path is unsupported")
	}
	model, action := rest[:separator], rest[separator+1:]
	switch action {
	case "generateContent", "streamGenerateContent", "countTokens":
		return model, action, nil
	default:
		return "", "", fmt.Errorf("Gemini CLI action %q is unsupported", action)
	}
}

// wrapGeminiCLIRequest 生成与 gemini-cli 一致的 Code Assist 请求体。
func wrapGeminiCLIRequest(action, model, projectID string, body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, errors.New("Gemini CLI request payload is invalid")
	}
	if action == "countTokens" {
		contents := gjson.GetBytes(body, "contents")
		raw := "[]"
		if contents.IsArray() {
			raw = contents.Raw
		}
		out, err := sjson.SetBytes([]byte(`{}`), "request.model", "models/"+model)
		if err != nil {
			return nil, err
		}
		return sjson.SetRawBytes(out, "request.contents", []byte(raw))
	}
	inner, err := sjson.DeleteBytes(body, "model")
	if err != nil {
		return nil, err
	}
	out := []byte(`{}`)
	for _, field := range []struct{ path, value string }{
		{"model", model}, {"project", projectID}, {"user_prompt_id", uuid.NewString()},
	} {
		if out, err = sjson.SetBytes(out, field.path, field.value); err != nil {
			return nil, err
		}
	}
	return sjson.SetRawBytes(out, "request", inner)
}

// geminiCLIStreamUnwrapper 把 Code Assist 的 `data: {"response": {...}}` 逐行还原为
// Gemini API 的 `data: {...}`；只含元数据的事件丢弃，错误事件原样保留。
type geminiCLIStreamUnwrapper struct {
	source  io.ReadCloser
	reader  *bufio.Reader
	pending []byte
	err     error
}

func (stream *geminiCLIStreamUnwrapper) Read(buffer []byte) (int, error) {
	for len(stream.pending) == 0 {
		if stream.err != nil {
			return 0, stream.err
		}
		line, err := stream.reader.ReadBytes('\n')
		if len(line) > 0 {
			stream.pending = unwrapGeminiCLISSELine(line)
		}
		if err != nil {
			stream.err = err
		}
	}
	count := copy(buffer, stream.pending)
	stream.pending = stream.pending[count:]
	return count, nil
}

func (stream *geminiCLIStreamUnwrapper) Close() error {
	return stream.source.Close()
}

func unwrapGeminiCLISSELine(line []byte) []byte {
	trimmed := bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return line
	}
	payload := bytes.TrimSpace(trimmed[len("data:"):])
	if len(payload) == 0 || payload[0] != '{' {
		return line
	}
	inner := gjson.GetBytes(payload, "response")
	if !inner.IsObject() {
		if gjson.GetBytes(payload, "error").Exists() {
			return line
		}
		return nil
	}
	out := make([]byte, 0, len(inner.Raw)+8)
	out = append(out, "data: "...)
	out = append(out, inner.Raw...)
	return append(out, '\n')
}

func normalizeGeminiCLIExecutionError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var statusError interface {
		error
		StatusCode() int
	}
	if !errors.As(err, &statusError) || statusError == nil || statusError.StatusCode() == 0 {
		return err
	}
	status := statusError.StatusCode()
	retryAfter := time.Duration(0)
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			retryAfter = *value
		}
	}
	raw := []byte(statusError.Error())
	if retryAfter <= 0 {
		retryAfter = geminiCLIRetryDelay(raw)
	}
	typeValue, codeValue := antigravityExecutionErrorFields(raw)
	return &GeminiCLIExecutionError{
		status: status, typeValue: typeValue, codeValue: codeValue, retryAfter: retryAfter,
		requestScoped: status >= http.StatusBadRequest && status < http.StatusInternalServerError &&
			status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusTooManyRequests,
		summary: geminiCLIExecutionSummary(status, typeValue),
	}
}

// geminiCLIRetryDelay 读取 google.rpc.RetryInfo 的 retryDelay，上限一天。
func geminiCLIRetryDelay(raw []byte) time.Duration {
	for _, detail := range gjson.GetBytes(raw, "error.details").Array() {
		value := strings.TrimSpace(detail.Get("retryDelay").String())
		if value == "" || len(value) > 32 {
			continue
		}
		delay, err := time.ParseDuration(value)
		if err != nil || delay <= 0 {
			continue
		}
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		return delay
	}
	return 0
}

func geminiCLIExecutionSummary(status int, typeValue string) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Gemini CLI authorization was rejected"
	case status == http.StatusForbidden:
		return "Gemini CLI access was denied"
	case status == http.StatusTooManyRequests:
		return "Gemini CLI upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Gemini CLI upstream service failed"
	case typeValue != "":
		return fmt.Sprintf("Gemini CLI upstream request failed: %s", typeValue)
	default:
		return fmt.Sprintf("Gemini CLI upstream request failed with status %d", status)
	}
}

var _ GeminiCLIHTTPExecutor = (*geminiCLIHTTPExecutor)(nil)
var _ cliproxyexecutor.RequestScopedError = (*GeminiCLIExecutionError)(nil)
var _ cliproxyexecutor.StatusError = (*GeminiCLIExecutionError)(nil)

// GeminiCLIModelSupportsWebSearch uses the same capability check as the request translator.
func GeminiCLIModelSupportsWebSearch(model string) bool {
	return geminiresponses.ModelSupportsWebSearch(model)
}
