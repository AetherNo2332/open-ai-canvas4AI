package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestPiNativeCompactionTimeoutTerminatesRun(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionBranch(t, db, run)
	preparation := json.RawMessage(`{"firstKeptEntryId":"compact-assistant-1","tokensBefore":24000,"isSplitTurn":false,"messagesToSummarize":[{"role":"user","content":"Keep the first request"}],"turnPrefixMessages":[],"fileOps":{"read":[],"written":[],"edited":[]},"settings":{"enabled":true,"reserveTokens":10000,"keepRecentTokens":1000}}`)
	op, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{
		SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000, Preparation: preparation,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PiNativeCompactionModelRequest{CallID: "history", SystemPrompt: "You are a context summarization assistant.", Prompt: "Summarize this history", MaxTokens: 2000}
	call, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", call.TaskID).Updates(map[string]any{
		"status": model.TaskStatusFailed, "error": cloudAgentStepTimeoutError,
	}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil || view.Status != "failed" {
		t.Fatalf("native timeout response: view=%+v err=%v", view, err)
	}
	failed, state := agentInterjectionState(t, s, run.ID)
	if failed.Status != "failed" || !failed.CleanupPending || !agentHasEventWithReason(state, "run_failed", "model_step_timeout") {
		t.Fatalf("native summary timeout must terminate Agent: status=%s cleanup=%v", failed.Status, failed.CleanupPending)
	}
	if _, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input); err == nil {
		t.Fatal("terminal Agent allowed another compaction call")
	}
}

