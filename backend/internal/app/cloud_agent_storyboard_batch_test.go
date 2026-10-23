package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func storyboardBatchArgs(t *testing.T, raw string) cloudAgentStoryboardEditArgs {
	t.Helper()
	var args cloudAgentStoryboardEditArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func storyboardBatchRows() []map[string]any {
	return []map[string]any{{"id": "r1", "durationSeconds": float64(5), "plotDescription": "old one"}, {"id": "r2", "durationSeconds": float64(6), "plotDescription": "old two", "imageNodeId": "keep"}}
}

func TestStoryboardBatchUpdateDistinctPatches(t *testing.T) {
	rows := storyboardBatchRows()
	args := storyboardBatchArgs(t, `{"action":"batch_update","updates":[{"rowId":"r1","patch":{"imageGenerationPrompt":"one","motion":"pan"}},{"rowId":"r2","patch":{"videoMotionPrompt":"two","shotSize":"wide","lightingAndAtmosphere":"dawn"}}]}`)
	next, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), rows, args, "user", "call")
	if err != nil {
		t.Fatal(err)
	}
	if next[0]["imageGenerationPrompt"] != "one" || next[0]["motion"] != "pan" || next[1]["videoMotionPrompt"] != "two" || next[1]["imageNodeId"] != "keep" {
		t.Fatalf("wrong rows: %v", next)
	}
	if rows[0]["imageGenerationPrompt"] != nil {
		t.Fatal("input mutated")
	}
}

func TestStoryboardBatchRejectsInvalidAndDuplicateRows(t *testing.T) {
	for _, sample := range []struct{ updates, path string }{
		{`[{"rowId":"r1","patch":{"dialogue":"new"}},{"rowId":"missing","patch":{"dialogue":"x"}}]`, "updates[1].rowId"},
		{`[{"rowId":"r1","patch":{"dialogue":"new"}},{"rowId":"r1","patch":{"dialogue":"x"}}]`, "updates[1].rowId"},
		{`[{"rowId":"r1","patch":{"dialogue":"new"}},{"rowId":"r2","patch":{"status":"success"}}]`, "updates[1].patch"},
		{`[{"rowId":"r1","patch":{"dialogue":"new"}},{"rowId":"r2","patch":{"assets":[{"ref":"missing","role":"character"}]}}]`, "updates[1].patch"},
	} {
		rows := storyboardBatchRows()
		before, _ := json.Marshal(rows)
		_, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), rows, storyboardBatchArgs(t, `{"action":"batch_update","updates":`+sample.updates+`}`), "user", "call")
		if err == nil || !strings.Contains(err.Error(), sample.path) {
			t.Fatalf("expected %s error, got %v", sample.path, err)
		}
		after, _ := json.Marshal(rows)
		if string(before) != string(after) {
			t.Fatal("failed batch changed rows")
		}
	}
}

func TestStoryboardBatchAppendAndVersionConflict(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	createCloudAgentStoryboardForTest(t, s, canvas)
	doc, _ := creationDocument(canvas.PayloadJSON)
	hash := cloudAgentCanvasHash(doc)
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "append-batch", map[string]any{"nodeId": "storyboard-1", "snapshotHash": hash, "action": "append_rows", "rows": []map[string]any{{"durationSeconds": 3.0, "plotDescription": "three"}, {"durationSeconds": 4.0, "plotDescription": "four"}}})
	one, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, call)
	if err != nil {
		t.Fatal(err)
	}
	two, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, call)
	if err != nil {
		t.Fatal(err)
	}
	_, _, a, _ := storyboardNodeFromDocument(one.Document, "storyboard-1")
	_, _, b, _ := storyboardNodeFromDocument(two.Document, "storyboard-1")
	if len(a) != 4 || a[2]["id"] == a[3]["id"] || a[2]["id"] != b[2]["id"] {
		t.Fatalf("unstable append IDs: %v", a)
	}
	policy, _ := s.RuntimePolicy()
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err == nil {
		t.Fatal("stale snapshot accepted")
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	storedDoc, _ := creationDocument(stored.PayloadJSON)
	_, _, persisted, _ := storyboardNodeFromDocument(storedDoc, "storyboard-1")
	if len(persisted) != 4 {
		t.Fatalf("stale append changed rows: %d", len(persisted))
	}
	full := make([]map[string]any, 99)
	for i := range full {
		full[i] = map[string]any{"id": fmt.Sprint(i)}
	}
	args := storyboardBatchArgs(t, `{"action":"append_rows","rows":[{"durationSeconds":3,"plotDescription":"x"},{"durationSeconds":3,"plotDescription":"y"}]}`)
	if _, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), full, args, "user", "call"); err == nil {
		t.Fatal("capacity exceeded")
	}
}

