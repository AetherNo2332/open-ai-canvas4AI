package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// These helpers simulate the Node worker through the public Pi bridge. Only the
// provider response is faked; admission, checkpoints and business tools persist
// through the same transitions that the real worker uses.
func agentStartPiModelStep(t *testing.T, s *Service, runID string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	run, state := agentInterjectionState(t, s, runID)
	if run.LeaseOwner == "" {
		claimed, err := s.ClaimPiAgent("worker-a")
		if err != nil || claimed == nil || claimed.RunID != runID {
			t.Fatalf("claim Pi run %s: snapshot=%+v err=%v", runID, claimed, err)
		}
		run, state = agentInterjectionState(t, s, runID)
	}
	if state.ActiveTaskID != "" {
		return run, state
	}
	modelFailureNudge := ""
	if state.ActiveTaskID == "" && state.LastStepTaskID == state.PiModelFailureTaskID {
		modelFailureNudge = state.PiModelFailureNudge
	}
	noToolNudge := ""
	if state.ActiveTaskID == "" && state.LastStepTaskID == state.PiNoToolTaskID {
		noToolNudge = state.PiNoToolNudge
	}
	if nudge := firstNonEmpty(modelFailureNudge, noToolNudge); nudge != "" {
		// session.prompt(decision.nudge) emits a user checkpoint before the retry.
		// Without that message a truncated retry has the previous step's identical
		// fingerprint, so Go correctly returns the already-completed model task.
		agentPiCheckpointForTest(t, s, runID, "", map[string]any{
			"role": "user", "content": []map[string]any{{"type": "text", "text": nudge}},
		}, nil)
		run, state = agentInterjectionState(t, s, runID)
	}
	request, _ := piFirstStepRequest(&state)
	request.Canonical.Messages = cloudAgentCanonicalWithPlan(&state).Messages
	if !cloudAgentAwaitingFirstStep(&state) {
		request.Harness = cloudAgentFrozenHarness(state.Snapshot)
		request.HarnessHash = state.Snapshot.HarnessHash
		request.Canonical = cloudAgentCanonicalWithPlan(&state)
		request.Canonical.SystemPrompt = cloudAgentRenderTestPrompt(state.Canonical.SystemPrompt, request.Harness)
		request.Canonical.Tools = cloudAgentVisibleToolsForCategories(state.Canonical.Tools, cloudAgentActivatedCategories(&state), nil, state.ToolScope)
	}
	snapshot, err := s.PiAgentSnapshot("user", runID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range snapshot.PiMessages {
		var message map[string]any
		if json.Unmarshal(raw, &message) == nil && stringField(message, "role") == "user" {
			request.Canonical.Messages = append(request.Canonical.Messages, map[string]any{"role": "user", "content": piAssistantText(message["content"])})
		}
	}
	if _, err := s.PiModelStep("user", runID, run.LeaseOwner, request); err != nil {
		t.Fatalf("start Pi model step: %v", err)
	}
	return agentInterjectionState(t, s, runID)
}

func agentPiToolTaskID(t *testing.T, s *Service, runID string) string {
	t.Helper()
	_, state := agentInterjectionState(t, s, runID)
	return firstNonEmpty(state.PiToolBatchTaskID, state.LastStepTaskID)
}

func agentPiCheckpointForTest(t *testing.T, s *Service, runID, taskID string, message map[string]any, interjectionIDs []string) {
	t.Helper()
	run, _ := agentInterjectionState(t, s, runID)
	snapshot, err := s.PiAgentSnapshot("user", runID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	sequence := len(snapshot.PiMessages) + 1
	entryID := fmt.Sprintf("test-%s-%d", runID, sequence)
	var parent any
	if snapshot.PiActiveLeafID != "" {
		parent = snapshot.PiActiveLeafID
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(map[string]any{"type": "message", "id": entryID, "parentId": parent,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": message})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PiCheckpointMessage("user", runID, run.LeaseOwner, PiMessageCheckpoint{
		Sequence: sequence, Message: encoded, TaskID: taskID, InterjectionIDs: interjectionIDs,
		SessionRevision: snapshot.PiSessionRevision, ActiveLeafID: entryID, SessionEntries: []json.RawMessage{entry},
	}); err != nil {
		t.Fatalf("checkpoint Pi message: %v", err)
	}
}

func agentPiDeliverInterjectionsForTest(t *testing.T, s *Service, runID string) {
	t.Helper()
	_, state := agentInterjectionState(t, s, runID)
	if len(state.PendingInterjections) == 0 {
		return
	}
	var ids, texts []string
	for _, item := range state.PendingInterjections {
		ids = append(ids, item.ID)
		texts = append(texts, "【用户插话】"+item.Text)
	}
	agentPiCheckpointForTest(t, s, runID, "", map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": strings.Join(texts, "\n")}}}, ids)
}

func agentSettleStep(t *testing.T, s *Service, db *gorm.DB, runID, text, callID, tool, args string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	var calls []cloudAgentCall
	if tool != "" {
		calls = []cloudAgentCall{piAgentTestCall(callID, tool, args)}
	}
	return agentSettlePiOutputForTest(t, s, db, runID, text, calls)
}

func agentSettleTextStep(t *testing.T, s *Service, db *gorm.DB, runID, text string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	return agentSettleStep(t, s, db, runID, text, "", "", "")
}

func agentSettleToolStep(t *testing.T, s *Service, db *gorm.DB, runID, callID, tool, args string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	return agentSettleStep(t, s, db, runID, "", callID, tool, args)
}

func agentSettleBatch(t *testing.T, s *Service, db *gorm.DB, runID string, calls []cloudAgentCall) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	return agentSettlePiOutputForTest(t, s, db, runID, "", calls)
}

func agentSettlePiOutputForTest(t *testing.T, s *Service, db *gorm.DB, runID, text string, calls []cloudAgentCall) (*model.CloudAgentExecution, cloudAgentRuntime) {
	run, state := agentStagePiOutputForTest(t, s, db, runID, text, calls)
	if len(calls) == 0 {
		if _, err := s.PiNoToolTurn("user", runID, run.LeaseOwner, state.ActiveTaskID); err != nil {
			t.Fatalf("Pi no-tool decision: %v", err)
		}
	} else {
		for _, call := range calls {
			if err := agentPiExecuteCallForTest(t, s, runID, call.ID); err != nil {
				t.Fatalf("Pi tool %s: %v", call.ID, err)
			}
		}
	}
	return agentInterjectionState(t, s, runID)
}

// agentStagePiOutputForTest persists a provider response and admits its tool
// batch through the production Pi bridge without executing the calls.
func agentStagePiOutputForTest(t *testing.T, s *Service, db *gorm.DB, runID, text string, calls []cloudAgentCall) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	run, state := agentStartPiModelStep(t, s, runID)
	encoded, err := json.Marshal(map[string]any{"text": text, "toolCalls": calls, "stopReasonKind": cloudAgentStopKindStop})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", state.ActiveTaskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": string(encoded),
	}).Error; err != nil {
		t.Fatal(err)
	}
	content := make([]map[string]any, 0, len(calls)+1)
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, call := range calls {
		var args any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			t.Fatal(err)
		}
		content = append(content, map[string]any{"type": "toolCall", "id": call.ID, "name": call.Function.Name, "arguments": args})
	}
	agentPiCheckpointForTest(t, s, runID, state.ActiveTaskID, map[string]any{"role": "assistant", "content": content}, nil)
	if len(calls) == 0 {
		return agentInterjectionState(t, s, runID)
	}
	if err := s.PiToolBatch("user", runID, run.LeaseOwner, PiToolBatchRequest{TaskID: state.ActiveTaskID, Calls: calls}); err != nil {
		t.Fatalf("Pi tool batch: %v", err)
	}
	return agentInterjectionState(t, s, runID)
}

