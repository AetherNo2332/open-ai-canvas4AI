package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCanvasMediaNewVersionRetainsAndAdoptsWithoutChangingNode(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio"} {
		t.Run(kind, func(t *testing.T) {
			s, db, repo, cs, p, req, _ := taskFixture(t)
			for _, id := range []string{"old", "new"} {
				if err := db.Create(&model.Resource{ID: id, UserID: "owner", Kind: kind, MimeType: kind + "/test", Status: model.ResourceStatusReady}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&model.Task{ID: "original-task", UserID: "owner", ProjectID: req.Command.CanvasID, Status: model.TaskStatusSucceeded}).Error; err != nil {
				t.Fatal(err)
			}
			seed := req.Command
			seed.OperationID = "seed-original-media"
			_, _, err := cs.ExecuteHost(context.Background(), p, seed, "task_result", map[string]any{"seed": "original"}, true, func(_ *repository.Repository, doc commands.Document) (commands.Document, map[string]bool, error) {
				node := findNode(doc, "target")
				node["type"] = kind
				m := metadata(node)
				m["content"], m["storageKey"], m["taskId"], m["status"], m["composerContent"] = "/api/resources/old/file", "resource:old", "original-task", "success", "a newer draft"
				return doc, nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			snap, err := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			req.Command.BaseRevision = snap.Revision
			req.Command.OperationID = "new-version"
			req.Intent = "new_version"
			req.Task.Mode = kind
			handle, err := s.Submit(context.Background(), p, req)
			if err != nil {
				t.Fatal(err)
			}
			if handle.VersionID == "" {
				t.Fatal("submission omitted durable version identity")
			}
			replay, err := s.Submit(context.Background(), p, req)
			if err != nil || replay.TaskID != handle.TaskID || replay.VersionID != handle.VersionID {
				t.Fatalf("retry changed identity: %+v %v", replay, err)
			}
			snap, err = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			node := findNode(snap.Document, "target")
			previousVersionID := mediaversions.ReadState(node).CurrentVersionID
			if metadata(node)["storageKey"] != "resource:old" || metadata(node)["status"] != "success" || mediaversions.ReadState(node).ActiveTaskID != handle.TaskID {
				t.Fatalf("old media unavailable during update: %+v", metadata(node))
			}
			result := fmt.Sprintf(`{"%s":{"resourceId":"new"}}`, kind)
			if kind == "image" {
				result = `{"images":[{"resourceId":"new"}]}`
			}
			if err = db.Model(&model.Task{}).Where("id = ?", handle.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": result}).Error; err != nil {
				t.Fatal(err)
			}
			if err = repo.EnqueueCanvasMaterialization(handle.TaskID); err != nil {
				t.Fatal(err)
			}
			if err = s.Materialize(context.Background(), handle.TaskID); err != nil {
				t.Fatal(err)
			}
			snap, err = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			node = findNode(snap.Document, "target")
			if metadata(node)["storageKey"] != "resource:new" || metadata(node)["composerContent"] != "a newer draft" || mediaversions.ReadState(node).CurrentVersionID != handle.VersionID {
				t.Fatalf("new media not adopted or draft overwritten: %+v", metadata(node))
			}
			if len(objectList(snap.Document["nodes"])) != 1 {
				t.Fatal("regeneration scattered a new canvas node")
			}
			choose := req.Command
			choose.OperationID = "choose-old-version"
			choose.BaseRevision = snap.Revision
			choose.Operations = []commands.Operation{{Type: "use_media_version", ID: "target", VersionID: previousVersionID}}
			if _, err = cs.Execute(context.Background(), p, choose); err != nil {
				t.Fatal(err)
			}
			snap, err = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			if metadata(findNode(snap.Document, "target"))["storageKey"] != "resource:old" {
				t.Fatal("history selection did not change actual node media")
			}
			page, err := cs.ReadMediaVersions(context.Background(), p, snap.CanvasID, "target", 0)
			if err != nil || len(page.Items) != 2 || !page.CanCreateVersion || page.State.CurrentVersionID != previousVersionID {
				t.Fatalf("history read: %+v %v", page, err)
			}
			unsafe := choose
			unsafe.OperationID = "unsafe-media-patch"
			unsafe.BaseRevision = snap.Revision
			unsafe.Operations = []commands.Operation{{Type: "bind_asset", ID: "target", AssetID: str(page.Items[0].Media["assetId"])}}
			if _, err = cs.Execute(context.Background(), p, unsafe); err == nil {
				t.Fatal("generic edit replaced managed media")
			}
			unsafe.OperationID = "mixed-selection-patch"
			unsafe.Operations = append([]commands.Operation{{Type: "use_media_version", ID: "target", VersionID: handle.VersionID}}, unsafe.Operations...)
			if _, err = cs.Execute(context.Background(), p, unsafe); err == nil {
				t.Fatal("mixed batch bypassed managed media protection")
			}
			row, err := repo.CanvasProjectForUser(p.OwnerID, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			delete(metadata(findNode(snap.Document, "target")), "mediaVersion")
			raw, _ := json.Marshal(snap.Document)
			row.PayloadJSON = string(raw)
			if err = repo.UpsertCanvasProject(row); !errors.Is(err, repository.ErrCanvasMediaVersionManaged) {
				t.Fatalf("legacy save erased managed state: %v", err)
			}
		})
	}
}
