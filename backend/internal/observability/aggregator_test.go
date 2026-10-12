package observability

import (
	"testing"
	"time"
)

func TestAggregatorSnapshotAggregatesLifecycleAndTokens(t *testing.T) {
	now := time.Now().UTC()
	a := NewAggregator(16)
	a.Record(Event{TaskID: "task-1", RunID: "run-1", TraceID: "trace-1", Kind: KindTask, Status: StatusCompleted, StartedAt: now.Add(-2 * time.Second), EndedAt: now, InputTokens: 10, OutputTokens: 5})
	a.Record(Event{TaskID: "task-1", RunID: "run-2", TraceID: "trace-2", Kind: KindTask, Status: StatusFailed, StartedAt: now.Add(-time.Second), EndedAt: now, RetryOf: "run-1", Reason: "tool failed"})
	a.Record(Event{TaskID: "task-1", RunID: "run-2", Kind: KindTool, Status: StatusCompleted, StartedAt: now.Add(-time.Second), EndedAt: now})
	a.Record(Event{TaskID: "task-1", RunID: "run-2", Kind: KindLLM, Status: StatusCompleted, InputTokens: 10, OutputTokens: 5, CachedTokens: 2})
	s := a.Snapshot(time.Minute)
	if s.Tasks.Total != 2 || s.Tasks.Completed != 1 || s.Tasks.Failed != 1 || s.Tasks.Retried != 1 {
		t.Fatalf("unexpected task snapshot: %+v", s.Tasks)
	}
	if s.LLM.Calls != 1 || s.LLM.InputTokens != 10 || s.LLM.CachedTokens != 2 {
		t.Fatalf("unexpected llm snapshot: %+v", s.LLM)
	}
	if len(s.RecentFailures) != 1 || s.RecentFailures[0].TaskID != "task-1" {
		t.Fatalf("unexpected failures: %+v", s.RecentFailures)
	}
}

func TestAggregatorDoesNotExposeHighCardinalityLabels(t *testing.T) {
	got := MetricLabels()
	if len(got) != 8 {
		t.Fatalf("labels = %v", got)
	}
	for _, label := range got {
		if label == "task_id" || label == "run_id" || label == "trace_id" {
			t.Fatalf("high cardinality label exposed: %s", label)
		}
	}
}

func TestSnapshotCountsOnlyTerminalRunsAndUnknownCosts(t *testing.T) {
	a := NewAggregator(16)
	now := time.Now().UTC()
	a.Record(Event{RunID: "run", Kind: KindTask, Status: StatusRunning, StartedAt: now})
	a.Record(Event{RunID: "run", Kind: KindTask, Status: StatusCompleted, StartedAt: now, EndedAt: now})
	a.Record(Event{RunID: "run", Kind: KindTask, Status: StatusCompleted, StartedAt: now, EndedAt: now})
	known := int64(12)
	a.Record(Event{Kind: KindLLM, EndedAt: now, CostMicrocredits: &known})
	a.Record(Event{Kind: KindLLM, EndedAt: now})
	s := a.Snapshot(time.Minute)
	if s.Tasks.Total != 1 || s.Tasks.SuccessRate != 100 {
		t.Fatalf("terminal runs counted incorrectly: %+v", s.Tasks)
	}
	if s.Cost.Known || s.Cost.PerSuccessfulTask != nil {
		t.Fatalf("partial cost presented as complete: %+v", s.Cost)
	}
}