func TestStoryboardBatchPersistent89DistinctPrompts(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	doc, _ := creationDocument(canvas.PayloadJSON)
	input := []map[string]any{}
	for i := 0; i < 89; i++ {
		input = append(input, map[string]any{"durationSeconds": 5.0, "plotDescription": fmt.Sprintf("shot %d", i)})
	}
	policy, _ := s.RuntimePolicy()
	create := cloudAgentStoryboardCall(t, "canvas_create_storyboard", "create-89", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "board-89", "title": "89 shots", "rows": input})
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, create, policy); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ = creationDocument(stored.PayloadJSON)
	_, _, rows, _ := storyboardNodeFromDocument(doc, "board-89")
	updates := []map[string]any{}
	for i, row := range rows {
		updates = append(updates, map[string]any{"rowId": row["id"], "patch": map[string]any{"imageGenerationPrompt": fmt.Sprintf("image %d", i), "videoMotionPrompt": fmt.Sprintf("video %d", i), "camera": "tracking", "motion": "pan", "shotSize": "wide", "lightingAndAtmosphere": "dawn"}})
	}
	edit := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "edit-89", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "board-89", "action": "batch_update", "updates": updates})
	if err := validateCloudAgentToolArguments(workflowTool(t, "canvas_edit_storyboard"), edit.Function.Arguments); err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, edit, policy); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.repo.CanvasProjectForUser("user", canvas.ID)
	doc, _ = creationDocument(stored.PayloadJSON)
	_, _, rows, _ = storyboardNodeFromDocument(doc, "board-89")
	for i, row := range rows {
		if row["imageGenerationPrompt"] != fmt.Sprintf("image %d", i) || row["videoMotionPrompt"] != fmt.Sprintf("video %d", i) || row["lightingAndAtmosphere"] != "dawn" {
			t.Fatalf("persisted row %d: %v", i, row)
		}
	}
}

func TestStoryboardCreativeFieldsContract(t *testing.T) {
	p := cloudAgentStoryboardPatchSchema()["properties"].(map[string]any)
	for _, field := range append([]string{"durationSeconds"}, cloudAgentStoryboardTextFields...) {
		if p[field] == nil {
			t.Errorf("missing creative field %s", field)
		}
	}
	for _, field := range []string{"status", "imageNodeId", "videoNodeId", "taskId", "assetBindings", "characters"} {
		if p[field] != nil {
			t.Errorf("protected or raw reference field %s", field)
		}
	}
}

func TestStoryboardBatchStrictContractsAndReferenceMigration(t *testing.T) {
	for _, sample := range []struct {
		raw   string
		valid bool
	}{
		{`{"action":"batch_update","updates":[{"rowId":"r1","patch":{"motion":"pan"}}]}`, true},
		{`{"action":"append_rows","rows":[{"durationSeconds":3,"plotDescription":"new","camera":"wide"}]}`, true},
		{`{"action":"batch_update","updates":[]}`, false},
		{`{"action":"batch_update","rowId":"r1","updates":[{"rowId":"r1","patch":{"dialogue":"x"}}]}`, false},
		{`{"action":"update","rowId":"r1","patch":{"dialogue":"x"},"rows":[]}`, false},
		{`{"action":"append_rows","rows":[{"id":"forged","durationSeconds":3,"plotDescription":"x"}]}`, false},
		{`{"action":"batch_update","updates":[{"rowId":"r1","patch":{"imageNodeId":"forged"}}]}`, false},
	} {
		var fields map[string]any
		_ = json.Unmarshal([]byte(sample.raw), &fields)
		fields["nodeId"] = "board"
		fields["snapshotHash"] = "h"
		raw, _ := json.Marshal(fields)
		err := validateCloudAgentToolArguments(workflowTool(t, "canvas_edit_storyboard"), string(raw))
		if (err == nil) != sample.valid {
			t.Errorf("valid=%v %s: %v", sample.valid, raw, err)
		}
	}
	for _, raw := range []string{`{"action":"batch_update","updates":[{"rowId":"r1","patch":{"assets":[]}}]}`, `{"action":"append_rows","rows":[{"durationSeconds":3,"plotDescription":"x","assets":[]}]}`} {
		if !cloudAgentStoryboardEditWritesReferences(storyboardBatchArgs(t, raw)) {
			t.Fatalf("reference migration bypass: %s", raw)
		}
	}
}

