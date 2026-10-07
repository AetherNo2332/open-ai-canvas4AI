package app

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type diagnosticActivityRecords struct {
	Canvases  []map[string]any
	History   []map[string]any
	Mutations []map[string]any
	Runs      []map[string]any
	Events    []map[string]any
	Messages  []map[string]any
}

// These projections intentionally exclude raw checkpoints, canvas payloads,
// model reasoning, tool arguments and tool result bodies.
func collectDiagnosticActivity(data *repository.DiagnosticActivity, collection *diagnosticCollection) {
	collection.Truncated = collection.Truncated || data.Truncated
	for _, canvas := range data.Canvases {
		collection.Activity.Canvases = append(collection.Activity.Canvases, map[string]any{
			"canvasId": sanitizeDiagnosticIdentifier(canvas.ID), "projectId": sanitizeDiagnosticIdentifier(canvas.ProjectID),
			"title": redactDiagnosticText(canvas.Title, 240), "revision": canvas.Revision, "createdAt": canvas.CreatedAt, "updatedAt": canvas.UpdatedAt,
		})
	}
	for _, snapshot := range data.Snapshots {
		collection.Activity.History = append(collection.Activity.History, map[string]any{
			"id": sanitizeDiagnosticIdentifier(snapshot.ID), "canvasId": sanitizeDiagnosticIdentifier(snapshot.CanvasID), "revision": snapshot.Revision,
			"title": redactDiagnosticText(snapshot.Title, 240), "reason": redactDiagnosticText(snapshot.Reason, 80), "nodeCount": snapshot.NodeCount,
			"connectionCount": snapshot.ConnectionCount, "payloadBytes": snapshot.PayloadBytes, "contentUpdatedAt": snapshot.ContentUpdatedAt, "createdAt": snapshot.CreatedAt,
		})
	}
	for _, mutation := range data.Mutations {
		collection.Activity.Mutations = append(collection.Activity.Mutations, map[string]any{
			"id": sanitizeDiagnosticIdentifier(mutation.ID), "canvasId": sanitizeDiagnosticIdentifier(mutation.CanvasID), "runId": sanitizeDiagnosticIdentifier(mutation.RunID),
			"stepId": sanitizeDiagnosticIdentifier(mutation.StepID), "operation": redactDiagnosticText(mutation.Operation, 80), "status": redactDiagnosticText(mutation.Status, 32),
			"hasSubmittedTask": mutation.HasSubmittedTask, "createdAt": mutation.CreatedAt, "undoneAt": mutation.UndoneAt,
		})
	}
	for _, run := range data.Runs {
		collection.Activity.Runs = append(collection.Activity.Runs, diagnosticAgentRun(run))
	}
	for _, row := range data.Events {
		var event struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		err := json.Unmarshal([]byte(row.EventJSON), &event)
		record := map[string]any{"runId": sanitizeDiagnosticIdentifier(row.RunID), "sequence": row.Sequence, "createdAt": row.CreatedAt, "type": redactDiagnosticText(event.Type, 80)}
		if err != nil {
			record["type"] = "unreadable_event"
		} else {
			record["payload"] = diagnosticEventPayload(event.Payload, collection)
		}
		collection.Activity.Events = append(collection.Activity.Events, record)
	}
	for _, row := range data.PiEntries {
		var entry struct {
			Type    string         `json:"type"`
			Message map[string]any `json:"message"`
		}
		if json.Unmarshal([]byte(row.EntryJSON), &entry) != nil {
			collection.Activity.Messages = append(collection.Activity.Messages, map[string]any{"runId": sanitizeDiagnosticIdentifier(row.RunID), "sequence": row.Sequence, "entryId": sanitizeDiagnosticIdentifier(row.EntryID), "source": "pi_session", "createdAt": row.CreatedAt, "role": "unreadable_message", "payloadOmitted": row.EntryJSON == ""})
			continue
		}
		if entry.Type != "message" {
			continue
		}
		if record := diagnosticAgentMessage(row.RunID, row.Sequence, "pi_session", &row.CreatedAt, entry.Message, collection); record != nil {
			record["entryId"] = sanitizeDiagnosticIdentifier(row.EntryID)
			collection.Activity.Messages = append(collection.Activity.Messages, record)
		}
	}
	for _, row := range data.Messages {
		var message map[string]any
		if json.Unmarshal([]byte(row.MessageJSON), &message) != nil {
			collection.Activity.Messages = append(collection.Activity.Messages, map[string]any{"runId": sanitizeDiagnosticIdentifier(row.RunID), "sequence": row.Sequence, "source": "legacy_transcript", "role": "unreadable_message", "payloadOmitted": row.MessageJSON == ""})
			continue
		}
		if record := diagnosticAgentMessage(row.RunID, row.Sequence, "legacy_transcript", nil, message, collection); record != nil {
			collection.Activity.Messages = append(collection.Activity.Messages, record)
		}
	}
}

