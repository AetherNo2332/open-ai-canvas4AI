package tasks

import (
	"context"
	"testing"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
)

func TestMediaVersionSaveRecoveryReusesOriginalTaskAndReceipt(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	handle := f.submit(t)
	if err := f.db.Model(&model.Task{}).Where("id = ?", handle.TaskID).Updates(map[string]any{"media_recovery_json": "saved-provider-receipt", "media_stage": "upload"}).Error; err != nil {
		t.Fatal(err)
	}
	f.finish(t, handle.TaskID, model.TaskStatusFailed, "")
	if outcome := f.materialize(t, handle.TaskID); outcome != "media_save_pending" {
		t.Fatalf("save failure outcome %s", outcome)
	}
	snap := f.snapshot(t)
	state := mediaversions.ReadState(findNode(snap.Document, "output"))
	if state.ActiveTaskID != handle.TaskID {
		t.Fatal("save failure released original task")
	}
	attempt := f.request
	attempt.Command.OperationID = "new-paid-task"
	attempt.Command.BaseRevision = snap.Revision
	attempt.Intent = "new_version"
	if _, err := f.service.Submit(context.Background(), f.principal, attempt); err == nil {
		t.Fatal("recoverable media allowed another paid request")
	}
	f.readyResource(t, "saved-media")
	f.finish(t, handle.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"saved-media"}]}`)
	if outcome := f.materialize(t, handle.TaskID); outcome != "applied" {
		t.Fatalf("recovered outcome %s", outcome)
	}
	current := f.snapshot(t)
	node := findNode(current.Document, "output")
	if metadata(node)["storageKey"] != "resource:saved-media" || mediaversions.ReadState(node).ActiveTaskID != "" {
		t.Fatalf("recovered output: %+v", node)
	}
	if tasks, _, outputs, _ := f.counts(t); tasks != 1 || outputs != 1 || f.submitter.calls != 1 {
		t.Fatalf("recovery duplicated task: %d/%d prepare=%d", tasks, outputs, f.submitter.calls)
	}
}
