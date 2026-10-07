package app

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
	"testing"
	"time"
)

func TestWorkerRecoveryReplayedUserCheckpointsCannotResetBudget(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	var started *time.Time
	for i := 1; i <= 5; i++ {
		if i > 1 {
			expireRecoveryLease(t, db, run.ID)
			if _, err := s.ClaimPiAgent(fmt.Sprintf("replay-worker-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		agentPiCheckpointForTest(t, s, run.ID, "", map[string]any{"role": "user", "content": fmt.Sprintf("same logical prompt %d", 0), "timestamp": time.Now().UnixMilli()}, nil)
		row, _ := s.repo.CloudAgentControlRow("user", run.ID)
		session, _, _ := s.repo.CloudAgentPiSession("user", run.ConversationID)
		result, err := s.PiWorkerRecovery("user", run.ID, fmt.Sprintf("%s@%d", row.LeaseOwner, session.LeaseEpoch), PiWorkerRecoveryRequest{Revision: row.Revision, Class: "http_5xx", Operation: "model"})
		if err != nil {
			t.Fatal(err)
		}
		row, _ = s.repo.CloudAgentControlRow("user", run.ID)
		if i == 1 {
			started = row.RecoveryStartedAt
		}
		if result.Attempts != i || row.RecoveryStartedAt == nil || !row.RecoveryStartedAt.Equal(*started) {
			t.Fatalf("replay reset cycle at %d: %+v", i, result)
		}
		if i == 5 && row.Status != "failed" {
			t.Fatal("replayed prompt evaded finite budget")
		}
	}
}

func TestWorkerRecoveryTaskBudgetSurvivesRouteAndTaskSwitches(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	// Genuine receipts may end a cycle, but cannot erase the same task's cumulative failures.
	sequence := []struct {
		task, operation string
		want            int
	}{{"pi-root-task", "model", 1}, {"other-task", "control", 1}, {"pi-root-task", "renew", 2}, {"pi-root-task", "checkpoint", 3}, {"pi-root-task", "model", 4}, {"pi-root-task", "model", 5}}
	for i, step := range sequence {
		if i > 0 {
			expireRecoveryLease(t, db, run.ID)
			if _, err := s.ClaimPiAgent(fmt.Sprintf("switch-worker-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		row, _ := s.repo.CloudAgent("user", run.ID)
		if err := s.repo.MutateCloudAgent("user", run.ID, row.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			current.ActiveTaskID = step.task
			state.event(run.ID, "tool_completed", map[string]any{"callId": fmt.Sprintf("receipt-%d", i)})
			return cloudAgentSave(current, &state)
		}); err != nil {
			t.Fatal(err)
		}
		// cloudAgentSave synchronizes ActiveTaskID from state; set the current valid in-flight task.
		if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("active_task_id", step.task).Error; err != nil {
			t.Fatal(err)
		}
		row, _ = s.repo.CloudAgentControlRow("user", run.ID)
		session, _, _ := s.repo.CloudAgentPiSession("user", run.ConversationID)
		_, err := s.PiWorkerRecovery("user", run.ID, fmt.Sprintf("%s@%d", row.LeaseOwner, session.LeaseEpoch), PiWorkerRecoveryRequest{Revision: row.Revision, Class: "http_5xx", Operation: step.operation, TaskID: step.task})
		if err != nil {
			t.Fatal(err)
		}
		row, _ = s.repo.CloudAgentControlRow("user", run.ID)
		if row.RecoveryOperationAttempts != step.want {
			t.Fatalf("same task budget erased at %d: got %d want %d", i, row.RecoveryOperationAttempts, step.want)
		}
		if i == len(sequence)-1 && row.Status != "failed" {
			t.Fatal("same task cumulative budget did not terminate")
		}
	}
}

func TestWorkerRecoveryNewUserInputIsNotAnExecutionReceipt(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	agentPiCheckpointForTest(t, s, run.ID, "", map[string]any{"role": "user", "content": "original prompt"}, nil)
	started := time.Now().Add(-time.Minute)
	db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{"recovery_attempts": 3, "recovery_started_at": started})
	row, _ := s.repo.CloudAgent("user", run.ID)
	err := s.repo.MutateCloudAgent("user", run.ID, row.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "additional real user input"})
		return cloudAgentSave(current, &state)
	})
	if err != nil {
		t.Fatal(err)
	}
	row, _ = s.repo.CloudAgentControlRow("user", run.ID)
	if row.RecoveryAttempts != 3 || row.RecoveryStartedAt == nil || !row.RecoveryStartedAt.Equal(started) {
		t.Fatal("user input erased execution recovery budget")
	}
	// Ensure the persisted input survives the control changes.
	hydrated, _ := s.repo.CloudAgent("user", run.ID)
	state, _ := cloudAgentDecode(hydrated)
	body, _ := json.Marshal(state.Canonical.Messages)
	if !strings.Contains(string(body), "additional real user input") {
		t.Fatal("user input lost")
	}
}

func expireRecoveryLease(t *testing.T, db *gorm.DB, runID string) {
	t.Helper()
	stale := time.Now().Add(-time.Second)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", runID).Updates(map[string]any{"lease_expires_at": stale, "next_recovery_at": stale}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("active_run_id = ?", runID).Update("lease_expires_at", stale).Error; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRecoveryIsDurableFencedAndIdempotent(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("%s@%d", run.LeaseOwner, session.LeaseEpoch)
	input := PiWorkerRecoveryRequest{Revision: run.Revision, Class: "http_5xx", Operation: "snapshot", HTTPStatus: 503}
	result, err := s.PiWorkerRecovery("user", run.ID, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "scheduled" || result.Attempts != 1 || result.NextRecoveryAt == nil {
		t.Fatalf("unexpected recovery: %+v", result)
	}
	again, err := s.PiWorkerRecovery("user", run.ID, owner, input)
	if err != nil || again.Attempts != 1 {
		t.Fatalf("duplicate counted: %+v %v", again, err)
	}
	if claim, err := s.ClaimPiAgent("worker-b"); err != nil || claim != nil {
		t.Fatalf("early claim: %+v %v", claim, err)
	}
	expireRecoveryLease(t, db, run.ID)
	claim, err := s.ClaimPiAgent("worker-b")
	if err != nil || claim == nil {
		t.Fatalf("due recovery: %+v %v", claim, err)
	}
	if _, err = s.PiWorkerRecovery("user", run.ID, owner, input); err == nil {
		t.Fatal("old epoch accepted")
	}
	row, _ := s.repo.CloudAgent("user", run.ID)
	if row.RecoveryAttempts != 1 {
		t.Fatalf("restart erased durable attempts: %d", row.RecoveryAttempts)
	}
}

func TestWorkerRecoveryRepeatedCrashesTerminateWithoutErrorReport(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	for i := 0; i < 5; i++ {
		expireRecoveryLease(t, db, run.ID)
		_, err := s.ClaimPiAgent(fmt.Sprintf("worker-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	row, state := reloadPiRun(t, s, run.ID)
	if row.Status != "failed" || !row.CleanupPending || !agentHasEvent(*state, "run_failed") {
		t.Fatalf("repeated reclaims left %s", row.Status)
	}
}

func TestWorkerRecoveryExhaustionSurvivesRenewedLease(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	started := time.Now().Add(-11 * time.Minute)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{
		"recovery_status": "scheduled", "recovery_started_at": started, "recovery_attempts": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	swept, err := s.SweepWorkerRecoveries()
	if err != nil || swept != 1 {
		t.Fatalf("sweep %d %v", swept, err)
	}
	row, _ := reloadPiRun(t, s, run.ID)
	if row.Status != "failed" {
		t.Fatal("renewed lease masked exhausted recovery")
	}
}

func TestWorkerRecoveryHealthyTaskAndApprovalWaitDoNotSpendCrashBudget(t *testing.T) {
	for _, phase := range []string{"waiting_model", "waiting_tool", "waiting_approval", "waiting_resource"} {
		t.Run(phase, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			started := time.Now().Add(-11 * time.Minute)
			status := "running"
			if phase == "waiting_approval" {
				status = phase
			}
			taskStatus := model.TaskStatusRunning
			if phase == "waiting_resource" {
				taskStatus = model.TaskStatusQueued
			}
			if err := db.Model(&model.Task{}).Where("id = ?", "pi-root-task").Update("status", taskStatus).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{
				"status": status, "runtime_phase": phase, "active_task_id": "pi-root-task", "wait_kind": "model",
				"recovery_status": "reconciling", "recovery_attempts": 4, "recovery_started_at": started,
			}).Error; err != nil {
				t.Fatal(err)
			}
			expireRecoveryLease(t, db, run.ID)
			if _, err := s.ClaimPiAgent("healthy-takeover"); err != nil {
				t.Fatal(err)
			}
			row, err := s.repo.CloudAgentControlRow("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if row.Status == "failed" || row.RecoveryAttempts != 4 {
				t.Fatalf("normal %s wait killed: status=%s attempts=%d", phase, row.Status, row.RecoveryAttempts)
			}
			if n, err := s.SweepWorkerRecoveries(); err != nil || n != 0 {
				t.Fatalf("normal wait swept: %d %v", n, err)
			}
		})
	}
}

func TestWorkerRecoveryProgressClearsCycleButNotSameOperationBudget(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	// Upgrade before recording a cycle, then append an acknowledged tool receipt.
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{
		"recovery_attempts": 3, "recovery_operation_attempts": 3, "recovery_call_id": "same-call", "recovery_status": "reconciling",
	}).Error; err != nil {
		t.Fatal(err)
	}
	row, _ := s.repo.CloudAgent("user", run.ID)
	if err := s.repo.MutateCloudAgent("user", run.ID, row.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.event(run.ID, "tool_completed", map[string]any{"callId": "ack-call"})
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	row, _ = s.repo.CloudAgent("user", run.ID)
	if row.RecoveryAttempts != 0 || row.ProgressVersion != 1 || row.RecoveryOperationAttempts != 3 {
		t.Fatalf("progress budget: cycle=%d progress=%d operation=%d", row.RecoveryAttempts, row.ProgressVersion, row.RecoveryOperationAttempts)
	}
}

func TestWorkerRecoveryRetryAfterBeyondWindowAndUnknownResultTerminate(t *testing.T) {
	for _, fault := range []string{"rate_limited", "result_unknown"} {
		t.Run(fault, func(t *testing.T) {
			s, _, run := piAgentTestLeasedFixture(t)
			session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			input := PiWorkerRecoveryRequest{Revision: run.Revision, Class: fault, Operation: "model", RetryAfterMS: 11 * 60 * 1000}
			if fault == "rate_limited" {
				input.HTTPStatus = 429
			}
			result, err := s.PiWorkerRecovery("user", run.ID, fmt.Sprintf("%s@%d", run.LeaseOwner, session.LeaseEpoch), input)
			if err != nil {
				t.Fatal(err)
			}
			row, state := reloadPiRun(t, s, run.ID)
			if row.Status != "failed" || !row.CleanupPending || !agentHasEvent(*state, "run_failed") {
				t.Fatalf("ambiguous/overdue left running: %+v", result)
			}
			if fault == "result_unknown" && result.Status != "needs_review" {
				t.Fatalf("unknown result hidden: %+v", result)
			}
			if n, err := s.DrainPendingPiAgentCleanups(); err != nil || n != 1 {
				t.Fatalf("terminal cleanup: %d %v", n, err)
			}
			row, _ = reloadPiRun(t, s, run.ID)
			if row.CleanupPending {
				t.Fatal("cleanup stayed pending")
			}
		})
	}
}

func TestWorkerRecoverySweepDoesNotStarveBehindHundredHealthyWaits(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	if err := db.Model(&model.Task{}).Where("id = ?", "pi-root-task").Update("status", model.TaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	earlier := time.Now().Add(-12 * time.Minute)
	for i := 0; i < 100; i++ {
		keeper := model.CloudAgentExecution{ID: fmt.Sprintf("healthy-%03d", i), UserID: "user", Engine: "pi", Status: "running", RuntimePhase: "waiting_model", ActiveTaskID: "pi-root-task", RecoveryStatus: "reconciling", RecoveryStartedAt: &earlier, RecoveryAttempts: 1}
		if err := db.Create(&keeper).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := time.Now().Add(-11 * time.Minute)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{"recovery_status": "reconciling", "recovery_started_at": target, "recovery_attempts": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if count, err := s.SweepWorkerRecoveries(); err != nil || count != 1 {
		t.Fatalf("eligible run starved: count=%d err=%v", count, err)
	}
	row, _ := s.repo.CloudAgentControlRow("user", run.ID)
	if row.Status != "failed" {
		t.Fatal("expired run still active")
	}
}
