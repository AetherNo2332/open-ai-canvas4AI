package database_test

import (
	"errors"
	"testing"
	"time"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

func openAgentSchedulerRepositoryDB(t *testing.T) (*repository.Repository, *gorm.DB) {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal("could not open isolated Agent operation database")
	}
	if err := db.AutoMigrate(&model.AgentToolOperation{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{},
		&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}); err != nil {
		t.Fatal("could not create Agent operation tables")
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return repository.New(db), db
}

func TestAgentSchedulerToolOperationPrepareAndFinishAreIdempotent(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	operation := model.AgentToolOperation{
		ID: "operation-idempotent", UserID: "user", RunID: "run", TaskID: "task", CallID: "call", Status: "queued",
	}
	if err := repo.PrepareAgentToolOperation(&operation); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimAgentToolOperation(operation.ID, "owner-a")
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, err = %v", claimed, err)
	}
	if err := repo.PrepareAgentToolOperation(&operation); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishAgentToolOperation(operation, "owner-a", "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishAgentToolOperation(operation, "owner-a", "succeeded", ""); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("duplicate finish error = %v, want ownership conflict", err)
	}

	var count int64
	if err := db.Model(&model.AgentToolOperation{}).Where("id = ?", operation.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate prepare created %d operation rows", count)
	}
	var stored model.AgentToolOperation
	if err := db.First(&stored, "id = ?", operation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" {
		t.Fatalf("duplicate prepare changed terminal operation status to %q", stored.Status)
	}
	var wakes int64
	if err := db.Model(&model.AgentWakeEvent{}).Where("kind = ? AND run_id = ? AND task_id = ?", "tool_changed", operation.RunID, operation.TaskID).Count(&wakes).Error; err != nil {
		t.Fatal(err)
	}
	if wakes != 1 {
		t.Fatalf("duplicate finish produced %d tool wake events", wakes)
	}
}

func TestAgentSchedulerToolOperationRejectsExpiredOwnerCompletion(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	operation := model.AgentToolOperation{
		ID: "operation-expired-owner", UserID: "user", RunID: "run", TaskID: "task", CallID: "call", Status: "queued",
	}
	if err := repo.PrepareAgentToolOperation(&operation); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimAgentToolOperation(operation.ID, "owner-expired")
	if err != nil || !claimed {
		t.Fatalf("claim = %v, err = %v", claimed, err)
	}
	if err := db.Model(&model.AgentToolOperation{}).Where("id = ?", operation.ID).
		Update("lease_expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishAgentToolOperation(operation, "owner-expired", "succeeded", "late completion"); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("expired owner finish error = %v, want ownership conflict", err)
	}
	var stored model.AgentToolOperation
	if err := db.First(&stored, "id = ?", operation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "running" {
		t.Fatalf("expired owner changed operation status to %q", stored.Status)
	}
	var wakes int64
	if err := db.Model(&model.AgentWakeEvent{}).Where("kind = ? AND run_id = ?", "tool_changed", operation.RunID).Count(&wakes).Error; err != nil {
		t.Fatal(err)
	}
	if wakes != 0 {
		t.Fatalf("expired completion emitted %d tool wake events", wakes)
	}
}

func TestAgentSchedulerToolOperationLeaseTakeoverFencesOldOwner(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	operation := model.AgentToolOperation{
		ID: "operation-takeover", UserID: "user", RunID: "run", TaskID: "task", CallID: "call", Status: "queued",
	}
	if err := repo.PrepareAgentToolOperation(&operation); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimAgentToolOperation(operation.ID, "owner-old")
	if err != nil || !claimed {
		t.Fatalf("initial claim = %v, err = %v", claimed, err)
	}
	if err := db.Model(&model.AgentToolOperation{}).Where("id = ?", operation.ID).
		Update("lease_expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimAgentToolOperation(operation.ID, "owner-new")
	if err != nil || !claimed {
		t.Fatalf("takeover claim = %v, err = %v", claimed, err)
	}
	if err := repo.FinishAgentToolOperation(operation, "owner-old", "succeeded", "stale"); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("old owner finish error = %v, want ownership conflict", err)
	}
	if err := repo.FinishAgentToolOperation(operation, "owner-new", "succeeded", ""); err != nil {
		t.Fatalf("current owner finish: %v", err)
	}
	var wakes int64
	if err := db.Model(&model.AgentWakeEvent{}).Where("kind = ? AND run_id = ?", "tool_changed", operation.RunID).Count(&wakes).Error; err != nil {
		t.Fatal(err)
	}
	if wakes != 1 {
		t.Fatalf("takeover completion produced %d wake events, want one", wakes)
	}
}

func TestAgentSchedulerLeaseReleaseRequiresCurrentSessionEpoch(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	expires := time.Now().Add(time.Minute)
	run := model.CloudAgentExecution{
		ID: "run-release-epoch", UserID: "user", Engine: "pi", Status: "running", LeaseOwner: "same-worker",
		LeaseExpiresAt: &expires, ConversationID: "conversation-release-epoch", Revision: 7,
	}
	session := model.CloudAgentPiSession{
		ID: model.PiSessionStorageID(run.UserID, run.ConversationID), UserID: run.UserID,
		ConversationID: run.ConversationID, CanvasID: "canvas", HeaderJSON: `{}`, Revision: 3,
		ActiveRunID: run.ID, LeaseOwner: "same-worker", LeaseEpoch: 2, LeaseExpiresAt: &expires,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.ReleasePiAgentLease(run.UserID, run.ID, "same-worker", 1); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("stale epoch release error = %v, want ownership conflict", err)
	}
	var afterStaleRun model.CloudAgentExecution
	var afterStaleSession model.CloudAgentPiSession
	if err := db.First(&afterStaleRun, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&afterStaleSession, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterStaleRun.LeaseOwner != "same-worker" || afterStaleSession.LeaseOwner != "same-worker" || afterStaleSession.LeaseEpoch != 2 {
		t.Fatalf("stale release modified the current lease: run=%+v session=%+v", afterStaleRun, afterStaleSession)
	}
	if err := repo.ReleasePiAgentLease(run.UserID, run.ID, "same-worker", 2); err != nil {
		t.Fatalf("current epoch release failed: %v", err)
	}
	if err := db.First(&afterStaleRun, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&afterStaleSession, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterStaleRun.LeaseOwner != "" || afterStaleSession.LeaseOwner != "" || afterStaleSession.LeaseEpoch != 2 {
		t.Fatalf("current epoch release did not atomically clear owners: run=%+v session=%+v", afterStaleRun, afterStaleSession)
	}
}
