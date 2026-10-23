package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func baselineOperation(t *testing.T, id string, patch map[string]any, fields string) Operation {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": "update_node", "id": id, "patch": patch})
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var baseline map[string]any
	if err := json.Unmarshal([]byte(fields), &baseline); err != nil {
		t.Fatal(err)
	}
	input["fieldBaselines"] = baseline
	raw, _ = json.Marshal(input)
	var op Operation
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatal(err)
	}
	return op
}

func mergeRequest(before Snapshot, id string, ops ...Operation) Command {
	for i := range ops {
		if baseline := ops[i].FieldBaselines; baseline != nil && baseline.Generation == nil {
			if generation, exists := before.EntityGenerations["node:"+ops[i].ID]; exists {
				copy := *baseline
				copy.Generation = &generation
				ops[i].FieldBaselines = &copy
			}
		}
	}
	return Command{Protocol: 1, CanvasID: before.CanvasID, DocumentEpoch: before.DocumentEpoch, ClientID: "client", OperationID: id, BaseRevision: before.Revision, ExpectedEntityVersions: before.EntityVersions, Operations: ops}
}

func TestFieldMergePreservesDifferentFieldsAcrossWriters(t *testing.T) {
	for _, writer := range []string{"command", "legacy", "host"} {
		t.Run(writer, func(t *testing.T) {
			svc, db, _, before := commandFixture(t)
			switch writer {
			case "command":
				_, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "rename", Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "remote title"}}))
				if err != nil {
					t.Fatal(err)
				}
			case "legacy":
				row, err := repository.New(db).CanvasProjectForUser("owner", before.CanvasID)
				if err != nil {
					t.Fatal(err)
				}
				doc, _ := cloneDocument(before.Document)
				entity(doc, "node:a")["title"] = "remote title"
				raw, _ := json.Marshal(doc)
				row.PayloadJSON = string(raw)
				if err := repository.New(db).UpsertCanvasProject(row); err != nil {
					t.Fatal(err)
				}
			case "host":
				_, _, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, mergeRequest(before, "host-title"), "task_result", map[string]any{"kind": "test"}, false, func(_ *repository.Repository, doc Document) (Document, map[string]bool, error) {
					entity(doc, "node:a")["title"] = "remote title"
					return doc, map[string]bool{"node:a": true}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			op := baselineOperation(t, "a", map[string]any{"x": 320, "content": "local body"}, `{"nodeType":"text","fields":{"x":{"exists":true,"value":0},"content":{"exists":true,"value":""}}}`)
			if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "different-field", op)); err != nil {
				t.Fatalf("unrelated title blocked field merge: %v", err)
			}
			after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			node := entity(after.Document, "node:a")
			if node["title"] != "remote title" || node["position"].(map[string]any)["x"] != float64(320) || node["metadata"].(map[string]any)["content"] != "local body" {
				t.Fatalf("lost fields: %+v", node)
			}
		})
	}
}

func TestFieldMergeSameFieldConflictAndReceiptIdentity(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	op := baselineOperation(t, "a", map[string]any{"content": "first"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
	req := mergeRequest(before, "baseline-id", op)
	first, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay changed: %+v %v", replay, err)
	}
	changed := baselineOperation(t, "a", map[string]any{"content": "first"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":"first"}}}`)
	reused := req
	reused.Operations = []Operation{changed}
	_, err = svc.Execute(context.Background(), ownerContext().Principal, reused)
	assertReason(t, err, "operation_id_reused")
	stale := mergeRequest(before, "same-field", op)
	_, err = svc.Execute(context.Background(), ownerContext().Principal, stale)
	assertReason(t, err, "entity_version_conflict")
	var count int64
	db.Model(&model.CanvasCommandEvent{}).Where("operation_id IN ?", []string{"baseline-id", "same-field"}).Count(&count)
	if count != 1 {
		t.Fatalf("rejected or replayed operation created event: %d", count)
	}
}

func TestFieldMergeMixedStructuralBatchStaysStrict(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			svc, _, _, before := commandFixture(t)
			_, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "rename", Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "remote"}}))
			if err != nil {
				t.Fatal(err)
			}
			patch := baselineOperation(t, "a", map[string]any{"content": "local"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
			structure := Operation{Type: "delete_node", ID: "b"}
			ops := []Operation{patch, structure}
			if !first {
				ops = []Operation{structure, patch}
			}
			_, err = svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "mixed", ops...))
			assertReason(t, err, "entity_version_conflict")
			after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			if entity(after.Document, "node:b") == nil || entity(after.Document, "node:a")["metadata"].(map[string]any)["content"] != "" {
				t.Fatal("partial mixed batch committed")
			}
		})
	}
}

