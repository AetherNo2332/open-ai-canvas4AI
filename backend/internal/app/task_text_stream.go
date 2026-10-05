package app

import (
	"strings"
	"sync"
	"time"
)

const (
	taskTextStreamFlushBytes    = 2 << 10
	taskTextStreamFlushInterval = 500 * time.Millisecond
)

// taskTextStreamPublisher batches model deltas before persisting them. Streaming
// is an observability and recovery enhancement: persistence failure must not turn
// an otherwise successful model response into a failed generation task.
type taskTextStreamPublisher struct {
	service  *Service
	userID   string
	taskID   string
	mu       sync.Mutex
	buffer   strings.Builder
	timer    *time.Timer
	disabled bool
	closed   bool
	sink     func(string) error
	// flushBytes / flushInterval 可按渠道覆盖：普通文本流要"打字机"式小批量，
	// 而画布 Agent 的 delta 只是可回放留痕（SSE 本身就是 ~1s 批量下发），
	// 每条 delta 一条事件的信封成本远大于文本本身。
	flushBytes    int
	flushInterval time.Duration
}

func newTaskTextStreamPublisher(service *Service, userID string, taskID string) *taskTextStreamPublisher {
	return &taskTextStreamPublisher{
		service: service, userID: userID, taskID: taskID,
		flushBytes: taskTextStreamFlushBytes, flushInterval: taskTextStreamFlushInterval,
	}
}

func (p *taskTextStreamPublisher) batchBytes() int {
	if p.flushBytes > 0 {
		return p.flushBytes
	}
	return taskTextStreamFlushBytes
}

func (p *taskTextStreamPublisher) batchInterval() time.Duration {
	if p.flushInterval > 0 {
		return p.flushInterval
	}
	return taskTextStreamFlushInterval
}

func (p *taskTextStreamPublisher) Publish(delta string) {
	if p == nil || delta == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disabled || p.closed {
		return
	}
	p.buffer.WriteString(delta)
	if p.buffer.Len() >= p.batchBytes() {
		p.flushLocked()
		return
	}
	if p.timer == nil {
		p.timer = time.AfterFunc(p.batchInterval(), p.flush)
	}
}

func (p *taskTextStreamPublisher) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.flushLocked()
}

func (p *taskTextStreamPublisher) flush() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushLocked()
}

func (p *taskTextStreamPublisher) flushLocked() {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.disabled || p.buffer.Len() == 0 {
		return
	}
	content := p.buffer.String()
	p.buffer.Reset()
	var err error
	if p.sink != nil {
		err = p.sink(content)
	} else {
		_, err = p.service.AppendTaskTextDelta(p.userID, p.taskID, content)
	}
	if err != nil {
		p.disabled = true
		_ = p.service.log(p.userID, p.taskID, "warn", "文本流持久化已降级", err.Error())
	}
}
