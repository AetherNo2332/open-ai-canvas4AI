package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestStoryboardIncrementalAssetsKeepsDomainCapacity(t *testing.T) {
	doc := workflowDocument()
	bindings := []any{}
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("image-%d", i)
		doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": id, "type": "image"})
		if i < 100 {
			bindings = append(bindings, map[string]any{"nodeId": id, "role": "prop", "priority": 80.0})
		}
	}
	rows := []map[string]any{{"id": "r", "durationSeconds": 5.0, "plotDescription": "original", "assetBindings": bindings}}
	if _, _, err := cloudAgentPlanStoryboardRows(doc, rows, cloudAgentStoryboardEditArgs{Action: "add_assets", RowID: "r", Assets: []cloudAgentStoryboardAsset{{Ref: "image-100", Role: "prop"}}}, "user", "capacity"); err == nil {
		t.Fatal("incremental operation exceeded domain's 100-asset capacity")
	}
	if len(creationMaps(rows[0]["assetBindings"])) != 100 {
		t.Fatal("capacity failure mutated original row")
	}
}

func TestStoryboardAssetReceiptBudget(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	policy, _ := s.RuntimePolicy()
	doc := workflowDocument()
	doc["nodes"] = []any{}
	rows := []any{}
	ids := []string{}
	bindings := []any{}
	for i := 0; i < 31; i++ {
		id := fmt.Sprintf("image-%02d", i)
		doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": id, "type": "image", "title": id})
		if i < 30 {
			bindings = append(bindings, map[string]any{"nodeId": id, "role": "prop", "priority": 80.0})
		}
	}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("row-%02d", i)
		ids = append(ids, id)
		rows = append(rows, map[string]any{"id": id, "shotNumber": float64(i + 1), "durationSeconds": 5.0, "plotDescription": "original", "assetBindings": bindings})
	}
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": rows}}})
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "budget", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "board", "action": "add_assets", "rowIds": ids, "assets": []map[string]any{{"ref": "image-30", "role": "prop"}}})
	result, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy)
	if err != nil {
		t.Fatal(err)
	}
	receipt := cloudAgentModelToolResult(call.Function.Name, result).(map[string]any)
	raw, _ = json.Marshal(receipt)
	if len(raw) > 32<<10 || receipt["assetChangesNextRead"] == nil {
		t.Fatalf("asset impact response is unbounded: %d bytes", len(raw))
	}
	read := receipt["assetChangesNextRead"].(map[string]any)
	if _, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "asset-impact-followup", read["arguments"])); err != nil {
		t.Fatalf("impact followup is not actionable: %v", err)
	}
	t.Logf("bounded impact receipt: %d bytes", len(raw))
}

