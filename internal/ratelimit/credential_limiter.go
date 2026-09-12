package ratelimit

import (
	"sync"
	"time"
)

// CredentialLimiter 按凭据统计 RPM 滑动窗口与在途请求数。与 AccessKeyRPM 同为
// 进程内状态；两项限制都在调度器选中凭据时扣减，被调度即计入，不看上游结果，
// 否则本地限额会被上游 429 绕开。
type CredentialLimiter struct {
	mu       sync.Mutex
	windows  map[uint]timestampDeque
	inFlight map[uint]int64
	now      func() time.Time
	lastGC   time.Time
}

func NewCredentialLimiter() *CredentialLimiter {
	return &CredentialLimiter{
		windows: make(map[uint]timestampDeque), inFlight: make(map[uint]int64), now: time.Now,
	}
}

// Available 只读判断凭据当前是否还有名额，不扣减。limit <= 0 视为不限。
func (limiter *CredentialLimiter) Available(credentialID uint, rpmLimit, concurrencyLimit int64) bool {
	if limiter == nil || (rpmLimit <= 0 && concurrencyLimit <= 0) {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.availableLocked(credentialID, rpmLimit, concurrencyLimit, limiter.now())
}

func (limiter *CredentialLimiter) availableLocked(credentialID uint, rpmLimit, concurrencyLimit int64, now time.Time) bool {
	if concurrencyLimit > 0 && limiter.inFlight[credentialID] >= concurrencyLimit {
		return false
	}
	if rpmLimit > 0 {
		window := limiter.windows[credentialID]
		window.dropThrough(now.Add(-time.Minute))
		limiter.windows[credentialID] = window
		if int64(window.len()) >= rpmLimit {
			return false
		}
	}
	return true
}

// Acquire 在同一把锁内完成判定与扣减：RPM 窗口记一次，并发在途加一。
// 成功后返回的 release 只归还并发名额，RPM 记录不回滚；重复调用 release 无副作用。
func (limiter *CredentialLimiter) Acquire(credentialID uint, rpmLimit, concurrencyLimit int64) (release func(), allowed bool) {
	if limiter == nil || (rpmLimit <= 0 && concurrencyLimit <= 0) {
		return func() {}, true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	limiter.gcLocked(now)
	if !limiter.availableLocked(credentialID, rpmLimit, concurrencyLimit, now) {
		return nil, false
	}
	if rpmLimit > 0 {
		window := limiter.windows[credentialID]
		window.push(now)
		limiter.windows[credentialID] = window
	}
	if concurrencyLimit <= 0 {
		return func() {}, true
	}
	limiter.inFlight[credentialID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			defer limiter.mu.Unlock()
			if limiter.inFlight[credentialID] <= 1 {
				delete(limiter.inFlight, credentialID)
				return
			}
			limiter.inFlight[credentialID]--
		})
	}, true
}

// RetryAfter 返回该凭据 RPM 窗口最早可恢复的时刻；没有 RPM 限制或未满时返回零值。
func (limiter *CredentialLimiter) RetryAfter(credentialID uint, rpmLimit int64) time.Time {
	if limiter == nil || rpmLimit <= 0 {
		return time.Time{}
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	window := limiter.windows[credentialID]
	window.dropThrough(now.Add(-time.Minute))
	limiter.windows[credentialID] = window
	count := window.len()
	if int64(count) < rpmLimit {
		return time.Time{}
	}
	return window.at(count - int(rpmLimit)).Add(time.Minute)
}

// Usage 返回当前 RPM 窗口内的请求数与在途请求数，供管理面实时展示。
func (limiter *CredentialLimiter) Usage(credentialID uint) (rpmUsed, inFlight int64) {
	if limiter == nil {
		return 0, 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	window := limiter.windows[credentialID]
	window.dropThrough(limiter.now().Add(-time.Minute))
	limiter.windows[credentialID] = window
	return int64(window.len()), limiter.inFlight[credentialID]
}

// InFlightForTest 返回当前在途数，仅供测试断言名额是否泄漏。
func (limiter *CredentialLimiter) InFlightForTest(credentialID uint) int64 {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.inFlight[credentialID]
}

func (limiter *CredentialLimiter) gcLocked(now time.Time) {
	if !limiter.lastGC.IsZero() && now.Sub(limiter.lastGC) < time.Minute {
		return
	}
	cutoff := now.Add(-time.Minute)
	for id, window := range limiter.windows {
		window.dropThrough(cutoff)
		if window.len() == 0 {
			delete(limiter.windows, id)
			continue
		}
		limiter.windows[id] = window
	}
	limiter.lastGC = now
}
