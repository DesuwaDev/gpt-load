package control

import "sync"

const (
	// degradationManualQueueLimit bounds the immediate-probe backlog. Every
	// queued entry eventually calls a real upstream, so an unbounded queue would
	// let repeated clicks pile up work nobody can see or cancel.
	degradationManualQueueLimit = 512
	// degradationManualDrainLimit bounds one drain so a large selection is
	// consumed in batches. 每批之间调度器会重新读一次全局配置，中途调小并发上限
	// 或超时能在下一批立刻生效，而不是等整批跑完。
	degradationManualDrainLimit = 64
)

// degradationRunCoordinator is the process-local registry of detection runs.
//
// inflight keeps one monitor from being probed twice at once — the scheduled
// sweep, the overload reaction and the manual queue all claim through it. queue
// holds the monitors an operator asked to probe right now: a bulk probe calls
// real upstreams one selection at a time, so the HTTP request only registers
// the intent and the scheduler drains it under the configured concurrency cap.
//
// 队列刻意不持久化。长期有效的排期始终在 degradation_monitors.next_run_at_ms
// 上，重启丢掉的只是一次"现在就跑"的临时请求，重新点一次即可，不会留下一批
// 无人认领的后台任务。
type degradationRunCoordinator struct {
	mu       sync.Mutex
	queue    []uint
	queued   map[uint]struct{}
	draining map[uint]struct{}
	inflight map[uint]struct{}
	wake     chan struct{}
}

// ensureLocked builds the lazily created members. Tests construct Service
// values directly without a constructor, so every entry point has to tolerate a
// zero coordinator.
func (c *degradationRunCoordinator) ensureLocked() {
	if c.queued == nil {
		c.queued = make(map[uint]struct{})
	}
	if c.draining == nil {
		c.draining = make(map[uint]struct{})
	}
	if c.inflight == nil {
		c.inflight = make(map[uint]struct{})
	}
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
}

// wakeChannel returns the signal the scheduler selects on, so a bulk probe
// starts immediately instead of waiting out the heartbeat.
func (c *degradationRunCoordinator) wakeChannel() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked()
	return c.wake
}

// enqueueManual registers monitors for an immediate probe and reports how many
// entries were added. An id already waiting collapses into the existing entry,
// and anything past the backlog limit is dropped.
func (c *degradationRunCoordinator) enqueueManual(ids []uint) int {
	c.mu.Lock()
	c.ensureLocked()
	added := 0
	for _, id := range ids {
		if id == 0 || len(c.queue) >= degradationManualQueueLimit {
			continue
		}
		if _, waiting := c.queued[id]; waiting {
			continue
		}
		c.queued[id] = struct{}{}
		c.queue = append(c.queue, id)
		added++
	}
	wake := c.wake
	c.mu.Unlock()
	if added > 0 {
		signalDegradationWake(wake)
	}
	return added
}

// drainManual removes up to limit queued ids, oldest first, and marks them as
// draining so a batch that has left the queue but has not started executing
// still counts as active work.
func (c *degradationRunCoordinator) drainManual(limit int) []uint {
	if limit < 1 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked()
	if limit > len(c.queue) {
		limit = len(c.queue)
	}
	if limit == 0 {
		return nil
	}
	ids := make([]uint, limit)
	copy(ids, c.queue[:limit])
	c.queue = append(c.queue[:0], c.queue[limit:]...)
	for _, id := range ids {
		delete(c.queued, id)
		c.draining[id] = struct{}{}
	}
	return ids
}

// finishManual clears the draining mark and re-signals while work is still
// waiting, so a backlog keeps moving instead of stalling until the next tick.
func (c *degradationRunCoordinator) finishManual(ids []uint) {
	c.mu.Lock()
	c.ensureLocked()
	for _, id := range ids {
		delete(c.draining, id)
	}
	pending := len(c.queue) > 0
	wake := c.wake
	c.mu.Unlock()
	if pending {
		signalDegradationWake(wake)
	}
}

func signalDegradationWake(wake chan struct{}) {
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// claim reserves a monitor for execution and reports whether the caller won it.
func (c *degradationRunCoordinator) claim(monitorID uint) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked()
	if _, running := c.inflight[monitorID]; running {
		return false
	}
	c.inflight[monitorID] = struct{}{}
	return true
}

func (c *degradationRunCoordinator) release(monitorID uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, monitorID)
}

// active counts the distinct monitors this process is about to probe or is
// probing. 监控页只用它判断"还有没有在跑"来决定是否继续轮询，所以三个集合要
// 去重后再计数，队列与执行中的同一条不能算成两条。
func (c *degradationRunCoordinator) active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[uint]struct{}, len(c.queue)+len(c.draining)+len(c.inflight))
	for _, id := range c.queue {
		seen[id] = struct{}{}
	}
	for id := range c.draining {
		seen[id] = struct{}{}
	}
	for id := range c.inflight {
		seen[id] = struct{}{}
	}
	return len(seen)
}
