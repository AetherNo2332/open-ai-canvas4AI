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
	session, _, err := repo.CloudAgentPiSession("user-1", "conversation-1")
	if err != nil {
		t.Fatalf("CloudAgentPiSession() error = %v", err)
	}
	entries := []model.CloudAgentPiEntry{
		{SessionID: session.ID, EntryID: "entry-1", UserID: "user-1", RunID: "run-1", EntryJSON: `{"type":"message","id":"entry-1","parentId":null}`},
		{SessionID: session.ID, EntryID: "entry-2", ParentID: "entry-1", UserID: "user-1", RunID: "run-1", EntryJSON: `{"type":"message","id":"entry-2","parentId":"entry-1"}`},
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
		SessionID: session.ID, EntryID: "entry-2", ParentID: "entry-1", UserID: "user-1", RunID: "run-1",
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
	if err := db.Model(&model.CloudAgentPiSession{}).Where("conversation_id = ? AND user_id = ?", "conversation-1", "user-1").Update("active_run_id", "").Error; err != nil {
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

func TestPiSessionsWithSameConversationIDAreIsolatedByOwner(t *testing.T) {
	repo, _ := newPiSessionTestRepository(t)
	conversationID := "shared-client-conversation"
	runs := []*model.CloudAgentExecution{
		{ID: "run-user-1", UserID: "user-1", CanvasID: "canvas-1", ConversationID: conversationID, CreatedAt: time.Now()},
		{ID: "run-user-2", UserID: "user-2", CanvasID: "canvas-2", ConversationID: conversationID, CreatedAt: time.Now()},
	}
	for _, run := range runs {
		if err := repo.AttachCloudAgentPiSession(run); err != nil {
			t.Fatalf("attach session for %s: %v", run.UserID, err)
		}
	}

	sessions := make([]*model.CloudAgentPiSession, len(runs))
	for index, run := range runs {
		session, _, err := repo.CloudAgentPiSession(run.UserID, conversationID)
		if err != nil {
			t.Fatalf("load session for %s: %v", run.UserID, err)
		}
		sessions[index] = session
	}
	if sessions[0].ID == sessions[1].ID {
		t.Fatalf("different owners share Pi session storage ID %q", sessions[0].ID)
	}

	for index, run := range runs {
		session := sessions[index]
		entryID := "entry-" + run.UserID
		entry := model.CloudAgentPiEntry{
			SessionID: session.ID, EntryID: entryID, UserID: run.UserID, RunID: run.ID,
			EntryJSON: `{"type":"message","id":"` + entryID + `","owner":"` + run.UserID + `"}`,
		}
		if _, err := repo.AppendCloudAgentPiSessionEntries(run.UserID, conversationID, run.ID, session.Revision, entryID, []model.CloudAgentPiEntry{entry}); err != nil {
			t.Fatalf("append entry for %s: %v", run.UserID, err)
		}
	}
	for index, run := range runs {
		_, entries, err := repo.CloudAgentPiSession(run.UserID, conversationID)
		if err != nil {
			t.Fatalf("reload session for %s: %v", run.UserID, err)
		}
		if len(entries) != 1 || entries[0].UserID != run.UserID || entries[0].SessionID != sessions[index].ID {
			t.Fatalf("session entries leaked across owners for %s: %#v", run.UserID, entries)
		}
	}
}
