package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// measureFixtureCanvas builds the canvas shape the token audit measured: a
// handful of storyboard nodes holding 89 shot rows in total, plus media and
// text nodes. It only uses APIs whose signatures are identical in the pristine
// v1.3.0 tree and in the optimized tree, so the same file dumps a comparable
// payload from both.
func measureFixtureCanvas() map[string]any {
	nodes := []map[string]any{}
	remaining := 89
	for index := 0; index < 5; index++ {
		rows := 20
		if index == 4 {
			rows = remaining
		}
		remaining -= rows
		rowItems := make([]any, 0, rows)
		for shot := 0; shot < rows; shot++ {
			rowItems = append(rowItems, map[string]any{
				"id":                    cloudAgentID("user", "row:"+string(rune('a'+index))+":"+string(rune('0'+shot%10))),
				"shotNumber":            float64(shot + 1),
				"durationSeconds":       3.0,
				"plotDescription":       strings.Repeat("镜头画面", 20),
				"videoMotionPrompt":     strings.Repeat("运镜动作", 20),
				"imageGenerationPrompt": strings.Repeat("首帧提示", 12),
				"dialogue":              strings.Repeat("台词内容", 6),
				"imageNodeId":           "image-" + string(rune('a'+index)),
				"videoNodeId":           "video-" + string(rune('a'+index)),
				"assetBindings":         []any{map[string]any{"nodeId": "hero", "role": "character", "priority": float64(1)}},
				"characters":            []any{map[string]any{"characterName": "主角", "characterAssetId": "asset-1", "characterVersionId": "v1"}},
			})
		}
		nodes = append(nodes, map[string]any{
			"id": "storyboard-" + string(rune('a'+index)), "type": "script", "title": "分镜脚本" + string(rune('A'+index)),
			"position": map[string]any{"x": float64(100 * index), "y": 200.0}, "width": 920.0, "height": 360.0,
			"metadata": map[string]any{"status": "idle", "storyboard": map[string]any{"rows": rowItems}},
		})
	}
	for index, kind := range []string{"image", "video"} {
		nodes = append(nodes, map[string]any{
			"id": kind + "-" + string(rune('a'+index)), "type": kind, "title": "媒体节点" + string(rune('A'+index)),
			"position": map[string]any{"x": float64(100 * index), "y": 600.0}, "width": 720.0, "height": 405.0,
			"metadata": map[string]any{"status": "idle", "prompt": strings.Repeat("媒体提示词内容", 30), "composerContent": strings.Repeat("媒体提示词内容", 30)},
		})
	}
	nodes = append(nodes, map[string]any{
		"id": "text-a", "type": "text", "title": "创意说明",
		"position": map[string]any{"x": 10.0, "y": 20.0}, "width": 340.0, "height": 240.0,
		"metadata": map[string]any{"status": "idle", "content": strings.Repeat("创意说明正文", 40)},
	})
	nodes = append(nodes, map[string]any{
		"id": "markdown-a", "type": "markdown", "title": "交付文档",
		"position": map[string]any{"x": 20.0, "y": 40.0}, "width": 420.0, "height": 320.0,
		"metadata": map[string]any{"status": "idle", "content": strings.Repeat("文档正文", 40)},
	})
	return map[string]any{"nodes": nodes, "connections": []map[string]any{{"id": "edge-1", "fromNodeId": "image-a", "toNodeId": "video-a"}}}
}

// TestMeasureDumpPayload writes the request the Agent would send for this canvas
// so the token audit can price it with a real tokenizer. It asserts nothing
// about size: the audit compares two dumps taken from two trees.
func TestMeasureDumpPayload(t *testing.T) {
	out := os.Getenv("CLOUD_AGENT_MEASURE_OUT")
	if out == "" {
		t.Skip("CLOUD_AGENT_MEASURE_OUT not set")
	}
	doc := measureFixtureCanvas()
	raw, _ := json.Marshal(doc)
	canvas := &model.CanvasProject{PayloadJSON: string(raw), Title: "示例画布"}

	summary, err := cloudAgentCanvasSummary(canvas)
	if err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.Prompt = "请分析这个画布"
	// Full permission mode exposes all tool schemas, as in the audited session.
	req.PermissionMode = "auto"
	system, _, err := compileCloudAgentPolicies(req, nil, summary, cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")})
	if err != nil {
		t.Fatal(err)
	}
	canonical := cloudAgentCanonical(system, nil, req.Prompt, req)
	canonical.Messages = append(canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "type": "function", "function": map[string]any{"name": "canvas_get_state", "arguments": `{"offset":0}`}}}})

	view, err := cloudAgentCanvasState(nil, "user", "agent-canvas", doc, 0, nil, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, _ := json.Marshal(view)
	canonical.Messages = append(canonical.Messages, map[string]any{"role": "tool", "tool_call_id": "call_1", "content": string(resultJSON)})

	// Wire shape: the provider body actually sent upstream.
	messages := []any{}
	if strings.TrimSpace(canonical.SystemPrompt) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": canonical.SystemPrompt})
	}
	for _, message := range canonical.Messages {
		messages = append(messages, message)
	}
	wire := map[string]any{
		"messages": messages, "model": "deepseek-chat", "parallel_tool_calls": false,
		"prompt_cache_key": canonical.PromptCacheKey, "stream": true,
		"stream_options": map[string]any{"include_usage": true}, "tools": canonical.Tools,
	}
	compact, _ := json.Marshal(wire)
	segments := map[string]any{
		"wire":     json.RawMessage(compact),
		"system":   canonical.SystemPrompt,
		"digest":   summary,
		"messages": canonical.Messages,
		"tools":    canonical.Tools,
	}
	segmentsJSON, _ := json.Marshal(segments)
	if err := os.WriteFile(out+".payload.json", segmentsJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+".wire.json", compact, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("dumped wire payload: %d bytes, digest %d bytes, tools %d schemas", len(compact), len(summary), len(canonical.Tools))
}
