package bifrost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
)

// commandCodeAttempt 记录 generate 请求翻译前的 OpenAI Chat 形态，供用量与思考配置回溯。
type commandCodeAttempt struct {
	chatBody   []byte
	hasTools   bool
	listModels bool
}

const commandCodeProbeBody = `{"messages":[{"role":"user","content":"ping"}],"max_tokens":32}`

// prepareCommandCode 把任意客户端协议统一成 OpenAI Chat，再翻译为 /alpha/generate 请求。
func (r *Runtime) prepareCommandCode(
	spec execution.AttemptSpec,
	provider schemas.ModelProvider,
	key schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	prepared := preparedAttempt{
		provider: provider, mode: channel.RouteMode(spec.RouteMode),
		upstreamProtocol: protocol.OpenAICompletions, clientProtocol: spec.ClientProtocol,
		directKey: key, secrets: secrets,
	}
	version := commandCodeVersions.Version(context.Background())
	if spec.Operation == execution.OperationListModels {
		prepared.commandCode = &commandCodeAttempt{listModels: true}
		prepared.passthrough = &schemas.BifrostPassthroughRequest{
			Provider: provider, Method: http.MethodGet, Path: commandCodeModelsPath,
			UpstreamURL: r.fixedConfig.targetBaseURL, SafeHeaders: commandCodeHeaders(version, "", false),
		}
		return prepared, nil
	}
	var chatBody []byte
	switch {
	case spec.Operation == execution.OperationProbe:
		chatBody = []byte(commandCodeProbeBody)
	case spec.ClientProtocol == protocol.OpenAICompletions:
		chatBody = spec.Body
	default:
		request, err := buildConvertedResponsesRequest(spec, provider)
		if err != nil {
			var classified interface{ ConversionCode() string }
			if errors.As(err, &classified) {
				failure := notSentConversionFailure(classified.ConversionCode(), err.Error())
				return preparedAttempt{}, &failure
			}
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, err.Error())
			return preparedAttempt{}, &failure
		}
		prepared.responsesRequest = request
		var failure *execution.AttemptResult
		prepared, failure = finishConvertedPreparation(spec, channel.ProviderCommandCode, prepared)
		if failure != nil {
			return preparedAttempt{}, failure
		}
		chatBody, err = commandCodeConvertedChat(prepared.responsesRequest)
		if err != nil {
			failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "cannot preserve Command Code Chat request")
			return preparedAttempt{}, &failure
		}
	}
	plan, err := buildCommandCodeRequest(chatBody, spec.UpstreamModel, time.Now())
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Command Code request")
		return preparedAttempt{}, &failure
	}
	prepared.commandCode = &commandCodeAttempt{chatBody: bytes.Clone(chatBody), hasTools: plan.hasTools}
	prepared.passthrough = &schemas.BifrostPassthroughRequest{
		Provider: provider, Model: spec.UpstreamModel, Method: http.MethodPost, Path: commandCodeGeneratePath,
		UpstreamURL: r.fixedConfig.targetBaseURL, Body: plan.body,
		SafeHeaders: commandCodeHeaders(version, plan.threadID, true),
	}
	return prepared, nil
}

