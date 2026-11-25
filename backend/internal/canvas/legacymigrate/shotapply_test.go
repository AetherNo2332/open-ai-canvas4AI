package legacymigrate

import (
	"encoding/json"
	"reflect"
	"testing"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/references"
)

func shotApplyDocument(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{
		"idCounters": {},
		"nodes": [
			{"id": "asset-1", "type": "image", "title": "参考图", "position": {"x": 0, "y": 0}, "width": 720, "height": 405, "metadata": {"storageKey": "resource:r1"}},
			{"id": "hero", "type": "text", "title": "阿岚", "position": {"x": 0, "y": 420}, "width": 340, "height": 240, "metadata": {"workflowKind": "character", "characterAssetId": "c1"}},
			{"id": "board", "type": "script", "title": "分镜", "position": {"x": 400, "y": 0}, "width": 920, "height": 360, "metadata": {"storyboard": {"rows": [
				{"id": "row-1", "shotNumber": 1, "durationSeconds": 4, "plotDescription": "动作", "dialogue": "阿岚：走吧", "imageGenerationPrompt": "雨夜天台", "videoMotionPrompt": "缓慢推近", "previsSceneId": "scene-1", "assetBindings": [{"nodeId": "asset-1", "role": "style", "priority": 80}], "characters": [{"characterAssetId": "c1"}]},
				{"id": "row-2", "shotNumber": 2, "imageNodeId": "old-image", "videoNodeId": "old-video", "locked": true}
			]}}},
			{"id": "old-image", "type": "image", "title": "已出图", "position": {"x": 1400, "y": 0}, "width": 720, "height": 405, "metadata": {"content": "https://example.test/old.png"}},
			{"id": "old-video", "type": "video", "title": "已出片", "position": {"x": 1400, "y": 420}, "width": 720, "height": 405, "metadata": {"content": "https://example.test/old.mp4"}}
		],
		"connections": []
	}`), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestApplyShotSubmapsCreatesScopedShotsAndPreservesResults(t *testing.T) {
	doc := shotApplyDocument(t)
	before, _ := json.Marshal(doc)
	counters := map[string]int{}
	allocate := func(prefix string) string {
		counters[prefix]++
		return prefix + "-" + itoa(counters[prefix])
	}
	applied, err := ApplyShotSubmaps(doc, allocate)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("shot migration mutated the source document")
	}
	if applied.Rows != 2 || applied.Submaps != 2 || applied.ExistingImages != 1 || applied.ExistingVideos != 1 {
		t.Fatalf("unexpected migration counts: %+v", applied)
	}
	doc = applied.Document
	nodes := nodeMap(doc)
	if text(doc["referenceFieldsVersion"]) != "1" {
		t.Fatalf("referenceFieldsVersion = %#v", doc["referenceFieldsVersion"])
	}
	board := object(object(nodes["board"]["metadata"])["storyboard"])
	if rows := list(board["rows"]); len(rows) != 0 {
		t.Fatalf("storyboard rows not consumed: %v", rows)
	}
	first, ok := nodes["submap-1"]
	if !ok || text(first["type"]) != "submap" {
		t.Fatalf("first shot submap missing: %#v", first)
	}
	if text(object(first["metadata"])["workflowId"]) != "storyboard" {
		t.Fatalf("shot workflow lost: %#v", first["metadata"])
	}
	workflow := object(object(first["metadata"])["shotMigration"])
	if text(workflow["sourceRowId"]) != "row-1" || number(object(workflow["preserved"])["durationSeconds"]) != 4 {
		t.Fatalf("row audit fields lost: %#v", workflow)
	}
	text1 := nodes["submap-1__text-1"]
	image := nodes["submap-1__image-1"]
	video := nodes["submap-1__video-1"]
	if text(object(text1["metadata"])["content"]) != "雨夜天台" {
		t.Fatalf("image prompt text lost: %#v", text1)
	}
	if text(image["scopeId"]) != "submap-1" || text(image["title"]) != "首帧" {
		t.Fatalf("image child not scoped: %#v", image)
	}
	if text(object(video["metadata"])["videoStartFrameNodeId"]) != "submap-1__image-1" || text(object(video["metadata"])["seconds"]) != "4" {
		t.Fatalf("video frame/duration lost: %#v", video["metadata"])
	}
	bindings := list(object(object(video["metadata"])["generationSpec"])["referenceBindings"])
	roles := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		roles = append(roles, text(binding["role"])+":"+text(binding["nodeId"]))
	}
	want := []string{
		"source-text:submap-1__text-1", "source-text:submap-1__text-2", "source-text:submap-1__text-3",
		"reference:asset-1", "reference:hero", "first-frame:submap-1__image-1",
	}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("video bindings = %v, want %v", roles, want)
	}
	second, ok := nodes["submap-2"]
	if !ok || text(object(object(second["metadata"])["shotMigration"])["preserved"].(map[string]any)["imageNodeId"]) != "old-image" {
		t.Fatalf("existing result not preserved: %#v", second)
	}
	if nodes["old-image"]["scopeId"] != nil || nodes["old-video"]["scopeId"] != nil {
		t.Fatal("existing result nodes must keep their original scope")
	}
	if len(list(object(second["metadata"])["shotMigration"].(map[string]any)["preserved"].(map[string]any)["assetBindings"])) != 0 {
		t.Fatal("unexpected asset bindings on second row")
	}
	if _, err := references.Reconcile(doc, doc, capability.BuiltinRegistry(), func() string { return "edge" }); err != nil {
		t.Fatalf("migrated document is not a valid reference document: %v", err)
	}
	again, err := ApplyShotSubmaps(doc, allocate)
	if err != nil {
		t.Fatal(err)
	}
	if again.Rows != 0 {
		t.Fatalf("repeat migration was not idempotent: %+v", again)
	}
}

func nodeMap(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, node := range list(doc["nodes"]) {
		out[text(node["id"])] = node
	}
	return out
}

func itoa(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
