package database

import (
	"testing"
	"yingce/backend/internal/model"
)

func TestAgentConversationArchiveMigrationPreservesHistory(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:agent-archive-upgrade?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CloudAgentExecution{ID: "keep-archive-history", UserID: "user", Status: "completed", Revision: 7, StateJSON: `{"existing":"unchanged"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Migrator().DropColumn(&model.CloudAgentExecution{}, "ArchivedAt"); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("version = ?", 67).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	var run model.CloudAgentExecution
	if err = db.First(&run, "id = ?", "keep-archive-history").Error; err != nil || run.Revision != 7 || run.StateJSON != `{"existing":"unchanged"}` || run.ArchivedAt != nil {
		t.Fatalf("history changed: %#v %v", run, err)
	}
	if db.Migrator().HasColumn(&model.CloudAgentExecution{}, "conversation_archived") {
		t.Fatal("directory projection became a stored column")
	}
}
