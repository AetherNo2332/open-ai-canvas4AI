package app

import (
	"encoding/json"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W06：资产卡投送与收录。重点是不变量：同资产只一张卡、重跑不动用户拖过的位置、
// 无差异不写画布、收录从节点快照建行并往目标集投新卡。

func seedShortplayCanvas(t *testing.T, db *gorm.DB, owner, projectID, canvasID string, nodes []map[string]any) {
	t.Helper()
	raw := make([]any, 0, len(nodes))
	for _, node := range nodes {
		raw = append(raw, node)
	}
	payload, err := json.Marshal(map[string]any{"nodes": raw, "connections": []any{}, "referenceFieldsVersion": 1})
	if err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: canvasID, UserID: owner, ProjectID: projectID, Title: canvasID, PayloadJSON: string(payload), Revision: 1, UpdatedAt: time.Now()}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

func storyInputNode() map[string]any {
	return map[string]any{
		"id": "story-input", "type": "text", "title": "故事输入", "position": map[string]any{"x": 0.0, "y": 0.0},
		"width": 340.0, "height": 240.0, "metadata": map[string]any{"workflowKind": "story_input", "content": "雨夜借伞"},
	}
}

func readCanvasNodes(t *testing.T, db *gorm.DB, canvasID string) []map[string]any {
	t.Helper()
	var row model.CanvasProject
	if err := db.First(&row, "id = ?", canvasID).Error; err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(row.PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	nodes, _ := doc["nodes"].([]any)
	result := make([]map[string]any, 0, len(nodes))
	for _, raw := range nodes {
		if node, ok := raw.(map[string]any); ok {
			result = append(result, node)
		}
	}
	return result
}

func TestInjectShortplayAssetCardsIsIdempotentAndKeepsUserPosition(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-cards", "sp-cards")
	asset := seedAsset(t, svc, "sp-cards", 1, shortplay.AssetCharacter, "沈青禾", "")
	seedShortplayCanvas(t, db, "owner", "p-cards", "sp-cards-c1", []map[string]any{storyInputNode()})
	if err := svc.repo.UpdateShortplayEpisodeAssetIDs("sp-cards-ep1", "[1]"); err != nil {
		t.Fatal(err)
	}
	svc.injectShortplayAssetCards("sp-cards")

	nodes := readCanvasNodes(t, db, "sp-cards-c1")
	cards := 0
	for _, node := range nodes {
		if node["type"] == shortplayAssetNodeType {
			cards++
			metadata, _ := node["metadata"].(map[string]any)
			if shortplayMetadataAssetID(metadata["shortplayAssetId"]) != asset.ID {
				t.Fatalf("card asset id = %v", metadata["shortplayAssetId"])
			}
			if metadata["shortplayAssetCategory"] != shortplay.AssetCharacter {
				t.Fatalf("card category = %v", metadata["shortplayAssetCategory"])
			}
		}
	}
	if cards != 1 {
		t.Fatalf("cards = %d, want 1", cards)
	}
	var afterFirst model.CanvasProject
	if err := db.First(&afterFirst, "id = ?", "sp-cards-c1").Error; err != nil {
		t.Fatal(err)
	}
	// 模拟用户拖动卡片：重跑注入只刷新内容，不动位置。
	position := map[string]any{"x": 900.0, "y": 640.0}
	for index := range nodes {
		if nodes[index]["type"] == shortplayAssetNodeType {
			nodes[index]["position"] = position
		}
	}
	doc := map[string]any{"nodes": toAnyNodes(nodes), "connections": []any{}, "referenceFieldsVersion": 1}
	payload, _ := json.Marshal(doc)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "sp-cards-c1").
		Updates(map[string]any{"payload_json": string(payload), "revision": afterFirst.Revision + 1}).Error; err != nil {
		t.Fatal(err)
	}
	svc.injectShortplayAssetCards("sp-cards")
	nodes = readCanvasNodes(t, db, "sp-cards-c1")
	if len(nodes) != 2 {
		t.Fatalf("nodes after re-inject = %d, want 2", len(nodes))
	}
	for _, node := range nodes {
		if node["type"] != shortplayAssetNodeType {
			continue
		}
		got := node["position"].(map[string]any)
		if got["x"] != position["x"] || got["y"] != position["y"] {
			t.Fatalf("user position overwritten: %#v", got)
		}
	}
	// 无差异重跑不写画布：revision 不再增长。
	var beforeIdle model.CanvasProject
	if err := db.First(&beforeIdle, "id = ?", "sp-cards-c1").Error; err != nil {
		t.Fatal(err)
	}
	svc.injectShortplayAssetCards("sp-cards")
	var afterIdle model.CanvasProject
	if err := db.First(&afterIdle, "id = ?", "sp-cards-c1").Error; err != nil {
		t.Fatal(err)
	}
	if afterIdle.Revision != beforeIdle.Revision {
		t.Fatalf("idle inject moved revision %d -> %d", beforeIdle.Revision, afterIdle.Revision)
	}
}

func toAnyNodes(nodes []map[string]any) []any {
	result := make([]any, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node)
	}
	return result
}

