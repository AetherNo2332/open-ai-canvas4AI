package repository

import (
	"context"
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
)

func TestPruneMediaVersionsRespectsLiveSnapshotUndoAndTaskRoots(t *testing.T) {
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: t.TempDir() + "/prune.db"})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err = database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	payload := `{"nodes":[{"id":"node","metadata":{"mediaVersion":{"epoch":"epoch","generation":1,"currentVersionId":"base"}}}]}`
	project := model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: payload}
	if err = db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.CanvasDocumentEpoch{CanvasID: "canvas", OwnerID: "owner", Epoch: "epoch"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []model.CanvasMediaVersion{
		{ID: "base", OwnerID: "owner", CanvasID: "canvas", Epoch: "epoch", NodeID: "node", Generation: 1, Sequence: 1, RequestJSON: `{"prompt":"counted"}`, MediaJSON: `{}`, Status: "ready"},
		{ID: "orphan", OwnerID: "owner", CanvasID: "canvas", Epoch: "other", NodeID: "other", Generation: 1, Sequence: 1, RequestJSON: `{"prompt":"orphan"}`, Status: "ready"},
	} {
		if err = db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := New(db)
	prune := func() {
		t.Helper()
		if err := repo.CanvasAtomicWrite(context.Background(), "owner", "canvas", func(tx *Repository) error { return tx.PruneCanvasMediaVersions("owner", "canvas") }); err != nil {
			t.Fatal(err)
		}
	}
	prune()
	var count int64
	db.Model(&model.CanvasMediaVersion{}).Count(&count)
	if count != 1 {
		t.Fatalf("live/orphan count=%d", count)
	}
	if err = db.Create(&model.CanvasSnapshot{ID: "snapshot", CanvasID: "canvas", UserID: "owner", PayloadJSON: payload}).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&model.CanvasProject{}).Where("id = ?", "canvas").Update("payload_json", `{"nodes":[]}`)
	prune()
	db.Model(&model.CanvasMediaVersion{}).Count(&count)
	if count != 1 {
		t.Fatal("snapshot root pruned")
	}
	db.Delete(&model.CanvasSnapshot{}, "id = ?", "snapshot")
	var doc map[string]any
	_ = json.Unmarshal([]byte(payload), &doc)
	change, _ := json.Marshal(map[string]any{"entities": []any{map[string]any{"key": "node:node", "value": doc["nodes"].([]any)[0]}}})
	db.Create(&model.CanvasActorHistory{ID: "undo", OwnerID: "owner", CanvasID: "canvas", Epoch: "epoch", State: "applied", ForwardJSON: string(change), InverseJSON: "{}"})
	prune()
	db.Model(&model.CanvasMediaVersion{}).Count(&count)
	if count != 1 {
		t.Fatal("undo root pruned")
	}
	db.Model(&model.CanvasDocumentEpoch{}).Where("canvas_id = ?", "canvas").Update("epoch", "restored")
	if err = db.Create(&model.CanvasMediaVersion{ID: "source-child", OwnerID: "owner", CanvasID: "canvas", Epoch: "restored", NodeID: "node", Generation: 2, Sequence: 1, SourceVersionID: "base", Status: "ready", MediaJSON: `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&model.CanvasProject{}).Where("id = ?", "canvas").Update("payload_json", `{"nodes":[{"id":"node","metadata":{"mediaVersion":{"epoch":"restored","generation":2,"currentVersionId":"source-child"}}}]}`)
	prune()
	db.Model(&model.CanvasMediaVersion{}).Count(&count)
	if count != 2 {
		t.Fatal("live source chain lost")
	}
	db.Model(&model.CanvasProject{}).Where("id = ?", "canvas").Update("payload_json", `{"nodes":[]}`)
	before, _ := repo.UserStorageUsage("owner")
	prune()
	after, _ := repo.UserStorageUsage("owner")
	db.Model(&model.CanvasMediaVersion{}).Count(&count)
	if count != 0 || after.TaskBytes >= before.TaskBytes {
		t.Fatalf("orphan did not free metadata quota: %d/%d count=%d", before.TaskBytes, after.TaskBytes, count)
	}
	taskID := "task"
	db.Create(&model.Task{ID: taskID, UserID: "owner", Status: model.TaskStatusSucceeded})
	db.Create(&model.CanvasMediaVersion{ID: "task-result", OwnerID: "owner", CanvasID: "gone", Epoch: "epoch", NodeID: "node", Generation: 1, Sequence: 1, TaskID: &taskID, Status: "ready"})
	if err = repo.PruneCanvasMediaVersions("owner", ""); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.CanvasMediaVersion{}).Count(&count)
	if count != 1 {
		t.Fatal("retained task receipt pruned")
	}
}
