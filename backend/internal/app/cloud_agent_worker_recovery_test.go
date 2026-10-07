package app

import (
	"fmt"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"testing"
	"time"
)

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
	for _, phase := range []string{"waiting_model", "waiting_tool", "waiting_approval"} {
		t.Run(phase, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			started := time.Now().Add(-11 * time.Minute)
			status := "running"
			if phase == "waiting_approval" {
				status = phase
			}
			if err := db.Model(&model.Task{}).Where("id = ?", "pi-root-task").Update("status", model.TaskStatusRunning).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{
				"status": status, "runtime_phase": phase, "active_task_id": "pi-root-task",
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
