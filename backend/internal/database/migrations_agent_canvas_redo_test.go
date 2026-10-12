package database

import (
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestMigrateSchemaV58PreservesExistingAgentUndoSnapshot(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:agent-parity-redo-v58?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	// Rehearse the actual v57 shape, not an already-expanded model table.
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasColumn(&model.CloudAgentCanvasMutation{}, "after_json") {
		if err := db.Migrator().DropColumn(&model.CloudAgentCanvasMutation{}, "after_json"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version = ?", 58).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO cloud_agent_canvas_mutations (id,run_id,user_id,canvas_id,step_id,operation,before_json,status) VALUES ('m','r','u','c','s','canvas_apply_ops','{"nodes":[]}','applied')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	var mutation model.CloudAgentCanvasMutation
	if err := db.First(&mutation, "id = ?", "m").Error; err != nil {
		t.Fatal(err)
	}
	if mutation.BeforeJSON != `{"nodes":[]}` || mutation.Status != "applied" || mutation.AfterJSON != "" {
		t.Fatalf("migration changed existing undo history: %#v", mutation)
	}
	if !db.Migrator().HasColumn(&model.CloudAgentCanvasMutation{}, "after_json") {
		t.Fatal("redo snapshot column missing")
	}
}
