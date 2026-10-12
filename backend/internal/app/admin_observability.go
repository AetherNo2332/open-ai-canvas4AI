package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/observability"
	"infinite-canvas/backend/internal/repository"
	"strconv"
	"strings"
	"time"
)

func (s *Service) AdminObservability(actor *model.User, window time.Duration, query AnalyticsQuery) (observability.Snapshot, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return observability.Snapshot{}, err
	}
	if window < time.Minute || window > 24*time.Hour {
		return observability.Snapshot{}, BadAuthRequest("观测窗口必须为 60 到 86400 秒")
	}
	filter := normalizeAnalyticsFilter(query)
	if strings.TrimSpace(query.From) == "" && strings.TrimSpace(query.To) == "" {
		filter.From = time.Now().UTC().Add(-window)
		filter.To = time.Now().UTC()
	}
	return s.committedObservability(filter)
}

func (s *Service) committedObservability(filter repository.AnalyticsFilter) (observability.Snapshot, error) {
	now := time.Now().UTC()
	data, err := s.repo.ObservabilityData(filter.From)
	if err != nil {
		return observability.Snapshot{}, err
	}
	data = filterObservabilityData(data, filter)
	window := filter.To.Sub(filter.From)
	if window <= 0 {
		window = time.Minute
	}
	snapshot, _ := buildCommittedObservability(data, now, window)
	snapshot.GrafanaURL = configuredGrafanaURL()
	return snapshot, nil
}

func filterObservabilityData(data repository.ObservabilityData, filter repository.AnalyticsFilter) repository.ObservabilityData {
	runIDs := make(map[string]bool)
	tasks := make([]model.Task, 0, len(data.Tasks))
	for _, task := range data.Tasks {
		if task.CreatedAt.Before(filter.From) || !task.CreatedAt.Before(filter.To) {
			continue
		}
		if filter.UserID != "" && task.UserID != filter.UserID {
			continue
		}
		if filter.Model != "" && task.Model != filter.Model {
			continue
		}
		if filter.Capability != "" && capabilityFromTaskType(task.Type) != filter.Capability {
			continue
		}
		tasks = append(tasks, task)
		if task.AgentRunID != "" {
			runIDs[task.AgentRunID] = true
		}
	}
	if filter.UserID == "" && filter.Model == "" && filter.Capability == "" && filter.ChannelID == "" {
		for _, run := range data.Runs {
			if !run.CreatedAt.Before(filter.From) && run.CreatedAt.Before(filter.To) {
				runIDs[run.ID] = true
			}
		}
	}
	logs := make([]model.ApiCallLog, 0, len(data.Logs))
	channelTaskIDs := make(map[string]bool)
	if filter.ChannelID != "" {
		runIDs = make(map[string]bool)
	}
	for _, log := range data.Logs {
		if log.CreatedAt.Before(filter.From) || !log.CreatedAt.Before(filter.To) {
			continue
		}
		if filter.UserID != "" && log.UserID != filter.UserID {
			continue
		}
		if filter.Model != "" && log.Model != filter.Model {
			continue
		}
		if filter.ChannelID != "" && log.ChannelID != filter.ChannelID {
			continue
		}
		if filter.Capability != "" && log.Capability != filter.Capability {
			continue
		}
		task := taskByID(data.Tasks, log.TaskID)
		if task.AgentRunID != "" {
			runIDs[task.AgentRunID] = true
		}
		logs = append(logs, log)
		channelTaskIDs[log.TaskID] = true
	}
	if filter.ChannelID != "" {
		channelTasks := make([]model.Task, 0, len(tasks))
		for _, task := range tasks {
			if channelTaskIDs[task.ID] {
				channelTasks = append(channelTasks, task)
			}
		}
		tasks = channelTasks
	}
	runs := make([]model.CloudAgentExecution, 0, len(data.Runs))
	for _, run := range data.Runs {
		if runIDs[run.ID] && !run.CreatedAt.Before(filter.From) && run.CreatedAt.Before(filter.To) {
			runs = append(runs, run)
		}
	}
	events := make([]model.CloudAgentEventRecord, 0, len(data.Events))
	for _, event := range data.Events {
		if runIDs[event.RunID] && !event.CreatedAt.Before(filter.From) && event.CreatedAt.Before(filter.To) {
			events = append(events, event)
		}
	}
	return repository.ObservabilityData{Runs: runs, Tasks: tasks, Logs: logs, Events: events, Workers: data.Workers, Truncated: data.Truncated}
}

func taskByID(tasks []model.Task, id string) model.Task {
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	return model.Task{}
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
		e := observability.Event{TaskID: run.ID, RunID: run.ID, TraceID: observationTraceID(run.ID), SessionID: observationTraceID(run.ConversationID), Kind: observability.KindTask, Status: observability.Status(run.Status), StartedAt: run.CreatedAt, EndedAt: run.UpdatedAt, Key: "run:" + run.ID, ParentRunID: run.ParentID, Reason: run.FailureMessage}
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
