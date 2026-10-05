package repository

import (
	"errors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"path/filepath"
	"sync"
	"testing"
)

func newWorkspaceRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.AgentWorkspace{}, &model.AgentWorkspaceSkill{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestAgentWorkspaceCASAcrossIndependentConnections(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "workspace.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	open := func() *Repository {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		return New(db)
	}
	first, second := open(), open()
	if err := first.db.AutoMigrate(&model.AgentWorkspace{}, &model.AgentWorkspaceSkill{}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.AgentWorkspaceSnapshot("user", "canvas"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, repo := range []*Repository{first, second} {
		wg.Add(1)
		go func(repo *Repository) {
			defer wg.Done()
			<-start
			_, err := repo.UpdateAgentWorkspace("user", "canvas", 0, "new", "hash")
			results <- err
		}(repo)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var appErr *kernel.AppError
		if errors.As(err, &appErr) && appErr.Reason == kernel.ReasonAgentWorkspaceRevisionConflict {
			conflicts++
		} else {
			t.Fatalf("expected CAS conflict, got %v", err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", success, conflicts)
	}
}

func TestAgentWorkspaceCreatesOneRowAndFreezesSnapshot(t *testing.T) {
	repo := newWorkspaceRepository(t)
	first, err := repo.AgentWorkspaceSnapshot("user-1", "canvas-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 0 || first.CanvasID != "canvas-1" {
		t.Fatalf("initial workspace = %+v", first)
	}
	revision, err := repo.UpdateAgentWorkspace("user-1", "canvas-1", 0, "# rules", "hash-1")
	if err != nil || revision != 1 {
		t.Fatalf("update: revision=%d err=%v", revision, err)
	}
	if _, err := repo.ReplaceAgentWorkspaceSkills("user-1", "canvas-1", 1, []model.AgentWorkspaceSkill{{SkillID: "skill-1", SkillVersionID: "version-1", Position: 0, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.AgentWorkspaceSnapshot("user-1", "canvas-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || got.AgentsMDHash != "hash-1" || len(got.Skills) != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestAgentWorkspaceCASAndUserIsolation(t *testing.T) {
	repo := newWorkspaceRepository(t)
	if _, err := repo.UpdateAgentWorkspace("user-1", "canvas-1", 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateAgentWorkspace("user-1", "canvas-1", 0, "stale", "hash"); err == nil {
		t.Fatal("stale revision should fail")
	} else {
		var appErr *kernel.AppError
		if !errors.As(err, &appErr) || appErr.Reason != kernel.ReasonAgentWorkspaceRevisionConflict {
			t.Fatalf("unexpected conflict: %v", err)
		}
	}
	if _, err := repo.AgentWorkspaceSnapshot("user-2", "canvas-1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-user workspace must be hidden: %v", err)
	}
}

func TestAgentWorkspaceEmptyHashAndSnapshotIsolation(t *testing.T) {
	repo := newWorkspaceRepository(t)
	before, err := repo.AgentWorkspaceSnapshot("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	if before.AgentsMDHash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty document must have canonical SHA256: %q", before.AgentsMDHash)
	}
	if _, err := repo.UpdateAgentWorkspace("user", "canvas", 0, "new", "hash"); err != nil {
		t.Fatal(err)
	}
	if before.AgentsMD != "" || before.Revision != 0 {
		t.Fatal("read snapshot was mutated")
	}
}
