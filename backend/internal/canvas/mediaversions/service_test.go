package mediaversions

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func versionFixture(t *testing.T, kind string) (*repository.Repository, Scope, map[string]any) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "versions.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn, _ := db.DB(); _ = conn.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.Resource{}, &model.Task{}, &model.CanvasMediaVersion{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Task{ID: "old-task", UserID: "owner", Status: model.TaskStatusSucceeded}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "new"} {
		if err = db.Create(&model.Resource{ID: id, UserID: "owner", MimeType: kind + "/test", Status: model.ResourceStatusReady}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return repository.New(db), Scope{OwnerID: "owner", CanvasID: "canvas", Epoch: "epoch", NodeID: "node", Generation: 1}, map[string]any{
		"id": "node", "type": kind, "metadata": map[string]any{"content": "/api/resources/old/file", "storageKey": "resource:old", "status": "success", "taskId": "old-task", "composerContent": "next draft"},
	}
}

func versionTx(t *testing.T, repo *repository.Repository, fn func(*repository.Repository) error) {
	t.Helper()
	if err := repo.CommandTransaction(context.Background(), func(tx *repository.Repository) error {
		if err := tx.LockCanvasWrite("owner", ""); err != nil {
			return err
		}
		return fn(tx)
	}); err != nil {
		t.Fatal(err)
	}
}

func completedMetadata() map[string]any {
	return map[string]any{"content": "/api/resources/new/file", "storageKey": "resource:new", "status": "success", "taskId": "new-task", "prompt": "submitted prompt"}
}

func TestMediaVersionsKeepCurrentAndAdoptAllMediaKinds(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio"} {
		t.Run(kind, func(t *testing.T) {
			repo, scope, node := versionFixture(t, kind)
			task := model.Task{ID: "new-task", UserID: "owner", Type: "canvas_" + kind, Prompt: "submitted prompt", Status: model.TaskStatusQueued}
			var pending *model.CanvasMediaVersion
			versionTx(t, repo, func(tx *repository.Repository) error {
				var err error
				pending, err = Begin(tx, scope, node, task, "new_version")
				return err
			})
			m := node["metadata"].(map[string]any)
			if m["storageKey"] != "resource:old" || m["status"] != "success" {
				t.Fatalf("old output disappeared: %+v", m)
			}
			state := ReadState(node)
			if pending.Sequence != 2 || state.CurrentVersionID == "" || state.PendingVersionID != pending.ID || state.ActiveTaskID != task.ID {
				t.Fatalf("version state: %+v %+v", pending, state)
			}
			var replay *model.CanvasMediaVersion
			versionTx(t, repo, func(tx *repository.Repository) error {
				var err error
				replay, err = Begin(tx, scope, node, task, "new_version")
				return err
			})
			if replay.ID != pending.ID {
				t.Fatal("replay created another version")
			}
			task.Status = model.TaskStatusSucceeded
			versionTx(t, repo, func(tx *repository.Repository) error {
				adopted, reason, err := Finish(tx, scope, node, task, completedMetadata(), nil)
				if err == nil && (!adopted || reason != "adopted") {
					t.Fatalf("result not adopted: %v %s", adopted, reason)
				}
				return err
			})
			if m["storageKey"] != "resource:new" || m["composerContent"] != "next draft" {
				t.Fatalf("media/draft projection: %+v", m)
			}
			if ReadState(node).CurrentVersionID != pending.ID || ReadState(node).PendingVersionID != "" {
				t.Fatal("pending state not settled")
			}
			versionTx(t, repo, func(tx *repository.Repository) error { return Use(tx, scope, node, state.CurrentVersionID) })
			if m["storageKey"] != "resource:old" {
				t.Fatal("history cannot restore old output")
			}
		})
	}
}

func TestMediaVersionsManualSelectionAndFailureRetainCurrent(t *testing.T) {
	for _, failure := range []bool{false, true} {
		repo, scope, node := versionFixture(t, "image")
		task := model.Task{ID: "new-task", UserID: "owner", Type: "canvas_image", Status: model.TaskStatusQueued}
		versionTx(t, repo, func(tx *repository.Repository) error {
			_, err := Begin(tx, scope, node, task, "new_version")
			return err
		})
		current := ReadState(node).CurrentVersionID
		if !failure {
			versionTx(t, repo, func(tx *repository.Repository) error { return Use(tx, scope, node, current) })
		}
		task.Status = model.TaskStatusSucceeded
		if failure {
			task.Status = model.TaskStatusFailed
		}
		versionTx(t, repo, func(tx *repository.Repository) error {
			adopted, _, err := Finish(tx, scope, node, task, completedMetadata(), nil)
			if adopted {
				t.Fatal("failed task or explicit user selection was overwritten")
			}
			return err
		})
		if node["metadata"].(map[string]any)["storageKey"] != "resource:old" || ReadState(node).PendingVersionID != "" {
			t.Fatal("old output or settlement lost")
		}
	}
}

func TestMediaVersionsRejectSecondTaskAndWrongOwner(t *testing.T) {
	repo, scope, node := versionFixture(t, "image")
	task := model.Task{ID: "new-task", UserID: "owner", Type: "canvas_image", Status: model.TaskStatusQueued}
	versionTx(t, repo, func(tx *repository.Repository) error {
		_, err := Begin(tx, scope, node, task, "new_version")
		return err
	})
	task.ID = "second-task"
	if _, err := Begin(repo, scope, node, task, "new_version"); err == nil {
		t.Fatal("two unresolved versions accepted")
	}
	scope.OwnerID = "other"
	if err := Use(repo, scope, node, ReadState(node).CurrentVersionID); err == nil {
		t.Fatal("foreign version selected")
	}
}
