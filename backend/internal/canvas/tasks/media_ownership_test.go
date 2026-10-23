package tasks

import (
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestOrdinaryMediaSubmissionFreezesActualStoryboardOwnership(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	first := f.submit(t)
	f.readyResource(t, "old")
	f.finish(t, first.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"old"}]}`)
	f.materialize(t, first.TaskID)
	f.request.TargetNodeID = "output"
	f.request.Consumer = Consumer{}
	second := f.resubmit(t, "ordinary-version")
	if reason := f.bindOutput(t, "rebind", "row", "replacement"); reason != "" {
		t.Fatal(reason)
	}
	f.readyResource(t, "new")
	f.finish(t, second.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"new"}]}`)
	if outcome := f.materialize(t, second.TaskID); outcome != "output_binding_changed" {
		t.Fatalf("ordinary late result adopted after rebind: %s", outcome)
	}
	if metadata(findNode(f.snapshot(t).Document, "output"))["storageKey"] != "resource:old" {
		t.Fatal("old media overwritten")
	}
}

func TestMediaOwnerRecreationAndLegacyABARejectLateAdoption(t *testing.T) {
	for _, mutation := range []string{"parent-reborn", "row-reborn", "legacy-aba"} {
		t.Run(mutation, func(t *testing.T) {
			f := newReferenceLifecycleFixture(t)
			first := f.submit(t)
			f.readyResource(t, "old")
			f.finish(t, first.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"old"}]}`)
			f.materialize(t, first.TaskID)
			f.request.TargetNodeID = "output"
			f.request.Consumer = Consumer{}
			second := f.resubmit(t, "ordinary-version")
			switch mutation {
			case "parent-reborn":
				f.execute(t, "remove-board", commands.Operation{Type: "delete_node", ID: "board"})
				f.undo(t, "undo-board")
			case "row-reborn":
				doc := f.snapshot(t).Document
				board := metadata(findNode(doc, "board"))["storyboard"].(map[string]any)
				rows := board["rows"]
				board["rows"] = []any{}
				saveMediaOwnershipDocument(t, f, doc)
				board["rows"] = rows
				saveMediaOwnershipDocument(t, f, doc)
			case "legacy-aba":
				doc := f.snapshot(t).Document
				row := boardRow(t, doc)
				row["imageNodeId"] = "replacement"
				saveMediaOwnershipDocument(t, f, doc)
				row["imageNodeId"] = "output"
				saveMediaOwnershipDocument(t, f, doc)
			}
			f.readyResource(t, "new")
			f.finish(t, second.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"new"}]}`)
			if outcome := f.materialize(t, second.TaskID); outcome != "output_binding_changed" {
				t.Fatalf("recreated owner adopted: %s", outcome)
			}
			if metadata(findNode(f.snapshot(t).Document, "output"))["storageKey"] != "resource:old" {
				t.Fatal("old media overwritten")
			}
		})
	}
}

func saveMediaOwnershipDocument(t *testing.T, f *referenceLifecycleFixture, doc commands.Document) {
	t.Helper()
	row, err := f.repo.CanvasProjectForUser(f.principal.OwnerID, f.canvas())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	row.PayloadJSON = string(raw)
	if err = f.repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
}
