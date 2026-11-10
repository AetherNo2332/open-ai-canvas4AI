package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentBuildBoardUsesExistingKeyframesAndAssetsForNewVideos(t *testing.T) {
	s, db, _ := agentMediaFixture(t)
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	nodes := creationMaps(doc["nodes"])
	nodes = append(nodes, map[string]any{
		"id": "sb", "type": "script", "title": "已有关键帧的分镜",
		"position": map[string]any{"x": 0, "y": 0}, "width": 920, "height": 360,
		"metadata": map[string]any{"status": "idle", "workflowKind": "script", "storyboard": map[string]any{
			"rows":           []any{map[string]any{"id": "r1", "imageNodeId": "hero", "imageGenerationPrompt": "英雄抬头", "videoMotionPrompt": "英雄缓缓转身"}},
			"visibleColumns": []any{}, "referenceNodeIds": []any{},
		}},
	})
	doc["nodes"] = nodes
	raw, _ := json.Marshal(doc)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	args := map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "storyboardNodeId": "sb",
		"assets": []map[string]any{{"id": "cat", "category": "character", "title": "叮当猫", "prompt": "叮当猫设定", "rowIds": []string{"r1"}}},
	}
	encoded, _ := json.Marshal(args)
	call := piAgentTestCall("board-existing", "canvas_build_board", string(encoded))
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	result, err := applyCloudAgentBuildBoard(s.repo, "user", "agent-canvas", call, policy)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["rows"].([]map[string]any)
	if len(rows) != 1 || stringValue(rows[0]["keyframeNodeId"]) != "hero" {
		t.Fatalf("board receipt omitted existing keyframe: %+v", rows)
	}
	videoID := stringValue(rows[0]["videoNodeId"])
	if videoID == "" {
		t.Fatal("board did not create missing video")
	}
	canvas, err = s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	after, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	video := agentBatchNodeByID(creationMaps(after["nodes"]), videoID)
	if metadata, _ := video["metadata"].(map[string]any); metadata["videoStartFrameNodeId"] != "hero" {
		t.Fatalf("new video lost the existing start frame: %+v", metadata)
	}
	connections := map[string]bool{}
	for _, edge := range creationMaps(after["connections"]) {
		connections[stringValue(edge["fromNodeId"])+"->"+stringValue(edge["toNodeId"])] = true
	}
	if !connections["hero->"+videoID] || !connections["cat->hero"] {
		t.Fatalf("board omitted existing reference connections: %+v", connections)
	}
	args["snapshotHash"] = cloudAgentCanvasHash(after)
	encoded, _ = json.Marshal(args)
	call.Function.Arguments = string(encoded)
	repeated, err := applyCloudAgentBuildBoard(s.repo, "user", "agent-canvas", call, policy)
	if err != nil {
		t.Fatal(err)
	}
	repeatedRows := repeated.(map[string]any)["rows"].([]map[string]any)
	if len(repeatedRows) != 1 || repeatedRows[0]["keyframeNodeId"] != "hero" || repeatedRows[0]["videoNodeId"] != videoID {
		t.Fatalf("replayed board receipt lost output identities: %+v", repeatedRows)
	}
	afterRepeat, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil || afterRepeat.PayloadJSON != canvas.PayloadJSON {
		t.Fatalf("repeated board changed the completed graph: %v", err)
	}
}
