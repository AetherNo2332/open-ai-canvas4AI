package tasks

import (
	"context"
	"errors"
	"testing"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestTaskCenterCannotReuseCanvasGenerationAttempt(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	h := f.submit(t)
	f.finish(t, h.TaskID, model.TaskStatusFailed, "")
	f.materialize(t, h.TaskID)
	task, err := f.repo.Task(h.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RetryTaskWithBilling(f.principal.OwnerID, task, nil, 10); !errors.Is(err, repository.ErrCanvasTaskRequiresNewVersion) {
		t.Fatalf("canvas attempt reused: %v", err)
	}
	stored, err := f.repo.Task(h.TaskID)
	if err != nil || stored.Status != model.TaskStatusFailed {
		t.Fatalf("rejected retry changed task: %#v %v", stored, err)
	}
}

func TestRecoveredSuccessSurvivesPriorMaterializerCompletion(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	h := f.submit(t)
	if err := f.db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"media_recovery_json": "checkpoint", "media_stage": "upload"}).Error; err != nil {
		t.Fatal(err)
	}
	f.finish(t, h.TaskID, model.TaskStatusFailed, "")
	f.readyResource(t, "recovered-result")
	completed := false
	cs := commands.NewService(f.repo, commands.ServiceOptions{AfterCommit: func(receipt commands.Receipt) {
		if completed || receipt.OperationID != "task-result:"+h.TaskID {
			return
		}
		completed = true
		task, err := f.repo.Task(h.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.repo.RequeueTaskMediaRecovery(task, "retry-checkpoint"); err != nil {
			t.Fatal(err)
		}
		f.finish(t, h.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"recovered-result"}]}`)
	}})
	svc := NewService(f.repo, cs, f.submitter)
	if err := svc.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("interleaving was not reached")
	}
	job, err := f.repo.CanvasMaterialization(h.TaskID)
	if err != nil || job.State != "pending" {
		t.Fatalf("new recovery already closed: %#v %v", job, err)
	}
	// A stale finisher cannot close the next resolution either.
	if err := f.repo.FinishCanvasMaterialization(h.TaskID, "stale", 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	node := findNode(f.snapshot(t).Document, "output")
	if metadata(node)["status"] != "success" || metadata(node)["taskStatus"] != "succeeded" || metadata(node)["storageKey"] != "resource:recovered-result" {
		t.Fatal("prior failure materialization swallowed recovered success")
	}
}
