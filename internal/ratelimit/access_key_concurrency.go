package ratelimit

import "sync"

// AccessKeyConcurrency 按访问密钥统计在途请求数。与 AccessKeyRPM 同为进程内
// 状态：单实例部署下无需持久化，重启后计数自然归零。
type AccessKeyConcurrency struct {
	mu       sync.Mutex
	inFlight map[uint]int64
}

func NewAccessKeyConcurrency() *AccessKeyConcurrency {
	return &AccessKeyConcurrency{inFlight: make(map[uint]int64)}
}

// Acquire 为一次请求占用一个在途名额。limit <= 0 表示不限，此时不记账，
// 返回的 release 为空操作；成功占用后调用方必须在请求结束时调用 release。
func (limiter *AccessKeyConcurrency) Acquire(accessKeyID uint, limit int64) (release func(), allowed bool) {
	if limit <= 0 {
		return func() {}, true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.inFlight[accessKeyID] >= limit {
		return nil, false
	}
	limiter.inFlight[accessKeyID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			defer limiter.mu.Unlock()
			if limiter.inFlight[accessKeyID] <= 1 {
				delete(limiter.inFlight, accessKeyID)
				return
			}
			limiter.inFlight[accessKeyID]--
		})
	}, true
}

// InFlight 返回当前在途数，仅供测试与观测使用。
func (limiter *AccessKeyConcurrency) InFlight(accessKeyID uint) int64 {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return limiter.inFlight[accessKeyID]
}