func agentPiExecuteCallForTest(t *testing.T, s *Service, runID, callID string) error {
	t.Helper()
	run, state := agentInterjectionState(t, s, runID)
	taskID := firstNonEmpty(state.PiToolBatchTaskID, state.LastStepTaskID)
	receipt, err := s.PiToolAdvance("user", runID, run.LeaseOwner, taskID, callID)
	if err != nil || receipt == nil {
		return err
	}
	if receipt.Pending || receipt.Terminated {
		return nil
	}
	var call cloudAgentCall
	run, state = agentInterjectionState(t, s, runID)
	for _, candidate := range state.Calls {
		if candidate.ID == callID {
			call = candidate
			break
		}
	}
	if call.ID == "" {
		return fmt.Errorf("Pi tool %s is absent from admitted batch", callID)
	}
	if !cloudAgentRunTerminal(run.Status) {
		agentPiCheckpointToolReceiptForTest(t, s, runID, call, receipt)
	}
	return nil
}

// advancePiAgentForTest performs one boundary transition, matching the runner's
// checkpoint-before-batch ordering. Call agentStartPiModelStep before writing a
// fake result: holding reservations are never executable model responses.
func advancePiAgentForTest(t *testing.T, s *Service, runID string) error {
	t.Helper()
	run, state := agentInterjectionState(t, s, runID)
	if cloudAgentRunTerminal(run.Status) {
		return nil
	}
	if state.ActiveTaskID != "" {
		task, err := s.repo.TaskForUser("user", state.ActiveTaskID)
		if err != nil {
			return err
		}
		if !cloudAgentTaskTerminal(task.Status) {
			return nil
		}
		if task.Status != model.TaskStatusSucceeded {
			return s.PiFailModelStep("user", runID, run.LeaseOwner, task.ID)
		}
		var result struct {
			Text           string           `json:"text"`
			ReasoningText  string           `json:"reasoningText"`
			ToolCalls      []cloudAgentCall `json:"toolCalls"`
			StopReasonKind string           `json:"stopReasonKind"`
		}
		if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
			return err
		}
		if strings.TrimSpace(result.StopReasonKind) == "" {
			result.StopReasonKind = cloudAgentStopKindUnknown
		}
		content := make([]map[string]any, 0, len(result.ToolCalls)+2)
		if result.ReasoningText != "" {
			content = append(content, map[string]any{"type": "thinking", "thinking": result.ReasoningText})
		}
		if result.Text != "" {
			content = append(content, map[string]any{"type": "text", "text": result.Text})
		}
		for _, call := range result.ToolCalls {
			var args any
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return err
			}
			content = append(content, map[string]any{"type": "toolCall", "id": call.ID, "name": call.Function.Name, "arguments": args})
		}
		agentPiCheckpointForTest(t, s, runID, task.ID, map[string]any{"role": "assistant", "content": content}, nil)
		if len(result.ToolCalls) > 0 && cloudAgentStepStopDisposition(&state, result.StopReasonKind) == cloudAgentStepDispositionAccept {
			return s.PiToolBatch("user", runID, run.LeaseOwner, PiToolBatchRequest{TaskID: task.ID, Calls: result.ToolCalls})
		}
		_, err = s.PiNoToolTurn("user", runID, run.LeaseOwner, task.ID)
		return err
	}
	if state.CallIndex < len(state.Calls) {
		call := state.Calls[state.CallIndex]
		receipt, err := s.PiToolAdvance("user", runID, run.LeaseOwner, state.PiToolBatchTaskID, call.ID)
		if err == nil && receipt != nil && !receipt.Pending && !receipt.Terminated {
			latest, _ := agentInterjectionState(t, s, runID)
			if !cloudAgentRunTerminal(latest.Status) {
				agentPiCheckpointToolReceiptForTest(t, s, runID, call, receipt)
			}
		}
		return err
	}
	agentPiDeliverInterjectionsForTest(t, s, runID)
	agentStartPiModelStep(t, s, runID)
	return nil
}

func agentPiCheckpointToolReceiptForTest(t *testing.T, s *Service, runID string, call cloudAgentCall, receipt *PiToolReceipt) {
	t.Helper()
	agentPiCheckpointForTest(t, s, runID, "", map[string]any{
		"role": "toolResult", "toolCallId": call.ID, "toolName": call.Function.Name,
		"content": []map[string]any{{"type": "text", "text": string(receipt.Result)}}, "isError": receipt.IsError,
	}, nil)
}