// commandCodeConvertedChat 复用 bifrost 的 Responses → Chat 转换，并把思考预算折算为档位。
func commandCodeConvertedChat(request *schemas.BifrostResponsesRequest) ([]byte, error) {
	chat := request.ToChatRequest()
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	ctx.SetValue(schemas.BifrostContextKeyIsCustomProvider, true)
	wire := openai.ToOpenAIChatRequest(ctx, chat)
	if wire == nil {
		return nil, fmt.Errorf("missing Chat request")
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	object, err := decodeNativeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if request.Params != nil && request.Params.Reasoning != nil {
		effort := ""
		if request.Params.Reasoning.Effort != nil {
			effort = *request.Params.Reasoning.Effort
		} else if request.Params.Reasoning.MaxTokens != nil {
			effort = commandCodeEffortForBudget(*request.Params.Reasoning.MaxTokens)
		}
		if effort != "" {
			object["reasoning_effort"], err = json.Marshal(effort)
			if err != nil {
				return nil, err
			}
		}
	}
	return encodeNativeJSONObject(object)
}

func commandCodeEffortForBudget(budget int) string {
	switch {
	case budget <= 0:
		return ""
	case budget <= 2000:
		return "low"
	case budget <= 8000:
		return "medium"
	case budget <= 16000:
		return "high"
	case budget <= 32000:
		return "xhigh"
	default:
		return "max"
	}
}

// commandCodeWireSpec 只保留发往上游所需的执行参数：客户端请求头不透传，避免上游据此判定为代理。
func commandCodeWireSpec(spec execution.AttemptSpec) execution.AttemptSpec {
	wire := spec
	wire.Header = make(http.Header)
	wire.ClientProtocol = protocol.OpenAICompletions
	wire.ClientModel = spec.UpstreamModel
	if spec.Operation != execution.OperationListModels {
		wire.Operation = execution.OperationChatCompletion
	}
	return wire
}

func commandCodeCompletionID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "chatcmpl-commandcode"
	}
	return "chatcmpl-" + hex.EncodeToString(value[:])
}

func (r *Runtime) executeCommandCode(parent context.Context, spec execution.AttemptSpec, prepared preparedAttempt) execution.AttemptResult {
	if prepared.commandCode.listModels {
		return r.executeCommandCodeListModels(parent, spec, prepared)
	}
	result := r.executePassthrough(parent, commandCodeWireSpec(spec), prepared)
	if prepared.mode == channel.RouteConverted {
		result.AppliedReasoning = inspectWireAppliedReasoning(prepared.commandCode.chatBody)
	}
	result.Usage = nil
	if !result.ResponseStarted {
		return result
	}
	body, ok := decodeCommandCodeBody(result.Header, result.Body, r.unaryResponseBodyLimit(spec))
	if !ok {
		return invalidCommandCodeResponse(result.Header)
	}
	result.Header = commandCodeJSONHeaders(result.Header)
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		result.Body = redactSecrets(normalizeCommandCodeError(body), prepared.secrets)
		result.Error = passthroughHTTPError(result.StatusCode, result.Header, result.Body, prepared.secrets)
		return result
	}
	translator := newCommandCodeTranslator(commandCodeCompletionID(), spec.UpstreamModel, time.Now().Unix(), prepared.commandCode.hasTools)
	events := 0
	for _, line := range bytes.Split(body, []byte("\n")) {
		event, ok, err := parseCommandCodeLine(line)
		if err != nil {
			return invalidCommandCodeResponse(result.Header)
		}
		if !ok {
			continue
		}
		events++
		if _, err := translator.translate(event); err != nil {
			return invalidCommandCodeResponse(result.Header)
		}
	}
	if translator.failure != "" {
		return commandCodeGenerationFailure(result.Header, translator.failure, prepared.secrets)
	}
	if events == 0 {
		return invalidCommandCodeResponse(result.Header)
	}
	translator.finishMissing()
	completion, err := translator.completion()
	if err != nil {
		return invalidCommandCodeResponse(result.Header)
	}
	result.Model = spec.UpstreamModel
	result.Usage, err = usageEvidenceFromPassthrough(openai.ExtractOpenAIPassthroughUsage(
		http.MethodPost, openAIChatPath, prepared.commandCode.chatBody, completion,
	))
	if err != nil {
		return invalidCommandCodeResponse(result.Header)
	}
	result.Body = completion
	if prepared.mode == channel.RouteConverted && spec.Operation != execution.OperationProbe {
		var chat schemas.BifrostChatResponse
		if err := json.Unmarshal(completion, &chat); err != nil {
			return invalidCommandCodeResponse(result.Header)
		}
		ctx := schemas.NewBifrostContext(parent, schemas.NoDeadline)
		defer ctx.Cancel()
		response := chat.ToBifrostResponsesResponse()
		response.Store = schemas.Ptr(false)
		result.Body, _, err = encodeConvertedResponsesResponse(spec.ClientProtocol, ctx, response)
		if err != nil {
			return invalidCommandCodeResponse(result.Header)
		}
	}
	if needsClientModelAlias(spec) {
		result.Body, err = rewriteClientResponseModel(spec.ClientProtocol, result.Body, spec.ClientModel)
		if err != nil {
			return invalidCommandCodeResponse(result.Header)
		}
	}
	return result
}

