package control

import "testing"

func TestDegradationRuntimeReportsNoEpisodeByDefault(t *testing.T) {
	runtime := NewDegradationRuntime()
	if got := runtime.Episode(7, "gpt-5-codex"); got != 0 {
		t.Fatalf("Episode before any publish = %d, want 0", got)
	}
	if got := runtime.Episode(0, "gpt-5-codex"); got != 0 {
		t.Fatalf("Episode of an unidentified credential = %d, want 0", got)
	}
}

func TestDegradationRuntimeKeepsEpisodeWhileDegraded(t *testing.T) {
	runtime := NewDegradationRuntime()
	degraded := degradedTarget{credentialID: 7, upstreamModel: "gpt-5-codex"}
	runtime.publish(map[degradedTarget]struct{}{degraded: {}})

	episode := runtime.Episode(7, "gpt-5-codex")
	if episode == 0 {
		t.Fatal("a degraded target reported no episode")
	}
	// A second monitor going degraded republishes the whole set. The target that
	// was already degraded must keep its number, or every conversation on it
	// would rotate a second time for someone else's state change.
	other := degradedTarget{credentialID: 9, upstreamModel: "gpt-5-codex"}
	runtime.publish(map[degradedTarget]struct{}{degraded: {}, other: {}})
	if got := runtime.Episode(7, "gpt-5-codex"); got != episode {
		t.Fatalf("Episode after an unrelated republish = %d, want the running %d", got, episode)
	}
	if got := runtime.Episode(9, "gpt-5-codex"); got == 0 || got == episode {
		t.Fatalf("the newly degraded target got episode %d, want a distinct non-zero id", got)
	}
	// The same credential serving a different model is a different target.
	if got := runtime.Episode(7, "gpt-5"); got != 0 {
		t.Fatalf("Episode of an unmonitored model = %d, want 0", got)
	}
}

func TestDegradationRuntimeStartsANewEpisodeAfterRecovery(t *testing.T) {
	runtime := NewDegradationRuntime()
	degraded := degradedTarget{credentialID: 7, upstreamModel: "gpt-5-codex"}
	runtime.publish(map[degradedTarget]struct{}{degraded: {}})
	first := runtime.Episode(7, "gpt-5-codex")

	runtime.publish(map[degradedTarget]struct{}{})
	if got := runtime.Episode(7, "gpt-5-codex"); got != 0 {
		t.Fatalf("Episode after recovery = %d, want 0", got)
	}

	runtime.publish(map[degradedTarget]struct{}{degraded: {}})
	if got := runtime.Episode(7, "gpt-5-codex"); got == 0 || got == first {
		t.Fatalf("the second degradation reused episode %d, want a new id", got)
	}
}
