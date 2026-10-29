package mediaversions

import (
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func imageNode(storage string, extra map[string]any) map[string]any {
	m := map[string]any{}
	if storage != "" {
		m["storageKey"], m["content"] = "resource:"+storage, "/api/resources/"+storage+"/file"
	}
	for k, v := range extra {
		m[k] = v
	}
	return map[string]any{"id": "n", "type": "image", "position": map[string]any{"x": 0.0, "y": 0.0}, "width": 400.0, "height": 300.0, "metadata": m}
}

func resultFor(node map[string]any, storage, taskID string) map[string]any {
	out := cloneMap(node)
	m := meta(out)
	m["storageKey"], m["content"], m["taskId"], m["prompt"], m["taskStatus"], m["status"] = "resource:"+storage, "/api/resources/"+storage+"/file", taskID, "new prompt", "succeeded", "success"
	return out
}

func TestFinishAppendsAndAdoptsNewVersion(t *testing.T) {
	node := imageNode("old", nil)
	base, err := Begin(node, model.Task{ID: "t1", Status: model.TaskStatusQueued}, []map[string]any{node})
	if err != nil {
		t.Fatal(err)
	}
	if meta(node)["status"] == "loading" || meta(node)["storageKey"] != "resource:old" {
		t.Fatalf("old media must stay usable during generation: %+v", meta(node))
	}
	next, outcome := Finish(node, resultFor(node, "new", "t1"), base, model.Task{ID: "t1", Status: model.TaskStatusSucceeded}, "", time.Now())
	if !outcome.Adopted || outcome.Reason != "applied" || outcome.VersionID == "" {
		t.Fatalf("outcome %+v", outcome)
	}
	versions := List(next)
	if len(versions) != 2 || meta(next)["storageKey"] != "resource:new" || meta(next)["currentMediaVersionId"] != outcome.VersionID {
		t.Fatalf("version history: %+v", meta(next))
	}
	if str(versionGeneration(Find(next, outcome.VersionID))["taskId"]) != "t1" || Find(next, outcome.VersionID)["source"] != "generate" {
		t.Fatalf("agent version must be traceable by generation.taskId: %+v", Find(next, outcome.VersionID))
	}
}

func TestFinishKeepsManualSelectionMadeDuringGeneration(t *testing.T) {
	v1 := map[string]any{"id": "v1", "number": 1.0, "createdAt": "x", "source": "upload", "media": map[string]any{"storageKey": "resource:a"}}
	v2 := map[string]any{"id": "v2", "number": 2.0, "createdAt": "x", "source": "upload", "media": map[string]any{"storageKey": "resource:b"}}
	node := imageNode("b", map[string]any{"mediaVersions": []any{v1, v2}, "currentMediaVersionId": "v2"})
	base, _ := Begin(node, model.Task{ID: "t1", Status: model.TaskStatusQueued}, []map[string]any{node})
	adopted, err := Adopt(node, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if meta(adopted)["taskId"] != nil {
		t.Fatal("upstream adopt releases the task binding")
	}
	next, outcome := Finish(adopted, resultFor(adopted, "c", "t1"), base, model.Task{ID: "t1", Status: model.TaskStatusSucceeded}, "", time.Now())
	if outcome.Adopted || outcome.Reason != "selection_changed" || len(List(next)) != 3 || meta(next)["storageKey"] != "resource:a" || meta(next)["currentMediaVersionId"] != "v1" {
		t.Fatalf("manual selection overwritten: %+v %+v", outcome, meta(next))
	}
	// Even with the task marker still present, a changed current version means history only.
	meta(adopted)["taskId"] = "t1"
	_, outcome = Finish(adopted, resultFor(adopted, "c", "t1"), base, model.Task{ID: "t1", Status: model.TaskStatusSucceeded}, "", time.Now())
	if outcome.Adopted || outcome.Reason != "selection_changed" {
		t.Fatalf("pointer change not detected: %+v", outcome)
	}
}

func TestFinishFailureAndBlockKeepCurrent(t *testing.T) {
	node := imageNode("old", nil)
	base, _ := Begin(node, model.Task{ID: "t1", Status: model.TaskStatusQueued}, []map[string]any{node})
	next, outcome := Finish(cloneMap(node), map[string]any{"id": "n", "type": "image"}, base, model.Task{ID: "t1", Status: model.TaskStatusFailed}, "", time.Now())
	if outcome.Adopted || meta(next)["storageKey"] != "resource:old" || meta(next)["status"] != "success" || len(List(next)) != 0 {
		t.Fatalf("failure changed media: %+v", meta(next))
	}
	next, outcome = Finish(cloneMap(node), resultFor(node, "new", "t1"), base, model.Task{ID: "t1", Status: model.TaskStatusSucceeded}, "output_binding_changed", time.Now())
	if outcome.Reason != "output_binding_changed" || meta(next)["storageKey"] != "resource:old" || meta(next)["taskId"] != nil {
		t.Fatalf("blocked result applied: %+v", meta(next))
	}
}

func TestBeginRejectsLockedAndMarksEmptyNodeLoading(t *testing.T) {
	if _, err := Begin(imageNode("old", map[string]any{"locked": true}), model.Task{ID: "t"}, nil); err == nil {
		t.Fatal("locked node accepted")
	}
	empty := imageNode("", nil)
	if _, err := Begin(empty, model.Task{ID: "t"}, nil); err != nil || meta(empty)["status"] != "loading" {
		t.Fatalf("empty node: %v %+v", err, meta(empty))
	}
}
