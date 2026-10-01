package database

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func openAgentSchedulerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}, &model.CloudAgentExecution{}, &model.CloudAgentEventRecord{},
		&model.CloudAgentMessageRecord{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentToolOperation{}); err != nil {
		t.Fatal(err)
	}
	InstallAgentEventCallbacks(db)
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func agentWakeEventsAfter(t *testing.T, db *gorm.DB, cursor int64) []model.AgentWakeEvent {
	t.Helper()
	var events []model.AgentWakeEvent
	if err := db.Where("sequence > ?", cursor).Order("sequence ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	return events
}

func TestAgentSchedulerTaskUpdatesAppendWakeEventsAtomically(t *testing.T) {
	db := openAgentSchedulerDB(t)
	if err := db.Create(&model.Task{ID: "task-1", UserID: "user-1", Status: model.TaskStatusQueued}).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Model(&model.Task{}).Where("id = ?", "task-1").Updates(map[string]any{
		"status": model.TaskStatusRunning,
	}).Error; err != nil {
		t.Fatal(err)
	}
	first := agentWakeEventsAfter(t, db, 0)
	if len(first) != 1 || first[0].Sequence != 1 || first[0].Kind != "task_changed" || first[0].TaskID != "task-1" ||
		first[0].RunID != "" || first[0].UserID != "" || first[0].Revision != 0 {
		t.Fatalf("unexpected task wake event: %+v", first)
	}

	rollback := errors.New("force outer transaction rollback")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Task{}).Where("id = ?", "task-1").Updates(map[string]any{
			"status": model.TaskStatusSucceeded,
		}).Error; err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v, want rollback sentinel", err)
	}
	var task model.Task
	if err := db.First(&task, "id = ?", "task-1").Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusRunning {
		t.Fatalf("rolled-back task status = %q", task.Status)
	}
	if events := agentWakeEventsAfter(t, db, 0); len(events) != 1 {
		t.Fatalf("rollback left a wake event: %+v", events)
	}
	var counters int64
	if err := db.Model(&model.AgentEventCounter{}).Count(&counters).Error; err != nil {
		t.Fatal(err)
	}
	if counters != 1 {
		t.Fatalf("rollback did not restore the counter row: count=%d", counters)
	}

	// The rolled-back append must not consume a sequence number.
	if err := db.Model(&model.Task{}).Where("id = ?", "task-1").Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	events := agentWakeEventsAfter(t, db, 1)
	if len(events) != 1 || events[0].Sequence != 2 || events[0].TaskID != "task-1" {
		t.Fatalf("wake sequence contains a gap or wrong event: %+v", events)
	}
}

func TestAgentSchedulerTaskWakeCarriesPiRunIdentityWithoutCrossUserLeak(t *testing.T) {
	db := openAgentSchedulerDB(t)
	piRun := model.CloudAgentExecution{ID: "run-owned", UserID: "user-owner", Engine: "pi", Status: "running", Revision: 27}
	if err := db.Create(&piRun).Error; err != nil {
		t.Fatal(err)
	}
	for _, task := range []model.Task{
		{ID: "task-owned", UserID: piRun.UserID, AgentRunID: piRun.ID, Status: model.TaskStatusQueued},
		// A task owned by another user must not disclose the referenced run's identity.
		{ID: "task-cross-user", UserID: "user-other", AgentRunID: piRun.ID, Status: model.TaskStatusQueued},
	} {
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, taskID := range []string{"task-owned", "task-cross-user"} {
		if err := db.Model(&model.Task{}).Where("id = ?", taskID).Update("status", model.TaskStatusRunning).Error; err != nil {
			t.Fatal(err)
		}
	}

	var owned model.AgentWakeEvent
	if err := db.Where("kind = ? AND task_id = ?", "task_changed", "task-owned").First(&owned).Error; err != nil {
		t.Fatal(err)
	}
	if owned.RunID != piRun.ID || owned.UserID != piRun.UserID || owned.Revision != piRun.Revision {
		t.Fatalf("associated task wake missing run identity: %+v", owned)
	}
	var crossUser model.AgentWakeEvent
	if err := db.Where("kind = ? AND task_id = ?", "task_changed", "task-cross-user").First(&crossUser).Error; err != nil {
		t.Fatal(err)
	}
	if crossUser.RunID != "" || crossUser.UserID != "" || crossUser.Revision != 0 {
		t.Fatalf("cross-user task wake disclosed run identity: %+v", crossUser)
	}
}

func TestAgentSchedulerRunSaveAndUpdatesCaptureRunIdentityAndRevision(t *testing.T) {
	db := openAgentSchedulerDB(t)
	run := model.CloudAgentExecution{ID: "run-1", UserID: "user-1", Engine: "pi", Status: "queued", Revision: 10}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}

	run.Status = "running"
	run.Revision = 11
	if err := db.Save(&run).Error; err != nil {
		t.Fatal(err)
	}
	events := agentWakeEventsAfter(t, db, 0)
	if len(events) != 2 || events[0].Sequence != 1 || events[0].Kind != "run_changed" ||
		events[0].RunID != "run-1" || events[0].UserID != "user-1" || events[0].Revision != 10 ||
		events[1].Sequence != 2 || events[1].Kind != "run_changed" || events[1].RunID != "run-1" || events[1].Revision != 11 {
		t.Fatalf("Save produced wrong run wake event: %+v", events)
	}

	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", "run-1").Updates(map[string]any{
		"status": "waiting_approval", "revision": 12, "runtime_phase": "waiting_approval",
	}).Error; err != nil {
		t.Fatal(err)
	}
	events = agentWakeEventsAfter(t, db, 2)
	if len(events) != 1 || events[0].Sequence != 3 || events[0].Kind != "run_changed" ||
		events[0].RunID != "run-1" || events[0].Revision != 12 {
		t.Fatalf("Updates produced wrong run wake event: %+v", events)
	}

	// Delta reads start strictly after the caller's cursor and do not duplicate
	// the event at that cursor when advancing through multiple pages.
	page1 := agentWakeEventsAfter(t, db, 0)
	page2 := agentWakeEventsAfter(t, db, page1[len(page1)-1].Sequence)
	if len(page1) != 3 || len(page2) != 0 {
		t.Fatalf("unexpected cursor pages: page1=%+v page2=%+v", page1, page2)
	}
}

