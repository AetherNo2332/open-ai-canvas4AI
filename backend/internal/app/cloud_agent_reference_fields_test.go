package app

import (
	"encoding/json"
	"testing"
)

func TestCloudAgentReferencePreview(t *testing.T) {
	doc := map[string]any{"referenceFieldsVersion": 1, "nodes": []any{map[string]any{"id": "source", "type": "image"}, map[string]any{"id": "target", "type": "compare", "metadata": map[string]any{}}}, "connections": []any{}}
	ops := []agentCanvasOp{{Type: "update_node", ID: "target", Patch: map[string]any{"inputBindings": []any{map[string]any{"id": "node:source", "nodeId": "source", "mediaType": "image", "role": "reference", "order": float64(0), "resolution": "latest"}}}}}
	if _, err := applyCloudAgentCanvasPlan(doc, ops); err != nil {
		t.Fatal(err)
	}
	if len(creationMaps(doc["connections"])) != 1 {
		t.Fatal("approval preview omitted the declared reference connection")
	}
}

func TestCloudAgentHeadlessReferenceEdit(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	doc, _ := creationDocument(canvas.PayloadJSON)
	doc["referenceFieldsVersion"] = 1
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "asset", "type": "image", "metadata": map[string]any{}})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "headless", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "rowId": rows[0]["id"], "action": "update", "patch": map[string]any{"assetBindings": []any{map[string]any{"nodeId": "asset", "role": "style", "priority": 80}}}})
	policy, _ := s.RuntimePolicy()
	if _, err = applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	after, _ := creationDocument(stored.PayloadJSON)
	if len(creationMaps(after["connections"])) != 1 {
		t.Fatal("headless mutation persisted fields without connections")
	}
}
