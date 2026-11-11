package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/geminiimage"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/geminicli"
)

type geminiCLIProviderCredential struct{ value geminicli.Credential }

func (credential geminiCLIProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type geminiCLIProviderBridge struct {
	executor         geminicli.Executor
	compactionCipher compactionCipher
}

func newGeminiCLIProviderBridge() *geminiCLIProviderBridge {
	return &geminiCLIProviderBridge{executor: geminicli.NewExecutor()}
}

func (*geminiCLIProviderBridge) ProviderKind() channel.ProviderKind { return channel.ProviderGeminiCLI }

func (*geminiCLIProviderBridge) UpstreamProtocol() protocol.Protocol { return protocol.Gemini }

func (*geminiCLIProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	valid := false
	switch route.ClientProtocol {
	case protocol.Gemini:
		valid = (route.Operation == execution.OperationChatCompletion || route.Operation == execution.OperationCountTokens) &&
			route.RouteMode == execution.RouteNative
	case protocol.Anthropic:
		valid = (route.Operation == execution.OperationChatCompletion || route.Operation == execution.OperationCountTokens) &&
			route.RouteMode == execution.RouteConverted
	case protocol.OpenAICompletions:
		valid = route.Operation == execution.OperationChatCompletion && route.RouteMode == execution.RouteConverted
	case protocol.OpenAIImages:
		valid = (route.Operation == execution.OperationImagesGenerate || route.Operation == execution.OperationImagesEdit) && route.RouteMode == execution.RouteConverted
	case protocol.OpenAIResponses:
		valid = (route.Operation == execution.OperationResponsesCreate || route.Operation == execution.OperationResponsesInputTokens || route.Operation == execution.OperationResponsesCompact) &&
			route.RouteMode == execution.RouteConverted
	}
	if !valid {
		return fmt.Errorf("route is not implemented by Gemini CLI")
	}
	return nil
}

func (*geminiCLIProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := geminicli.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return geminiCLIProviderCredential{value: credential}, nil
}

// ValidateRequest 校验图片、压缩及工具转换，避免静默丢弃不支持的参数。
func (bridge *geminiCLIProviderBridge) ValidateRequest(request providerRequest) error {
	format := strings.TrimSpace(request.Format)
	if format == "openai-image" {
		if request.RequestPath == "/v1/images/edits" {
			_, err := geminiimage.ConvertEditRequest(request.Payload, request.Headers.Get("Content-Type"))
			return err
		}
		if request.RequestPath != "/v1/images/generations" {
			return errors.New("unsupported Gemini CLI Images endpoint")
		}
		return geminiimage.ValidateRequest(request.Payload)
	}
	var payload any
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return errors.New("Gemini CLI request payload is invalid")
	}
	var err error
	request, err = bridge.expandCompaction(request)
	if err != nil {
		return err
	}
	if request.RequestPath == "/v1/responses/compact" {
		return bridge.validateCompact(request)
	}
	if format == "openai-response" {
		if root, ok := payload.(map[string]any); ok {
			tools, _ := root["tools"].([]any)
			for _, rawTool := range tools {
				tool, _ := rawTool.(map[string]any)
				toolType, _ := tool["type"].(string)
				switch strings.TrimSpace(toolType) {
				case "function":
				case "web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11":
					if err := validateGeminiCLIWebSearch(tool, request.Model); err != nil {
						return err
					}
				default:
					return errors.New("Gemini CLI supports only function and web_search Responses tools")
				}
			}
		}
	}
	if antigravityPayloadContainsRemoteImage(payload, format) {
		return errors.New("Gemini CLI does not support remote image URLs")
	}
	return nil
}

func (bridge *geminiCLIProviderBridge) executeRequest(request providerRequest) geminicli.ExecuteRequest {
	return geminicli.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...),
		Format: request.Format, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		BaseURL:         request.BaseURL, ProxyURL: request.ProxyURL,
	}
}

func (bridge *geminiCLIProviderBridge) credential(credential providerCredential) (geminicli.Credential, error) {
	value, ok := credential.(geminiCLIProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return geminicli.Credential{}, errors.New("Gemini CLI provider bridge credential mismatch")
	}
	return value.value, nil
}

func (bridge *geminiCLIProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, err := bridge.credential(credential)
	if err != nil {
		return providerResponse{}, err
	}
	request, err = bridge.expandCompaction(request)
	if err != nil {
		return providerResponse{}, err
	}
	if request.RequestPath == "/v1/responses/compact" {
		return bridge.compact(ctx, credentialID, value, request)
	}
	images := strings.TrimSpace(request.Format) == "openai-image"
	if images {
		var payload []byte
		if request.RequestPath == "/v1/images/edits" {
			payload, err = geminiimage.ConvertEditRequest(request.Payload, request.Headers.Get("Content-Type"))
		} else {
			payload, err = geminiimage.ConvertRequest(request.Payload)
		}
		if err != nil {
			return providerResponse{}, err
		}
		request.Format, request.Payload, request.OriginalRequest = "gemini", payload, bytes.Clone(payload)
		request.Headers = request.Headers.Clone()
		if request.Headers == nil {
			request.Headers = make(http.Header)
		}
		request.Headers.Set("Content-Type", "application/json")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value, bridge.executeRequest(request))
	var imageUsage *execution.UsageEvidence
	if err == nil && images {
		response.Payload, imageUsage, err = geminiimage.ConvertResponse(response.Payload)
	}
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens, Usage: imageUsage,
	}, err
}