func TestAgentSchedulerIgnoresUnmatchedAndUnrelatedUpdates(t *testing.T) {
	db := openAgentSchedulerDB(t)
	if err := db.Create(&model.Task{ID: "task-1", UserID: "user-1", Status: model.TaskStatusQueued}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", "missing").Update("status", model.TaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", "task-1").Update("progress", 50).Error; err != nil {
		t.Fatal(err)
	}
	if events := agentWakeEventsAfter(t, db, 0); len(events) != 0 {
		t.Fatalf("unmatched or unrelated update emitted wake events: %+v", events)
	}

	if err := db.Create(&model.CloudAgentExecution{ID: "legacy", UserID: "user-1", Engine: "go", Status: "queued", Revision: 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", "legacy").Updates(map[string]any{
		"status": "running", "revision": 3,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if events := agentWakeEventsAfter(t, db, 0); len(events) != 0 {
		t.Fatalf("non-Pi run update emitted an event: %+v", events)
	}
}

func TestAgentSchedulerMigration44PreservesHistoricalAgentRows(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CloudAgentExecution{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{}); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	run := model.CloudAgentExecution{ID: "historical-run", UserID: "user-1", Engine: "pi", Status: "completed", Revision: 9, CreatedAt: created}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	oldEvent := model.CloudAgentEventRecord{RunID: run.ID, Sequence: 1, UserID: run.UserID, EventJSON: `{"type":"run_started"}`, CreatedAt: created}
	oldMessage := model.CloudAgentMessageRecord{RunID: run.ID, Kind: "canonical", Sequence: 1, UserID: run.UserID, MessageJSON: `{"role":"user","content":"keep me"}`}
	if err := db.Create(&oldEvent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&oldMessage).Error; err != nil {
		t.Fatal(err)
	}

	var migration44 *migration
	for index := range schemaMigrations {
		if schemaMigrations[index].version == 44 {
			migration44 = &schemaMigrations[index]
			break
		}
	}
	if migration44 == nil {
		t.Fatal("migration 44 is not registered")
	}
	if err := migration44.apply(db); err != nil {
		t.Fatalf("apply migration 44: %v", err)
	}
	for _, value := range []any{&model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentToolOperation{}, &model.AgentRuntimeInstance{}} {
		if !db.Migrator().HasTable(value) {
			t.Fatalf("migration 44 did not create %T", value)
		}
	}
	var gotRun model.CloudAgentExecution
	if err := db.First(&gotRun, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	var gotEvents []model.CloudAgentEventRecord
	if err := db.Where("run_id = ?", run.ID).Find(&gotEvents).Error; err != nil {
		t.Fatal(err)
	}
	var gotMessages []model.CloudAgentMessageRecord
	if err := db.Where("run_id = ?", run.ID).Find(&gotMessages).Error; err != nil {
		t.Fatal(err)
	}
	if gotRun.Status != run.Status || gotRun.Revision != run.Revision || len(gotEvents) != 1 ||
		gotEvents[0].EventJSON != oldEvent.EventJSON || len(gotMessages) != 1 || gotMessages[0].MessageJSON != oldMessage.MessageJSON {
		t.Fatalf("migration 44 changed historical run data: run=%+v events=%+v messages=%+v", gotRun, gotEvents, gotMessages)
	}
}

func openAgentSchedulerPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_AGENT_POSTGRES_DSN is not set")
	}
	db, err := Open(Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal("could not open the configured Agent scheduler integration database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("could not inspect the configured Agent scheduler integration database")
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var databaseName string
	if err := db.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatal("could not verify the Agent scheduler integration database name")
	}
	if databaseName != "canvas_event_integration" {
		t.Skip("TEST_AGENT_POSTGRES_DSN does not point to canvas_event_integration")
	}
	if err := db.AutoMigrate(&model.AgentEventCounter{}, &model.AgentWakeEvent{}); err != nil {
		t.Fatal("could not prepare Agent scheduler integration tables")
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AgentEventCounter{ID: 1}).Error; err != nil {
		t.Fatal("could not initialize the Agent scheduler integration counter")
	}
	return db
}

func agentSchedulerPostgresCounter(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var counter model.AgentEventCounter
	if err := db.First(&counter, "id = ?", 1).Error; err != nil {
		t.Fatal("could not read the Agent scheduler integration counter")
	}
	return counter.Value
}

// Wait until B is visibly blocked on PostgreSQL's counter-row lock. This
// proves the test actually exercises commit ordering instead of relying only
// on a timing assumption about goroutine scheduling.
func waitAgentSchedulerPostgresCounterLock(t *testing.T, db *gorm.DB, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("producer B returned before acquiring the counter lock: %v", err)
		default:
		}
		var waiting int64
		err := db.Raw(`SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND state = 'active'
			AND query ILIKE '%agent_event_counters%'`).Scan(&waiting).Error
		if err != nil {
			t.Fatal("could not inspect PostgreSQL lock wait state")
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("producer B never became visible waiting for the Agent event counter lock")
}

func runAgentSchedulerPostgresCommitOrder(t *testing.T, rollbackA bool) {
	t.Helper()
	db := openAgentSchedulerPostgresDB(t)
	base := agentSchedulerPostgresCounter(t, db)
	caseID := t.Name() + "-" + time.Now().UTC().Format("20060102T150405.000000000")
	runA, runB := caseID+"-A", caseID+"-B"
	appendRun := func(tx *gorm.DB, runID string) error {
		return model.AppendAgentWake(tx, model.AgentWakeEvent{RunID: runID, UserID: "scheduler-test", Kind: "run_changed"})
	}

	aHasCounterLock := make(chan struct{})
	releaseA := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseA) }) }
	defer release()
	aDone := make(chan error, 1)
	rollbackSentinel := errors.New("intentional producer A rollback")
	go func() {
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := appendRun(tx, runA); err != nil {
				return err
			}
			close(aHasCounterLock)
			<-releaseA
			if rollbackA {
				return rollbackSentinel
			}
			return nil
		})
		aDone <- err
	}()
	select {
	case <-aHasCounterLock:
	case err := <-aDone:
		t.Fatalf("producer A failed before holding the counter lock: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("producer A did not acquire the event counter lock")
	}

	bStarted := make(chan struct{})
	bDone := make(chan error, 1)
	go func() {
		close(bStarted)
		bDone <- db.Transaction(func(tx *gorm.DB) error { return appendRun(tx, runB) })
	}()
	<-bStarted
	waitAgentSchedulerPostgresCounterLock(t, db, bDone)

	// A's increment and B's attempted increment are both uncommitted. A reader
	// must still observe the committed prefix and must not see B leapfrog A.
	if visible := agentSchedulerPostgresCounter(t, db); visible != base {
		t.Fatalf("reader saw uncommitted counter: got %d want %d", visible, base)
	}
	var visibleB int64
	if err := db.Model(&model.AgentWakeEvent{}).Where("run_id = ?", runB).Count(&visibleB).Error; err != nil {
		t.Fatal("could not read visible wake events")
	}
	if visibleB != 0 {
		t.Fatalf("reader observed producer B before producer A finished: %d rows", visibleB)
	}
	var visibleMax int64
	if err := db.Model(&model.AgentWakeEvent{}).Select("COALESCE(MAX(sequence), 0)").Scan(&visibleMax).Error; err != nil {
		t.Fatal("could not read visible wake sequence")
	}
	if visibleMax != base {
		t.Fatalf("reader saw a wake sequence beyond the committed prefix: got %d want %d", visibleMax, base)
	}

	release()
	aErr := <-aDone
	bErr := <-bDone
	if rollbackA {
		if !errors.Is(aErr, rollbackSentinel) {
			t.Fatalf("producer A error = %v, want rollback", aErr)
		}
	} else if aErr != nil {
		t.Fatalf("producer A commit failed: %v", aErr)
	}
	if bErr != nil {
		t.Fatalf("producer B commit failed: %v", bErr)
	}

	var rows []model.AgentWakeEvent
	if err := db.Where("run_id IN ?", []string{runA, runB}).Order("sequence ASC").Find(&rows).Error; err != nil {
		t.Fatal("could not read committed producer events")
	}
	if rollbackA {
		if len(rows) != 1 || rows[0].RunID != runB || rows[0].Sequence != base+1 {
			t.Fatalf("A rollback left a sequence hole or event: base=%d rows=%+v", base, rows)
		}
	} else if len(rows) != 2 || rows[0].RunID != runA || rows[0].Sequence != base+1 || rows[1].RunID != runB || rows[1].Sequence != base+2 {
		t.Fatalf("committed producer order is not contiguous A then B: base=%d rows=%+v", base, rows)
	}
}

func TestAgentSchedulerPostgresAppendCommitOrder(t *testing.T) {
	runAgentSchedulerPostgresCommitOrder(t, false)
}

func TestAgentSchedulerPostgresAppendRollbackLeavesNoGap(t *testing.T) {
	runAgentSchedulerPostgresCommitOrder(t, true)
}