func TestFieldMergeSameFieldLegacyAndHostRejectAtomically(t *testing.T) {
	for _, writer := range []string{"legacy", "host"} {
		t.Run(writer, func(t *testing.T) {
			svc, db, _, before := commandFixture(t)
			if writer == "legacy" {
				row, _ := repository.New(db).CanvasProjectForUser("owner", before.CanvasID)
				doc, _ := cloneDocument(before.Document)
				entity(doc, "node:a")["metadata"].(map[string]any)["content"] = "remote body"
				raw, _ := json.Marshal(doc)
				row.PayloadJSON = string(raw)
				if err := repository.New(db).UpsertCanvasProject(row); err != nil {
					t.Fatal(err)
				}
			} else {
				_, _, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, mergeRequest(before, "host-body"), "task_result", nil, false, func(_ *repository.Repository, doc Document) (Document, map[string]bool, error) {
					entity(doc, "node:a")["metadata"].(map[string]any)["content"] = "remote body"
					return doc, map[string]bool{"node:a": true}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			op := baselineOperation(t, "a", map[string]any{"content": "local body"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
			other := baselineOperation(t, "b", map[string]any{"x": 1200}, `{"nodeType":"text","fields":{"x":{"exists":true,"value":1000}}}`)
			_, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "same-remote-field", other, op))
			assertReason(t, err, "entity_version_conflict")
			after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			if !reflect.DeepEqual(current, after) {
				t.Fatal("failed field batch partially changed snapshot")
			}
		})
	}
}

func TestFieldMergeRichTextDerivedWriteAndProgress(t *testing.T) {
	svc, _, _, initial := commandFixture(t)
	oldRich := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "old"}}}}}
	newRich := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "new"}}}}}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(initial, "seed-rich", Operation{Type: "update_node", ID: "a", Patch: map[string]any{"richText": oldRich}})); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, initial.CanvasID)
	_, _, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, mergeRequest(before, "task-progress"), "task_result", nil, false, func(_ *repository.Repository, doc Document) (Document, map[string]bool, error) {
		meta := entity(doc, "node:a")["metadata"].(map[string]any)
		meta["progress"] = float64(65)
		meta["status"] = "processing"
		return doc, map[string]bool{"node:a": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(oldRich)
	insufficient := baselineOperation(t, "a", map[string]any{"richText": newRich}, `{"nodeType":"text","fields":{"richText":{"exists":true,"value":`+string(raw)+`}}}`)
	_, err = svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "missing-derived-guard", insufficient))
	assertReason(t, err, "entity_version_conflict")
	complete := baselineOperation(t, "a", map[string]any{"richText": newRich}, `{"nodeType":"text","fields":{"richText":{"exists":true,"value":`+string(raw)+`},"content":{"exists":true,"value":"old"}}}`)
	if _, err = svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "rich-author-merge", complete)); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	meta := entity(after.Document, "node:a")["metadata"].(map[string]any)
	if meta["content"] != "new" || !sameJSON(meta["richText"], newRich) || meta["progress"] != float64(65) || meta["status"] != "processing" {
		t.Fatalf("author/progress lost: %+v", meta)
	}
}

func TestFieldMergeGenerationMirrorsRequireGuardAndKeepProgress(t *testing.T) {
	svc, _, _, initial := commandFixture(t)
	spec := map[string]any{"version": float64(1), "mode": "image", "prompt": "old prompt", "options": map[string]any{}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(initial, "seed-media", Operation{Type: "add_node", ID: "img", NodeType: "image", X: num(0), Y: num(500)}, Operation{Type: "update_node", ID: "img", Patch: map[string]any{"generationSpec": spec}})); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, initial.CanvasID)
	_, _, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, mergeRequest(before, "media-progress"), "task_result", nil, false, func(_ *repository.Repository, doc Document) (Document, map[string]bool, error) {
		meta := entity(doc, "node:img")["metadata"].(map[string]any)
		meta["progress"] = float64(80)
		return doc, map[string]bool{"node:img": true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	missing := baselineOperation(t, "img", map[string]any{"content": "new prompt"}, `{"nodeType":"image","fields":{"content":{"exists":true,"value":"old prompt"}}}`)
	_, err = svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "no-generation-guard", missing))
	assertReason(t, err, "entity_version_conflict")
	raw, _ := json.Marshal(spec)
	guarded := baselineOperation(t, "img", map[string]any{"content": "new prompt"}, `{"nodeType":"image","fields":{"content":{"exists":true,"value":"old prompt"},"generationSpec":{"exists":true,"value":`+string(raw)+`}}}`)
	if _, err = svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "guarded-prompt", guarded)); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	meta := entity(after.Document, "node:img")["metadata"].(map[string]any)
	if meta["composerContent"] != "new prompt" || meta["generationSpec"].(map[string]any)["prompt"] != "new prompt" || meta["progress"] != float64(80) {
		t.Fatalf("prompt/progress drift: %+v", meta)
	}
}

