package gateway

import (
	"net/http"
	"testing"
)

// stubDegradedTargets reports a fixed episode for one credential, standing in
// for the detector's published verdict.
type stubDegradedTargets struct {
	credentialID uint
	episode      uint64
}

func (stub *stubDegradedTargets) Episode(credentialID uint, _ string) uint64 {
	if stub == nil || credentialID != stub.credentialID {
		return 0
	}
	return stub.episode
}

func TestHandlerRotatesCacheKeyOncePerDegradationEpisode(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(5)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	snapshot := manager.Current()
	group := snapshot.Groups[1]
	group.CacheKeyRotationEnabled = true
	snapshot.Groups[1] = group
	verdicts := &stubDegradedTargets{credentialID: 1}
	handler.degradedTargets = verdicts

	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	verdicts.episode = 1
	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)
	verdicts.episode = 2
	serveAffinityRequest(t, engine, body)
	// The credential recovered; the conversation stays where the last rotation
	// put it rather than being dragged back to a shard it already left.
	verdicts.episode = 0
	serveAffinityRequest(t, engine, body)

	keys := make([]string, 0, len(forwarder.inputs))
	for _, input := range forwarder.inputs {
		keys = append(keys, input.ContinuityKey)
	}
	if len(keys) != 5 {
		t.Fatalf("attempts = %d, want 5", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("the healthy request sent an empty continuity key")
	}
	if keys[1] == keys[0] {
		t.Fatal("a degraded target did not move the conversation to a new shard")
	}
	// Every later turn of the same episode must reuse that one move, or the
	// conversation would never keep an upstream cache while degraded.
	if keys[2] != keys[1] {
		t.Fatalf("second turn of the episode = %q, want %q", keys[2], keys[1])
	}
	if keys[3] == keys[1] || keys[3] == keys[0] {
		t.Fatalf("a new episode reused continuity key %q", keys[3])
	}
	if keys[4] != keys[3] {
		t.Fatalf("recovery moved the conversation again: %q, want %q", keys[4], keys[3])
	}
}

func TestHandlerLeavesCacheKeyAloneWhenRotationIsDisabled(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	handler.degradedTargets = &stubDegradedTargets{credentialID: 1, episode: 1}

	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)

	if forwarder.inputs[0].ContinuityKey != forwarder.inputs[1].ContinuityKey {
		t.Fatal("the disabled setting still rotated the continuity key")
	}
	if forwarder.inputs[0].ContinuityKey == "" {
		t.Fatal("continuity key is empty, so the assertion proves nothing")
	}
}

func TestHandlerRotatesCacheKeyWhenUpstreamHasNoCapacity(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode:         http.StatusTooManyRequests,
			Header:             http.Header{"Retry-After": {"30"}},
			Body:               []byte(`{"error":"rate_limit"}`),
			ClassificationBody: []byte(`{"error":"rate_limit"}`),
			RequestWritten:     true,
		},
		successfulAffinityResult(),
		successfulAffinityResult(),
	}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	snapshot := manager.Current()
	group := snapshot.Groups[1]
	group.CacheKeyRotationEnabled = true
	snapshot.Groups[1] = group

	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)

	if len(forwarder.inputs) != 3 {
		t.Fatalf("attempts = %d, want 3", len(forwarder.inputs))
	}
	first := forwarder.inputs[0].ContinuityKey
	// The rejected shard is chosen by the key, not by the credential, so the
	// retry has to carry the new key or it lands on the same busy shard.
	if forwarder.inputs[1].ContinuityKey == first {
		t.Fatal("the retry reused the continuity key the upstream had no room for")
	}
	if forwarder.inputs[2].ContinuityKey != forwarder.inputs[1].ContinuityKey {
		t.Fatalf(
			"the next request = %q, want the rotated %q",
			forwarder.inputs[2].ContinuityKey,
			forwarder.inputs[1].ContinuityKey,
		)
	}
}

func TestHandlerDoesNotRotateCacheKeyOnANonCapacityFailure(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode:         http.StatusBadRequest,
			Header:             make(http.Header),
			Body:               []byte(`{"error":"bad request"}`),
			ClassificationBody: []byte(`{"error":"bad request"}`),
			RequestWritten:     true,
		},
		successfulAffinityResult(),
		successfulAffinityResult(),
	}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	snapshot := manager.Current()
	group := snapshot.Groups[1]
	group.CacheKeyRotationEnabled = true
	snapshot.Groups[1] = group

	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)

	if len(forwarder.inputs) != 3 {
		t.Fatalf("attempts = %d, want 3", len(forwarder.inputs))
	}
	// The upstream had room and answered; it just did not like the request.
	// Moving the conversation would throw away its cache for nothing.
	for index, input := range forwarder.inputs {
		if input.ContinuityKey != forwarder.inputs[0].ContinuityKey {
			t.Fatalf("attempt %d continuity key = %q, want %q", index, input.ContinuityKey, forwarder.inputs[0].ContinuityKey)
		}
	}
}
