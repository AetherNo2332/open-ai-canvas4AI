package app

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

func seedPiCompactionBranch(t *testing.T, db *gorm.DB, run *model.CloudAgentExecution) (int64, string) {
	t.Helper()
	ids := []string{"compact-user-1", "compact-assistant-1", "compact-user-current"}
	messages := []map[string]any{
		{"role": "user", "content": "Keep the first request"},
		{"role": "assistant", "content": "First answer"},
		{"role": "user", "content": "Current unanswered request"},
	}
	entries := make([]model.CloudAgentPiEntry, 0, len(ids))
	parentID := ""
	for index, id := range ids {
		var parent any
		if parentID != "" {
			parent = parentID
		}
		value := map[string]any{"type": "message", "id": id, "parentId": parent,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": messages[index]}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, model.CloudAgentPiEntry{SessionID: run.ConversationID, Sequence: index + 1,
			EntryID: id, UserID: run.UserID, RunID: run.ID, ParentID: parentID, EntryJSON: string(raw)})
		parentID = id
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", run.ConversationID).
		Updates(map[string]any{"active_leaf_id": parentID, "revision": 2}).Error; err != nil {
		t.Fatal(err)
	}
	return 2, parentID
}

func TestPiContextCompactionUsesGoTaskAndCommitsPiEntryAtomically(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	sessionRevision, activeLeafID := seedPiCompactionBranch(t, db, run)
	request := PiContextCompactionStart{SessionRevision: sessionRevision, ActiveLeafID: activeLeafID,
		Reason: "threshold", TokensBefore: 24_000}

	operation, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, request)
	if err != nil {
		t.Fatalf("begin Go-backed Pi compaction: %v", err)
	}
	if operation.OperationID == "" || operation.TaskID == "" || operation.Status != string(model.TaskStatusQueued) {
		t.Fatalf("invalid initial compaction operation: %+v", operation)
	}
	created := operation.TaskID
	retry, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, request)
	if err != nil || retry.TaskID != created || retry.OperationID != operation.OperationID {
		t.Fatalf("retry did not resume the same compaction task: retry=%+v err=%v", retry, err)
	}
	var taskCount int64
	if err := db.Model(&model.Task{}).Where("user_id = ? AND operation = ?", run.UserID, cloudAgentContextCompactionOperation).Count(&taskCount).Error; err != nil || taskCount != 1 {
		t.Fatalf("compaction retry created %d billed tasks, error %v", taskCount, err)
	}

	if err := db.Model(&model.Task{}).Where("id = ?", created).Updates(map[string]any{
		"status": model.TaskStatusFailed, "result_json": `{}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	ready, err := s.PiContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID)
	if err != nil || ready.Status != "succeeded" || !ready.Fallback || ready.Summary == "" || ready.FirstKeptEntryID != "compact-user-1" {
		t.Fatalf("failed model task did not produce the Go fallback checkpoint: ready=%+v err=%v", ready, err)
	}
	if ready.Details["sourceDigest"] != ready.SourceDigest || ready.Details["checkpointDigest"] != cloudAgentTextDigest(ready.Summary) {
		t.Fatalf("compaction signature details are inconsistent: %+v", ready)
	}
	entry := map[string]any{"type": "compaction", "id": "compact-entry-1", "parentId": activeLeafID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "summary": ready.Summary,
		"firstKeptEntryId": ready.FirstKeptEntryID, "tokensBefore": ready.TokensBefore, "details": ready.Details, "fromHook": true}
	rawEntry, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	input := PiContextCompactionCommit{SessionRevision: sessionRevision, Entry: rawEntry}
	if err := db.Exec(`CREATE TRIGGER fail_pi_compaction_state_commit BEFORE UPDATE OF state_json ON cloud_agent_executions
		BEGIN SELECT RAISE(ABORT, 'injected compaction checkpoint failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiCommitContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID, input); err == nil {
		t.Fatal("injected Go checkpoint failure did not abort Pi compaction commit")
	}
	if err := db.Exec("DROP TRIGGER fail_pi_compaction_state_commit").Error; err != nil {
		t.Fatal(err)
	}
	rolledBackSession, rolledBackEntries, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil || rolledBackSession.Revision != sessionRevision || rolledBackSession.ActiveLeafID != activeLeafID || len(rolledBackEntries) != 3 {
		t.Fatalf("Pi compaction entry escaped a failed Go checkpoint transaction: session=%+v entries=%d err=%v",
			rolledBackSession, len(rolledBackEntries), err)
	}
	_, rolledBackState := reloadPiRun(t, s, run.ID)
	if rolledBackState.ContextCompaction == nil || rolledBackState.ContextCompaction.PiOperationID != operation.OperationID {
		t.Fatalf("failed atomic commit cleared the active operation: %+v", rolledBackState.ContextCompaction)
	}
	newRevision, err := s.PiCommitContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID, input)
	if err != nil || newRevision != sessionRevision+1 {
		t.Fatalf("commit Go checkpoint with Pi entry: revision=%d err=%v", newRevision, err)
	}
	newRevision, err = s.PiCommitContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID, input)
	if err != nil || newRevision != sessionRevision+1 {
		t.Fatalf("identical compaction commit was not idempotent: revision=%d err=%v", newRevision, err)
	}

	storedSession, storedEntries, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil || storedSession.Revision != sessionRevision+1 || storedSession.ActiveLeafID != "compact-entry-1" || len(storedEntries) != 4 {
		t.Fatalf("Pi tree was not committed with compaction entry: session=%+v entries=%d err=%v", storedSession, len(storedEntries), err)
	}
	storedRun, state := reloadPiRun(t, s, run.ID)
	if state.ContextCompaction != nil || state.ContextCheckpoint == nil || state.ContextCheckpoint.CompactedTurnCount != 2 || storedRun.Status != "running" {
		t.Fatalf("Go checkpoint and Pi operation state were not finalized: status=%s checkpoint=%+v compaction=%+v",
			storedRun.Status, state.ContextCheckpoint, state.ContextCompaction)
	}
	var eventRecords []model.CloudAgentEventRecord
	if err := db.Where("run_id = ?", run.ID).Find(&eventRecords).Error; err != nil {
		t.Fatal(err)
	}
	compactedEvents := 0
	for _, record := range eventRecords {
		var event CloudAgentEvent
		if json.Unmarshal([]byte(record.EventJSON), &event) == nil && event.Type == "context_compacted" {
			compactedEvents++
		}
	}
	if compactedEvents != 1 {
		t.Fatalf("compaction event count = %d, want 1", compactedEvents)
	}
}