func (bridge *geminiCLIProviderBridge) CountTokens(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, err := bridge.credential(credential)
	if err != nil {
		return providerResponse{}, err
	}
	request, err = bridge.expandCompaction(request)
	if err != nil {
		return providerResponse{}, err
	}
	response, err := bridge.executor.CountTokens(ctx, credentialID, value, bridge.executeRequest(request))
	return providerResponse{Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone()}, err
}

func (bridge *geminiCLIProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, err := bridge.credential(credential)
	if err != nil {
		return nil, err
	}
	request, err = bridge.expandCompaction(request)
	if err != nil {
		return nil, err
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value, bridge.executeRequest(request))
	if response == nil {
		return nil, err
	}
	result := &providerStreamResponse{
		Headers: response.Headers.Clone(), AppliedReasoningEffort: response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}
	if err != nil {
		return result, err
	}
	chunks := make(chan providerStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			select {
			case chunks <- providerStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: chunk.Err}:
			case <-ctx.Done():
				return
			}
		}
	}()
	result.Chunks = chunks
	return result, nil
}

func (*geminiCLIProviderBridge) ClassifyError(
	ctx context.Context,
	err error,
	_ providerCredential,
) (int, *execution.ErrorEvidence) {
	if err == nil {
		return 0, nil
	}
	if errors.Is(err, errGeminiCLICompactionResponse) {
		return http.StatusBadGateway, &execution.ErrorEvidence{
			Kind: execution.ErrorKindProvider, StatusCode: http.StatusBadGateway,
			Hint: execution.FailureHintRequestRejected, OriginHint: execution.ErrorOriginUpstream,
			ScopeHint: execution.ErrorScopeRequest, Code: "invalid_compaction_response",
			Summary: errGeminiCLICompactionResponse.Error(), ReplaySafety: execution.ReplaySafetyUnknown,
		}
	}
	if errors.Is(err, geminiimage.ErrInvalidResponse) {
		return http.StatusBadGateway, &execution.ErrorEvidence{
			Kind: execution.ErrorKindProvider, StatusCode: http.StatusBadGateway,
			Hint: execution.FailureHintRequestRejected, OriginHint: execution.ErrorOriginUpstream,
			ScopeHint: execution.ErrorScopeRequest, Code: "invalid_image_response",
			Summary: geminiimage.ErrInvalidResponse.Error(), ReplaySafety: execution.ReplaySafetyUnknown,
		}
	}
	status := 0
	var statusError interface{ StatusCode() int }
	if errors.As(err, &statusError) && statusError != nil {
		status = statusError.StatusCode()
	}
	kind := execution.ErrorKindTransport
	if errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		kind = execution.ErrorKindTimeout
	} else if errors.Is(err, context.Canceled) || ctx != nil && errors.Is(context.Cause(ctx), context.Canceled) {
		kind = execution.ErrorKindCanceled
	} else if status != 0 {
		kind = execution.ErrorKindHTTP
	} else if _, ok := err.(net.Error); ok {
		kind = execution.ErrorKindTransport
	}
	typeValue, codeValue := antigravityErrorTypeCode(err)
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Type: typeValue, Code: codeValue,
		Summary: geminiCLIProviderErrorSummary(status),
	}
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			evidence.RetryAfter = *value
		}
	}
	switch {
	case status == http.StatusUnauthorized:
		evidence.Hint = execution.FailureHintRefreshRequired
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case status == http.StatusForbidden:
		evidence.Hint = execution.FailureHintCandidateUnavailable
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case requestScopedFailure(err):
		evidence.Hint = execution.FailureHintRequestRejected
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case status == http.StatusForbidden:
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
		evidence.ScopeHint = execution.ErrorScopeCredential
	case status >= http.StatusInternalServerError:
		evidence.Hint = execution.FailureHintHostError
	}
	annotateProviderErrorEvidence(evidence, err)
	return status, evidence
}

func geminiCLIProviderErrorSummary(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Gemini CLI authorization was rejected"
	case status == http.StatusForbidden:
		return "Gemini CLI access was denied"
	case status == http.StatusTooManyRequests:
		return "Gemini CLI upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Gemini CLI upstream service failed"
	case status >= http.StatusBadRequest:
		return "Gemini CLI upstream request was rejected"
	default:
		return "Gemini CLI upstream request failed"
	}
}

var _ providerBridge = (*geminiCLIProviderBridge)(nil)
var _ providerTokenCounter = (*geminiCLIProviderBridge)(nil)
var _ providerRequestValidator = (*geminiCLIProviderBridge)(nil)

// Search options without a Gemini equivalent must not be silently ignored.
func validateGeminiCLIWebSearch(tool map[string]any, model string) error {
	if !geminicli.ModelSupportsWebSearch(model) {
		return errors.New("Gemini CLI model does not support Responses web search conversion")
	}
	for key, value := range tool {
		switch key {
		case "type":
		case "external_web_access":
			if value != true {
				return errors.New("Gemini CLI web search requires live web access")
			}
		case "filters":
			filters, ok := value.(map[string]any)
			if !ok || len(filters) != 1 {
				return errors.New("Gemini CLI web search supports only allowed_domains filters")
			}
			domains, ok := filters["allowed_domains"].([]any)
			if !ok {
				return errors.New("web search allowed_domains must be an array")
			}
			for _, item := range domains {
				domain, ok := item.(string)
				if !ok || strings.TrimSpace(domain) == "" {
					return errors.New("web search domains must be non-empty strings")
				}
			}
		default:
			return errors.New("Gemini CLI cannot preserve this web search option")
		}
	}
	return nil
}
