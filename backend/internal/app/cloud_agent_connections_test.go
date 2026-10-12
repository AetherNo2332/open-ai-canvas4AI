package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func cloneCloudAgentConnectionDocument(doc map[string]any) map[string]any {
	raw, _ := json.Marshal(doc)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func TestCloudAgentManualConnectionParity(t *testing.T) {
	nodes := []map[string]any{
		{"id": "note", "type": "text"}, {"id": "markdown", "type": "markdown"},
		{"id": "image", "type": "image"}, {"id": "video", "type": "video"},
		{"id": "audio", "type": "audio"}, {"id": "drawing", "type": "drawing"},
		{"id": "config", "type": "config"}, {"id": "table", "type": "batch-table"},
		{"id": "convert", "type": "media-conversion"}, {"id": "frame", "type": "frame"},
		{"id": "script", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "r1", "shotNumber": 1.0, "durationSeconds": 3.0, "plotDescription": "test"}}}}},
	}
	for _, tc := range []struct {
		from, to, fromHandle, toHandle string
		allowed                        bool
	}{
		{"note", "markdown", "", "", true}, {"drawing", "image", "", "", true},
		{"table", "config", "", "", true}, {"image", "table", "", "", true},
		{"note", "table", "", "", false}, {"config", "table", "", "", false},
		{"video", "convert", "", "", true}, {"drawing", "convert", "", "", false},
		{"audio", "image", "", "", false}, {"image", "audio", "", "", false},
		{"frame", "note", "", "", false}, {"note", "note", "", "", false},
		{"note", "script", "", "storyboard:context", true},
		{"image", "script", "", "row:r1", true},
		{"script", "video", "row:r1", "", true},
		{"image", "script", "", "row:missing", false},
		{"image", "table", "", "batch-reference:reference-1", true},
		{"image", "table", "", "batch-reference:missing", false},
	} {
		t.Run(tc.from+"-"+tc.to+tc.fromHandle+tc.toHandle, func(t *testing.T) {
			doc := map[string]any{"nodes": nodes, "connections": []any{}}
			// The planner modifies decoded documents; each case needs its own JSON clone.
			doc = cloneCloudAgentConnectionDocument(doc)
			_, err := applyCloudAgentCanvasPlan(doc, []agentCanvasOp{{Type: "connect_nodes", ID: "edge", FromNodeID: tc.from, ToNodeID: tc.to, FromHandleID: tc.fromHandle, ToHandleID: tc.toHandle}})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
			if err == nil && tc.toHandle == "row:r1" {
				_, _, rows, _ := storyboardNodeFromDocument(doc, "script")
				if len(creationMaps(rows[0]["assetBindings"])) != 1 {
					t.Fatal("row asset binding missing")
				}
			}
		})
	}
}

func TestCloudAgentConnectionPortsAndAtomicity(t *testing.T) {
	doc := map[string]any{"nodes": []any{
		map[string]any{"id": "image", "type": "image"},
		map[string]any{"id": "script", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "r1"}, map[string]any{"id": "r2"}}}}},
	}, "connections": []any{}}
	ops := []agentCanvasOp{{Type: "connect_nodes", ID: "a", FromNodeID: "image", ToNodeID: "script", ToHandleID: "row:r1"}, {Type: "connect_nodes", ID: "b", FromNodeID: "image", ToNodeID: "script", ToHandleID: "row:r2"}}
	if _, err := applyCloudAgentCanvasPlan(doc, ops); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(doc)
	if len(creationMaps(doc["connections"])) != 2 {
		t.Fatal("distinct row ports collapsed")
	}
	state, err := cloudAgentCanvasState(nil, "user", "canvas", doc, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	readEdges := creationMaps(state.(map[string]any)["connections"])
	if len(readEdges) != 2 || readEdges[0]["toHandleId"] != "row:r1" || readEdges[1]["toHandleId"] != "row:r2" {
		t.Fatal("read projection lost ports")
	}
	duplicate := ops[0]
	duplicate.ID = "duplicate"
	if _, err := applyCloudAgentCanvasPlan(doc, []agentCanvasOp{duplicate}); err == nil {
		t.Fatal("duplicate endpoint and handle accepted")
	}
	ops[0].ID = "context"
	ops[0].ToHandleID = "storyboard:context"
	ops[1].ID = "invalid"
	ops[1].ToHandleID = "row:missing"
	if _, err := applyCloudAgentCanvasPlan(doc, ops); err == nil {
		t.Fatal("missing row accepted")
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("failed batch leaked row or edge edits")
	}
}