func decodeCommandCodeBody(headers http.Header, body []byte, limit int64) ([]byte, bool) {
	encoding, err := contentcoding.ParseContentEncoding(headers.Values("Content-Encoding"))
	if err != nil {
		return nil, false
	}
	decoded, err := contentcoding.DecodeLimited(encoding, body, limit)
	return decoded, err == nil
}

func commandCodeJSONHeaders(source http.Header) http.Header {
	headers := source.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	httpheader.StripRepresentationMetadata(headers)
	headers.Del("Content-Encoding")
	headers.Set("Content-Type", "application/json")
	return headers
}

// normalizeCommandCodeError 把上游错误体统一为 {"error":{...}}，纯文本错误也保留原文。
func normalizeCommandCodeError(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	var object map[string]json.RawMessage
	if json.Unmarshal(trimmed, &object) == nil && object != nil {
		var detail map[string]json.RawMessage
		if json.Unmarshal(object["error"], &detail) == nil && detail != nil {
			if encoded, err := json.Marshal(map[string]json.RawMessage{"error": object["error"]}); err == nil {
				return encoded
			}
		}
		for _, key := range []string{"error", "message", "detail"} {
			var message string
			if json.Unmarshal(object[key], &message) == nil && strings.TrimSpace(message) != "" {
				return commandCodeErrorBody(message, "upstream_error")
			}
		}
	}
	message := strings.TrimSpace(strings.ToValidUTF8(string(trimmed), ""))
	if len(message) > 1024 {
		message = strings.ToValidUTF8(message[:1024], "")
	}
	if message == "" {
		message = "Command Code request failed."
	}
	return commandCodeErrorBody(message, "upstream_error")
}

func commandCodeErrorBody(message, kind string) []byte {
	body, err := json.Marshal(map[string]map[string]string{"error": {"type": kind, "message": message}})
	if err != nil {
		return []byte(`{"error":{"type":"upstream_error","message":"Command Code request failed."}}`)
	}
	return body
}

// commandCodeGenerationFailure 表示上游已接受请求但在事件流中报告生成失败。
func commandCodeGenerationFailure(headers http.Header, message string, secrets []string) execution.AttemptResult {
	body := redactSecrets(commandCodeErrorBody(message, "upstream_error"), secrets)
	result := execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		StatusCode: http.StatusBadGateway, Header: commandCodeJSONHeaders(headers), Body: body,
	}
	result.Error = passthroughHTTPError(http.StatusBadGateway, result.Header, body, secrets)
	result.Error.Kind = execution.ErrorKindProvider
	result.Error.OriginHint = execution.ErrorOriginUpstream
	return result
}

func invalidCommandCodeResponse(headers http.Header) execution.AttemptResult {
	result := startedUnaryFailure(http.StatusBadGateway, headers, execution.ErrorKindProvider, "Command Code returned an invalid generate response")
	result.Error.Code = "invalid_upstream_response"
	result.Error.OriginHint = execution.ErrorOriginUpstream
	result.Error.ScopeHint = execution.ErrorScopeGroup
	return result
}

