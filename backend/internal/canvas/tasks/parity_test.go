package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"testing"
	"time"
)

func TestMediaOutputMetadataMatchesCanvasContract(t *testing.T) {
	for _, mode := range []string{"image", "video", "audio"} {
		t.Run(mode, func(t *testing.T) {
			s, db, repo, cs, p, req, _ := taskFixture(t)
			snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			node := findNode(snap.Document, "target")
			node["type"] = mode
			metadata(node)["unknownFixture"] = "preserved"
			raw, _ := json.Marshal(snap.Document)
			db.Model(&model.CanvasProject{}).Where("id = ?", snap.CanvasID).Update("payload_json", string(raw))
			req.Task.Mode = mode
			h, err := s.Submit(context.Background(), p, req)
			if err != nil {
				t.Fatal(err)
			}
			resource := model.Resource{ID: "media-fixture", UserID: p.OwnerID, Kind: mode, Status: model.ResourceStatusReady, MimeType: mode + "/fixture", Size: 1024, Width: 1920, Height: 1080, DurationMs: 2300}
			if err = db.Create(&resource).Error; err != nil {
				t.Fatal(err)
			}
			result := fmt.Sprintf(`{"%s":{"storageKey":"resource:media-fixture","dataUrl":"/api/resources/media-fixture/file","hasAudio":true}}`, mode)
			if mode == "image" {
				result = `{"images":[{"storageKey":"resource:media-fixture","dataUrl":"/api/resources/media-fixture/file"}]}`
			}
			db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": result})
			repo.EnqueueCanvasMaterialization(h.TaskID)
			if err = s.Materialize(context.Background(), h.TaskID); err != nil {
				t.Fatal(err)
			}
			snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
			m := metadata(findNode(snap.Document, "target"))
			if m["status"] != "success" || m["storageKey"] != "resource:media-fixture" || numeric(m["bytes"]) != 1024 || m["mimeType"] != mode+"/fixture" || m["unknownFixture"] != "preserved" {
				t.Fatalf("metadata mismatch: %+v", m)
			}
		})
	}
}
func TestStructuredOutputAndRowTaskGuard(t *testing.T) {
	for _, kind := range []string{"batch", "storyboard"} {
		t.Run(kind, func(t *testing.T) {
			s, db, repo, cs, p, req, stub := taskFixture(t)
			snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			source := findNode(snap.Document, "target")
			key, nodeType, field := "batchTable", "batch-table", "outputNodeId"
			if kind == "storyboard" {
				key, nodeType, field = "storyboard", "script", "imageNodeId"
			}
			source["type"] = nodeType
			metadata(source)[key] = map[string]any{"rows": []any{map[string]any{"id": "row1", field: "output", "unknown": "keep"}}}
			output := map[string]any{"id": "output", "type": "image", "position": map[string]any{"x": 500, "y": 0}, "width": 320, "height": 320, "metadata": map[string]any{}}
			snap.Document["nodes"] = append(snap.Document["nodes"].([]any), output)
			raw, _ := json.Marshal(snap.Document)
			db.Model(&model.CanvasProject{}).Where("id = ?", snap.CanvasID).Update("payload_json", string(raw))
			req.Task.Mode = "image"
			req.Consumer = Consumer{Kind: kind, RowID: "row1", OutputNodeID: "output"}
			if kind == "storyboard" {
				req.Consumer.Field = field
			}
			h, err := s.Submit(context.Background(), p, req)
			if err != nil {
				t.Fatal(err)
			}
			second := req
			second.Command.OperationID = "second"
			current, _ := cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
			second.Command.BaseRevision = current.Revision
			if _, err = s.Submit(context.Background(), p, second); err == nil || stub.calls != 1 {
				t.Fatal("row task replaced while active")
			}
			db.Create(&model.Resource{ID: "structured-image", UserID: p.OwnerID, Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Size: 1234, Width: 640, Height: 480})
			db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"images":[{"storageKey":"resource:structured-image","dataUrl":"/api/resources/structured-image/file"}]}`})
			repo.EnqueueCanvasMaterialization(h.TaskID)
			if err = s.Materialize(context.Background(), h.TaskID); err != nil {
				t.Fatal(err)
			}
			snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
			row := consumerRow(findNode(snap.Document, "target"), req.Consumer)
			if row["unknown"] != "keep" || row["status"] != "success" || metadata(findNode(snap.Document, "output"))["storageKey"] != "resource:structured-image" {
				t.Fatalf("structured output differs: %+v", snap.Document)
			}
		})
	}
}
func TestCancellationEnqueuesJobAtomically(t *testing.T) {
	s, _, repo, _, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.CancelTaskIfStatus(p.OwnerID, h.TaskID, model.TaskStatusQueued, time.Now()); err != nil || !ok {
		t.Fatal(err)
	}
	if _, err = repo.CanvasMaterialization(h.TaskID); err != nil {
		t.Fatal("cancelled task without durable job")
	}
}
func TestReopenedSQLiteOnlyAppliesSavedOutput(t *testing.T) {
	s, db, repo, _, p, req, stub := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"saved before crash"}`})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	outputs, err := s.materializeOutputs(context.Background(), model.Task{ID: h.TaskID, UserID: p.OwnerID, Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"saved before crash"}`})
	if err != nil || len(outputs) != 1 {
		t.Fatal(err)
	}
	var paths []struct{ Name, File string }
	if err = db.Raw("PRAGMA database_list").Scan(&paths).Error; err != nil {
		t.Fatal(err)
	}
	var path string
	for _, p := range paths {
		if p.Name == "main" {
			path = p.File
		}
	}
	oldSQL, _ := db.DB()
	if err = oldSQL.Close(); err != nil {
		t.Fatal(err)
	}
	newDB, err := gorm.Open(sqlite.Open(path+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql, _ := newDB.DB(); _ = sql.Close() })
	newRepo := repository.New(newDB)
	newCommands := commands.NewService(newRepo, commands.ServiceOptions{})
	reopened := NewService(newRepo, newCommands, stub)
	if err = reopened.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ := newCommands.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	raw, _ := json.Marshal(snap.Document)
	if !contains(string(raw), "saved before crash") || stub.calls != 1 {
		t.Fatal("regenerated saved result")
	}
}

func TestStoryboardDocumentResultParsesNestedJSONAndPreservesMetadata(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	n := findNode(snap.Document, "target")
	n["type"] = "script"
	metadata(n)["storyboard"] = map[string]any{"rows": []any{map[string]any{"id": "old", "durationSeconds": 6.0}}, "referenceNodeIds": []any{}, "unknown": "keep"}
	raw, _ := json.Marshal(snap.Document)
	db.Model(&model.CanvasProject{}).Where("id = ?", snap.CanvasID).Update("payload_json", string(raw))
	req.Task.Operation = "storyboard"
	req.Consumer = Consumer{Kind: "storyboard_document"}
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"text": "```json\n{\"title\":\"专业拆镜\",\"rows\":[{\"durationSeconds\":4,\"plotDescription\":{\"text\":\"入场\"},\"dialogue\":[\"一句\",\"两句\"],\"videoMotionPrompt\":\"推镜\"}]}\n```"}
	result, _ := json.Marshal(payload)
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(result)})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	n = findNode(snap.Document, "target")
	board := metadata(n)["storyboard"].(map[string]any)
	rows := objectList(board["rows"])
	if n["title"] != "专业拆镜" || board["unknown"] != "keep" || len(rows) != 1 || rows[0]["plotDescription"] != "入场" || rows[0]["dialogue"] != "一句、两句" || rows[0]["id"] == "old" {
		t.Fatalf("storyboard differs: %+v", snap.Document)
	}
}
