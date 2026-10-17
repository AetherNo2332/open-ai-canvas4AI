package database

import (
	"infinite-canvas/backend/internal/model"
	"testing"
	"time"
)

func TestToolSnapshotMigration59IsIdempotentAndPreservesRuns(t *testing.T) {
	db := openAgentSchedulerDB(t)
	before := model.CloudAgentExecution{ID: "snapshot-history", UserID: "user", Engine: "pi", Status: "completed", Revision: 7, StateJSON: `{"canonical":{"tools":[]},"sentinel":"preserve history"}`}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	var step *migration
	for i := range schemaMigrations {
		if schemaMigrations[i].version == 59 {
			step = &schemaMigrations[i]
			break
		}
	}
	if step == nil {
		t.Fatal("snapshot migration59 missing")
	}
	if err := step.apply(db); err != nil {
		t.Fatal(err)
	}
	runtime := model.AgentToolRuntime{InstanceID: "registered", BootID: "boot", SnapshotID: "snapshot", LoadGeneration: 1, ManualDraining: true, Revision: 3, LastHeartbeatAt: time.Now()}
	if err := db.Create(&runtime).Error; err != nil {
		t.Fatal(err)
	}
	if err := step.apply(db); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{&model.AgentToolSnapshot{}, &model.AgentToolSnapshotContent{}, &model.AgentToolRuntime{}, &model.AgentToolSnapshotAlert{}, &model.AgentToolSnapshotCounter{}} {
		if !db.Migrator().HasTable(value) {
			t.Fatalf("missing table %T", value)
		}
	}
	var after model.CloudAgentExecution
	if err := db.First(&after, "id = ?", before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.StateJSON != before.StateJSON || after.Revision != before.Revision || after.Status != before.Status {
		t.Fatalf("historical run changed: %+v", after)
	}
	var preserved model.AgentToolRuntime
	if err := db.First(&preserved, "instance_id = ?", runtime.InstanceID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.BootID != runtime.BootID || preserved.Revision != runtime.Revision || !preserved.ManualDraining {
		t.Fatalf("migration reset registration/drain: %+v", preserved)
	}
}
