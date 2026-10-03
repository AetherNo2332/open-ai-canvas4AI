package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/observability"
	"infinite-canvas/backend/internal/repository"
	"strconv"
	"time"
)

func (s *Service) AdminObservability(actor *model.User, window time.Duration) (observability.Snapshot, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return observability.Snapshot{}, err
	}
	if window < time.Minute || window > 24*time.Hour {
		return observability.Snapshot{}, BadAuthRequest("观测窗口必须为 60 到 86400 秒")
	}
	return s.committedObservability(window)
}

func (s *Service) committedObservability(window time.Duration) (observability.Snapshot, error) {
	now := time.Now().UTC()
	data, err := s.repo.ObservabilityData(now.Add(-window))
	if err != nil {
		return observability.Snapshot{}, err
	}
	snapshot, _ := buildCommittedObservability(data, now, window)
	snapshot.GrafanaURL = configuredGrafanaURL()
	return snapshot, nil
}

func observationTraceID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}

func buildCommittedObservability(data repository.ObservabilityData, now time.Time, window time.Duration) (observability.Snapshot, []observability.Event) {
	events := []observability.Event{}
	runByID := map[string]model.CloudAgentExecution{}
	taskByID := map[string]model.Task{}
	for _, task := range data.Tasks {
		taskByID[task.ID] = task
	}
	for _, run := range data.Runs {
		runByID[run.ID] = run
		e := observability.Event{TaskID: run.ID, RunID: run.ID, TraceID: observationTraceID(run.ID), SessionID: observationTraceID(run.ConversationID), Kind: observability.KindTask, Status: observability.Status(run.Status), StartedAt: run.CreatedAt, EndedAt: run.UpdatedAt, Key: "run:" + run.ID, ParentRunID: run.ParentID}
		if run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled" || run.Status == "expired" {
			events = append(events, e)
		}
	}
	retries := 0
	queueDepth := 0
	oldest := int64(0)
	for _, task := range data.Tasks {
		if task.AgentRunID == "" {
			continue
		}
		runID := task.AgentRunID
		traceID := observationTraceID(runID)
		start := task.CreatedAt
		if task.StartedAt != nil {
			events = append(events, observability.Event{Key: "queue:" + task.ID, TaskID: runID, RunID: runID, TraceID: traceID, Kind: observability.KindQueue, Status: observability.StatusCompleted, StartedAt: start, EndedAt: *task.StartedAt})
			if task.CompletedAt != nil {
				events = append(events, observability.Event{Key: "execute:" + task.ID, TaskID: runID, RunID: runID, TraceID: traceID, Kind: observability.KindExecute, Status: observability.StatusCompleted, StartedAt: *task.StartedAt, EndedAt: *task.CompletedAt})
			}
		}
		if task.Status == model.TaskStatusQueued {
			queueDepth++
			age := int64(now.Sub(start).Seconds())
			if age > oldest {
				oldest = age
			}
		}
		if task.Attempts > 1 {
			retries += task.Attempts - 1
		}
	}
	for _, log := range data.Logs {
		task, ok := taskByID[log.TaskID]
		if !ok || task.AgentRunID == "" || log.Capability != "text" {
			continue
		}
		start := log.StartedAt
		if start.IsZero() {
			start = log.CreatedAt.Add(-time.Duration(log.DurationMs) * time.Millisecond)
		}
		status := observability.StatusCompleted
		if log.Status == model.ApiCallStatusFailed {
			status = observability.StatusFailed
		}
		events = append(events, observability.Event{Key: "llm:" + log.ID, TaskID: task.AgentRunID, RunID: task.AgentRunID, TraceID: observationTraceID(task.AgentRunID), Model: log.Model, Kind: observability.KindLLM, Status: status, StartedAt: start, EndedAt: start.Add(time.Duration(log.DurationMs) * time.Millisecond), InputTokens: log.InputTokens, OutputTokens: log.OutputTokens, CachedTokens: log.CachedTokens})
	}
	pending := map[string]time.Time{}
	// Repository returns newest first; reverse to pair start and finish receipts.
	for i := len(data.Events) - 1; i >= 0; i-- {
		record := data.Events[i]
		if _, ok := runByID[record.RunID]; !ok {
			continue
		}
		var event CloudAgentEvent
		if json.Unmarshal([]byte(record.EventJSON), &event) != nil {
			continue
		}
		key := record.RunID + ":" + stringValue(event.Payload["callId"])
		kind := observability.KindTool
		status := observability.StatusCompleted
		switch event.Type {
		case "tool_started", "tool_call":
			pending[key] = record.CreatedAt
			continue
		case "tool_completed":
		case "tool_failed":
			status = observability.StatusFailed
		case "context_compaction_requested":
			pending[record.RunID+":compaction"] = record.CreatedAt
			continue
		case "context_compacted":
			kind = observability.KindCompaction
			key = record.RunID + ":compaction"
		default:
			continue
		}
		start := pending[key]
		if start.IsZero() {
			start = record.CreatedAt
		}
		events = append(events, observability.Event{Key: "event:" + record.RunID + ":" + strconv.Itoa(record.Sequence), TaskID: record.RunID, RunID: record.RunID, TraceID: observationTraceID(record.RunID), Kind: kind, Status: status, ToolType: stringValue(event.Payload["toolName"]), StartedAt: start, EndedAt: record.CreatedAt})
		delete(pending, key)
	}
	aggregate := observability.NewAggregator(len(events) + 1)
	for _, event := range events {
		aggregate.Record(event)
	}
	snapshot := aggregate.Snapshot(window)
	snapshot.Available = len(events) > 0 || len(data.Workers) > 0
	snapshot.Delayed = data.Truncated
	snapshot.Queue.Depth = queueDepth
	snapshot.Queue.OldestAgeSeconds = oldest
	snapshot.Tasks.Retried = retries
	capacity := 0
	for _, worker := range data.Workers {
		if worker.Draining {
			continue
		}
		snapshot.Worker.Online++
		snapshot.Worker.Busy += worker.Active
		capacity += worker.Capacity
	}
	if capacity > 0 {
		snapshot.Worker.BusyRatio = 100 * float64(snapshot.Worker.Busy) / float64(capacity)
	}
	if snapshot.Tasks.Total > 0 {
		snapshot.Tools.StepsPerTask = float64(snapshot.Tools.Calls) / float64(snapshot.Tasks.Total)
	}
	if snapshot.Worker.Online == 0 && snapshot.Queue.Depth > 0 {
		snapshot.Alerts = append(snapshot.Alerts, "worker_unavailable")
	}
	if snapshot.Queue.OldestAgeSeconds > 300 {
		snapshot.Alerts = append(snapshot.Alerts, "queue_wait_exceeded")
	}
	if snapshot.Tasks.Total >= 10 && snapshot.Tasks.SuccessRate < 80 {
		snapshot.Alerts = append(snapshot.Alerts, "success_rate_low")
	}
	return snapshot, events
}
