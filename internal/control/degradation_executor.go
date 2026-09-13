package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/degradation"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

const (
	// degradationChatPath is the one request shape every channel module declares.
	// Each module registers an OpenAI-completions chat route, so a single body
	// and a single response parser cover every provider: native routes pass the
	// body through, converted routes are translated by the execution layer in
	// both directions.
	degradationChatPath = "/v1/chat/completions"
	// degradationConvertedMaxTokens caps converted routes only. Providers that
	// require an explicit output budget (Anthropic) otherwise fall back to a
	// default that a thinking model can spend entirely on reasoning, which would
	// truncate the answer. Native passthrough omits the field so no upstream can
	// reject a parameter it does not know.
	degradationConvertedMaxTokens = 16384
	degradationSummaryLimit       = 480
)

// Stable degradation error codes. They are persisted on the monitor and on the
// run history, so the UI can group failures without parsing upstream prose.
const (
	degradationErrorRouteUnsupported  = "route_unsupported"
	degradationErrorQuotaExhausted    = "quota_exhausted"
	degradationErrorInvalidCredential = "invalid_credential"
	degradationErrorRateLimited       = "rate_limited"
	degradationErrorOverloaded        = "upstream_overloaded"
	degradationErrorTimeout           = "upstream_timeout"
	degradationErrorModelUnavailable  = "model_unavailable"
	degradationErrorInvalidRequest    = "invalid_request"
	degradationErrorEmptyResponse     = "empty_response"
	degradationErrorUpstream          = "upstream_error"
	degradationErrorInternal          = "internal_error"
)

// degradationProbeRequest is one detection call plan.
type degradationProbeRequest struct {
	upstreamModel  string
	effort         models.DegradationReasoningEffort
	challenges     []degradation.Challenge
	retryLimit     int
	requestTimeout time.Duration
}

// degradationProbeResult carries whatever the detection managed to collect.
// A partial result is still usable: the analyzer scores the samples it has.
type degradationProbeResult struct {
	samples      []degradation.Sample
	attempts     int
	errorCode    string
	errorSummary string
}

// executeDegradationProbe issues one chat completion per challenge and returns
// the raw answers. Transport and upstream failures are reported through
// errorCode instead of an error value; only context cancellation and internal
// faults return an error.
func (s *Service) executeDegradationProbe(
	ctx context.Context,
	target degradationTarget,
	request degradationProbeRequest,
) (degradationProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return degradationProbeResult{}, err
	}
	if s == nil || s.executor == nil {
		return degradationProbeResult{}, app_errors.ErrInternalServer
	}
	if target.channelID == "" || target.resolvedTarget.ChannelID != target.channelID {
		return degradationProbeResult{}, app_errors.ErrInternalServer
	}
	if strings.TrimSpace(request.upstreamModel) == "" || len(request.challenges) == 0 {
		return degradationProbeResult{}, app_errors.ErrValidation
	}

	routeMode, supported := target.resolvedTarget.ModeForModel(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		request.upstreamModel,
	)
	if !supported {
		return degradationProbeResult{
			errorCode:    degradationErrorRouteUnsupported,
			errorSummary: "channel does not expose a chat completion route for this model",
		}, nil
	}

	probeCtx := ctx
	if request.requestTimeout > 0 {
		total := request.requestTimeout * time.Duration(len(request.challenges)*(request.retryLimit+1))
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, total)
		defer cancel()
	}
	requestID, err := s.newExecutionID()
	if err != nil {
		return degradationProbeResult{}, fmt.Errorf(
			"create degradation request identity: %w", app_errors.ErrInternalServer,
		)
	}

	result := degradationProbeResult{samples: make([]degradation.Sample, 0, len(request.challenges))}
	var sequence uint32
	for _, challenge := range request.challenges {
		body, buildErr := buildDegradationChatBody(request, challenge, routeMode)
		if buildErr != nil {
			return degradationProbeResult{}, fmt.Errorf(
				"build degradation request body: %w", app_errors.ErrInternalServer,
			)
		}
		text, code, summary, attempts, callErr := s.callDegradationChallenge(
			probeCtx, ctx, target, request, routeMode, requestID, &sequence, body,
		)
		result.attempts += attempts
		if callErr != nil {
			return degradationProbeResult{}, callErr
		}
		if code != "" {
			result.errorCode, result.errorSummary = code, summary
			if degradationFailureIsTerminal(code) {
				return result, nil
			}
			continue
		}
		result.samples = append(result.samples, degradation.Sample{
			Text: text, ExpectedCount: challenge.ExpectedCount,
		})
	}
	if len(result.samples) > 0 {
		result.errorCode, result.errorSummary = "", ""
	}
	return result, nil
}

