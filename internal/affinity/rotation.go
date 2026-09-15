package affinity

import (
	"container/list"
	"strconv"
	"sync"
	"time"
)

type rotationEntry struct {
	key           string
	generation    uint64
	degradedEpoch uint64
	expiresAt     time.Time
}

// RotationStore records continuity keys that must move to a new upstream shard.
// Rotation only changes the session identity sent upstream; credential selection
// and soft affinity are resolved before it and are left untouched.
type RotationStore struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	recent   list.List
	capacity int
	ttl      time.Duration
	now      func() time.Time
	revision uint64
}

func NewRotationStore() *RotationStore {
	return newRotationStore(DefaultCapacity, DefaultTTL, time.Now)
}

func newRotationStore(capacity int, ttl time.Duration, now func() time.Time) *RotationStore {
	return &RotationStore{
		entries:  make(map[string]*list.Element),
		capacity: capacity,
		ttl:      ttl,
		now:      now,
	}
}

// Configure applies one frozen runtime configuration revision. Moving to a newer
// revision drops every remembered rotation, so a conversation returns to its
// original key rather than staying on a shard chosen under old configuration.
func (store *RotationStore) Configure(revision uint64, capacity int, ttl time.Duration) bool {
	if store == nil || revision == 0 || capacity <= 0 || ttl <= 0 || store.now == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if revision < store.revision {
		return false
	}
	if revision == store.revision {
		return store.capacity == capacity && store.ttl == ttl
	}
	store.revision = revision
	store.capacity = capacity
	store.ttl = ttl
	store.entries = make(map[string]*list.Element)
	store.recent.Init()
	return true
}

// Generation reports the rotation count currently recorded for key. Zero means
// the key was never rotated and must reach the upstream unchanged.
func (store *RotationStore) Generation(key string) uint64 {
	if store == nil || key == "" || store.now == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	element := store.currentRotationLocked(key, store.now())
	if element == nil {
		return 0
	}
	store.recent.MoveToFront(element)
	return element.Value.(*rotationEntry).generation
}

// Rotate advances key to its next generation and returns it. Repeated failures
// on the same conversation keep moving it rather than settling on one shard.
func (store *RotationStore) Rotate(key string) uint64 {
	if store == nil || key == "" || store.now == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.capacity <= 0 || store.ttl <= 0 {
		return 0
	}
	now := store.now()
	if element := store.currentRotationLocked(key, now); element != nil {
		entry := element.Value.(*rotationEntry)
		entry.generation++
		entry.expiresAt = now.Add(store.ttl)
		store.recent.MoveToFront(element)
		return entry.generation
	}
	store.evictLocked()
	entry := &rotationEntry{key: key, generation: 1, expiresAt: now.Add(store.ttl)}
	store.entries[key] = store.recent.PushFront(entry)
	return entry.generation
}

// RotateForEpoch advances key at most once per epoch and returns the generation
// to use. The caller passes a marker that changes when the upstream target
// enters a new bad episode, so a conversation moves once per episode instead of
// on every request for as long as that episode lasts. Epoch zero means no
// episode is running and leaves the recorded rotation alone.
func (store *RotationStore) RotateForEpoch(key string, epoch uint64) uint64 {
	if store == nil || key == "" || epoch == 0 || store.now == nil {
		return store.Generation(key)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.capacity <= 0 || store.ttl <= 0 {
		return 0
	}
	now := store.now()
	if element := store.currentRotationLocked(key, now); element != nil {
		entry := element.Value.(*rotationEntry)
		store.recent.MoveToFront(element)
		if entry.degradedEpoch == epoch {
			return entry.generation
		}
		entry.generation++
		entry.degradedEpoch = epoch
		entry.expiresAt = now.Add(store.ttl)
		return entry.generation
	}
	store.evictLocked()
	entry := &rotationEntry{
		key: key, generation: 1, degradedEpoch: epoch, expiresAt: now.Add(store.ttl),
	}
	store.entries[key] = store.recent.PushFront(entry)
	return entry.generation
}

func (store *RotationStore) evictLocked() {
	for len(store.entries) >= store.capacity {
		oldest := store.recent.Back()
		if oldest == nil {
			return
		}
		store.removeRotationLocked(oldest)
	}
}

func (store *RotationStore) entryCount() int {
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.entries)
}

func (store *RotationStore) currentRotationLocked(key string, now time.Time) *list.Element {
	element := store.entries[key]
	if element == nil {
		return nil
	}
	if !now.Before(element.Value.(*rotationEntry).expiresAt) {
		store.removeRotationLocked(element)
		return nil
	}
	return element
}

func (store *RotationStore) removeRotationLocked(element *list.Element) {
	delete(store.entries, element.Value.(*rotationEntry).key)
	store.recent.Remove(element)
}

// RotatedContinuityKey derives the continuity key for a rotation generation.
// Generation zero returns base unchanged, so a conversation that was never
// rotated keeps the exact key it had before this feature existed.
func RotatedContinuityKey(base string, generation uint64) string {
	if base == "" || generation == 0 {
		return base
	}
	return base + "#r" + strconv.FormatUint(generation, 10)
}