// executeCommandCodeListModels 读取上游模型目录；目录接口可能只对 Provider 档位开放，
// 失败时退回内置目录，保证标准订阅也能完成模型发现。
func (r *Runtime) executeCommandCodeListModels(parent context.Context, spec execution.AttemptSpec, prepared preparedAttempt) execution.AttemptResult {
	result := r.executePassthrough(parent, commandCodeWireSpec(spec), prepared)
	var upstream []commandCodeUpstreamModel
	if result.ResponseStarted && result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		if body, ok := decodeCommandCodeBody(result.Header, result.Body, r.unaryResponseBodyLimit(spec)); ok {
			var page struct {
				Data []commandCodeUpstreamModel `json:"data"`
			}
			if json.Unmarshal(body, &page) == nil {
				upstream = page.Data
			}
		}
	}
	if result.Error != nil && result.Error.Kind == execution.ErrorKindCanceled {
		return result
	}
	body, err := encodeCommandCodeModelList(prepared.clientProtocol, prepared.provider, commandCodeModelList(upstream))
	if err != nil {
		return startedUnaryFailure(http.StatusOK, commandCodeJSONHeaders(nil), execution.ErrorKindInternal, "encode model list")
	}
	headers := commandCodeJSONHeaders(nil)
	if result.ResponseStarted {
		headers = commandCodeJSONHeaders(result.Header)
	}
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		StatusCode: http.StatusOK, Header: headers, Body: body, UpstreamRequestID: result.UpstreamRequestID,
	}
}

