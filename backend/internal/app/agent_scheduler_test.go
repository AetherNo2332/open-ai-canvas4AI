package app

import (
	"fmt"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"testing"
	"time"
)

func TestPiEventSchedulerPhaseFencingAfterSameOwnerTakeover(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentToolOperation{}, &model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	session, _, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	old := fmt.Sprintf("%s@%d", run.LeaseOwner, session.LeaseEpoch)
	if err = s.PiRuntimePhase(run.UserID, run.ID, old, PiRuntimePhaseRequest{Phase: "waiting_model", Kind: "model", WaitID: "task-reference"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []any{&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}} {
		if err = db.Model(entry).Where("id = ?", run.ID).Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := s.ClaimPiAgent(run.LeaseOwner)
	if err != nil || claimed == nil {
		t.Fatalf("takeover: %v", err)
	}
	current := fmt.Sprintf("%s@%d", run.LeaseOwner, claimed.PiSessionLeaseEpoch)
	if current == old {
		t.Fatal("epoch did not advance")
	}
	if err = s.PiRuntimePhase(run.UserID, run.ID, old, PiRuntimePhaseRequest{Phase: "advancing"}); err == nil {
		t.Fatal("old phase writer accepted")
	}
	if err = s.RenewPiAgentLease(run.UserID, run.ID, old); err == nil {
		t.Fatal("old renewal accepted")
	}
	if err = s.PiRelease(run.UserID, run.ID, old); err == nil {
		t.Fatal("old release accepted")
	}
	if err = s.PiRuntimePhase("other-user", run.ID, current, PiRuntimePhaseRequest{Phase: "advancing"}); err == nil {
		t.Fatal("cross user accepted")
	}
	if err = s.PiRuntimePhase(run.UserID, run.ID, current, PiRuntimePhaseRequest{Phase: "advancing"}); err != nil {
		t.Fatal(err)
	}
}

func TestPiEventSchedulerDurableToolAdmissionAndReceiptRecovery(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	if err := db.AutoMigrate(&model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentToolOperation{}); err != nil {
		t.Fatal(err)
	}
	const taskID = "async-tool-task"
	if err := db.Create(&model.Task{ID: taskID, UserID: run.UserID, ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"toolCalls":[{"id":"call-1","function":{"name":"canvas_list_node_types","arguments":"{}"}}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch(run.UserID, run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{piAgentTestCall("call-1", "canvas_list_node_types", "{}")}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.PiToolAdvanceAsync(run.UserID, run.ID, run.LeaseOwner, taskID, "call-1")
	if err != nil || !first.Pending || first.OperationID == "" {
		t.Fatalf("durable admission: %+v %v", first, err)
	}
	second, err := s.PiToolAdvanceAsync(run.UserID, run.ID, run.LeaseOwner, taskID, "call-1")
	if err != nil || second.OperationID != first.OperationID {
		t.Fatal("retry changed identity", err)
	}
	var count int64
	db.Model(&model.AgentToolOperation{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate operation", count)
	}
	session, _, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	old := fmt.Sprintf("%s@%d", run.LeaseOwner, session.LeaseEpoch)
	for _, entry := range []any{&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}} {
		if err = db.Model(entry).Where("id = ?", run.ID).Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := s.ClaimPiAgent(run.LeaseOwner)
	if err != nil || snapshot == nil {
		t.Fatal("takeover", err)
	}
	fresh := fmt.Sprintf("%s@%d", run.LeaseOwner, snapshot.PiSessionLeaseEpoch)
	before, beforeState := reloadPiRun(t, s, run.ID)
	if _, err = s.PiToolAdvanceAsync(run.UserID, run.ID, old, taskID, "call-1"); err == nil {
		t.Fatal("old async epoch accepted")
	}
	if _, err = s.PiToolAdvance(run.UserID, run.ID, old, taskID, "call-1"); err == nil {
		t.Fatal("old sync epoch accepted")
	}
	after, afterState := reloadPiRun(t, s, run.ID)
	db.Model(&model.AgentToolOperation{}).Count(&count)
	if before.Revision != after.Revision || beforeState.CallIndex != afterState.CallIndex || len(before.Transcript) != len(after.Transcript) || count != 1 {
		t.Fatal("stale tool writer changed durable state")
	}
	// The Agent may lose admission or receipt responses. Existing Go receipt is authoritative.
	receipt, err := s.PiToolAdvance(run.UserID, run.ID, fresh, taskID, "call-1")
	if err != nil || receipt.Pending {
		t.Fatal("tool execution", err)
	}
	replay, err := s.PiToolAdvanceAsync(run.UserID, run.ID, fresh, taskID, "call-1")
	if err != nil || replay.Pending || string(replay.Result) != string(receipt.Result) {
		t.Fatal("receipt replay", err)
	}
	claimed, err := s.repo.ClaimAgentToolOperation(first.OperationID, "executor-old")
	if err != nil || !claimed {
		t.Fatal("claim", err)
	}
	db.Model(&model.AgentToolOperation{}).Where("id = ?", first.OperationID).Update("lease_expires_at", time.Now().Add(-time.Minute))
	claimed, err = s.repo.ClaimAgentToolOperation(first.OperationID, "executor-new")
	if err != nil || !claimed {
		t.Fatal("reclaim", err)
	}
	operation := model.AgentToolOperation{ID: first.OperationID, RunID: run.ID, UserID: run.UserID, TaskID: taskID}
	if err = s.repo.FinishAgentToolOperation(operation, "executor-old", "succeeded", ""); err == nil {
		t.Fatal("old operation owner accepted")
	}
	if err = s.repo.FinishAgentToolOperation(operation, "executor-new", "succeeded", ""); err != nil {
		t.Fatal(err)
	}
}

func TestPiEventSchedulerCapacityWaitingSkipsUnavailableWatchdog(t *testing.T) {
	s, db, run := piAgentTestFixture(t)
	if err := db.AutoMigrate(&model.AgentRuntimeInstance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(run).Updates(map[string]any{"status": "queued", "created_at": time.Now().Add(-24 * time.Hour), "runtime_phase": "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.AgentCapacity("executor", 64, 64); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.repo.UnclaimedPiAgentRuns(time.Now(), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("capacity wait was declared unavailable: %d %v", len(candidates), err)
	}
	waiting, err := s.repo.CloudAgentControlRow(run.UserID, run.ID)
	if err != nil || waiting.RuntimePhase != "waiting_resource" || waiting.WaitKind != "capacity" {
		t.Fatalf("capacity phase missing: %+v %v", waiting, err)
	}
	if err = s.repo.AgentCapacity("executor", 63, 64); err != nil {
		t.Fatal(err)
	}
	ready, err := s.repo.CloudAgentControlRow(run.UserID, run.ID)
	if err != nil || ready.RuntimePhase != "ready" {
		t.Fatalf("capacity release missing: %+v %v", ready, err)
	}
	candidates, err = s.repo.UnclaimedPiAgentRuns(time.Now(), 10)
	if err != nil || len(candidates) != 1 {
		t.Fatal("real unavailable run no longer detected", err)
	}
}
