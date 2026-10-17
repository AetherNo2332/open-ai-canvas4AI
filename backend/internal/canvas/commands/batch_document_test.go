package commands

import "testing"

func TestGeneratedBatchDocumentPreservesCellsAndStableBindings(t *testing.T) {
	doc := fixture(t, `{"id":"canvas","nodes":[{"id":"text","type":"text","position":{"x":10,"y":20}},{"id":"image","type":"image"}],"connections":[]}`)
	context, err := PrepareBatchDocument(doc, "text", map[string]any{"inputNodeIds": []any{"image"}, "targetRowCount": float64(2)}, "managed-model")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGeneratedBatchDocument(doc, "task", `{"text":"结果如下：\n{\"columns\":[\"卖点\",\"卖点\",\"细节\"],\"rows\":[{\"卖点\":[\"轻\",\"薄\"],\"细节\":123},{\"卖点\":\"耐用\",\"细节\":\"防水\"}]}"}`, context)
	if err != nil {
		t.Fatal(err)
	}
	table := entity(result, "node:batch-table-task")["metadata"].(map[string]any)["batchTable"].(map[string]any)
	rows := maps(table["rows"])
	if len(rows) != 2 || len(maps(table["textColumns"])) != 2 || rows[0]["cells"].(map[string]any)["ai-col-0"] != "轻、薄" {
		t.Fatal("table mismatch", table)
	}
	if len(maps(result["connections"])) != 1 {
		t.Fatal("input binding missing")
	}
	if _, err := ApplyGeneratedBatchDocument(doc, "task", `{"text":"{\"columns\":[\"x\"],\"rows\":[{\"x\":\"only one\"}]}"}`, context); err == nil {
		t.Fatal("short output claimed complete")
	}
}
func TestGeneratedVideoBatchKeepsSourceTimingAndPromptFallbacks(t *testing.T) {
	doc := fixture(t, `{"id":"canvas","nodes":[{"id":"text","type":"text","position":{"x":0,"y":0}},{"id":"video","type":"video"}],"connections":[]}`)
	context, err := PrepareBatchDocument(doc, "text", map[string]any{"inputNodeIds": []any{"video"}, "video": true}, "model")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGeneratedBatchDocument(doc, "video-task", `{"text":"{\"title\":\"拆解\",\"rows\":[{\"plotDescription\":\"产品推近\",\"motion\":\"缓慢推进\",\"durationSeconds\":3,\"keyframeTimeSeconds\":1.5}]}"}`, context)
	if err != nil {
		t.Fatal(err)
	}
	table := entity(result, "node:batch-table-video-task")["metadata"].(map[string]any)["batchTable"].(map[string]any)
	row := maps(table["storyboardRows"])[0]
	if row["videoMotionPrompt"] != "产品推近；缓慢推进" || row["keyframeTimeMs"] != float64(1500) {
		t.Fatal("video fields lost", row)
	}
	if _, err := PrepareBatchDocument(doc, "text", map[string]any{"inputNodeIds": []any{"foreign"}}, "model"); err == nil {
		t.Fatal("foreign input accepted")
	}
}
