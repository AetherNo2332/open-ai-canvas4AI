package database_test

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestAgentContextPolicyMigration(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		db, err := database.Open(database.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "agent-context.db") + "?_busy_timeout=5000"})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		checkAgentContextPolicyMigration(t, db)
	})
	t.Run("PostgreSQL", func(t *testing.T) {
		checkAgentContextPolicyMigration(t, openAgentAdmissionAuditPostgresDB(t))
	})
}

func checkAgentContextPolicyMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	results := make(chan error, 2)
	var writers sync.WaitGroup
	for _, tag := range []string{"zh-CN", "ja-JP"} {
		writers.Go(func() {
			_, err := repo.SaveAgentSharedPreferences("user", model.AgentSharedPreferences{Language: tag})
			results <- err
		})
	}
	writers.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, repository.ErrTaskStateConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("concurrent revision 0 writes: successes=%d conflicts=%d", success, conflicts)
	}
	canvas := model.AgentScopeMemoryKey{UserID: "user", ScopeType: "canvas", ScopeID: "object"}
	drama := model.AgentScopeMemoryKey{UserID: "user", ScopeType: "drama", ScopeID: "object"}
	otherUser := model.AgentScopeMemoryKey{UserID: "other", ScopeType: "canvas", ScopeID: "object"}
	write := model.AgentScopeMemoryWrite{AgentScopeMemoryKey: canvas, RunID: "run", CallID: "call", OutcomeJSON: `{}`}
	if err := repo.SaveAgentScopeMemory(model.AgentScopeMemory{AgentScopeMemoryKey: canvas, Revision: 1, EntriesJSON: "[]"}, write); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []model.AgentScopeMemoryKey{drama, otherUser} {
		if _, err := repo.AgentScopeMemory(scope); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("memory readable from the wrong ownership key: %v", err)
		}
	}
	if saved, err := repo.AgentScopeMemory(canvas); err != nil || saved.Revision != 1 {
		t.Fatalf("migration retry lost the scoped memory: %+v %v", saved, err)
	}
	if prefs, err := repo.AgentSharedPreferences("user"); err != nil || prefs.Revision != 1 {
		t.Fatalf("migration retry lost the preference revision: %+v %v", prefs, err)
	}
}