func TestPiNativeCompactionReusesBilledCallAndAcceptsNativeBoundary(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionBranch(t, db, run)
	preparation := json.RawMessage(`{"firstKeptEntryId":"compact-assistant-1","tokensBefore":24000,"isSplitTurn":false,"messagesToSummarize":[{"role":"user","content":"Keep the first request"}],"turnPrefixMessages":[],"fileOps":{"read":[],"written":[],"edited":[]},"settings":{"enabled":true,"reserveTokens":10000,"keepRecentTokens":1000}}`)
	op, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{
		SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000, Preparation: preparation,
	})
	if err != nil || op.Status != "prepared" || op.FirstKeptEntryID != "compact-assistant-1" {
		t.Fatalf("native begin: %+v %v", op, err)
	}
	input := PiNativeCompactionModelRequest{CallID: "history", SystemPrompt: "You are a context summarization assistant.", Prompt: "Summarize this history", MaxTokens: 2000}
	call, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil || call.TaskID == "" {
		t.Fatalf("native call: %+v %v", call, err)
	}
	again, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil || again.TaskID != call.TaskID {
		t.Fatalf("call replay: %+v %v", again, err)
	}
	altered := input
	altered.Prompt += "changed"
	if _, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, altered); err == nil {
		t.Fatal("changed call admitted")
	}
	var task model.Task
	db.First(&task, "id = ?", call.TaskID)
	var taskInput map[string]any
	json.Unmarshal([]byte(task.InputJSON), &taskInput)
	if taskInput["config"].(map[string]any)["systemPrompt"] != input.SystemPrompt || taskInput["textOptions"].(map[string]any)["maxOutputTokens"] != float64(2000) {
		t.Fatalf("native request parameters lost: %s", task.InputJSON)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", call.TaskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": `{"text":"## Goal\nPreserve film continuity","stopReasonKind":"stop","usage":{"input":100,"output":20,"totalTokens":120}}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil || replay.Status != "succeeded" {
		t.Fatalf("completed call replay: %+v %v", replay, err)
	}
	if _, err := s.PiFinishNativeContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID, PiNativeCompactionComplete{Summary: "invented summary"}); err == nil {
		t.Fatal("unbacked summary accepted")
	}
	ready, err := s.PiFinishNativeContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID, PiNativeCompactionComplete{Summary: "## Goal\nPreserve film continuity"})
	if err != nil || ready.Status != "succeeded" || ready.Fallback {
		t.Fatalf("native completion: %+v %v", ready, err)
	}
	entry, _ := json.Marshal(map[string]any{"type": "compaction", "id": "native-entry", "parentId": leaf,
		"summary": ready.Summary, "firstKeptEntryId": ready.FirstKeptEntryID, "tokensBefore": ready.TokensBefore, "details": ready.Details, "usage": ready.Usage})
	if _, err := s.PiCommitContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID, PiContextCompactionCommit{SessionRevision: revision, Entry: entry}); err != nil {
		t.Fatal(err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	if state.ContextCheckpoint.HistorySummary != "## Goal\nPreserve film continuity" || state.ContextCompaction != nil {
		t.Fatalf("native checkpoint lost: %+v", state.ContextCheckpoint)
	}
	var preparations int64
	db.Model(&model.CloudAgentMessageRecord{}).Where("run_id = ? AND kind = ?", run.ID, cloudAgentMessageKindCompaction).Count(&preparations)
	if preparations != 0 {
		t.Fatal("committed compaction retained its temporary preparation record")
	}
	var count int64
	db.Model(&model.Task{}).Where("operation = ?", cloudAgentContextCompactionOperation).Count(&count)
	if count != 1 {
		t.Fatalf("native replay billed %d tasks", count)
	}
}

func TestPiNativeCompactionLargePreparationLivesOutsideControlState(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionBranch(t, db, run)
	var first model.CloudAgentPiEntry
	db.Where("entry_id = ?", "compact-user-1").First(&first)
	var firstEntry map[string]any
	json.Unmarshal([]byte(first.EntryJSON), &firstEntry)
	firstEntry["message"].(map[string]any)["content"] = strings.Repeat("data", 150000)
	firstJSON, _ := json.Marshal(firstEntry)
	db.Model(&model.CloudAgentPiEntry{}).Where("session_id = ? AND entry_id = ?", first.SessionID, first.EntryID).Update("entry_json", string(firstJSON))
	prep, _ := json.Marshal(map[string]any{"firstKeptEntryId": "compact-assistant-1", "tokensBefore": 24000, "isSplitTurn": false,
		"messagesToSummarize": []map[string]any{{"role": "user", "content": strings.Repeat("data", 150000)}},
		"turnPrefixMessages":  []any{}, "fileOps": map[string]any{"read": []string{}, "written": []string{}, "edited": []string{}}})
	op, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000, Preparation: prep})
	if err != nil {
		t.Fatal(err)
	}
	stored, state := reloadPiRun(t, s, run.ID)
	if len(stored.StateJSON) >= cloudAgentStateHardLimitBytes || string(state.ContextCompaction.PiNative.Preparation) != string(prep) {
		t.Fatal("large preparation leaked into bounded state or could not be restored")
	}
	ready, err := s.PiContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID)
	if err != nil || string(ready.NativePreparation) != string(prep) {
		t.Fatalf("large preparation recovery: %v", err)
	}
	db.Model(&model.CloudAgentMessageRecord{}).Where("run_id = ? AND kind = ?", run.ID, cloudAgentMessageKindCompaction).Update("message_json", `{}`)
	if _, err := s.PiContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID); err == nil {
		t.Fatal("corrupt preparation restored")
	}
}

func TestPiNativeCompactionRecoveryEditCanRetainAnInvisibleSuffix(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionBranch(t, db, run)
	session, _, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"context_edit","id":"recovery-edit","parentId":"compact-user-current","targetId":"compact-assistant-1","replacement":null}`
	db.Create(&model.CloudAgentPiEntry{SessionID: session.ID, Sequence: 4, EntryID: "recovery-edit", ParentID: leaf, UserID: run.UserID, RunID: run.ID, EntryJSON: raw})
	db.Model(&model.CloudAgentPiSession{}).Where("id = ?", session.ID).Updates(map[string]any{"active_leaf_id": "recovery-edit", "revision": revision + 1})
	prep := json.RawMessage(`{"firstKeptEntryId":"recovery-edit","tokensBefore":24000,"isSplitTurn":false,"messagesToSummarize":[{"role":"user","content":"Keep the first request"},{"role":"user","content":"Current unanswered request"}],"turnPrefixMessages":[],"fileOps":{"read":[],"written":[],"edited":[]}}`)
	op, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{SessionRevision: revision + 1, ActiveLeafID: "recovery-edit", Reason: "overflow", TokensBefore: 24000, Preparation: prep})
	if err != nil {
		t.Fatal(err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	if len(state.Canonical.Messages) != 2 || state.ContextCompaction.PiFirstKeptIndex != 2 || op.FirstKeptEntryID != "recovery-edit" {
		t.Fatalf("abandoned assistant or wrong invisible boundary: %+v", state.ContextCompaction)
	}
}

func TestPiNativeCompactionRecoveryReplacementRemovesOldToolCalls(t *testing.T) {
	branch := []cloudAgentPiCompactionEntry{
		{Type: "message", ID: "user", Message: json.RawMessage(`{"role":"user","content":"request"}`)},
		{Type: "message", ID: "assistant", Message: json.RawMessage(`{"role":"assistant","content":[{"type":"toolCall","id":"abandoned-call","name":"canvas_get_state","arguments":{}}]}`)},
	}
	var edit cloudAgentPiCompactionEntry
	if err := json.Unmarshal([]byte(`{"type":"context_edit","id":"edit","targetId":"assistant","replacement":{"content":[{"type":"text","text":"recovered answer"}]}}`), &edit); err != nil {
		t.Fatal(err)
	}
	branch = append(branch, edit)
	source, err := cloudAgentPiCompactionSourceForBranch(branch, "edit")
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Messages) != 2 || source.Messages[1]["content"] != "recovered answer" || cloudAgentPiHasToolCalls(source.Messages[1]["tool_calls"]) {
		t.Fatalf("replacement did not match Pi projection: %+v", source.Messages)
	}
}