func TestCloudAgentConnectionStoryboardVideo(t *testing.T) {
	doc := map[string]any{"nodes": []any{
		map[string]any{"id": "drawing", "type": "drawing", "metadata": map[string]any{"drawingId": "d"}},
		map[string]any{"id": "image", "type": "image", "metadata": map[string]any{"storageKey": "resource:x"}},
		map[string]any{"id": "empty", "type": "image"},
		map[string]any{"id": "video", "type": "video"},
		map[string]any{"id": "script", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "r1", "shotNumber": 2, "durationSeconds": 5, "plotDescription": "fallback", "videoMotionPrompt": "walk", "videoPromptTemplateVariables": map[string]any{"scene": "street"}, "assetBindings": []any{map[string]any{"nodeId": "drawing"}, map[string]any{"nodeId": "image"}, map[string]any{"nodeId": "empty"}}}}}}},
	}, "connections": []any{}}
	if _, err := applyCloudAgentCanvasPlan(doc, []agentCanvasOp{{Type: "connect_nodes", ID: "output", FromNodeID: "script", ToNodeID: "video", FromHandleID: "row:r1"}}); err != nil {
		t.Fatal(err)
	}
	nodes := creationMaps(doc["nodes"])
	video := nodes[cloudAgentNodeIndex(nodes, "video")]
	m := video["metadata"].(map[string]any)
	if video["title"] != "镜头 2 · 视频" || m["prompt"] != "walk" || m["seconds"] != "5" || m["promptTemplateOperation"] != "storyboard_video" || m["composerContent"] != "参考资产：@绘图1 @图片1\nwalk" {
		t.Fatalf("manual video metadata mismatch: %+v", m)
	}
	_, _, rows, _ := storyboardNodeFromDocument(doc, "script")
	if rows[0]["videoNodeId"] != "video" {
		t.Fatal("output row missing")
	}
}

func TestCloudAgentConnectionCatalogCapacity(t *testing.T) {
	s, db, _ := agentMediaFixture(t)
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceVolcengineArkVideo), "seedance-test")
	profile.Video.References.MaxImages = 1
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "video-cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"nodes": []any{map[string]any{"id": "a", "type": "image"}, map[string]any{"id": "b", "type": "image"}, map[string]any{"id": "v", "type": "video"}}, "connections": []any{map[string]any{"fromNodeId": "a", "toNodeId": "v"}, map[string]any{"fromNodeId": "b", "toNodeId": "v"}}}
	if err := validateCloudAgentConnectionCapacities(s.repo, doc, []agentCanvasOp{{Type: "connect_nodes", ToNodeID: "v"}}); err == nil {
		t.Fatal("server catalog capacity ignored")
	}
}

func TestCloudAgentConnectionRejectsNullHandle(t *testing.T) {
	_, err := decodeCloudAgentCanvasArgs(`{"snapshotHash":"h","ops":[{"type":"connect_nodes","id":"e","fromNodeId":"a","toNodeId":"b","toHandleId":null}]}`)
	if err == nil {
		t.Fatal("null handle bypassed string contract")
	}
}

func TestCloudAgentConnectionCharacterOrderMatchesCanvas(t *testing.T) {
	nodes := []map[string]any{
		{"id": "a", "type": "drawing", "metadata": map[string]any{"workflowKind": "character", "characterAssetId": "asset-a"}},
		{"id": "b", "type": "text", "metadata": map[string]any{"workflowKind": "character", "characterAssetId": "asset-b"}},
	}
	script := map[string]any{"id": "s", "metadata": map[string]any{"storyboard": map[string]any{}}}
	row := map[string]any{"characters": []any{map[string]any{"characterAssetId": "asset-b"}, map[string]any{"characterAssetId": "asset-a"}}}
	if got := cloudAgentConnectionComposer(script, row, nodes, "walk"); got != "参考资产：@绘图1 @角色1\nwalk" {
		t.Fatalf("character order differs from manual UI: %s", got)
	}
}
