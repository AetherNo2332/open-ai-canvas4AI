package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 用户在生成期间手动切换版本（浏览器 adoptMediaVersion 整份保存）后，新结果只进入版本历史。
func TestMediaVersionWritebackKeepsManualAdoptionDuringGeneration(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	for _, id := range []string{"v1", "v2", "v3"} {
		if err := db.Create(&model.Resource{ID: id, UserID: "owner", Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed := req.Command
	seed.OperationID = "seed-history"
	_, _, err := cs.ExecuteHost(context.Background(), p, seed, "task_result", map[string]any{"seed": "history"}, true, func(_ *repository.Repository, doc commands.Document) (commands.Document, map[string]bool, error) {
		node := findNode(doc, "target")
		node["type"] = "image"
		m := metadata(node)
		m["content"], m["storageKey"], m["status"] = "/api/resources/v2/file", "resource:v2", "success"
		m["mediaVersions"] = []any{
			map[string]any{"id": "a", "number": 1, "createdAt": "2026-10-01T00:00:00.000Z", "source": "upload", "media": map[string]any{"storageKey": "resource:v1", "content": "/api/resources/v1/file"}},
			map[string]any{"id": "b", "number": 2, "createdAt": "2026-10-02T00:00:00.000Z", "source": "upload", "media": map[string]any{"storageKey": "resource:v2", "content": "/api/resources/v2/file"}},
		}
		m["currentMediaVersionId"] = "b"
		return doc, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	req.Command.BaseRevision, req.Command.OperationID, req.Intent, req.Task.Mode = snap.Revision, "regenerate", "new_version", "image"
	handle, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	page, err := cs.ReadMediaVersions(context.Background(), p, req.Command.CanvasID, "target")
	if err != nil || page.Generating == nil || page.Generating.TaskID != handle.TaskID || len(page.Items) != 2 || page.CanCreateVersion {
		t.Fatalf("in-flight generation must be reported apart from versions: %+v %v", page, err)
	}
	// Browser adopts V1 and saves the whole document (upstream adoptMediaVersion clears taskId).
	row, err := repo.CanvasProjectForUser(p.OwnerID, req.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	adopted, err := mediaversions.Adopt(findNode(snap.Document, "target"), "a")
	if err != nil {
		t.Fatal(err)
	}
	replaceNode(snap.Document, adopted)
	raw, _ := json.Marshal(snap.Document)
	row.PayloadJSON = string(raw)
	if err = repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&model.Task{}).Where("id = ?", handle.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"images":[{"resourceId":"v3"}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err = repo.EnqueueCanvasMaterialization(handle.TaskID); err != nil {
		t.Fatal(err)
	}
	if err = s.Materialize(context.Background(), handle.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	node := findNode(snap.Document, "target")
	versions := mediaversions.List(node)
	if len(versions) != 3 || metadata(node)["storageKey"] != "resource:v1" || metadata(node)["currentMediaVersionId"] != "a" {
		t.Fatalf("manual selection overwritten: %+v", metadata(node))
	}
	generation, _ := versions[2]["generation"].(map[string]any)
	if generation["taskId"] != handle.TaskID || versions[2]["source"] != "generate" {
		t.Fatalf("agent version lacks provenance: %+v", versions[2])
	}
	job, _ := repo.CanvasMaterialization(handle.TaskID)
	read, err := s.ReadSubmission(context.Background(), p, req.Command.CanvasID, req.Command.DocumentEpoch, req.Command.ClientID, req.Command.OperationID)
	if err != nil || job.Outcome != "selection_changed" || read.VersionID != str(versions[2]["id"]) {
		t.Fatalf("receipt: %+v job=%+v %v", read, job, err)
	}
	binding, _ := repo.CanvasTaskBinding(handle.TaskID)
	if base, _, _ := mediaversions.BindingBase(binding); base.Adopted || !base.Settled {
		t.Fatalf("binding outcome: %+v", base)
	}
}