func TestStoryboardBatchPersistentAtomicFailures(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	doc, _ := creationDocument(canvas.PayloadJSON)
	hash := cloudAgentCanvasHash(doc)
	policy, _ := s.RuntimePolicy()
	for _, bad := range []map[string]any{
		{"rowId": "missing", "patch": map[string]any{"dialogue": "bad"}},
		{"rowId": rows[0]["id"], "patch": map[string]any{"dialogue": "duplicate"}},
		{"rowId": rows[1]["id"], "patch": map[string]any{"status": "success"}},
		{"rowId": rows[1]["id"], "patch": map[string]any{"assets": []map[string]any{{"ref": "missing", "role": "character"}}}},
	} {
		call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "invalid-batch", map[string]any{"snapshotHash": hash, "nodeId": "storyboard-1", "action": "batch_update", "updates": []map[string]any{{"rowId": rows[0]["id"], "patch": map[string]any{"dialogue": "must not persist"}}, bad}})
		if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err == nil {
			t.Fatalf("accepted invalid batch: %v", bad)
		}
		stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		if stored.PayloadJSON != canvas.PayloadJSON {
			t.Fatal("failed batch persisted partial content")
		}
	}
}

func TestStoryboardBatchReferenceMigrationBlocksPersistence(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	doc, _ := creationDocument(canvas.PayloadJSON)
	delete(doc, "referenceFieldsVersion")
	raw, _ := json.Marshal(doc)
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	before := stored.PayloadJSON
	stored.PayloadJSON = string(raw)
	if err := saveCreationCanvasWithHistory(s.repo, stored, before); err != nil {
		t.Fatal(err)
	}
	policy, _ := s.RuntimePolicy()
	for _, payload := range []map[string]any{
		{"action": "batch_update", "updates": []map[string]any{{"rowId": rows[0]["id"], "patch": map[string]any{"assets": []any{}}}}},
		{"action": "append_rows", "rows": []map[string]any{{"durationSeconds": 3.0, "plotDescription": "new", "assets": []any{}}}},
	} {
		payload["snapshotHash"], payload["nodeId"] = cloudAgentCanvasHash(doc), "storyboard-1"
		call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "migration-batch", payload)
		if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); !cloudAgentReferenceMigrationBlocked(err) {
			t.Fatalf("migration bypass: %v", err)
		}
		current, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
		if current.PayloadJSON != stored.PayloadJSON {
			t.Fatal("blocked reference write persisted")
		}
	}
}

func TestStoryboardBatchRejectsOversizedAndInvalidAppend(t *testing.T) {
	rows := storyboardBatchRows()
	updates := make([]map[string]any, 101)
	for i := range updates {
		updates[i] = map[string]any{"rowId": "r1", "patch": map[string]any{"dialogue": "x"}}
	}
	for _, payload := range []map[string]any{
		{"action": "batch_update", "updates": updates},
		{"action": "append_rows", "rows": []map[string]any{{"durationSeconds": 3.0, "plotDescription": "ok"}, {"durationSeconds": -1.0, "plotDescription": "bad"}}},
		{"action": "append_rows", "rows": []map[string]any{{"durationSeconds": 3.0, "plotDescription": "ok"}, {"durationSeconds": 3.0, "plotDescription": "bad", "assets": []map[string]any{{"ref": "missing", "role": "character"}}}}},
	} {
		raw, _ := json.Marshal(payload)
		if _, _, err := cloudAgentPlanStoryboardRows(workflowDocument(), rows, storyboardBatchArgs(t, string(raw)), "user", "call"); err == nil {
			t.Fatalf("accepted invalid batch %s", raw)
		}
		if len(rows) != 2 || rows[0]["dialogue"] != nil {
			t.Fatal("invalid batch mutated input")
		}
	}
}
