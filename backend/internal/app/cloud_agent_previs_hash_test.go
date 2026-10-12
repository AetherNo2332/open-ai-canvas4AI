package app

import "testing"

func TestCloudAgentCanvasHashIncludesPrevisContent(t *testing.T) {
	before := map[string]any{"nodes": []any{}, "previsScenes": []any{map[string]any{"id": "scene", "title": "before"}}}
	after := map[string]any{"nodes": []any{}, "previsScenes": []any{map[string]any{"id": "scene", "title": "after"}}}
	if cloudAgentCanvasHash(before) == cloudAgentCanvasHash(after) {
		t.Fatal("previs edits must invalidate whole-canvas undo snapshots")
	}
}