func TestPiNativeCompactionWorkerPreservesPromptAndSettlesOnce(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("REDIS_URL", "")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] == true || len(creationMaps(body["tools"])) > 0 || !strings.Contains(fmt.Sprint(body["messages"]), "native-summary-policy") {
			t.Errorf("summary request lost native policy or exposed tools: %v", body)
		}
		if _, exists := body["tools"]; exists {
			t.Error("tool-free summary must omit tools, not send an empty array")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "Native worker summary"}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}})
	}))
	defer upstream.Close()
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	s = New(s.repo, s.dataDir)
	t.Cleanup(func() { _ = s.Close() })
	if err := db.Model(&model.ModelChannel{}).Where("id = ?", "channel").Updates(map[string]any{"base_url": upstream.URL, "api_key": "test-only"}).Error; err != nil {
		t.Fatal(err)
	}
	revision, leaf := seedPiCompactionMessages(t, db, run, []map[string]any{
		{"role": "user", "content": "film"}, {"role": "assistant", "content": "first answer"}, {"role": "user", "content": "continue"},
	})
	prep := json.RawMessage(`{"firstKeptEntryId":"pi-compact-message-2","tokensBefore":24000,"isSplitTurn":false,"messagesToSummarize":[{"role":"user","content":"film"}],"turnPrefixMessages":[],"fileOps":{"read":[],"written":[],"edited":[]}}`)
	op, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000, Preparation: prep})
	if err != nil {
		t.Fatal(err)
	}
	input := PiNativeCompactionModelRequest{CallID: "history", SystemPrompt: "native-summary-policy", Prompt: "Summarize the film", MaxTokens: min(256, op.SummaryMaxTokens)}
	call, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessNextTask(); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessNextTask(); err != nil {
		t.Fatal(err)
	}
	view, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
	if err != nil || view.Status != "succeeded" {
		var task model.Task
		db.First(&task, "id = ?", call.TaskID)
		t.Fatalf("native worker: %+v %v taskError=%s result=%s", view, err, task.Error, task.ResultJSON)
	}
	ready, err := s.PiFinishNativeContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID, PiNativeCompactionComplete{Summary: "Native worker summary"})
	if err != nil || ready.Fallback {
		t.Fatalf("native finalize: %+v %v", ready, err)
	}
	var usage map[string]int
	if json.Unmarshal(ready.Usage, &usage) != nil || usage["input"] != 100 || usage["output"] != 20 {
		t.Fatalf("summary usage lost: %s", ready.Usage)
	}
	var orders []model.BillingOrder
	if err := db.Where("task_id = ?", call.TaskID).Find(&orders).Error; err != nil || len(orders) != 1 || orders[0].Status != model.BillingStatusSettled || calls.Load() != 1 {
		t.Fatalf("native summary did not settle exactly once: orders=%+v requests=%d error=%v", orders, calls.Load(), err)
	}
}

