package app

import (
	"testing"

	"yingce/backend/internal/shortplay"
)

// W07 读面：集画布反查作品、按类型/关键词筛选，返回项目内资产 id 与媒体状态，不含下载凭证。
func TestCloudAgentListShortplayAssetsReadsBoundProject(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-agent", "sp-agent")
	seedShortplayCanvas(t, db, "owner", "p-agent", "sp-agent-c1", []map[string]any{storyInputNode()})
	first := seedVoicedAsset(t, svc, "sp-agent", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", shortplay.ImageReady)
	seedVoicedAsset(t, svc, "sp-agent", 2, shortplay.AssetProp, "油纸伞", "", "")
	if applied, err := svc.repo.UpdateShortplayAsset(first, first.Revision); err != nil || !applied {
		t.Fatalf("voice = %v, %v", applied, err)
	}

	state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: "sp-agent-c1"}}
	call := cloudAgentCall{ID: "call-1", Type: "function"}
	call.Function.Name = "shortplay_list_assets"
	call.Function.Arguments = `{"query":"青禾","kind":"character"}`

	raw, err := cloudAgentListShortplayAssets(svc.repo, "owner", state, call)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := raw.(map[string]any)
	if !ok || result["linked"] != true {
		t.Fatalf("result = %#v", raw)
	}
	assets, _ := result["assets"].([]shortplayAgentAsset)
	if len(assets) != 1 || assets[0].ID != 1 || assets[0].Name != "沈青禾" {
		t.Fatalf("assets = %#v", result["assets"])
	}
	if assets[0].Voice != "青年女声" || assets[0].AudioStatus != shortplay.ImageReady {
		t.Fatalf("asset view = %#v", assets[0])
	}

	// 自由画布返回 linked=false，不报错。
	seedShortplayCanvas(t, db, "owner", "", "free-agent", nil)
	freeRaw, err := cloudAgentListShortplayAssets(svc.repo, "owner", &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: "free-agent"}}, call)
	if err != nil {
		t.Fatal(err)
	}
	if free, _ := freeRaw.(map[string]any); free["linked"] != false {
		t.Fatalf("free canvas = %#v", freeRaw)
	}
}
