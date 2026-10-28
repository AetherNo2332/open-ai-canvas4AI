package database

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestAgentMessageQueueMigrationFromV64PreservesRuns(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:agent-message-queue-upgrade?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CloudAgentExecution{ID: "keep-run", UserID: "user", ConversationID: "keep-run", CanvasID: "canvas", Status: "completed", Revision: 7, StateJSON: `{"existing":"unchanged"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Migrator().DropTable(&model.AgentQueuedMessage{}, &model.AgentConversationQueue{}, &model.AgentUISetting{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Where("version = ?", 65).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err = MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []any{&model.AgentQueuedMessage{}, &model.AgentConversationQueue{}, &model.AgentUISetting{}} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("queue table missing: %T", table)
		}
	}
	if !db.Migrator().HasIndex(&model.AgentQueuedMessage{}, "idx_agent_message_key") {
		t.Fatal("message receipt uniqueness missing")
	}
	var run model.CloudAgentExecution
	if err = db.First(&run, "id = ?", "keep-run").Error; err != nil || run.Revision != 7 || run.StateJSON != `{"existing":"unchanged"}` {
		t.Fatalf("upgrade changed old execution: %+v %v", run, err)
	}
	var count int64
	if err = db.Model(&schemaMigration{}).Where("version = ?", 65).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("migration not recorded: %d %v", count, err)
	}
}
