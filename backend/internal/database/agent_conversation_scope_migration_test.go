package database_test

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
)

func TestAgentConversationScopeMigration(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		db, err := database.Open(database.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "scope.db")})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		checkAgentConversationScopeMigration(t, db)
	})
	t.Run("PostgreSQL", func(t *testing.T) {
		checkAgentConversationScopeMigration(t, openAgentAdmissionAuditPostgresDB(t))
	})
}

func checkAgentConversationScopeMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	legacy := `{"request":{"canvasId":"canvas"}}`
	drama := `{"request":{"canvasId":"","scope":{"type":"drama","id":"project"}},"context":{"version":1,"scope":{"userId":"user","business":{"type":"drama","id":"project"}}},"capabilities":{"definitionId":"drama.analysis","businessType":"drama"}}`
	for _, row := range []model.CloudAgentExecution{
		{ID: "legacy", UserID: "user", CanvasID: "canvas", StateJSON: legacy},
		{ID: "drama", UserID: "user", ConversationID: "drama", StateJSON: drama},
		{ID: "ambiguous", UserID: "user", CanvasID: "canvas", StateJSON: `{"request":{"canvasId":"another"}}`},
		{ID: "mixed-canvas", UserID: "user", ConversationID: "mixed", CanvasID: "canvas", StateJSON: legacy},
		{ID: "mixed-drama", UserID: "user", ConversationID: "mixed", StateJSON: drama},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.CloudAgentPiSession{ID: "drama-session", UserID: "user", ConversationID: "drama", ActiveRunID: "drama", HeaderJSON: `{"cwd":"canvas://"}`}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Exec("DELETE FROM schema_migrations WHERE version = 109").Error; err != nil {
			t.Fatal(err)
		}
		if err := database.MigrateSchema(db); err != nil {
			t.Fatal(err)
		}
	}
	var rows []model.CloudAgentExecution
	if err := db.Where("user_id = ?", "user").Find(&rows).Error; err != nil || len(rows) != 5 {
		t.Fatalf("migration changed history count: %d %v", len(rows), err)
	}
	for _, row := range rows {
		want := map[string]model.AgentConversationBinding{
			"legacy": {ScopeType: "canvas", ScopeID: "canvas", AgentDefinitionID: "canvas.default"},
			"drama":  {ScopeType: "drama", ScopeID: "project", AgentDefinitionID: "drama.analysis", AgentContextVersion: 1},
		}[row.ID]
		if row.AgentConversationBinding != want {
			t.Fatalf("%s binding %+v, expected %+v", row.ID, row.AgentConversationBinding, want)
		}
	}
	var session model.CloudAgentPiSession
	if err := db.First(&session, "id = ?", "drama-session").Error; err != nil ||
		session.ScopeID != "project" || session.HeaderJSON != `{"cwd":"drama://project"}` {
		t.Fatalf("wrong migrated session: %+v %v", session, err)
	}
}
