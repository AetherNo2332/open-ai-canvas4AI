package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestStoryboardChunkRetiredEpochCannotReplay(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	createCloudAgentStoryboardForTest(t, s, canvas)
	policy, _ := s.RuntimePolicy()
	before, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ := creationDocument(before.PayloadJSON)
	args := map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "append_rows", "operationId": "old-epoch/chunk-1", "rows": []map[string]any{{"durationSeconds": 5.0, "plotDescription": "one historical append"}}}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "old-op", args), policy); err != nil {
		t.Fatal(err)
	}
	current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	snapshot := &model.CanvasSnapshot{ID: "before-restore-test", CanvasID: canvas.ID, UserID: "user", Revision: current.Revision, PayloadJSON: current.PayloadJSON, Reason: "before_restore", CreatedAt: time.Now()}
	current.PayloadJSON = before.PayloadJSON
	if err := s.repo.SaveCanvasWithSnapshot(current, snapshot, nil, nil, time.Now().Add(-time.Hour), 20, true); err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "stale-retry", args), policy); err == nil {
		t.Fatal("retired epoch operation silently executed again after restore")
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ = creationDocument(stored.PayloadJSON)
	_, _, rows, _ := storyboardNodeFromDocument(doc, "storyboard-1")
	if len(rows) != 2 {
		t.Fatal("retired operation changed restored rows")
	}
	status, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "retired-status", map[string]any{"nodeId": "storyboard-1", "view": "summary", "operationIds": []string{"old-epoch/chunk-1"}}))
	if err != nil {
		t.Fatal(err)
	}
	if creationMaps(status.(map[string]any)["operations"])[0]["status"] != "retired_epoch" {
		t.Fatal("retired operation reported as never executed")
	}
}

func TestStoryboardChunkRecoveryPersistsAndDeduplicates(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	createCloudAgentStoryboardForTest(t, s, canvas)
	policy, _ := s.RuntimePolicy()
	var lastArgs map[string]any
	var last map[string]any
	for chunk := 1; chunk <= 3; chunk++ {
		current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		doc, _ := creationDocument(current.PayloadJSON)
		lastArgs = map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "append_rows", "operationId": fmt.Sprintf("fill-task/chunk-%d", chunk), "rows": []map[string]any{{"durationSeconds": 5.0, "plotDescription": fmt.Sprintf("chunk %d", chunk)}}}
		result, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "reused-provider-id", lastArgs), policy)
		if err != nil {
			t.Fatal(err)
		}
		last = result.(map[string]any)
	}
	// A new repository has no runtime state from the interrupted worker.
	restarted := repository.New(db)
	result, err := applyCloudAgentStoryboardMutation(restarted, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "another-step-id", lastArgs), policy)
	if err != nil {
		t.Fatal(err)
	}
	replayed := result.(map[string]any)
	a, _ := json.Marshal(last["createdRows"])
	b, _ := json.Marshal(replayed["createdRows"])
	if replayed["replayed"] != true || string(a) != string(b) {
		t.Fatalf("lost durable result: %v", replayed)
	}
	current, _ := restarted.CanvasProjectForUser("user", canvas.ID)
	doc, _ := creationDocument(current.PayloadJSON)
	_, _, rows, _ := storyboardNodeFromDocument(doc, "storyboard-1")
	if len(rows) != 5 {
		t.Fatalf("duplicate append on recovery: %d", len(rows))
	}
	lastArgs["rows"] = []map[string]any{{"durationSeconds": 5.0, "plotDescription": "different request"}}
	if _, err := applyCloudAgentStoryboardMutation(restarted, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "mismatch", lastArgs), policy); err == nil {
		t.Fatal("same operationId accepted different arguments")
	}
	status, err := cloudAgentReadStoryboard(restarted, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "progress", map[string]any{"nodeId": "storyboard-1", "view": "summary", "operationIds": []string{"fill-task/chunk-1", "fill-task/chunk-2", "fill-task/chunk-3", "fill-task/chunk-4"}}))
	if err != nil {
		t.Fatal(err)
	}
	operations := creationMaps(status.(map[string]any)["operations"])
	if len(operations) != 4 || operations[2]["status"] != "committed" || operations[3]["status"] != "not_found" {
		t.Fatalf("wrong durable progress: %v", operations)
	}
	rows[0]["dialogue"] = "human edit after interrupted run"
	if err := saveCloudAgentDocument(restarted, current, doc, policy); err != nil {
		t.Fatal(err)
	}
	lastArgs["rows"] = []map[string]any{{"durationSeconds": 5.0, "plotDescription": "chunk 3"}}
	if _, err := applyCloudAgentStoryboardMutation(restarted, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "replay-after-human", lastArgs), policy); err != nil {
		t.Fatal(err)
	}
	afterHuman, _ := restarted.CanvasProjectForUser("user", canvas.ID)
	doc, _ = creationDocument(afterHuman.PayloadJSON)
	_, _, rows, _ = storyboardNodeFromDocument(doc, "storyboard-1")
	if rows[0]["dialogue"] != "human edit after interrupted run" || len(rows) != 5 {
		t.Fatal("replay overwrote human edit")
	}
	lastArgs["operationId"] = "fill-task/chunk-4"
	if _, err := applyCloudAgentStoryboardMutation(restarted, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "stale-new-chunk", lastArgs), policy); err == nil {
		t.Fatal("fresh chunk bypassed conflict protection")
	}
	lastArgs["snapshotHash"] = cloudAgentCanvasHash(doc)
	lastArgs["rows"] = []map[string]any{{"durationSeconds": 5.0, "plotDescription": "chunk 4"}}
	if _, err := applyCloudAgentStoryboardMutation(restarted, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "remaining-chunk", lastArgs), policy); err != nil {
		t.Fatal(err)
	}
	finished, _ := restarted.CanvasProjectForUser("user", canvas.ID)
	doc, _ = creationDocument(finished.PayloadJSON)
	_, _, rows, _ = storyboardNodeFromDocument(doc, "storyboard-1")
	if len(rows) != 6 || rows[5]["plotDescription"] != "chunk 4" || rows[0]["dialogue"] != "human edit after interrupted run" {
		t.Fatal("remaining chunk did not complete safely")
	}
}