func TestFieldMergeRelatedInputRemainsStrictEvenWhenAlsoPatched(t *testing.T) {
	svc, _, _, initial := commandFixture(t)
	spec := map[string]any{"version": float64(1), "mode": "image", "prompt": "old", "options": map[string]any{}, "referenceBindings": []any{map[string]any{"id": "source", "nodeId": "a", "mediaType": "text", "role": "source-text", "order": float64(0), "resolution": "latest"}}, "textInputMode": "append-sources"}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(initial, "seed-reference", Operation{Type: "add_node", ID: "img", NodeType: "image", X: num(0), Y: num(500)}, Operation{Type: "update_node", ID: "img", Patch: map[string]any{"generationSpec": spec}})); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, initial.CanvasID)
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "change-input", Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "remote"}})); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(spec)
	consumer := baselineOperation(t, "img", map[string]any{"content": "new"}, `{"nodeType":"image","fields":{"content":{"exists":true,"value":"old"},"generationSpec":{"exists":true,"value":`+string(raw)+`}}}`)
	input := baselineOperation(t, "a", map[string]any{"x": 300}, `{"nodeType":"text","fields":{"x":{"exists":true,"value":0}}}`)
	_, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "input-cannot-be-exempt", input, consumer))
	assertReason(t, err, "entity_version_conflict")
	after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if entity(after.Document, "node:a")["position"].(map[string]any)["x"] != float64(0) || entity(after.Document, "node:img")["metadata"].(map[string]any)["composerContent"] != "old" {
		t.Fatal("input conflict partially wrote batch")
	}
}

