package database

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestPrevisRenderQueuedTasksFailedByMigration(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:migration-previs-render-v79?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	tasks := []model.Task{
		{ID: "pv-queued", UserID: "user", Type: model.TaskTypePrevisRender, Status: model.TaskStatusQueued},
		{ID: "pv-running", UserID: "user", Type: model.TaskTypePrevisRender, Status: model.TaskStatusRunning},
		{ID: "pv-done", UserID: "user", Type: model.TaskTypePrevisRender, Status: model.TaskStatusSucceeded},
		{ID: "video-queued", UserID: "user", Type: "canvas_video", Status: model.TaskStatusQueued},
	}
	for index := range tasks {
		if err := db.Create(&tasks[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version = ?", previsRenderRetiredMigrationVersion).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("upgrade to v79: %v", err)
		}
	}
	want := map[string]model.TaskStatus{"pv-queued": model.TaskStatusFailed, "pv-running": model.TaskStatusFailed, "pv-done": model.TaskStatusSucceeded, "video-queued": model.TaskStatusQueued}
	for id, status := range want {
		var task model.Task
		if err := db.First(&task, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if task.Status != status {
			t.Fatalf("%s status = %s, want %s", id, task.Status, status)
		}
		if status == model.TaskStatusFailed && (task.Error != "服务端预演渲染已下线，请在导演台中重新预览" || task.CompletedAt == nil) {
			t.Fatalf("%s failure not recorded: %+v", id, task)
		}
	}
	status, err := ReadSchemaStatus(db)
	if err != nil || !status.Ready || status.Current != CurrentSchemaVersion || CurrentSchemaVersion < 79 {
		t.Fatalf("schema not ready after upgrade: %+v, %v", status, err)
	}
}
