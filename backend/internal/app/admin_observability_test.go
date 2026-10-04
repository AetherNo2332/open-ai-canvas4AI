package app

import (
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"testing"
	"time"
)

func TestCommittedObservabilityProjection(t *testing.T) {
	now := time.Now().UTC()
	started, completed := now.Add(-500*time.Millisecond), now
	data := repository.ObservabilityData{Runs: []model.CloudAgentExecution{{ID: "run-1", Status: "completed", CreatedAt: now.Add(-time.Second), UpdatedAt: now}}, Logs: []model.ApiCallLog{{ID: "call", TaskID: "step", Capability: "text", Status: model.ApiCallStatusSucceeded, InputTokens: 40, OutputTokens: 8, DurationMs: 500, CreatedAt: now}}, Tasks: []model.Task{{ID: "step", AgentRunID: "run-1", Status: model.TaskStatusSucceeded, CreatedAt: now, StartedAt: &started, CompletedAt: &completed, Attempts: 2}}, Workers: []model.AgentRuntimeInstance{{ID: "worker", Capacity: 4, Active: 2, UpdatedAt: now}}}
	snapshot, events := buildCommittedObservability(data, now, time.Minute)
	if snapshot.Tasks.Completed != 1 || snapshot.Worker.BusyRatio != 50 || snapshot.LLM.InputTokens != 40 {
		t.Fatalf("wrong projection: %+v", snapshot)
	}
	if snapshot.Cost.Known || snapshot.Cost.PerSuccessfulTask != nil {
		t.Fatal("unknown cost must remain unknown")
	}
	if snapshot.Tasks.Retried != 1 {
		t.Fatal("retry missing")
	}
	if len(events) < 3 {
		t.Fatal("missing task/queue/LLM spans")
	}
	for _, event := range events {
		if len(event.TraceID) != 32 {
			t.Fatalf("invalid trace identity: %q", event.TraceID)
		}
	}
}

func TestObservabilityRejectsNonAdmin(t *testing.T) {
	_, err := (&Service{}).AdminObservability(&model.User{Role: "user"}, time.Minute, AnalyticsQuery{})
	if err == nil {
		t.Fatal("non-admin accepted")
	}
}

func TestCommittedObservabilityProjectionAppliesAnalyticsFilters(t *testing.T) {
	now := time.Now().UTC()
	data := repository.ObservabilityData{
		Runs: []model.CloudAgentExecution{
			{ID: "run-1", UserID: "user-1", Status: "failed", CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
			{ID: "run-2", UserID: "user-2", Status: "failed", CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
		},
		Tasks: []model.Task{
			{ID: "task-1", AgentRunID: "run-1", UserID: "user-1", Type: "agent", Model: "model-a", Status: model.TaskStatusFailed, CreatedAt: now.Add(-time.Minute)},
			{ID: "task-2", AgentRunID: "run-2", UserID: "user-2", Type: "agent", Model: "model-b", Status: model.TaskStatusFailed, CreatedAt: now.Add(-time.Minute)},
		},
	}
	filtered := filterObservabilityData(data, repository.AnalyticsFilter{From: now.Add(-2 * time.Minute), To: now.Add(time.Minute), UserID: "user-1", Model: "model-a", Capability: "text"})
	snapshot, _ := buildCommittedObservability(filtered, now, 3*time.Minute)
	if snapshot.Tasks.Total != 1 || len(snapshot.RecentFailures) != 1 {
		t.Fatalf("filter leaked unrelated agent data: %+v", snapshot)
	}
}
