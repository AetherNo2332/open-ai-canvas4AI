package app

import (
	"encoding/json"
	"testing"
	"yingce/backend/internal/model"
)

func TestStoryboardRevisionTracksDatabaseCharacterVersionAndRepresentation(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	policy, _ := s.RuntimePolicy()
	character := seedCloudAgentCharacter(t, db, canvas.ID)
	current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ := creationDocument(current.PayloadJSON)
	doc["referenceFieldsVersion"] = float64(1)
	doc["nodes"] = append(creationMaps(doc["nodes"]), character)
	_, board, _, _ := storyboardNodeFromDocument(doc, "storyboard-1")
	board["referenceNodeIds"] = []any{"character-card"}
	if err := saveCloudAgentDocument(s.repo, current, doc, policy); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		r, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "character-read", map[string]any{"nodeId": "storyboard-1", "view": "summary"}))
		if err != nil {
			t.Fatal(err)
		}
		return r.(map[string]any)
	}
	base := read()
	if err := db.Model(&model.Asset{}).Where("id = ?", "character-asset").Update("primary_version_id", "character-v1").Error; err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"snapshotHash": base["snapshotHash"], "storyboardRevision": base["storyboardRevision"], "nodeId": "storyboard-1", "action": "update", "rowId": rows[0]["id"], "patch": map[string]any{"dialogue": "based on version 2"}}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "stale-db-version", args), policy); err == nil {
		t.Fatal("DB-only current version switch bypassed dependency conflict")
	}
	current, _ = s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ = creationDocument(current.PayloadJSON)
	for _, n := range creationMaps(doc["nodes"]) {
		if n["id"] == "character-card" {
			m := cloudAgentNodeMetadata(n)
			m["characterVersionPolicy"], m["characterVersionId"] = "pinned", "character-v2"
		}
	}
	if err := saveCloudAgentDocument(s.repo, current, doc, policy); err != nil {
		t.Fatal(err)
	}
	base = read()
	if err := db.Model(&model.Asset{}).Where("id = ?", "character-asset").Update("primary_version_id", "character-v2").Error; err != nil {
		t.Fatal(err)
	}
	if read()["storyboardRevision"] != base["storyboardRevision"] {
		t.Fatal("unrelated current-head switch invalidated a pinned version")
	}
	if err := db.Create(&model.Resource{ID: "replacement-character-image", UserID: "user", Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Size: 321}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AssetRepresentation{}).Where("id = ?", "character-representation").Update("resource_id", "replacement-character-image").Error; err != nil {
		t.Fatal(err)
	}
	args["snapshotHash"], args["storyboardRevision"] = base["snapshotHash"], base["storyboardRevision"]
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "stale-representation", args), policy); err == nil {
		t.Fatal("DB-only actual image change bypassed dependency conflict")
	}
}

func TestStoryboardRevisionAllowsUnrelatedLayoutButRejectsDependencies(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	policy, _ := s.RuntimePolicy()
	mutate := func(change func(map[string]any)) {
		t.Helper()
		current, err := s.repo.CanvasProjectForUser("user", canvas.ID)
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := creationDocument(current.PayloadJSON)
		change(doc)
		if err := saveCloudAgentDocument(s.repo, current, doc, policy); err != nil {
			t.Fatal(err)
		}
	}
	mutate(func(doc map[string]any) {
		doc["referenceFieldsVersion"] = float64(1)
		doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "asset", "type": "image", "metadata": map[string]any{"prompt": "original"}}, map[string]any{"id": "unrelated", "type": "text", "position": map[string]any{"x": 0.0, "y": 0.0}})
		_, board, _, _ := storyboardNodeFromDocument(doc, "storyboard-1")
		board["referenceNodeIds"] = []any{"asset"}
	})
	read := func() map[string]any {
		t.Helper()
		result, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "read", map[string]any{"nodeId": "storyboard-1", "view": "summary"}))
		if err != nil {
			t.Fatal(err)
		}
		body := result.(map[string]any)
		if stringValue(body["storyboardRevision"]) == "" {
			t.Fatal("missing scoped revision")
		}
		return body
	}
	base := read()
	mutate(func(doc map[string]any) {
		for _, n := range creationMaps(doc["nodes"]) {
			if n["id"] == "unrelated" {
				n["position"] = map[string]any{"x": 500.0, "y": 600.0}
			}
		}
	})
	args := map[string]any{"snapshotHash": base["snapshotHash"], "nodeId": "storyboard-1", "action": "update", "rowId": rows[0]["id"], "patch": map[string]any{"dialogue": "scoped update"}}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "legacy-conflict", args), policy); err == nil {
		t.Fatal("full snapshot guard was disabled")
	}
	args["storyboardRevision"] = base["storyboardRevision"]
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "scoped", args), policy); err != nil {
		t.Fatal(err)
	}
	current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ := creationDocument(current.PayloadJSON)
	if creationMaps(doc["nodes"])[2]["position"].(map[string]any)["x"] != float64(500) {
		t.Fatal("scoped edit overwrote unrelated layout")
	}
	for _, change := range []func(map[string]any){
		func(doc map[string]any) {
			_, _, rows, _ := storyboardNodeFromDocument(doc, "storyboard-1")
			rows[1]["dialogue"] = "human edit"
		},
		func(doc map[string]any) {
			for _, n := range creationMaps(doc["nodes"]) {
				if n["id"] == "asset" {
					cloudAgentNodeMetadata(n)["prompt"] = "asset changed"
				}
			}
		},
	} {
		base = read()
		mutate(change)
		args["snapshotHash"], args["storyboardRevision"] = base["snapshotHash"], base["storyboardRevision"]
		before, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "must-conflict", args), policy); err == nil {
			t.Fatal("scoped revision ignored a real dependency change")
		}
		after, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		if before.PayloadJSON != after.PayloadJSON {
			t.Fatal("conflict partially wrote")
		}
	}
	// Even identical content restored under the same ID is a new incarnation.
	base = read()
	current, _ = s.repo.CanvasProjectForUser("user", canvas.ID)
	original := current.PayloadJSON
	mutate(func(doc map[string]any) {
		kept := []map[string]any{}
		for _, n := range creationMaps(doc["nodes"]) {
			if n["id"] != "storyboard-1" {
				kept = append(kept, n)
			}
		}
		doc["nodes"] = mapsAsAny(kept)
	})
	mutate(func(doc map[string]any) {
		var restored map[string]any
		_ = json.Unmarshal([]byte(original), &restored)
		doc["nodes"] = restored["nodes"]
	})
	latest := read()
	args["snapshotHash"], args["storyboardRevision"] = latest["snapshotHash"], base["storyboardRevision"]
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "old-incarnation", args), policy); err == nil {
		t.Fatal("retired storyboard incarnation accepted")
	}
}