func TestStoryboardReplayIsNotANewWriteSnapshot(t *testing.T) {
	state := cloudAgentRuntime{CanvasBatchHashes: []string{"current"}}
	result := map[string]any{"preview": cloudAgentApprovalPreview{}, "replayed": true, "snapshotHash": "historical", "beforeSnapshotHash": "older", "currentSnapshotHash": "current"}
	state.recordCanvasBatchHash(result, "canvas_edit_storyboard")
	if !reflect.DeepEqual(state.CanvasBatchHashes, []string{"current"}) {
		t.Fatalf("replay introduced a historical write version: %v", state.CanvasBatchHashes)
	}
	receipt := cloudAgentModelToolResult("canvas_edit_storyboard", result).(map[string]any)
	if receipt["outcome"] == nil || receipt["outcome"] == "" {
		t.Fatal("replay needs explicit historical-result guidance")
	}
	if !strings.Contains(stringValue(receipt["outcome"]), "历史") {
		t.Fatal("replay claims a fresh successful write")
	}
}

func TestStoryboardReplayDoesNotRequestApprovalAgain(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	createCloudAgentStoryboardForTest(t, s, canvas)
	policy, _ := s.RuntimePolicy()
	doc, _ := creationDocument(canvas.PayloadJSON)
	args := map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "append_rows", "operationId": "approval/chunk-1", "rows": []map[string]any{{"durationSeconds": 5.0, "plotDescription": "once"}}}
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "first", args)
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = "request_approval"
	req.IdempotencyKey = "replay-existing-storyboard-operation"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call.ID = "retry"
	agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if err := agentPiExecuteCallForTest(t, s, root.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	_, state := reloadAgentRun(t, s, root.ID)
	if state.Approval != nil {
		t.Fatal("already committed batch requested another approval")
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(stringField(state.Canonical.Messages[len(state.Canonical.Messages)-1], "content")), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["replayed"] != true {
		t.Fatalf("runtime did not return durable result: %v", receipt)
	}
}

func TestStoryboardChunkReceiptFailureRollsBackRows(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	createCloudAgentStoryboardForTest(t, s, canvas)
	policy, _ := s.RuntimePolicy()
	before, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ := creationDocument(before.PayloadJSON)
	if err := db.Callback().Create().Before("gorm:create").Register("test_receipt_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "canvas_command_receipts" {
			tx.AddError(fmt.Errorf("receipt persistence unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test_receipt_failure") })
	_, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "rollback", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "append_rows", "operationId": "fill-task/rollback", "rows": []map[string]any{{"durationSeconds": 5.0, "plotDescription": "must roll back"}}}), policy)
	if err == nil {
		t.Fatal("receipt failure reported success")
	}
	after, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	if !reflect.DeepEqual(before.PayloadJSON, after.PayloadJSON) {
		t.Fatal("rows saved without their durable receipt")
	}
}
