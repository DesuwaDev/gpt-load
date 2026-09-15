package affinity

import (
	"testing"
	"time"
)

func TestRotatedContinuityKeyLeavesUnrotatedKeysAlone(t *testing.T) {
	// Generation zero must be byte-identical to the pre-feature key, otherwise
	// enabling the setting would silently discard every warm upstream cache.
	if got := RotatedContinuityKey("tenant-a", 0); got != "tenant-a" {
		t.Fatalf("RotatedContinuityKey(base, 0) = %q, want the base key", got)
	}
	if got := RotatedContinuityKey("", 3); got != "" {
		t.Fatalf("RotatedContinuityKey(\"\", 3) = %q, want an empty key", got)
	}
	first := RotatedContinuityKey("tenant-a", 1)
	second := RotatedContinuityKey("tenant-a", 2)
	if first == "tenant-a" || second == "tenant-a" || first == second {
		t.Fatalf("rotated keys must all differ: base=%q first=%q second=%q", "tenant-a", first, second)
	}
}

func TestRotationStoreAdvancesGenerationPerRotation(t *testing.T) {
	store := newRotationStore(8, time.Hour, time.Now)
	if got := store.Generation("conversation-a"); got != 0 {
		t.Fatalf("Generation of an untouched key = %d, want 0", got)
	}
	if got := store.Rotate("conversation-a"); got != 1 {
		t.Fatalf("first Rotate = %d, want 1", got)
	}
	if got := store.Rotate("conversation-a"); got != 2 {
		t.Fatalf("second Rotate = %d, want 2", got)
	}
	if got := store.Generation("conversation-a"); got != 2 {
		t.Fatalf("Generation after two rotations = %d, want 2", got)
	}
	// Rotations are per conversation; one failing conversation must not move others.
	if got := store.Generation("conversation-b"); got != 0 {
		t.Fatalf("Generation of an unrelated key = %d, want 0", got)
	}
}

func TestRotationStoreForgetsRotationsAfterTTL(t *testing.T) {
	current := time.Now()
	store := newRotationStore(8, time.Hour, func() time.Time { return current })
	store.Rotate("conversation-a")

	current = current.Add(time.Hour)
	if got := store.Generation("conversation-a"); got != 0 {
		t.Fatalf("Generation after the TTL elapsed = %d, want 0", got)
	}
	if got := store.entryCount(); got != 0 {
		t.Fatalf("expired entry was not dropped: entryCount = %d", got)
	}
}

func TestRotationStoreEvictsTheLeastRecentlyUsedKey(t *testing.T) {
	store := newRotationStore(2, time.Hour, time.Now)
	store.Rotate("conversation-a")
	store.Rotate("conversation-b")
	// Reading promotes, so "conversation-a" must outlive "conversation-b".
	store.Generation("conversation-a")
	store.Rotate("conversation-c")

	if got := store.entryCount(); got != 2 {
		t.Fatalf("entryCount = %d, want the configured capacity 2", got)
	}
	if got := store.Generation("conversation-b"); got != 0 {
		t.Fatalf("least recently used key survived eviction: Generation = %d", got)
	}
	if got := store.Generation("conversation-a"); got != 1 {
		t.Fatalf("promoted key was evicted: Generation = %d, want 1", got)
	}
}

func TestRotationStoreConfigureDropsRotationsOnNewRevision(t *testing.T) {
	store := newRotationStore(8, time.Hour, time.Now)
	if !store.Configure(1, 8, time.Hour) {
		t.Fatal("Configure(1) was rejected")
	}
	store.Rotate("conversation-a")

	if !store.Configure(2, 8, time.Hour) {
		t.Fatal("Configure(2) was rejected")
	}
	if got := store.Generation("conversation-a"); got != 0 {
		t.Fatalf("a new revision kept the old rotation: Generation = %d, want 0", got)
	}
	if store.Configure(1, 8, time.Hour) {
		t.Fatal("an older revision must not be applied")
	}
}

func TestRotationStoreRotatesOncePerDegradedEpisode(t *testing.T) {
	store := newRotationStore(8, time.Hour, time.Now)
	// Epoch zero means no episode is running, so a healthy target must never
	// move a conversation off its warm upstream shard.
	if got := store.RotateForEpoch("conversation-a", 0); got != 0 {
		t.Fatalf("RotateForEpoch with no episode = %d, want 0", got)
	}
	first := store.RotateForEpoch("conversation-a", 7)
	if first != 1 {
		t.Fatalf("first rotation of an episode = %d, want 1", first)
	}
	// Every later request inside the same episode must reuse that one move,
	// otherwise the conversation would get a fresh shard on every turn and
	// never build a cache at all.
	for attempt := range 3 {
		if got := store.RotateForEpoch("conversation-a", 7); got != first {
			t.Fatalf("request %d inside the episode = %d, want %d", attempt, got, first)
		}
	}
	if got := store.RotateForEpoch("conversation-a", 8); got != 2 {
		t.Fatalf("rotation in a new episode = %d, want 2", got)
	}
	// A target that recovered leaves the conversation on the shard it moved to.
	if got := store.RotateForEpoch("conversation-a", 0); got != 2 {
		t.Fatalf("RotateForEpoch after recovery = %d, want the recorded 2", got)
	}
	if got := store.Generation("conversation-b"); got != 0 {
		t.Fatalf("an unrelated conversation was rotated: Generation = %d", got)
	}
}

func TestRotationStoreForEpochForgetsRotationsAfterTTL(t *testing.T) {
	current := time.Now()
	store := newRotationStore(8, time.Hour, func() time.Time { return current })
	store.RotateForEpoch("conversation-a", 4)

	current = current.Add(time.Hour)
	// The record expired, so the still-running episode starts the conversation
	// over at generation one rather than resuming from the forgotten count.
	if got := store.RotateForEpoch("conversation-a", 4); got != 1 {
		t.Fatalf("RotateForEpoch after the TTL elapsed = %d, want 1", got)
	}
	if got := store.entryCount(); got != 1 {
		t.Fatalf("entryCount = %d, want 1", got)
	}
}

func TestRotationStoreForEpochRespectsCapacity(t *testing.T) {
	store := newRotationStore(2, time.Hour, time.Now)
	store.RotateForEpoch("conversation-a", 1)
	store.RotateForEpoch("conversation-b", 1)
	store.RotateForEpoch("conversation-c", 1)

	if got := store.entryCount(); got != 2 {
		t.Fatalf("entryCount = %d, want the configured capacity 2", got)
	}
	if got := store.Generation("conversation-a"); got != 0 {
		t.Fatalf("least recently used key survived eviction: Generation = %d", got)
	}
}