func TestPlaceShortplayAssetCardsBindsEpisodeAndInjects(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-place", "sp-place")
	first := seedAsset(t, svc, "sp-place", 1, shortplay.AssetCharacter, "沈青禾", "")
	second := seedAsset(t, svc, "sp-place", 2, shortplay.AssetProp, "油纸伞", "")
	seedShortplayCanvas(t, db, "owner", "p-place", "sp-place-c2", []map[string]any{storyInputNode()})

	result, err := svc.PlaceShortplayAssetCards("owner", "sp-place", "sp-place-ep2", shortplay.PlaceAssetRequest{AssetIDs: []int{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Placed != 2 || len(result.NodeIDs) != 2 {
		t.Fatalf("place result = %#v", result)
	}
	episode, err := svc.repo.ShortplayEpisodeByID("sp-place", "sp-place-ep2")
	if err != nil {
		t.Fatal(err)
	}
	if got := shortplayIntList(episode.AssetIDsJSON); len(got) != 2 {
		t.Fatalf("episode asset ids = %v", got)
	}
	row, err := svc.repo.ShortplayAssetByID("sp-place", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if numbers := shortplay.SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON); len(numbers) != 1 || numbers[0] != 2 {
		t.Fatalf("asset episodes = %v", numbers)
	}
	nodes := readCanvasNodes(t, db, "sp-place-c2")
	cards := 0
	for _, node := range nodes {
		if node["type"] == shortplayAssetNodeType {
			cards++
		}
	}
	if cards != 2 {
		t.Fatalf("cards = %d, want 2", cards)
	}
}

func TestAdoptShortplayAssetCreatesRowAndProjectsCard(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-adopt", "sp-adopt")
	seedShortplayCanvas(t, db, "owner", "p-adopt", "sp-adopt-c1", []map[string]any{storyInputNode()})
	seedShortplayCanvas(t, db, "owner", "", "free-canvas", []map[string]any{{
		"id": "local-card", "type": shortplayAssetNodeType, "title": "临时角色",
		"position": map[string]any{"x": 0.0, "y": 0.0}, "width": 264.0, "height": 352.0,
		"metadata": map[string]any{
			"content": "路边卖伞的姑娘", "shortplayAssetCategory": "character",
			"shortplayAssetAppearance": "青布衫", "shortplayAssetAliases": []any{"阿禾"},
			"shortplayAssetVoice": "青年女声",
		},
	}})

	result, err := svc.AdoptShortplayAsset("owner", "sp-adopt", shortplay.AdoptAssetRequest{
		CanvasID: "free-canvas", NodeID: "local-card", EpisodeID: "sp-adopt-ep1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.AssetID == 0 || result.NodeID == "" {
		t.Fatalf("adopt result = %#v", result)
	}
	row, err := svc.repo.ShortplayAssetByID("sp-adopt", result.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != "临时角色" || row.Description != "路边卖伞的姑娘" || row.Voice != "青年女声" {
		t.Fatalf("adopted row = %#v", row)
	}
	cards := 0
	for _, node := range readCanvasNodes(t, db, "sp-adopt-c1") {
		if node["type"] != shortplayAssetNodeType {
			continue
		}
		cards++
		metadata, _ := node["metadata"].(map[string]any)
		if shortplayMetadataAssetID(metadata["shortplayAssetId"]) != result.AssetID {
			t.Fatalf("projected card asset id = %v", metadata["shortplayAssetId"])
		}
	}
	if cards != 1 {
		t.Fatalf("target cards = %d, want 1", cards)
	}
	// 源局部卡原样不动：仍然没有 shortplayAssetId。
	sourceNodes := readCanvasNodes(t, db, "free-canvas")
	for _, node := range sourceNodes {
		if node["id"] != "local-card" {
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		if shortplayMetadataAssetID(metadata["shortplayAssetId"]) != 0 {
			t.Fatalf("source card was promoted in place: %#v", metadata)
		}
	}
}

func TestApplyShortplayAssetImageFromCanvasRejectsFreeCanvas(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-writeback", "sp-writeback")
	seedShortplayCanvas(t, db, "owner", "", "free-writeback", nil)
	seedAsset(t, svc, "sp-writeback", 1, shortplay.AssetCharacter, "沈青禾", "")
	if _, _, err := svc.ApplyShortplayAssetImageFromCanvas("owner", "free-writeback", 1, "res-1"); err == nil {
		t.Fatal("free canvas image write-back was accepted")
	}
}

// 集行先有画布地址、画布行还没建时，投送必须先按需建画布再投卡；
// 曾经的 500 是这里直接读 record not found。
func TestPlaceShortplayAssetCardsCreatesMissingEpisodeCanvas(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-place-new", "sp-place-new")
	asset := seedAsset(t, svc, "sp-place-new", 1, shortplay.AssetCharacter, "沈青禾", "")
	result, err := svc.PlaceShortplayAssetCards("owner", "sp-place-new", "sp-place-new-ep1", shortplay.PlaceAssetRequest{AssetIDs: []int{asset.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Placed != 1 || len(result.NodeIDs) != 1 {
		t.Fatalf("place result = %#v", result)
	}
	canvas, err := svc.repo.CanvasProjectForUser("owner", "sp-place-new-c1")
	if err != nil {
		t.Fatalf("episode canvas not created: %v", err)
	}
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	nodes := creationMaps(doc["nodes"])
	cards := 0
	for _, node := range nodes {
		if stringValue(node["type"]) == shortplayAssetNodeType {
			cards++
		}
	}
	if cards != 1 {
		t.Fatalf("cards = %d", cards)
	}
}