// encodeCommandCodeModelList 保留带组织前缀的完整模型 ID：bifrost 的 Anthropic 与 Gemini
// 编码器会把首段当作提供方剥离（如 deepseek/、xai/），导致发现的模型名无法回查。
func encodeCommandCodeModelList(clientProtocol protocol.Protocol, provider schemas.ModelProvider, list *schemas.BifrostListModelsResponse) ([]byte, error) {
	switch clientProtocol {
	case protocol.Anthropic:
		type anthropicModel struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		}
		data := make([]anthropicModel, 0, len(list.Data))
		for _, model := range list.Data {
			name := model.ID
			if model.Name != nil {
				name = *model.Name
			}
			data = append(data, anthropicModel{ID: model.ID, Type: "model", DisplayName: name, CreatedAt: "1970-01-01T00:00:00Z"})
		}
		response := map[string]any{"data": data, "has_more": false}
		if len(data) > 0 {
			response["first_id"], response["last_id"] = data[0].ID, data[len(data)-1].ID
		}
		return json.Marshal(response)
	case protocol.Gemini:
		type geminiModel struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName,omitempty"`
			InputTokenLimit            int      `json:"inputTokenLimit,omitempty"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		}
		models := make([]geminiModel, 0, len(list.Data))
		for _, model := range list.Data {
			entry := geminiModel{Name: "models/" + model.ID, SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}}
			if model.Name != nil {
				entry.DisplayName = *model.Name
			}
			if model.ContextLength != nil {
				entry.InputTokenLimit = *model.ContextLength
			}
			models = append(models, entry)
		}
		return json.Marshal(map[string]any{"models": models})
	default:
		return encodeListModelsResponse(clientProtocol, provider, list)
	}
}

type commandCodeUpstreamModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
}

// commandCodeModelList 合并内置目录与上游目录：内置顺序在前保持默认模型稳定，
// 上游提供的名称与上下文长度优先；闭源模型不经 /alpha/generate 提供，予以排除。
func commandCodeModelList(upstream []commandCodeUpstreamModel) *schemas.BifrostListModelsResponse {
	byID := make(map[string]commandCodeUpstreamModel, len(upstream))
	for _, model := range upstream {
		if model.ID != "" && !commandCodeClosedModel(model.ID) {
			if _, exists := byID[model.ID]; !exists {
				byID[model.ID] = model
			}
		}
	}
	models := make([]schemas.Model, 0, len(commandCodeModels.Builtin)+len(byID))
	seen := make(map[string]bool)
	add := func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		name := commandCodeModels.ModelNames[id]
		contextLength := commandCodeModels.ContextWindows[id]
		if model, ok := byID[id]; ok {
			if model.Name != "" {
				name = model.Name
			}
			if model.ContextLength > 0 {
				contextLength = model.ContextLength
			}
		}
		entry := schemas.Model{ID: id}
		if name != "" {
			entry.Name = schemas.Ptr(name)
		}
		if contextLength > 0 {
			entry.ContextLength = schemas.Ptr(contextLength)
		}
		models = append(models, entry)
	}
	for _, id := range commandCodeModels.Builtin {
		add(id)
	}
	for _, model := range upstream {
		if _, ok := byID[model.ID]; ok {
			add(model.ID)
		}
	}
	return &schemas.BifrostListModelsResponse{Data: models}
}

// executeCommandCodeStream 自行驱动透传流：上游是逐行 JSON 而非 SSE，不能套用原生 SSE
// 的首事件校验与别名改写。事件翻译成 OpenAI Chat SSE 后交给与 Cline 共用的转换器。
func (r *Runtime) executeCommandCodeStream(parent context.Context, spec execution.AttemptSpec, prepared preparedAttempt, sink execution.StreamSink) execution.StreamResult {
	if prepared.commandCode.listModels {
		return streamFromAttemptFailure(notSentUnaryFailure(execution.ErrorKindInvalidRequest, "model list does not stream"))
	}
	ctx := schemas.NewBifrostContext(parent, schemas.NoDeadline)
	defer ctx.Cancel()
	stream := &clineStream{
		ctx: ctx, spec: spec, sink: sink, secrets: prepared.secrets,
		converted: prepared.mode == channel.RouteConverted,
	}
	if stream.converted {
		stream.encoder = newConvertedResponsesStreamEncoder(spec.ClientProtocol)
		stream.chatState = schemas.AcquireChatToResponsesStreamState()
		defer schemas.ReleaseChatToResponsesStreamState(stream.chatState)
	}
	result := r.runCommandCodeStream(parent, spec, prepared, func(event execution.StreamEvent) error {
		err := stream.push(event)
		if err != nil && stream.sinkError == nil {
			stream.protocolError = err
		}
		return err
	})
	if result.Error == nil || result.Error.Kind == execution.ErrorKindHTTP {
		if err := stream.finish(); err != nil && stream.sinkError == nil {
			stream.protocolError = err
		}
	}
	if stream.sinkError != nil {
		result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "stream consumer stopped"}
	} else if stream.protocolError != nil {
		result.Error = invalidCommandCodeResponse(result.Header).Error
	} else if stream.upstreamError != nil {
		result.Error = stream.upstreamError
	}
	if stream.converted {
		result.AppliedReasoning = inspectWireAppliedReasoning(prepared.commandCode.chatBody)
	}
	if stream.headers != nil {
		result.Header = stream.headers
	}
	return result
}

// runCommandCodeStream 与 executeNativeStream 共用超时、空闲、取消与错误分类，只替换数据处理：
// 上游逐行 JSON 事件在这里翻译为 OpenAI Chat SSE 后再交给 sink。
func (r *Runtime) runCommandCodeStream(
	parent context.Context,
	clientSpec execution.AttemptSpec,
	prepared preparedAttempt,
	sink execution.StreamSink,
) execution.StreamResult {
	spec := commandCodeWireSpec(clientSpec)
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	bifrostContext := r.newStreamingSDKContext(callContext, spec, prepared.directKey)
	cancelCall := func() {
		callCancel()
		bifrostContext.Cancel()
	}
	defer cancelCall()
	preResponse := startPreResponseGate(cancelCall, spec.Timeouts)
	defer preResponse.stop()

	outcomeChannel := make(chan passthroughStreamSDKResult, 1)
	go func() {
		stream, bifrostError := r.core.PassthroughStream(bifrostContext, prepared.provider, prepared.passthrough)
		outcomeChannel <- passthroughStreamSDKResult{stream: stream, err: bifrostError}
	}()

	var outcome passthroughStreamSDKResult
	select {
	case outcome = <-outcomeChannel:
		if preResponse.expired() || requestContext.Err() != nil {
			return streamContextFailure(requestContext, preResponse.expired(), false, nil, "", "", nil)
		}
	case <-callContext.Done():
		return streamContextFailure(requestContext, preResponse.expired(), false, nil, "", "", nil)
	}
	if outcome.err != nil {
		return streamErrorResult(outcome.err, bifrostContext, prepared.secrets, false, 0, nil, "", nil)
	}
	if outcome.stream == nil {
		return attemptedStreamFailure(execution.ErrorKindInternal, "execution runtime returned no stream")
	}

	sequence := uint64(0)
	responseObserved := false
	started := false
	status := 0
	var headers http.Header
	requestID := ""
	model := spec.UpstreamModel
	var usageEvidence *execution.UsageEvidence
	var errorBody bytes.Buffer
	var pending []byte
	events := 0
	translator := newCommandCodeTranslator(commandCodeCompletionID(), clientSpec.ClientModel, time.Now().Unix(), prepared.commandCode.hasTools)
	failureSent := false
	firstData := execution.NewFirstResponseSSEObserver(parent)
	idleTimer := newIdleTimer(spec.Timeouts.StreamIdle)
	defer idleTimer.stop()
	emitReady := func() error {
		if started {
			return nil
		}
		preResponse.stop()
		started = true
		sequence = 1
		readyHeaders := headers.Clone()
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			httpheader.StripRepresentationMetadata(readyHeaders)
			readyHeaders.Del("Content-Encoding")
			readyHeaders.Set("Content-Type", "text/event-stream")
		}
		return sink(execution.StreamEvent{
			Sequence: sequence, Kind: execution.StreamEventReady, StatusCode: status,
			Header: readyHeaders, UpstreamRequestID: requestID,
		})
	}
	emitData := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if err := emitReady(); err != nil {
			return err
		}
		firstData(data)
		sequence++
		return sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventData, Data: data})
	}
	emitChunks := func(chunks []map[string]any) error {
		for _, chunk := range chunks {
			if usage, ok := chunk["usage"]; ok {
				usageBody, err := json.Marshal(map[string]any{"usage": usage})
				if err == nil {
					if evidence, err := usageEvidenceFromPassthrough(openai.ExtractOpenAIPassthroughUsage(
						http.MethodPost, openAIChatPath, prepared.commandCode.chatBody, usageBody,
					)); err == nil && evidence != nil {
						usageEvidence = cloneUsage(evidence)
					}
				}
			}
		}
		data, err := marshalCommandCodeSSE(chunks)
		if err != nil {
			return err
		}
		return emitData(data)
	}
	// handleLine 返回 false 表示上游违反协议，调用方据此终止流。
	handleLine := func(line []byte) (bool, error) {
		event, ok, err := parseCommandCodeLine(line)
		if err != nil {
			return false, nil
		}
		if !ok {
			return true, nil
		}
		events++
		chunks, err := translator.translate(event)
		if err != nil {
			return false, nil
		}
		if err := emitChunks(chunks); err != nil {
			return true, err
		}
		if translator.failure != "" && !failureSent {
			failureSent = true
			return true, emitData(frameSSE(redactSecrets(commandCodeErrorBody(translator.failure, "upstream_error"), prepared.secrets)))
		}
		return true, nil
	}

	for {
		select {
		case <-callContext.Done():
			return nativeStreamContextFailure(requestContext, preResponse.expired(), started, status, headers, requestID, model, usageEvidence)
		case <-requestContext.Done():
			cancelCall()
			return nativeStreamContextFailure(requestContext, false, started, status, headers, requestID, model, usageEvidence)
		case <-idleTimer.channel():
			cancelCall()
			return nativeStreamContextFailure(requestContext, true, started, status, headers, requestID, model, usageEvidence)
		case chunk, open := <-outcome.stream:
			idleTimer.pause()
			if callContext.Err() != nil || requestContext.Err() != nil || preResponse.expired() {
				return nativeStreamContextFailure(requestContext, preResponse.expired(), started, status, headers, requestID, model, usageEvidence)
			}
			if !open {
				if !responseObserved {
					return attemptedStreamFailure(execution.ErrorKindInternal, "execution runtime returned an empty stream")
				}
				if status < http.StatusOK || status >= http.StatusMultipleChoices {
					body := redactSecrets(normalizeCommandCodeError(errorBody.Bytes()), prepared.secrets)
					if err := emitData(body); err != nil {
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
					return finishNativeStream(status, headers, requestID, model, usageEvidence, body, prepared.secrets)
				}
				if len(bytes.TrimSpace(pending)) > 0 {
					valid, err := handleLine(pending)
					if err != nil {
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
					if !valid {
						return commandCodeStreamProtocolFailure(started, status, headers, requestID, model, usageEvidence)
					}
				}
				if events == 0 {
					return commandCodeStreamProtocolFailure(started, status, headers, requestID, model, usageEvidence)
				}
				if translator.failure == "" {
					if err := emitChunks(translator.finishMissing()); err != nil {
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
				}
				if err := emitData([]byte("data: [DONE]\n\n")); err != nil {
					return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
				}
				if usageEvidence != nil {
					sequence++
					if err := sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventUsage, Usage: cloneUsage(usageEvidence)}); err != nil {
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
				}
				return finishNativeStream(status, headers, requestID, model, usageEvidence, nil, prepared.secrets)
			}
			if chunk == nil {
				cancelCall()
				return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			if chunk.BifrostError != nil {
				cancelCall()
				return streamErrorResult(chunk.BifrostError, bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			response := chunk.BifrostPassthroughResponse
			if response == nil || !validUpstreamStatus(response.StatusCode) {
				cancelCall()
				return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			if !responseObserved {
				responseObserved = true
				status = response.StatusCode
				headers = responseHeaders(response.Headers, bifrostContext, true)
				requestID = upstreamRequestID(headers)
				if status >= http.StatusOK && status < http.StatusMultipleChoices &&
					strings.TrimSpace(headers.Get("Content-Encoding")) != "" &&
					!strings.EqualFold(strings.TrimSpace(headers.Get("Content-Encoding")), "identity") {
					cancelCall()
					return attemptedStreamFailure(execution.ErrorKindInternal, "encoded upstream stream cannot be translated")
				}
				if status < http.StatusOK || status >= http.StatusMultipleChoices {
					if err := emitReady(); err != nil {
						callCancel()
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
				}
			} else if response.StatusCode != status {
				cancelCall()
				return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			if len(response.Body) > 0 {
				if status < http.StatusOK || status >= http.StatusMultipleChoices {
					appendBounded(&errorBody, response.Body, maxStreamErrorEvidenceBytes)
				} else {
					pending = append(pending, response.Body...)
					for {
						newline := bytes.IndexByte(pending, '\n')
						if newline < 0 {
							break
						}
						line := pending[:newline]
						pending = pending[newline+1:]
						valid, err := handleLine(line)
						if err != nil {
							cancelCall()
							return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
						}
						if !valid {
							cancelCall()
							return commandCodeStreamProtocolFailure(started, status, headers, requestID, model, usageEvidence)
						}
					}
					pending = bytes.Clone(pending)
					if len(pending) > execution.DefaultSSEEventLimitBytes {
						cancelCall()
						return commandCodeStreamProtocolFailure(started, status, headers, requestID, model, usageEvidence)
					}
				}
			}
			idleTimer.resume()
		}
	}
}

func commandCodeStreamProtocolFailure(
	started bool,
	status int,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	if !started {
		return attemptedStreamFailure(execution.ErrorKindInternal, "invalid Command Code event stream")
	}
	return nativeStreamProtocolFailure(status, headers, requestID, model, usageEvidence)
}
