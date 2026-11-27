package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W07 写面第一片：shortplay_asset create/update（含解锁）。
// 覆盖：审批预览无副作用、批准执行建行/改行并刷新卡、重放不重复写、跨账号/无绑定拒、
// 只读权限不暴露。

func shortplayAssetCall(action, args string) cloudAgentCall {
	call := cloudAgentCall{ID: "call-asset", Type: "function"}
	call.Function.Name = "shortplay_asset"
	call.Function.Arguments = `{"action":"` + action + `"` + args + `}`
	return call
}

func TestCloudAgentShortplayAssetApprovalPreviewHasNoSideEffects(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-w7", "sp-w7")
	seedShortplayCanvas(t, db, "owner", "p-w7", "sp-w7-c1", []map[string]any{storyInputNode()})
	call := shortplayAssetCall("create", `,"kind":"character","name":"沈青禾"`)
	if _, err := prepareCloudAgentShortplayAsset(svc.repo, "owner", "sp-w7-c1", call); err != nil {
		t.Fatal(err)
	}
	var assets int64
	if err := db.Model(&model.ShortplayAsset{}).Count(&assets).Error; err != nil || assets != 0 {
		t.Fatalf("preview wrote assets: %d, %v", assets, err)
	}
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", "sp-w7-c1").Error; err != nil || canvas.Revision != 1 {
		t.Fatalf("preview moved canvas: %+v, %v", canvas.Revision, err)
	}
}

func TestCloudAgentShortplayAssetCreateThenReplayIsIdempotent(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-w7c", "sp-w7c")
	seedShortplayCanvas(t, db, "owner", "p-w7c", "sp-w7c-c1", []map[string]any{storyInputNode()})
	call := shortplayAssetCall("create", `,"kind":"character","name":"沈青禾","appearance":"齐肩黑发","voice":"青年女声","aliases":["青禾"]`)
	for round := 0; round < 2; round++ {
		raw, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-w7c-c1", call, RuntimePolicySetting{})
		if err != nil {
			t.Fatal(err)
		}
		result := raw.(map[string]any)
		if result["assetId"].(int) != 1 || result["name"] != "沈青禾" {
			t.Fatalf("round %d result = %#v", round, result)
		}
	}
	var assets int64
	if err := db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ?", "sp-w7c").Count(&assets).Error; err != nil || assets != 1 {
		t.Fatalf("replay created %d rows, %v", assets, err)
	}
	if cards := countAssetCards(t, db, "sp-w7c-c1"); cards != 1 {
		t.Fatalf("replay created %d cards", cards)
	}
	row, err := svc.repo.ShortplayAssetByID("sp-w7c", 1)
	if err != nil || row.Voice != "青年女声" || len(shortplay.SplitAliasesJSON(row.AliasesJSON)) != 1 {
		t.Fatalf("created row = %#v, %v", row, err)
	}
}

func TestCloudAgentShortplayAssetUpdateUnlocksAndKeepsUnsetFields(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-w7u", "sp-w7u")
	seedShortplayCanvas(t, db, "owner", "p-w7u", "sp-w7u-c1", []map[string]any{storyInputNode()})
	locked := seedVoicedAsset(t, svc, "sp-w7u", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")
	locked.Locked, locked.Appearance = true, "齐肩黑发"
	if applied, err := svc.repo.UpdateShortplayAsset(locked, 1); err != nil || !applied {
		t.Fatalf("seed = %v, %v", applied, err)
	}
	call := shortplayAssetCall("update", `,"assetId":1,"locked":false,"appearance":"黑色长发"`)
	if _, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-w7u-c1", call, RuntimePolicySetting{}); err != nil {
		t.Fatal(err)
	}
	row, err := svc.repo.ShortplayAssetByID("sp-w7u", 1)
	if err != nil {
		t.Fatal(err)
	}
	if row.Locked || row.Appearance != "黑色长发" || row.Voice != "青年女声" {
		t.Fatalf("row after update = %#v", row)
	}
	if cards := countAssetCards(t, db, "sp-w7u-c1"); cards != 1 {
		t.Fatalf("update cards = %d", cards)
	}
}

func TestCloudAgentShortplayAssetRejectsCrossAccountAndFreeCanvas(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-w7x", "sp-w7x")
	seedShortplayCanvas(t, db, "other", "p-w7x", "sp-w7x-foreign", nil)
	seedShortplayCanvas(t, db, "owner", "", "sp-w7x-free", nil)
	call := shortplayAssetCall("create", `,"kind":"prop","name":"油纸伞"`)
	if _, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-w7x-foreign", call, RuntimePolicySetting{}); err == nil {
		t.Fatal("cross-account canvas write was accepted")
	}
	if _, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-w7x-free", call, RuntimePolicySetting{}); err == nil || !strings.Contains(err.Error(), "分集画布") {
		t.Fatalf("free canvas error = %v", err)
	}
}

func TestCloudAgentShortplayAssetHiddenFromReadOnlyRun(t *testing.T) {
	req := CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}}
	names := cloudAgentToolNames(cloudAgentVisibleTools(cloudAgentTools(req), "", nil, nil))
	for _, name := range names {
		if name == "shortplay_asset" {
			t.Fatalf("read-only run exposed shortplay_asset: %v", names)
		}
	}
}

func countAssetCards(t *testing.T, db *gorm.DB, canvasID string) int {
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
	count := 0
	for _, raw := range nodes {
		if node, ok := raw.(map[string]any); ok && node["type"] == shortplayAssetNodeType {
			count++
		}
	}
	return count
}
