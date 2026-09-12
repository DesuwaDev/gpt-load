package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/state"
)

// 并发限流器的记录桩：可脚本化拒绝，并追踪 release 是否被调用。
type recordingAccessKeyConcurrencyLimiter struct {
	mu       sync.Mutex
	calls    []rpmLimiterCall
	releases int
	reject   bool
}

func (limiter *recordingAccessKeyConcurrencyLimiter) Acquire(accessKeyID uint, limit int64) (func(), bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.calls = append(limiter.calls, rpmLimiterCall{accessKeyID: accessKeyID, limit: limit})
	if limiter.reject {
		return nil, false
	}
	return func() {
		limiter.mu.Lock()
		limiter.releases++
		limiter.mu.Unlock()
	}, true
}

func (limiter *recordingAccessKeyConcurrencyLimiter) snapshot() ([]rpmLimiterCall, int) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return append([]rpmLimiterCall(nil), limiter.calls...), limiter.releases
}

func publishConcurrencyLimitedAccessKey(t *testing.T, handler *Handler, manager *state.Manager, limit int64) {
	t.Helper()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: []byte(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status: state.AccessKeyStatusActive, ConcurrencyLimit: limit,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestHandlerRejectsWhenAccessKeyConcurrencyLimitReached(t *testing.T) {
	limiter := &recordingAccessKeyConcurrencyLimiter{reject: true}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, &scriptedForwarder{}, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-upstream",
	)
	handler.concurrency = limiter
	publishConcurrencyLimitedAccessKey(t, handler, manager, 3)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assertGatewayReasonTest(t, recorder, http.StatusTooManyRequests, reasonAccessKeyConcurrencyLimited.Code)
	calls, releases := limiter.snapshot()
	if len(calls) != 1 || calls[0] != (rpmLimiterCall{accessKeyID: 1, limit: 3}) {
		t.Fatalf("concurrency calls = %#v, want one call for key 1 with limit 3", calls)
	}
	if releases != 0 {
		t.Fatalf("releases = %d, want 0 after rejection", releases)
	}
}

func TestHandlerReleasesConcurrencySlotAfterRequestCompletes(t *testing.T) {
	limiter := &recordingAccessKeyConcurrencyLimiter{}
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: []byte(`{"id":"ok","model":"gpt-4o","choices":[]}`),
	}}}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-upstream",
	)
	handler.concurrency = limiter
	publishConcurrencyLimitedAccessKey(t, handler, manager, 3)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	calls, releases := limiter.snapshot()
	if len(calls) != 1 || releases != 1 {
		t.Fatalf("calls/releases = %d/%d, want 1/1", len(calls), releases)
	}
}

// RPM 先于并发判定：RPM 拒绝时不得占用在途名额。
func TestHandlerRPMRejectionDoesNotAcquireConcurrencySlot(t *testing.T) {
	limiter := &recordingAccessKeyConcurrencyLimiter{}
	rpm := &recordingAccessKeyRPMLimiter{decisions: []ratelimit.LimitDecision{{Allowed: false, RetryAfter: time.Second}}}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, &scriptedForwarder{}, rpm, &recordingRequestLogSink{}, "sk-upstream",
	)
	handler.concurrency = limiter
	publishConcurrencyLimitedAccessKey(t, handler, manager, 3)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assertGatewayReasonTest(t, recorder, http.StatusTooManyRequests, reasonAccessKeyRateLimited.Code)
	if calls, _ := limiter.snapshot(); len(calls) != 0 {
		t.Fatalf("concurrency limiter called %d times after RPM rejection, want 0", len(calls))
	}
}