func TestPiNativeCompactionSplitRestartFallbackAndStaleSource(t *testing.T) {
	for _, stop := range []string{"stop", "length"} {
		t.Run(stop, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			revision, leaf := seedPiCompactionMessages(t, db, run, []map[string]any{{"role": "user", "content": "history"}, {"role": "assistant", "content": "answer"}, {"role": "user", "content": "prefix"}, {"role": "assistant", "content": "kept"}, {"role": "user", "content": "continue"}})
			prep := json.RawMessage(`{"firstKeptEntryId":"pi-compact-message-4","tokensBefore":24000,"isSplitTurn":true,"messagesToSummarize":[{"role":"user","content":"history"},{"role":"assistant","content":"answer"}],"turnPrefixMessages":[{"role":"user","content":"prefix"}],"fileOps":{"read":[],"written":[],"edited":[]}}`)
			op, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000, Preparation: prep})
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"history", "turnPrefix"} {
				input := PiNativeCompactionModelRequest{CallID: id, SystemPrompt: "summary", Prompt: id, MaxTokens: 256}
				call, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
				if err != nil {
					t.Fatal(err)
				}
				kind := "stop"
				if id == "turnPrefix" {
					kind = stop
				}
				result, _ := json.Marshal(map[string]any{"text": id, "stopReasonKind": kind, "usage": map[string]int{"input": 10, "output": 2}})
				db.Model(&model.Task{}).Where("id = ?", call.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(result)})
				// A new service instance must recover the same operation and billed call.
				restarted := New(s.repo, s.dataDir)
				t.Cleanup(func() { _ = restarted.Close() })
				again, err := restarted.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, op.OperationID, input)
				if err != nil || again.TaskID != call.TaskID {
					t.Fatalf("restart replay: %+v %v", again, err)
				}
			}
			input := PiNativeCompactionComplete{Summary: "history\n\n---\n\n**Turn Context (split turn):**\n\nturnPrefix"}
			if stop == "length" {
				input = PiNativeCompactionComplete{Fallback: true}
			}
			ready, err := s.PiFinishNativeContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID, input)
			if err != nil || ready.Fallback != (stop == "length") {
				t.Fatalf("split completion: %+v %v", ready, err)
			}
			var count int64
			db.Model(&model.Task{}).Where("operation = ?", cloudAgentContextCompactionOperation).Count(&count)
			if count != 2 {
				t.Fatalf("split restart created %d tasks", count)
			}
			db.Model(&model.CloudAgentPiSession{}).Where("conversation_id = ?", run.ConversationID).Update("revision", revision+1)
			if _, err := s.PiFinishNativeContextCompaction("user", run.ID, run.LeaseOwner, op.OperationID, input); err == nil {
				t.Fatal("stale source accepted")
			}
		})
	}
}

func TestPiNativeCompactionRejectsToolResultCutWithoutBilling(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionMessages(t, db, run, []map[string]any{
		{"role": "user", "content": "request"}, {"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "call", "name": "canvas_get_state", "arguments": map[string]any{}}}},
		{"role": "toolResult", "toolCallId": "call", "toolName": "canvas_get_state", "content": "state"},
	})
	prep := json.RawMessage(`{"firstKeptEntryId":"pi-compact-message-3","tokensBefore":24000}`)
	if _, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000, Preparation: prep}); err == nil {
		t.Fatal("tool result boundary admitted")
	}
	var count int64
	db.Model(&model.Task{}).Where("operation = ?", cloudAgentContextCompactionOperation).Count(&count)
	if count != 0 {
		t.Fatalf("invalid native preparation created %d tasks", count)
	}
}