// callDegradationChallenge runs one challenge with its retries and returns the
// answer text, or the classified failure of the final attempt.
func (s *Service) callDegradationChallenge(
	probeCtx context.Context,
	parentCtx context.Context,
	target degradationTarget,
	request degradationProbeRequest,
	routeMode channel.RouteMode,
	requestID string,
	sequence *uint32,
	body []byte,
) (string, string, string, int, error) {
	attempts := 0
	lastCode, lastSummary := degradationErrorUpstream, ""
	for try := 0; try <= request.retryLimit; try++ {
		if err := parentCtx.Err(); err != nil {
			return "", "", "", attempts, err
		}
		if err := probeCtx.Err(); err != nil {
			return "", degradationErrorTimeout, "degradation detection budget expired", attempts, nil
		}
		attemptID, identityErr := s.newExecutionID()
		if identityErr != nil {
			return "", "", "", attempts, fmt.Errorf(
				"create degradation attempt identity: %w", app_errors.ErrInternalServer,
			)
		}
		*sequence++
		attempts++
		spec := execution.NewAttemptSpec(execution.AttemptSpec{
			RequestID: requestID, AttemptID: attemptID, Sequence: *sequence,
			ChannelID: string(target.channelID),
			RouteMode: execution.RouteMode(routeMode), ClientProtocol: protocol.OpenAICompletions,
			Operation:   execution.OperationChatCompletion,
			ClientModel: request.upstreamModel, UpstreamModel: request.upstreamModel,
			Method: http.MethodPost, Path: degradationChatPath,
			Header:            applyControlHeaderRules(target.headerRules, target.credential.apiKey),
			Body:              body,
			ConfiguredHeaders: target.headerRules.ConfiguredNames(),
			TargetConfig:      target.resolvedTarget.TargetConfig,
			Timeouts:          degradationTimeouts(target, request.requestTimeout),
			Credential:        target.credential.snapshot,
			Proxy:             target.credential.proxy,
			ProxyFingerprint:  target.credential.proxyFingerprint,
		})
		if validationErr := spec.Validate(); validationErr != nil {
			return "", "", "", attempts, fmt.Errorf(
				"build degradation attempt: %w", app_errors.ErrInternalServer,
			)
		}

		text, code, summary := s.runDegradationAttempt(probeCtx, spec, request.requestTimeout)
		if code == "" {
			return text, "", "", attempts, nil
		}
		if err := parentCtx.Err(); err != nil {
			return "", "", "", attempts, err
		}
		lastCode, lastSummary = code, summary
		if degradationFailureIsTerminal(code) {
			break
		}
	}
	return "", lastCode, lastSummary, attempts, nil
}

// runDegradationAttempt executes one upstream call under its own deadline.
func (s *Service) runDegradationAttempt(
	probeCtx context.Context,
	spec execution.AttemptSpec,
	timeout time.Duration,
) (string, string, string) {
	attemptCtx := probeCtx
	if timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(probeCtx, timeout)
		defer cancel()
	}
	return interpretDegradationAttempt(s.executor.Execute(attemptCtx, spec))
}

// degradationFailureIsTerminal reports whether retrying the same credential
// cannot plausibly succeed within this run.
func degradationFailureIsTerminal(code string) bool {
	switch code {
	case degradationErrorRouteUnsupported, degradationErrorQuotaExhausted,
		degradationErrorInvalidCredential, degradationErrorInvalidRequest,
		degradationErrorModelUnavailable:
		return true
	}
	return false
}

// degradationTimeouts widens the compiled Group timeouts to the configured
// detection budget. A reasoning answer of several hundred integers can take far
// longer than an ordinary proxied request, and the non-streaming call produces
// no bytes at all until the model is done.
func degradationTimeouts(target degradationTarget, budget time.Duration) execution.AttemptTimeouts {
	timeouts := executionTimeouts(target.timeouts)
	if budget <= 0 {
		return timeouts
	}
	if timeouts.FirstByte <= 0 || timeouts.FirstByte > budget {
		timeouts.FirstByte = budget
	}
	if timeouts.Request <= 0 || timeouts.Request > budget {
		timeouts.Request = budget
	}
	return timeouts
}

type degradationChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type degradationChatRequest struct {
	Model               string                   `json:"model"`
	Messages            []degradationChatMessage `json:"messages"`
	Stream              bool                     `json:"stream"`
	ReasoningEffort     string                   `json:"reasoning_effort,omitempty"`
	MaxCompletionTokens int                      `json:"max_completion_tokens,omitempty"`
}

