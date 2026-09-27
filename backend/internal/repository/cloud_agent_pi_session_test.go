package repository

import (
	"errors"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newPiSessionTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+newRepositoryID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CloudAgentPiSession{}, &model.CloudAgentPiEntry{}); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func TestPiSessionAppendIsScopedRevisionedAndIdempotent(t *testing.T) {
	repo, _ := newPiSessionTestRepository(t)
	run := &model.CloudAgentExecution{
		ID: "run-1", UserID: "user-1", CanvasID: "canvas-1", ConversationID: "conversation-1",
		Status: "queued", Engine: "pi", Revision: 1, CreatedAt: time.Now(),
	}
	if err := repo.AttachCloudAgentPiSession(run); err != nil {
		t.Fatalf("AttachCloudAgentPiSession() error = %v", err)
	}
	entries := []model.CloudAgentPiEntry{
		{SessionID: "conversation-1", EntryID: "entry-1", UserID: "user-1", RunID: "run-1", EntryJSON: `{"type":"message","id":"entry-1","parentId":null}`},
		{SessionID: "conversation-1", EntryID: "entry-2", ParentID: "entry-1", UserID: "user-1", RunID: "run-1", EntryJSON: `{"type":"message","id":"entry-2","parentId":"entry-1"}`},
	}
	revision, err := repo.AppendCloudAgentPiSessionEntries("user-1", "conversation-1", "run-1", 1, "entry-2", entries)
	if err != nil || revision != 2 {
		t.Fatalf("first append = revision %d, error %v; want revision 2", revision, err)
	}
	revision, err = repo.AppendCloudAgentPiSessionEntries("user-1", "conversation-1", "run-1", 1, "entry-2", entries)
	if err != nil || revision != 2 {
		t.Fatalf("identical replay = revision %d, error %v; want idempotent revision 2", revision, err)
	}
	if _, _, err := repo.CloudAgentPiSession("user-2", "conversation-1"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-user session lookup error = %v, want not found", err)
	}
	changed := []model.CloudAgentPiEntry{{
		SessionID: "conversation-1", EntryID: "entry-2", ParentID: "entry-1", UserID: "user-1", RunID: "run-1",
		EntryJSON: `{"type":"message","id":"entry-2","parentId":"entry-1","tampered":true}`,
	}}
	if _, err := repo.AppendCloudAgentPiSessionEntries("user-1", "conversation-1", "run-1", 2, "entry-2", changed); !errors.Is(err, ErrCreationConflict) {
		t.Fatalf("same entry ID with changed body error = %v, want creation conflict", err)
	}
	if _, err := repo.AppendCloudAgentPiSessionEntries("user-1", "conversation-1", "run-1", 1, "entry-1", nil); !errors.Is(err, ErrCreationConflict) {
		t.Fatalf("stale active leaf update error = %v, want creation conflict", err)
	}
}

func TestPiSessionRejectsConcurrentContinuationFromOneParent(t *testing.T) {
	repo, db := newPiSessionTestRepository(t)
	root := &model.CloudAgentExecution{ID: "root", UserID: "user-1", CanvasID: "canvas-1", ConversationID: "conversation-1", CreatedAt: time.Now()}
	if err := repo.AttachCloudAgentPiSession(root); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", "conversation-1").Update("active_run_id", "").Error; err != nil {
		t.Fatal(err)
	}
	first := &model.CloudAgentExecution{ID: "child-1", UserID: "user-1", CanvasID: "canvas-1", ConversationID: "conversation-1", ParentID: "root", CreatedAt: time.Now()}
	if err := repo.AttachCloudAgentPiSession(first); err != nil {
		t.Fatalf("first continuation attach error = %v", err)
	}
	sibling := &model.CloudAgentExecution{ID: "child-2", UserID: "user-1", CanvasID: "canvas-1", ConversationID: "conversation-1", ParentID: "root", CreatedAt: time.Now()}
	if err := repo.AttachCloudAgentPiSession(sibling); !errors.Is(err, ErrCreationConflict) {
		t.Fatalf("concurrent sibling attach error = %v, want creation conflict", err)
	}
}
