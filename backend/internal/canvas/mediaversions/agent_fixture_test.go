package mediaversions

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

// testdata/agent-writeback.json 由本测试产出，web/test/canvas-media-versions-agent.test.ts 读取；
// 未设置 WRITE_AGENT_FIXTURE=1 时校验提交的夹具与当前 Finish 输出一致。
func TestAgentWritebackFixture(t *testing.T) {
	restore := newVersionID
	ids := []string{"agent-v1", "agent-v2", "agent-v3"}
	newVersionID = func() string { id := ids[0]; ids = ids[1:]; return id }
	restoreClock := clock
	clock = func() time.Time { return time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { newVersionID, clock = restore, restoreClock })
	node := map[string]any{"id": "hero", "type": "image", "title": "主角", "position": map[string]any{"x": 0.0, "y": 0.0}, "width": 400.0, "height": 300.0, "metadata": map[string]any{"storageKey": "resource:old", "content": "/api/resources/old/file", "mimeType": "image/png", "naturalWidth": 800.0, "naturalHeight": 600.0, "status": "success", "prompt": "first"}}
	before := cloneMap(node)
	base, _ := Begin(node, model.Task{ID: "agent-task", Status: model.TaskStatusQueued}, nil)
	result := cloneMap(node)
	m := meta(result)
	m["storageKey"], m["content"], m["naturalWidth"], m["naturalHeight"], m["prompt"], m["taskStatus"], m["status"], m["producedModel"], m["taskCompletedAt"] = "resource:agent-new", "/api/resources/agent-new/file", 600.0, 900.0, "agent prompt", "succeeded", "success", "img-model", "2026-10-29T08:00:00.000Z"
	after, outcome := Finish(node, result, base, model.Task{ID: "agent-task", Status: model.TaskStatusSucceeded}, "", time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC))
	raw, _ := json.MarshalIndent(map[string]any{"before": before, "after": after, "outcome": outcome}, "", "  ")
	raw = append(raw, '\n')
	if os.Getenv("WRITE_AGENT_FIXTURE") == "1" {
		if err := os.WriteFile("testdata/agent-writeback.json", raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile("testdata/agent-writeback.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(stored, []byte("\r\n"), []byte("\n")), raw) {
		t.Fatal("testdata/agent-writeback.json 已过期；运行 WRITE_AGENT_FIXTURE=1 go test ./internal/canvas/mediaversions 重新生成")
	}
}
