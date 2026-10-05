package observability

import (
	"sort"
	"sync"
	"time"
)

type Aggregator struct {
	mu       sync.RWMutex
	capacity int
	events   []Event
	dropped  int64
}

func NewAggregator(capacity int) *Aggregator {
	if capacity < 1 {
		capacity = 1024
	}
	return &Aggregator{capacity: capacity, events: make([]Event, 0, capacity)}
}

func (a *Aggregator) Record(event Event) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) >= a.capacity {
		a.events = a.events[1:]
		a.dropped++
	}
	a.events = append(a.events, event)
}

func (a *Aggregator) Snapshot(window time.Duration) Snapshot {
	now := time.Now().UTC()
	if window <= 0 {
		window = 15 * time.Minute
	}
	cutoff := now.Add(-window)
	a.mu.RLock()
	events := append([]Event(nil), a.events...)
	a.mu.RUnlock()
	s := Snapshot{Available: true, GeneratedAt: now, Window: window, Alerts: []string{}, RecentFailures: []FailureSnapshot{}}
	latencies := make([]int64, 0)
	seen := map[string]bool{}
	unknownCost := false
	for _, e := range events {
		at := e.EndedAt
		if at.IsZero() {
			at = e.StartedAt
		}
		if at.IsZero() {
			at = now
		}
		if at.Before(cutoff) {
			continue
		}
		switch e.Kind {
		case KindTask:
			if e.Status != StatusCompleted && e.Status != StatusFailed && e.Status != StatusExpired && e.Status != StatusCancelled {
				continue
			}
			if e.RunID != "" && seen[e.RunID] {
				continue
			}
			seen[e.RunID] = true
			s.Tasks.Total++
			if e.Status == StatusCompleted {
				s.Tasks.Completed++
			}
			if e.Status == StatusFailed || e.Status == StatusExpired || e.Status == StatusCancelled {
				s.Tasks.Failed++
				s.RecentFailures = append(s.RecentFailures, FailureSnapshot{TaskID: e.TaskID, RunID: e.RunID, TraceID: e.TraceID, Status: e.Status, Reason: truncateReason(e.Reason), At: at})
			}
			if e.RetryOf != "" {
				s.Tasks.Retried++
			}
			if !e.StartedAt.IsZero() && !e.EndedAt.IsZero() {
				latencies = append(latencies, e.EndedAt.Sub(e.StartedAt).Milliseconds())
			}
		case KindTool:
			s.Tools.Calls++
			if e.Status == StatusCompleted {
				s.Tools.Succeeded++
			}
		case KindLLM:
			if e.CostMicrocredits == nil {
				unknownCost = true
			}
			s.LLM.Calls++
			s.LLM.InputTokens += e.InputTokens
			s.LLM.OutputTokens += e.OutputTokens
			s.LLM.CachedTokens += e.CachedTokens
			s.LLM.ReasoningTokens += e.ReasoningTokens
			if e.CostMicrocredits != nil {
				value := *e.CostMicrocredits
				if s.Cost.Microcredits == nil {
					s.Cost.Microcredits = new(int64)
				}
				*s.Cost.Microcredits += value
				s.Cost.Known = true
			}
		case KindWorker:
			if e.Status == StatusCompleted || e.Status == StatusRunning {
				s.Worker.Online++
			}
		case KindQueue:
			if e.Status == StatusQueued {
				s.Queue.Depth++
			}
		}
	}
	if s.Tasks.Total > 0 {
		s.Tasks.SuccessRate = float64(s.Tasks.Completed) * 100 / float64(s.Tasks.Total)
	}
	if s.Tools.Calls > 0 {
		s.Tools.SuccessRate = float64(s.Tools.Succeeded) * 100 / float64(s.Tools.Calls)
	}
	s.Cost.Known = s.Cost.Known && !unknownCost
	if s.Tasks.Completed > 0 && s.Cost.Microcredits != nil && s.Cost.Known {
		v := *s.Cost.Microcredits / int64(s.Tasks.Completed)
		s.Cost.PerSuccessfulTask = &v
	}
	sort.Slice(s.RecentFailures, func(i, j int) bool { return s.RecentFailures[i].At.After(s.RecentFailures[j].At) })
	if len(s.RecentFailures) > 20 {
		s.RecentFailures = s.RecentFailures[:20]
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		s.Tasks.P50LatencyMs = percentile(latencies, .50)
		s.Tasks.P95LatencyMs = percentile(latencies, .95)
		s.Tasks.P99LatencyMs = percentile(latencies, .99)
	}
	return s
}

func MetricLabels() []string {
	return []string{"environment", "pool", "role", "model", "agent_version", "prompt_version", "tool_type", "status"}
}
func percentile(values []int64, q float64) int64 {
	if len(values) == 0 {
		return 0
	}
	i := int(float64(len(values)-1)*q + .5)
	if i < 0 {
		i = 0
	}
	if i >= len(values) {
		i = len(values) - 1
	}
	return values[i]
}
func truncateReason(value string) string {
	if len(value) > 240 {
		return value[:240]
	}
	return value
}
