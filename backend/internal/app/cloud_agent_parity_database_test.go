package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func parityCall(name string, args any) cloudAgentCall {
	raw, _ := json.Marshal(args)
	call := cloudAgentCall{ID: "parity-call"}
	call.Function.Name = name
	call.Function.Arguments = string(raw)
	return call
}

func TestAgentParityDatabaseAssetOwnershipAndReadOnlyData(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[{"id":"image","type":"image","metadata":{}}],"connections":[]}`}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{
		&model.Resource{ID: "owned-resource", UserID: "user", Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady},
		&model.Resource{ID: "foreign-resource", UserID: "other", Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady},
		&model.Asset{ID: "owned-asset", UserID: "user", Kind: "image", Title: "Owned", PayloadJSON: `{"data":{"storageKey":"resource:owned-resource"}}`},
		&model.Asset{ID: "foreign-asset", UserID: "other", Kind: "image", Title: "Private", PayloadJSON: `{"data":{"storageKey":"resource:foreign-resource"}}`},
		&model.Asset{ID: "bad-link", UserID: "user", Kind: "image", PayloadJSON: `{"data":{"storageKey":"resource:foreign-resource"}}`},
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	hash := cloudAgentCanvasHash(doc)
	for _, id := range []string{"foreign-asset", "bad-link"} {
		call := parityCall("canvas_bind_asset", map[string]any{"nodeId": "image", "assetId": id, "snapshotHash": hash})
		if _, err := prepareCloudAgentAssetBinding(s.repo, "user", canvas.ID, call); err == nil {
			t.Fatalf("accepted inaccessible asset %s", id)
		}
	}
	plan, err := prepareCloudAgentAssetBinding(s.repo, "user", canvas.ID, parityCall("canvas_bind_asset", map[string]any{"nodeId": "image", "assetId": "owned-asset", "snapshotHash": hash}))
	if err != nil {
		t.Fatal(err)
	}
	meta := cloudAgentNodeMetadata(creationMaps(plan.Document["nodes"])[0])
	if meta["assetId"] != "owned-asset" || meta["storageKey"] != "resource:owned-resource" {
		t.Fatalf("bad binding: %#v", meta)
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	if stored.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("preview persisted a binding")
	}
	result, err := cloudAgentListAssets(s.repo, "user", parityCall("canvas_list_assets", map[string]any{"query": "", "kind": "image"}))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var listing struct {
		Assets []struct {
			AssetID string `json:"assetId"`
		}
	}
	if err = json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	for _, item := range listing.Assets {
		if item.AssetID == "foreign-asset" {
			t.Fatal("foreign asset leaked")
		}
	}
}

func TestAgentParityDatabaseHistoryRedoAndConflict(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[],"connections":[]}`}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	before := cloudAgentCanvasHash(doc)
	call := parityCall("canvas_apply_ops", map[string]any{"snapshotHash": before, "ops": []any{map[string]any{"type": "add_node", "id": "note", "nodeType": "text", "content": "Original"}}})
	if _, err := applyCloudAgentCanvas(s.repo, "user", canvas.ID, call, policy, cloudAgentMutationRecorderForRun(run.ID)); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	afterDoc, _ := creationDocument(saved.PayloadJSON)
	after := cloudAgentCanvasHash(afterDoc)
	state := &cloudAgentRuntime{Request: agentTestRequest()}
	if _, err := applyCloudAgentHistory(s.repo, "other", canvas.ID, run.ID, state, parityCall("canvas_undo", map[string]any{"snapshotHash": after}), policy); err == nil {
		t.Fatal("foreign undo accepted")
	}
	if _, err := applyCloudAgentHistory(s.repo, "user", canvas.ID, run.ID, state, parityCall("canvas_undo", map[string]any{"snapshotHash": "stale"}), policy); err == nil {
		t.Fatal("stale undo accepted")
	}
	for _, step := range []struct{ name, hash, want string }{{"canvas_undo", after, before}, {"canvas_redo", before, after}, {"canvas_undo", after, before}} {
		result, err := applyCloudAgentHistory(s.repo, "user", canvas.ID, run.ID, state, parityCall(step.name, map[string]any{"snapshotHash": step.hash}), policy)
		if err != nil {
			t.Fatal(err)
		}
		if result.(map[string]any)["snapshotHash"] != step.want {
			t.Fatalf("%s did not restore expected content", step.name)
		}
	}
}

func TestAgentParityDatabaseDrawingResourceOwnership(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	doc := map[string]any{"nodes": []any{map[string]any{"id": "draw", "type": "drawing", "metadata": map[string]any{}}}, "connections": []any{}}
	raw, _ := json.Marshal(doc)
	canvas := model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: string(raw)}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Resource{ID: "private-image", UserID: "other", Kind: "image", Status: model.ResourceStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	call := parityCall("canvas_edit_drawing", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "draw", "engine": "excalidraw", "operations": []any{map[string]any{"type": "upsert", "id": "file", "record": map[string]any{"id": "file", "dataURL": "resource:private-image", "mimeType": "image/png"}}}})
	if _, err := prepareCloudAgentDrawing(s.repo, "user", canvas.ID, call); err == nil {
		t.Fatal("foreign drawing resource accepted")
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	if stored.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("rejected drawing persisted")
	}
}
