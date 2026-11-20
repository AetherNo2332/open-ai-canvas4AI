package database_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func openAgentWakePostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_AGENT_POSTGRES_DSN is not set")
	}
	db, err := database.Open(database.Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal("could not open the configured Agent wake integration database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("could not inspect the configured Agent wake integration database")
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var databaseName string
	if err := db.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatal("could not verify the Agent wake integration database name")
	}
	if databaseName != "canvas_event_integration" {
		t.Skip("TEST_AGENT_POSTGRES_DSN does not point to canvas_event_integration")
	}
	if err := db.AutoMigrate(&model.Task{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentToolOperation{}); err != nil {
		t.Fatal("could not prepare Agent wake integration tables")
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AgentEventCounter{ID: 1}).Error; err != nil {
		t.Fatal("could not initialize the Agent wake integration counter")
	}
	database.InstallAgentEventCallbacks(db)
	return db
}

func agentWakePostgresCounter(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var counter model.AgentEventCounter
	if err := db.First(&counter, "id = ?", 1).Error; err != nil {
		t.Fatal("could not read the Agent wake integration counter")
	}
	return counter.Value
}

func agentWakePostgresEvents(t *testing.T, db *gorm.DB, column, marker string) []model.AgentWakeEvent {
	t.Helper()
	var events []model.AgentWakeEvent
	if err := db.Where(column+" = ?", marker).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal("could not read Agent wake integration events")
	}
	return events
}

// injectAgentWakeInsertFailure installs a constraint that rejects only the
// event carrying marker, so unrelated rows and other concurrent cases keep
// working. The returned function removes the fault early; cleanup is idempotent.
func injectAgentWakeInsertFailure(t *testing.T, db *gorm.DB, column, marker string) func() {
	t.Helper()
	if column != "task_id" && column != "run_id" {
		t.Fatalf("unsupported wake failure column %q", column)
	}
	name := fmt.Sprintf("agent_wake_fail_%d", time.Now().UnixNano())
	literal := strings.ReplaceAll(marker, "'", "''")
	ddl := fmt.Sprintf("ALTER TABLE agent_wake_events ADD CONSTRAINT %s CHECK (%s <> '%s')", name, column, literal)
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatalf("could not install the Agent wake insert failure: %v", err)
	}
	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		if err := db.Exec("ALTER TABLE agent_wake_events DROP CONSTRAINT IF EXISTS " + name).Error; err != nil {
			t.Errorf("could not remove the Agent wake insert failure: %v", err)
		}
	}
	t.Cleanup(remove)
	return remove
}

// TestAgentSchedulerPostgresWakeInsertFailureRollsBackBusiness proves that a
// wake insert failure is surfaced to the caller and rolls back the business
// write, the wake event and the counter together. A retry after the fault is
// removed must produce exactly one event with the next sequence.
func TestAgentSchedulerPostgresWakeInsertFailureRollsBackBusiness(t *testing.T) {
	t.Run("database callback", func(t *testing.T) {
		db := openAgentWakePostgresDB(t)
		marker := fmt.Sprintf("wc%d", time.Now().UnixNano())
		if err := db.Create(&model.Task{ID: marker, UserID: "user-callback", Status: model.TaskStatusQueued}).Error; err != nil {
			t.Fatal(err)
		}
		base := agentWakePostgresCounter(t, db)
		remove := injectAgentWakeInsertFailure(t, db, "task_id", marker)

		err := db.Model(&model.Task{}).Where("id = ?", marker).Update("status", model.TaskStatusRunning).Error
		if err == nil {
			t.Fatal("task update succeeded although the wake insert was rejected")
		}

		var stored model.Task
		if err := db.First(&stored, "id = ?", marker).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != model.TaskStatusQueued {
			t.Fatalf("failed wake insert committed the task status: %q", stored.Status)
		}
		if got := agentWakePostgresCounter(t, db); got != base {
			t.Fatalf("failed wake insert advanced the counter: got %d want %d", got, base)
		}
		if events := agentWakePostgresEvents(t, db, "task_id", marker); len(events) != 0 {
			t.Fatalf("failed wake insert left events behind: %+v", events)
		}

		remove()
		if err := db.Model(&model.Task{}).Where("id = ?", marker).Update("status", model.TaskStatusRunning).Error; err != nil {
			t.Fatalf("task update after removing the fault failed: %v", err)
		}
		if err := db.First(&stored, "id = ?", marker).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != model.TaskStatusRunning {
			t.Fatalf("retried task update did not commit: %q", stored.Status)
		}
		if got := agentWakePostgresCounter(t, db); got != base+1 {
			t.Fatalf("retried wake insert counter = %d, want %d", got, base+1)
		}
		events := agentWakePostgresEvents(t, db, "task_id", marker)
		if len(events) != 1 || events[0].Sequence != base+1 || events[0].Kind != "task_changed" {
			t.Fatalf("retried wake insert events = %+v, want one task_changed at %d", events, base+1)
		}
	})

	t.Run("repository transaction", func(t *testing.T) {
		db := openAgentWakePostgresDB(t)
		marker := fmt.Sprintf("wr%d", time.Now().UnixNano())
		expiry := time.Now().Add(time.Minute)
		operation := model.AgentToolOperation{
			ID: marker, UserID: "user-repository", RunID: marker, TaskID: marker, CallID: "call",
			Status: "running", LeaseOwner: "owner", LeaseExpiresAt: &expiry,
		}
		if err := db.Create(&operation).Error; err != nil {
			t.Fatal(err)
		}
		repo := repository.New(db)
		base := agentWakePostgresCounter(t, db)
		remove := injectAgentWakeInsertFailure(t, db, "run_id", marker)

		if err := repo.FinishAgentToolOperation(operation, "owner", "succeeded", ""); err == nil {
			t.Fatal("tool finish succeeded although the wake insert was rejected")
		}

		var stored model.AgentToolOperation
		if err := db.First(&stored, "id = ?", marker).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != "running" || stored.LeaseOwner != "owner" || stored.LeaseExpiresAt == nil {
			t.Fatalf("failed wake insert committed the tool operation state: %+v", stored)
		}
		if got := agentWakePostgresCounter(t, db); got != base {
			t.Fatalf("failed wake insert advanced the counter: got %d want %d", got, base)
		}
		if events := agentWakePostgresEvents(t, db, "run_id", marker); len(events) != 0 {
			t.Fatalf("failed wake insert left events behind: %+v", events)
		}

		remove()
		if err := repo.FinishAgentToolOperation(operation, "owner", "succeeded", ""); err != nil {
			t.Fatalf("tool finish after removing the fault failed: %v", err)
		}
		if err := db.First(&stored, "id = ?", marker).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != "succeeded" {
			t.Fatalf("retried tool finish did not commit: %q", stored.Status)
		}
		if got := agentWakePostgresCounter(t, db); got != base+1 {
			t.Fatalf("retried wake insert counter = %d, want %d", got, base+1)
		}
		events := agentWakePostgresEvents(t, db, "run_id", marker)
		if len(events) != 1 || events[0].Sequence != base+1 || events[0].Kind != "tool_changed" {
			t.Fatalf("retried wake insert events = %+v, want one tool_changed at %d", events, base+1)
		}
	})
}
