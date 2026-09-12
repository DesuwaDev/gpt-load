package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/state"
)

// 两个凭据各限并发 1：第一个被占满后调度器必须换到第二个，而不是 429。
func TestHandlerSwitchesCredentialWhenLocalLimitIsReached(t *testing.T) {
	limiter := ratelimit.NewCredentialLimiter()
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: []byte(`{"id":"ok","model":"gpt-4o","choices":[]}`),
	}}}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one", "sk-two",
	)
	handler.credentialLimiter = limiter
	publishLimitedCredentials(t, handler, manager, 0, 1)

	// 先把凭据 1 的唯一并发名额占住。
	holdOne, ok := limiter.Acquire(1, 0, 1)
	if !ok {
		t.Fatal("could not pre-occupy credential 1")
	}
	defer holdOne()

	recorder := serveChat(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs) != 1 || forwarder.inputs[0].Credential.ID != 2 {
		t.Fatalf("expected forward via credential 2, got inputs=%d", len(forwarder.inputs))
	}
	if limiter.InFlightForTest(2) != 0 {
		t.Fatal("credential 2 concurrency slot leaked after request completion")
	}
}

// 所有候选都满额且没有任何尝试发出时，答 429 而不是 503。
func TestHandlerReturns429WhenEveryCredentialIsLocallyLimited(t *testing.T) {
	limiter := ratelimit.NewCredentialLimiter()
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, &scriptedForwarder{}, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one",
	)
	handler.credentialLimiter = limiter
	publishLimitedCredentials(t, handler, manager, 1, 0)

	if _, ok := limiter.Acquire(1, 1, 0); !ok {
		t.Fatal("could not exhaust credential 1 rpm")
	}
	recorder := serveChat(engine)
	assertGatewayReasonTest(t, recorder, http.StatusTooManyRequests, reasonCredentialRateLimited.Code)
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing on credential rate limit")
	}
}

// RPM 记账发生在调度时，不看上游结果：上游失败的那次也消耗名额。
func TestHandlerChargesCredentialRPMEvenWhenUpstreamFails(t *testing.T) {
	limiter := ratelimit.NewCredentialLimiter()
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusInternalServerError, Header: make(http.Header),
		Body: []byte(`{"error":{"message":"boom"}}`),
	}}}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one",
	)
	handler.credentialLimiter = limiter
	publishLimitedCredentials(t, handler, manager, 1, 0)

	serveChat(engine)
	if limiter.Available(1, 1, 0) {
		t.Fatal("failed upstream attempt did not consume the credential rpm slot")
	}
}

func publishLimitedCredentials(t *testing.T, handler *Handler, manager *state.Manager, rpm, concurrency int64) {
	t.Helper()
	views := handler.registry.(*state.CredentialRegistry).Snapshot()
	entries, err := handler.registry.(*state.CredentialRegistry).SnapshotGroupCredentialEntriesExact(1, credentialIDsOf(views))
	if err != nil {
		t.Fatal(err)
	}
	for index := range entries {
		entries[index].RPMLimit = rpm
		entries[index].ConcurrencyLimit = concurrency
	}
	if err := handler.registry.(*state.CredentialRegistry).RestoreGroupCredentialEntriesExact(1, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: []byte(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: credentialConfigsOf(views),
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func credentialIDsOf(views []state.CredentialRuntimeView) []uint {
	ids := make([]uint, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	return ids
}

func credentialConfigsOf(views []state.CredentialRuntimeView) []state.CredentialConfig {
	configs := make([]state.CredentialConfig, 0, len(views))
	for _, view := range views {
		configs = append(configs, testCredentialConfig(view.ID, view.GroupID))
	}
	return configs
}

func serveChat(engine interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}