func buildDegradationChatBody(
	request degradationProbeRequest,
	challenge degradation.Challenge,
	routeMode channel.RouteMode,
) ([]byte, error) {
	payload := degradationChatRequest{
		Model:           request.upstreamModel,
		Messages:        []degradationChatMessage{{Role: "user", Content: challenge.Prompt}},
		Stream:          false,
		ReasoningEffort: string(request.effort),
	}
	if routeMode != channel.RouteNative {
		payload.MaxCompletionTokens = degradationConvertedMaxTokens
	}
	return json.Marshal(payload)
}

// interpretDegradationAttempt turns one attempt result into either the answer
// text or a classified failure.
func interpretDegradationAttempt(result execution.AttemptResult) (string, string, string) {
	if err := result.Validate(); err != nil {
		return "", degradationErrorInternal, "execution result failed contract validation"
	}
	if result.Error != nil || result.StatusCode < http.StatusOK ||
		result.StatusCode >= http.StatusMultipleChoices {
		code, summary := classifyDegradationFailure(result)
		return "", code, summary
	}
	text, err := extractDegradationAnswer(result.Body)
	if err != nil {
		return "", degradationErrorEmptyResponse, boundDegradationSummary(err.Error())
	}
	return text, "", ""
}

func classifyDegradationFailure(result execution.AttemptResult) (string, string) {
	evidence := result.Error
	if evidence == nil {
		return degradationErrorUpstream, fmt.Sprintf("upstream returned HTTP %d", result.StatusCode)
	}
	summary := boundDegradationSummary(evidence.Summary)
	status := evidence.StatusCode
	if status == 0 {
		status = result.StatusCode
	}
	haystack := strings.ToLower(strings.Join(
		[]string{evidence.Summary, evidence.Code, evidence.Type}, " ",
	))
	switch {
	case degradationQuotaExhausted(haystack):
		return degradationErrorQuotaExhausted, summary
	case evidence.Hint == execution.FailureHintInvalidCredential ||
		evidence.Hint == execution.FailureHintReauthorizationRequired ||
		evidence.Hint == execution.FailureHintRefreshRequired ||
		status == http.StatusUnauthorized || status == http.StatusForbidden:
		return degradationErrorInvalidCredential, summary
	case strings.Contains(haystack, "overload"):
		return degradationErrorOverloaded, summary
	case evidence.Hint == execution.FailureHintRateLimited || status == http.StatusTooManyRequests:
		return degradationErrorRateLimited, summary
	case evidence.Hint == execution.FailureHintModelUnavailable:
		return degradationErrorModelUnavailable, summary
	case evidence.Kind == execution.ErrorKindTimeout:
		return degradationErrorTimeout, summary
	case evidence.Kind == execution.ErrorKindConversionUnsupported:
		return degradationErrorRouteUnsupported, summary
	case evidence.Kind == execution.ErrorKindInvalidRequest || status == http.StatusBadRequest:
		return degradationErrorInvalidRequest, summary
	}
	if summary == "" && status > 0 {
		summary = fmt.Sprintf("upstream returned HTTP %d", status)
	}
	return degradationErrorUpstream, summary
}

// degradationQuotaExhaustedMarkers are the phrases that distinguish a spent
// balance from an ordinary rate limit. Both arrive as HTTP 429, but only the
// former should park a monitor instead of retrying it on the next tick.
var degradationQuotaExhaustedMarkers = []string{
	"insufficient_quota",
	"insufficient quota",
	"exceeded your current quota",
	"quota exhausted",
	"credit balance is too low",
	"out of credits",
	"billing_hard_limit_reached",
	"plan_limit_reached",
}

func degradationQuotaExhausted(haystack string) bool {
	for _, marker := range degradationQuotaExhaustedMarkers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// extractDegradationAnswer reads the assistant text out of an OpenAI chat
// completion response. Content arrives either as a plain string or as typed
// parts depending on the upstream and on whether the route was converted.
func extractDegradationAnswer(body []byte) (string, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := decodeSingleJSON(body, &payload); err != nil {
		return "", errors.New("upstream response is not a chat completion")
	}
	if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		return "", errors.New(payload.Error.Message)
	}
	var builder strings.Builder
	for _, choice := range payload.Choices {
		if part := decodeDegradationContent(choice.Message.Content); part != "" {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(part)
			continue
		}
		if text := strings.TrimSpace(choice.Text); text != "" {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(text)
		}
	}
	answer := strings.TrimSpace(builder.String())
	if answer == "" {
		return "", errors.New("upstream returned no assistant text")
	}
	return answer, nil
}

func decodeDegradationContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "" && part.Type != "text" && part.Type != "output_text" {
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(part.Text)
	}
	return strings.TrimSpace(builder.String())
}

func boundDegradationSummary(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= degradationSummaryLimit {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:degradationSummaryLimit]) + "…"
}