func TestStoryboardIncrementalAssets(t *testing.T) {
	doc := workflowDocument()
	creationMaps(doc["nodes"])[0]["type"] = "image"
	rows := []map[string]any{{"id": "r1", "durationSeconds": float64(5), "plotDescription": "original", "characters": []any{map[string]any{"characterImageNodeId": "hero", "characterName": "陈林"}}, "assetBindings": []any{}}}
	before, _ := json.Marshal(rows)
	next, _, err := cloudAgentPlanStoryboardRows(doc, rows, cloudAgentStoryboardEditArgs{Action: "add_assets", RowID: "r1", Assets: []cloudAgentStoryboardAsset{{Ref: "scene", Role: "environment"}}}, "user", "add")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next[0]["characters"], rows[0]["characters"]) || len(creationMaps(next[0]["assetBindings"])) != 1 {
		t.Fatalf("adding environment lost cast: %v", next)
	}
	next, _, err = cloudAgentPlanStoryboardRows(doc, next, cloudAgentStoryboardEditArgs{Action: "upsert_assets", RowID: "r1", Assets: []cloudAgentStoryboardAsset{{Ref: "scene", Role: "style"}}}, "user", "upsert")
	if err != nil || len(creationMaps(next[0]["assetBindings"])) != 1 || creationMaps(next[0]["assetBindings"])[0]["role"] != "style" {
		t.Fatalf("upsert: %v %v", next, err)
	}
	next, _, err = cloudAgentPlanStoryboardRows(doc, next, cloudAgentStoryboardEditArgs{Action: "remove_assets", RowID: "r1", Assets: []cloudAgentStoryboardAsset{{Ref: "hero", Role: "character"}}}, "user", "remove")
	if err != nil || len(creationMaps(next[0]["characters"])) != 0 || len(creationMaps(next[0]["assetBindings"])) != 1 {
		t.Fatalf("remove exact legacy cast: %v %v", next, err)
	}
	after, _ := json.Marshal(rows)
	if string(before) != string(after) {
		t.Fatal("planning mutated original")
	}
	if _, _, err = cloudAgentPlanStoryboardRows(doc, rows, cloudAgentStoryboardEditArgs{Action: "add_assets", RowID: "r1", Assets: []cloudAgentStoryboardAsset{{Ref: "scene", Role: "environment"}, {Ref: "missing", Role: "prop"}}}, "user", "invalid"); err == nil {
		t.Fatal("invalid asset accepted")
	}
	after, _ = json.Marshal(rows)
	if string(before) != string(after) {
		t.Fatal("invalid batch partially wrote")
	}
}

func TestStoryboardIncrementalSharedReferencesAndReceipt(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	doc, _ := creationDocument(canvas.PayloadJSON)
	doc["referenceFieldsVersion"] = float64(1)
	doc["nodes"] = append(creationMaps(doc["nodes"]), creationMaps(workflowDocument()["nodes"])...)
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "r1", "durationSeconds": 5.0, "plotDescription": "original", "assetBindings": []any{map[string]any{"nodeId": "hero", "role": "character", "priority": 100.0}}}}}}})
	_, board, _, _ := storyboardNodeFromDocument(doc, "board")
	board["referenceNodeIds"] = []any{"hero"}
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	policy, _ := s.RuntimePolicy()
	for _, action := range []string{"add_references", "remove_references"} {
		current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		doc, _ = creationDocument(current.PayloadJSON)
		call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", action, map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "board", "action": action, "references": []string{"scene"}})
		result, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy)
		if err != nil {
			t.Fatal(err)
		}
		receipt := result.(map[string]any)
		if receipt["referenceChanges"] == nil {
			t.Fatal("missing explicit shared reference impact")
		}
		current, _ = s.repo.CanvasProjectForUser("user", canvas.ID)
		doc, _ = creationDocument(current.PayloadJSON)
		_, b, rows, _ := storyboardNodeFromDocument(doc, "board")
		if cloudAgentStrings(b["referenceNodeIds"])[0] != "hero" || len(creationMaps(rows[0]["assetBindings"])) != 1 {
			t.Fatal("incremental shared references lost original cast")
		}
	}
	for _, action := range []string{"add_assets", "remove_assets"} {
		current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		doc, _ = creationDocument(current.PayloadJSON)
		result, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", action, map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "board", "action": action, "rowId": "r1", "assets": []map[string]any{{"ref": "scene", "role": "environment"}}}), policy)
		if err != nil {
			t.Fatal(err)
		}
		changes := creationMaps(result.(map[string]any)["assetChanges"])
		if len(changes) != 1 || len(creationMaps(changes[0]["retained"])) != 1 || changes[0]["scope"] != "row" || len(cloudAgentStrings(changes[0]["sharedNodeIds"])) != 1 {
			t.Fatalf("missing retained/shared impact: %v", changes)
		}
		key := "added"
		if action == "remove_assets" {
			key = "removed"
		}
		if len(creationMaps(changes[0][key])) != 1 || creationMaps(changes[0][key])[0]["nodeId"] != "scene" || changes[0]["effectiveBindings"] == nil {
			t.Fatalf("missing exact impact: %v", changes)
		}
	}
}