func TestFieldMergeMissingNullAndTypeAreDistinct(t *testing.T) {
	for _, tc := range []struct{ name, metadata, baseline, reason string }{
		{"missing", "{}", `{"nodeType":"text","fields":{"fontSize":{"exists":false,"value":null}}}`, ""},
		{"null", "{\"fontSize\":null}", `{"nodeType":"text","fields":{"fontSize":{"exists":true,"value":null}}}`, ""},
		{"missing-is-not-null", "{}", `{"nodeType":"text","fields":{"fontSize":{"exists":true,"value":null}}}`, "entity_version_conflict"},
		{"null-is-not-missing", "{\"fontSize\":null}", `{"nodeType":"text","fields":{"fontSize":{"exists":false,"value":null}}}`, "entity_version_conflict"},
		{"wrong-type", "{}", `{"nodeType":"markdown","fields":{"fontSize":{"exists":false,"value":null}}}`, "entity_version_conflict"},
		{"wrong-variant", "{\"workflowKind\":\"character\"}", `{"nodeType":"text","fields":{"fontSize":{"exists":false,"value":null}}}`, "entity_version_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := fixture(t, `{"nodes":[{"id":"a","type":"text","metadata":`+tc.metadata+`}],"connections":[]}`)
			op := baselineOperation(t, "a", map[string]any{"fontSize": 16}, tc.baseline)
			_, err := Plan(doc, command(op), ownerContext())
			if tc.reason != "" {
				assertReason(t, err, tc.reason)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFieldMergeCannotBypassOldClientFallbackDeletionLeaseOrSQLAtomicity(t *testing.T) {
	for _, scenario := range []string{"old-client", "no-generation", "no-entity-versions", "deleted", "lease", "receipt-failure"} {
		t.Run(scenario, func(t *testing.T) {
			svc, db, _, before := commandFixture(t)
			if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "rename", Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "remote"}})); err != nil {
				t.Fatal(err)
			}
			op := baselineOperation(t, "a", map[string]any{"content": "local"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
			req := mergeRequest(before, "protected", op)
			switch scenario {
			case "old-client":
				req.Operations[0].FieldBaselines = nil
			case "no-generation":
				req.Operations[0].FieldBaselines.Generation = nil
			case "no-entity-versions":
				req.ExpectedEntityVersions = nil
			case "deleted":
				current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
				if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(current, "delete", Operation{Type: "delete_node", ID: "a"})); err != nil {
					t.Fatal(err)
				}
			case "lease":
				if _, err := repository.New(db).AcquireCanvasEditLease("owner", before.CanvasID, repository.CanvasEditProof{NodeID: "a", SessionID: "other-tab", Token: strings.Repeat("t", 48)}); err != nil {
					t.Fatal(err)
				}
			case "receipt-failure":
				if err := db.Exec(`CREATE TRIGGER fail_merge_receipt BEFORE INSERT ON canvas_command_receipts BEGIN SELECT RAISE(ABORT, 'receipt unavailable'); END;`).Error; err != nil {
					t.Fatal(err)
				}
			}
			current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			_, err := svc.Execute(context.Background(), ownerContext().Principal, req)
			switch scenario {
			case "old-client", "no-generation":
				assertReason(t, err, "entity_version_conflict")
			case "no-entity-versions":
				assertReason(t, err, "revision_conflict")
			case "deleted":
				assertReason(t, err, "entity_deleted")
			case "lease":
				if !errors.Is(err, repository.ErrCanvasNodeLocked) {
					t.Fatalf("lease bypass: %v", err)
				}
			default:
				if err == nil {
					t.Fatal("failed receipt accepted")
				}
			}
			after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			if !reflect.DeepEqual(current, after) {
				t.Fatal("rejected write changed durable snapshot")
			}
		})
	}
}

func TestFieldMergeMalformedAndOversizedBaselinesRejected(t *testing.T) {
	for _, raw := range []string{
		`{"nodeType":"text","unknown":true,"fields":{"x":{"exists":true,"value":0}}}`,
		`{"nodeType":"text","fields":{"x":{"value":0}}}`,
		`{"nodeType":"text","fields":{"x":{"exists":true}}}`,
		`{"nodeType":"text","fields":{"x":{"exists":true,"value":0,"extra":1}}}`,
	} {
		var baseline NodeFieldBaselines
		if err := json.Unmarshal([]byte(raw), &baseline); err == nil {
			t.Fatalf("malformed baseline accepted: %s", raw)
		}
	}
	for _, scenario := range []string{"unknown", "prototype", "too-large", "depth", "type", "missing", "invalid-missing-value"} {
		t.Run(scenario, func(t *testing.T) {
			doc := fixture(t, `{"nodes":[{"id":"a","type":"text","position":{"x":0},"metadata":{"content":""}}],"connections":[]}`)
			op := Operation{Type: "update_node", ID: "a", Patch: map[string]any{"x": 1}, FieldBaselines: &NodeFieldBaselines{NodeType: "text", Fields: map[string]FieldBaseline{"x": {Exists: true, Value: 0}}}}
			switch scenario {
			case "unknown":
				op.FieldBaselines.Fields["position.x"] = FieldBaseline{}
			case "prototype":
				op.FieldBaselines.Fields["richText"] = FieldBaseline{Exists: true, Value: map[string]any{"constructor": map[string]any{"polluted": true}}}
			case "too-large":
				op.FieldBaselines.Fields["content"] = FieldBaseline{Exists: true, Value: strings.Repeat("a", 8<<20)}
			case "depth":
				var nested any = "leaf"
				for range 66 {
					nested = []any{nested}
				}
				op.FieldBaselines.Fields["richText"] = FieldBaseline{Exists: true, Value: nested}
			case "type":
				op.Patch["x"] = "invalid number"
			case "missing":
				op.FieldBaselines.Fields = map[string]FieldBaseline{"content": {Exists: true, Value: ""}}
			case "invalid-missing-value":
				op.FieldBaselines.Fields["x"] = FieldBaseline{Exists: false, Value: 1}
			}
			_, err := Plan(doc, command(op), ownerContext())
			assertReason(t, err, "invalid_operation")
		})
	}
}

func TestFieldMergeCannotCrossNodeLifecycle(t *testing.T) {
	for _, action := range []string{"command-rebuild", "undo-delete", "legacy-rebuild", "host-rebuild"} {
		t.Run(action, func(t *testing.T) {
			svc, db, _, before := commandFixture(t)
			stale := mergeRequest(before, "old-lifetime", baselineOperation(t, "a", map[string]any{"content": "stale draft"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`))
			original, _ := cloneDocument(before.Document)
			remove := func(doc Document) Document {
				nodes := []any{}
				for _, node := range maps(doc["nodes"]) {
					if node["id"] != "a" {
						nodes = append(nodes, node)
					}
				}
				doc["nodes"] = nodes
				return doc
			}
			switch action {
			case "command-rebuild", "undo-delete":
				if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "delete-lifetime", Operation{Type: "delete_node", ID: "a"})); err != nil {
					t.Fatal(err)
				}
				if action == "undo-delete" {
					if _, err := svc.Undo(context.Background(), ownerContext().Principal, before.CanvasID, before.DocumentEpoch, "client", "undo-delete-lifetime"); err != nil {
						t.Fatal(err)
					}
				} else {
					current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
					if _, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(current, "recreate-id", Operation{Type: "add_node", ID: "a", NodeType: "text", X: num(0), Y: num(0)})); err != nil {
						t.Fatal(err)
					}
				}
			case "legacy-rebuild":
				repo := repository.New(db)
				for _, doc := range []Document{remove(mustCloneFieldDocument(t, before.Document)), original} {
					row, _ := repo.CanvasProjectForUser("owner", before.CanvasID)
					raw, _ := json.Marshal(doc)
					row.PayloadJSON = string(raw)
					if err := repo.UpsertCanvasProject(row); err != nil {
						t.Fatal(err)
					}
				}
			case "host-rebuild":
				for i := range 2 {
					current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
					_, _, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, mergeRequest(current, fmt.Sprintf("host-lifetime-%d", i)), "task_result", nil, false, func(_ *repository.Repository, doc Document) (Document, map[string]bool, error) {
						if i == 0 {
							return remove(doc), map[string]bool{"node:a": true}, nil
						}
						return mustCloneFieldDocument(t, original), map[string]bool{"node:a": true}, nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err := svc.Execute(context.Background(), ownerContext().Principal, stale)
			assertReason(t, err, "entity_version_conflict")
			after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
			if entity(after.Document, "node:a")["metadata"].(map[string]any)["content"] != "" {
				t.Fatal("stale draft crossed node lifecycle")
			}
			if after.EntityGenerations["node:a"] == before.EntityGenerations["node:a"] {
				t.Fatal("rebuilt node reused generation")
			}
			var durable model.CanvasEntityVersion
			if err := db.First(&durable, "canvas_id = ? AND epoch = ? AND entity_key = ?", before.CanvasID, before.DocumentEpoch, "node:a").Error; err != nil {
				t.Fatal(err)
			}
			if durable.Generation != after.EntityGenerations["node:a"] {
				t.Fatal("snapshot generation differs from durable row")
			}
			events, eventErr := svc.ReadEvents(context.Background(), ownerContext().Principal, before.CanvasID, before.EventSeq, 10)
			if eventErr != nil || len(events) != 2 {
				t.Fatalf("lifecycle events: %d %v", len(events), eventErr)
			}
			if events[0].EntityGenerations["node:a"] != before.EntityGenerations["node:a"] || events[1].EntityGenerations["node:a"] != durable.Generation {
				t.Fatal("event lifecycle generations differ from committed instances")
			}
		})
	}
}

func TestFieldMergeGenerationZeroAndCreateProjection(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	if before.EntityGenerations["node:a"] != 2 {
		t.Fatalf("initial snapshot generation=%v", before.EntityGenerations)
	}
	created, err := svc.Create(context.Background(), ownerContext().Principal, CreateCanvasRequest{ClientID: "client", OperationID: "initial-generations", InitialOperations: []Operation{{Type: "add_node", ID: "initial", NodeType: "text", X: num(0), Y: num(0)}}})
	if err != nil || created.EntityGenerations["node:initial"] != 1 {
		t.Fatalf("create projection: %+v %v", created, err)
	}
	var durable model.CanvasEntityVersion
	if err := db.First(&durable, "canvas_id = ? AND entity_key = ?", created.CanvasID, "node:initial").Error; err != nil {
		t.Fatal(err)
	}
	if durable.Generation != created.EntityGenerations["node:initial"] {
		t.Fatal("initial create ACK generation differs from database")
	}
	if err := db.Model(&model.CanvasEntityVersion{}).Where("canvas_id = ? AND entity_key = ?", before.CanvasID, "node:a").Update("generation", 0).Error; err != nil {
		t.Fatal(err)
	}
	legacy, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if generation, exists := legacy.EntityGenerations["node:a"]; !exists || generation != 0 {
		t.Fatal("legacy zero generation missing from projection")
	}
	remote, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(legacy, "zero-title", Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "remote"}}))
	if err != nil {
		t.Fatal(err)
	}
	if generation, exists := remote.EntityGenerations["node:a"]; !exists || generation != 0 {
		t.Fatal("live update changed legacy generation")
	}
	op := baselineOperation(t, "a", map[string]any{"content": "local"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
	merged, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(legacy, "zero-merge", op))
	if err != nil || merged.EntityGenerations["node:a"] != 0 {
		t.Fatalf("zero-generation merge: %+v %v", merged, err)
	}
}

func TestFieldMergeSameBatchRecreationAndHistoryHaveNewGenerations(t *testing.T) {
	svc, _, _, before := commandFixture(t)
	stale := mergeRequest(before, "before-batch-rebirth", baselineOperation(t, "a", map[string]any{"content": "stale"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`))
	receipt, err := svc.Execute(context.Background(), ownerContext().Principal, mergeRequest(before, "net-zero-rebirth", Operation{Type: "delete_node", ID: "a"}, Operation{Type: "add_node", ID: "a", NodeType: "text", X: num(0), Y: num(0)}, Operation{Type: "reorder_nodes", ID: "canvas", NodeIDs: []string{"a", "b"}}))
	if err != nil {
		t.Fatal(err)
	}
	current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if !sameJSON(before.Document, current.Document) {
		t.Fatal("fixture must finish with identical document JSON")
	}
	if current.EntityGenerations["node:a"] == before.EntityGenerations["node:a"] || receipt.EntityGenerations["node:a"] != current.Revision {
		t.Fatal("net-zero recreation did not persist a new lifecycle")
	}
	_, err = svc.Execute(context.Background(), ownerContext().Principal, stale)
	assertReason(t, err, "entity_version_conflict")
	previous := current.EntityGenerations["node:a"]
	for _, redo := range []bool{false, true} {
		var changed Receipt
		if redo {
			changed, err = svc.Redo(context.Background(), ownerContext().Principal, before.CanvasID, before.DocumentEpoch, "client", "redo-rebirth")
		} else {
			changed, err = svc.Undo(context.Background(), ownerContext().Principal, before.CanvasID, before.DocumentEpoch, "client", "undo-rebirth")
		}
		if err != nil {
			t.Fatal(err)
		}
		if changed.EntityGenerations["node:a"] == previous || changed.EntityGenerations["node:a"] != changed.Revision {
			t.Fatal("history discarded recreated lifecycle provenance")
		}
		previous = changed.EntityGenerations["node:a"]
	}
}

func TestFieldMergeDoesNotBypassAuthorityOrGeneration(t *testing.T) {
	svc, _, _, before := commandFixture(t)
	op := baselineOperation(t, "a", map[string]any{"content": "local"}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
	req := mergeRequest(before, "authority", op)
	p := ownerContext().Principal
	p.Role = "viewer"
	_, err := svc.Execute(context.Background(), p, req)
	assertReason(t, err, "permission_denied")
	wrongGeneration := int64(999)
	req.Operations[0].FieldBaselines.Generation = &wrongGeneration
	_, err = svc.Execute(context.Background(), ownerContext().Principal, req)
	assertReason(t, err, "entity_version_conflict")
	after, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid proof changed durable canvas")
	}
}

func TestFieldMergeOldCommittedWireReplaysWithoutAddingGeneration(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	req := edit(before, "a", "old-client", "old-wire")
	saved, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	raw, _ := json.Marshal(saved)
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	delete(wire, "entityGenerations")
	raw, _ = json.Marshal(wire)
	if err := db.Model(&model.CanvasCommandReceipt{}).Where("operation_id = ?", req.OperationID).Update("receipt_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	got, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil || got.Revision != saved.Revision || got.EntityGenerations != nil {
		t.Fatalf("historical receipt was rewritten: %+v %v", got, err)
	}
}

func mustCloneFieldDocument(t *testing.T, doc Document) Document {
	t.Helper()
	out, err := cloneDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
