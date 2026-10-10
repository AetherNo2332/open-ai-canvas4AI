package database_test

import (
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
	"testing"
	"time"
)

func TestWorkerRecoveryPostgresMigrationControlAndTakeover(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	status, err := database.ReadSchemaStatus(db)
	if err != nil || status.Current != database.CurrentSchemaVersion {
		t.Fatalf("migration status: %+v %v", status, err)
	}
	id := agentAdmissionAuditID()
	expired := time.Now().Add(-time.Minute)
	seedAuditRun(t, db, id, id+"u", "canvas", &expired, 8)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", id).Update("lease_owner", "old-worker").Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	run, err := repo.ClaimPiAgentFair("new-worker", time.Now().Add(time.Minute), model.DefaultAgentSchedulerSetting())
	if err != nil || run == nil || run.RecoveryAttempts != 1 {
		t.Fatalf("durable takeover: %+v %v", run, err)
	}
	if err := repo.MutateCloudAgentControl(run.UserID, id, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.RecoveryOperationBudgets = `{"task:original":3,"call:original":2}`
		current.RecoveryOperationID = "checkpoint:" + strings.Repeat("t", 80) + ":" + strings.Repeat("c", 160)
		current.Status = "failed"
		current.CleanupPending = true
		current.EventCount++
		current.Journal = []model.CloudAgentEventRecord{{RunID: id, UserID: current.UserID, Sequence: current.EventCount, EventJSON: `{"type":"run_failed"}`}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateCloudAgentControl(run.UserID, id, run.Revision, func(*model.CloudAgentExecution, *repository.Repository) error { return nil }); err != repository.ErrCreationConflict {
		t.Fatalf("stale CAS accepted: %v", err)
	}
	terminal, err := repo.CloudAgentControlRow(run.UserID, id)
	if err != nil || terminal.Status != "failed" || !terminal.CleanupPending {
		t.Fatalf("terminal control: %+v %v", terminal, err)
	}
	if terminal.RecoveryOperationBudgets != `{"task:original":3,"call:original":2}` || len(terminal.RecoveryOperationID) != 252 {
		t.Fatalf("operation ledger/maximum identity not preserved: %+v", terminal)
	}
	session, err := repo.CloudAgentPiLease(run.UserID, id)
	if err != nil || session.ActiveRunID != "" || session.LeaseOwner != "" {
		t.Fatalf("terminal lease retained: %+v %v", session, err)
	}
	if n, err := repo.CloudAgentEventRecordCount(run.UserID, id); err != nil || n != 1 {
		t.Fatalf("atomic event count: %d %v", n, err)
	}
}
