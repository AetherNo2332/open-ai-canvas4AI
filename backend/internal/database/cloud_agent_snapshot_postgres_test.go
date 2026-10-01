package database_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

type cloudAgentSnapshotReaderMarker struct{}

func openCloudAgentSnapshotPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_AGENT_POSTGRES_DSN is not set")
	}
	db, err := database.Open(database.Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal("could not open configured Agent integration database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("could not inspect configured Agent integration database")
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var databaseName string
	if err := db.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatal("could not verify Agent integration database name")
	}
	if databaseName != "canvas_event_integration" {
		t.Skip("TEST_AGENT_POSTGRES_DSN does not point to canvas_event_integration")
	}
	if err := db.AutoMigrate(&model.CloudAgentExecution{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{}); err != nil {
		t.Fatal("could not prepare Cloud Agent snapshot tables")
	}
	return db
}

func TestAgentSchedulerPostgresCloudAgentReadUsesOneSnapshot(t *testing.T) {
	db := openCloudAgentSnapshotPostgresDB(t)
	const runID, userID = "snapshot-test-run", "snapshot-test-user"
	if err := db.Where("run_id = ?", runID).Delete(&model.CloudAgentEventRecord{}).Error; err != nil {
		t.Fatal("could not clean isolated journal fixture")
	}
	if err := db.Where("run_id = ?", runID).Delete(&model.CloudAgentMessageRecord{}).Error; err != nil {
		t.Fatal("could not clean isolated transcript fixture")
	}
	if err := db.Where("id = ?", runID).Delete(&model.CloudAgentExecution{}).Error; err != nil {
		t.Fatal("could not clean isolated run fixture")
	}
	run := model.CloudAgentExecution{
		ID: runID, UserID: userID, Engine: "pi", Status: "running", Revision: 1,
		CheckpointVersion: 2, EventCount: 1, MessageCount: 0,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal("could not seed snapshot run header")
	}
	if err := db.Create(&model.CloudAgentEventRecord{
		RunID: runID, UserID: userID, Sequence: 1, EventJSON: `{"type":"first"}`,
	}).Error; err != nil {
		t.Fatal("could not seed snapshot journal")
	}

	readerRepo := repository.New(db)
	readerReachedGate := make(chan struct{})
	writerCommitted := make(chan struct{})
	if err := db.Callback().Query().After("gorm:query").Register("test:agent_snapshot_reader_gate", func(tx *gorm.DB) {
		if tx.Statement.Table != "cloud_agent_executions" || tx.Statement.Context.Value(cloudAgentSnapshotReaderMarker{}) != true {
			return
		}
		select {
		case <-readerReachedGate:
			return
		default:
			close(readerReachedGate)
		}
		select {
		case <-writerCommitted:
		case <-time.After(10 * time.Second):
			tx.AddError(fmt.Errorf("snapshot test writer did not commit"))
		}
	}); err != nil {
		t.Fatal("could not install deterministic reader barrier")
	}

	type readResult struct {
		run *model.CloudAgentExecution
		err error
	}
	firstRead := make(chan readResult, 1)
	readerContext := context.WithValue(context.Background(), cloudAgentSnapshotReaderMarker{}, true)
	go func() {
		run, err := readerRepo.WithContext(readerContext).CloudAgent(userID, runID)
		firstRead <- readResult{run: run, err: err}
	}()
	select {
	case <-readerReachedGate:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not finish reading the run header")
	}

	writeErr := db.Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND user_id = ?", runID, userID).
			Updates(map[string]any{"event_count": 2, "revision": 2})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("run header update affected %d rows", updated.RowsAffected)
		}
		return tx.Create(&model.CloudAgentEventRecord{
			RunID: runID, UserID: userID, Sequence: 2, EventJSON: `{"type":"second"}`,
		}).Error
	})
	close(writerCommitted)
	if writeErr != nil {
		t.Fatalf("writer transaction failed: %v", writeErr)
	}

	var oldSnapshot readResult
	select {
	case oldSnapshot = <-firstRead:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not finish hydrating the old snapshot")
	}
	if oldSnapshot.err != nil {
		t.Fatalf("reader failed to load a consistent snapshot: %v", oldSnapshot.err)
	}
	if oldSnapshot.run.EventCount != 1 || len(oldSnapshot.run.Journal) != 1 || oldSnapshot.run.Journal[0].Sequence != 1 {
		t.Fatalf("reader mixed run header and journal snapshots: eventCount=%d journal=%+v",
			oldSnapshot.run.EventCount, oldSnapshot.run.Journal)
	}

	newSnapshot, err := repository.New(db).CloudAgent(userID, runID)
	if err != nil {
		t.Fatalf("new reader failed after commit: %v", err)
	}
	if newSnapshot.EventCount != 2 || len(newSnapshot.Journal) != 2 || newSnapshot.Journal[1].Sequence != 2 {
		t.Fatalf("new reader did not observe the committed header and journal: eventCount=%d journal=%+v",
			newSnapshot.EventCount, newSnapshot.Journal)
	}
}