func diagnosticAgentRun(run model.CloudAgentExecution) map[string]any {
	record := map[string]any{
		"runId": sanitizeDiagnosticIdentifier(run.ID), "conversationId": sanitizeDiagnosticIdentifier(run.ConversationID), "parentId": sanitizeDiagnosticIdentifier(run.ParentID),
		"canvasId": sanitizeDiagnosticIdentifier(run.CanvasID), "status": redactDiagnosticText(run.Status, 32), "engine": redactDiagnosticText(run.Engine, 24), "revision": run.Revision,
		"runtimePhase": redactDiagnosticText(run.RuntimePhase, 32), "waitKind": redactDiagnosticText(run.WaitKind, 32), "waitId": sanitizeDiagnosticIdentifier(run.WaitID), "waitReason": redactDiagnosticText(run.WaitReason, 160),
		"activeTaskId": sanitizeDiagnosticIdentifier(run.ActiveTaskID), "mediaTaskId": sanitizeDiagnosticIdentifier(run.MediaTaskID), "cleanupPending": run.CleanupPending,
		"failureMessage": redactDiagnosticText(run.FailureMessage, 1000), "eventCount": run.EventCount, "messageCount": run.MessageCount,
		"checkpointVersion": run.CheckpointVersion, "createdAt": run.CreatedAt, "updatedAt": run.UpdatedAt,
	}
	var state struct {
		Step              int    `json:"step"`
		CallIndex         int    `json:"callIndex"`
		ActiveTaskID      string `json:"activeTaskId"`
		MediaTaskID       string `json:"mediaTaskId"`
		LastStepTaskID    string `json:"lastStepTaskId"`
		PiToolBatchTaskID string `json:"piToolBatchTaskId"`
		Calls             []struct {
			ID       string `json:"id"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"calls"`
	}
	err := json.Unmarshal([]byte(run.StateJSON), &state)
	readable := err == nil && strings.HasPrefix(strings.TrimSpace(run.StateJSON), "{")
	record["checkpointReadable"] = readable
	if !readable {
		return record
	}
	record["step"], record["callIndex"], record["callCount"] = state.Step, state.CallIndex, len(state.Calls)
	record["checkpointActiveTaskId"], record["checkpointMediaTaskId"] = sanitizeDiagnosticIdentifier(state.ActiveTaskID), sanitizeDiagnosticIdentifier(state.MediaTaskID)
	record["lastStepTaskId"], record["piToolBatchTaskId"] = sanitizeDiagnosticIdentifier(state.LastStepTaskID), sanitizeDiagnosticIdentifier(state.PiToolBatchTaskID)
	validIndex := state.CallIndex >= 0 && state.CallIndex <= len(state.Calls)
	record["callIndexValid"] = validIndex
	if validIndex {
		record["pendingCallCount"] = len(state.Calls) - state.CallIndex
		calls := make([]map[string]string, 0, len(state.Calls))
		for i, call := range state.Calls {
			status := "settled"
			if i >= state.CallIndex {
				status = "pending"
			}
			calls = append(calls, map[string]string{"callId": sanitizeDiagnosticIdentifier(call.ID), "toolName": redactDiagnosticText(call.Function.Name, 120), "state": status})
		}
		record["calls"] = calls
	}
	return record
}

func diagnosticEventPayload(payload map[string]any, collection *diagnosticCollection) map[string]any {
	result := make(map[string]any)
	for _, key := range []string{"taskId", "callId", "tool", "toolName", "nodeId", "stepId", "status", "phase", "reason", "error", "message", "text", "operation", "waitKind", "waitReason"} {
		if value, ok := payload[key].(string); ok {
			result[key] = diagnosticActivityText(value, collection)
		}
	}
	for _, key := range []string{"step", "callIndex", "callCount", "pendingCallCount", "durationMs", "isError", "success", "taskSubmitted"} {
		switch value := payload[key].(type) {
		case float64, bool:
			result[key] = value
		}
	}
	return result
}

func diagnosticAgentMessage(runID string, sequence int, source string, at *time.Time, message map[string]any, collection *diagnosticCollection) map[string]any {
	role, _ := message["role"].(string)
	if message["type"] == "function_call" {
		role = "assistant"
	}
	if role != "user" && role != "assistant" && role != "tool" && role != "toolResult" {
		return nil
	}
	record := map[string]any{"runId": sanitizeDiagnosticIdentifier(runID), "sequence": sequence, "source": source, "role": role}
	if at != nil {
		record["createdAt"] = at
	} else {
		record["timeScope"] = "active_run_transcript_snapshot"
	}
	for _, key := range []string{"toolCallId", "tool_call_id", "toolName", "name", "stopReason"} {
		if value, ok := message[key].(string); ok {
			record[key] = redactDiagnosticText(value, 120)
		}
	}
	if value, ok := message["isError"].(bool); ok {
		record["isError"] = value
	}
	// Tool bodies can contain complete canvas payloads or media; receipts are
	// represented by identity/status here and the corresponding event errors.
	if role == "tool" || role == "toolResult" {
		return record
	}
	var texts []string
	var calls []map[string]string
	if message["type"] == "function_call" {
		calls = append(calls, diagnosticMessageCall(message))
	}
	if rawCalls, ok := message["tool_calls"].([]any); ok {
		for _, raw := range rawCalls {
			if call, ok := raw.(map[string]any); ok {
				calls = append(calls, diagnosticMessageCall(call))
			}
		}
	}
	switch content := message["content"].(type) {
	case string:
		texts = append(texts, content)
	case []any:
		for _, raw := range content {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := block["type"].(string)
			if kind == "toolCall" {
				calls = append(calls, diagnosticMessageCall(block))
			}
			if kind == "text" || kind == "input_text" || kind == "output_text" {
				if text, ok := block["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
	}
	record["text"] = diagnosticActivityText(strings.Join(texts, "\n"), collection)
	if len(calls) > 0 {
		record["calls"] = calls
	}
	return record
}

func diagnosticMessageCall(call map[string]any) map[string]string {
	id, _ := call["id"].(string)
	if value, ok := call["call_id"].(string); ok {
		id = value
	}
	name, _ := call["name"].(string)
	if function, ok := call["function"].(map[string]any); ok {
		name, _ = function["name"].(string)
	}
	return map[string]string{"callId": sanitizeDiagnosticIdentifier(id), "toolName": redactDiagnosticText(name, 120)}
}

func diagnosticActivityText(text string, collection *diagnosticCollection) string {
	if utf8.RuneCountInString(text) > diagnosticMaxEventText {
		collection.Truncated = true
	}
	return redactDiagnosticText(text, diagnosticMaxEventText)
}
