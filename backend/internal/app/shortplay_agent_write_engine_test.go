package app

import (
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W07 引擎级审批回归：真实 run 里拒绝审批不写资产/卡/任务，批准只执行一次，
// 批准回执可重放且不重复写。place/image 的重复执行按 job 去重与卡刷新收敛。

func shortplayRunFixture(t *testing.T, action string, extra string) (*Service, *gorm.DB, *model.CloudAgentExecution, *cloudAgentRuntime, cloudAgentCall) {
	t.Helper()
	s, db, run := piAgentTestFixture(t)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", run.CanvasID).Update("project_id", "p-run").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Project{ID: "p-run", UserID: "user", Name: "运行短剧", Type: "shortplay"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Shortplay{ID: "sp-run", ProjectID: "p-run", UserID: "user", SettingsJSON: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ShortplayEpisode{ID: "sp-run-ep1", ShortplayID: "sp-run", Number: 1, Title: "第一集", CanvasID: run.CanvasID}).Error; err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "call-" + action}
	call.Function.Name = "shortplay_asset"
	call.Function.Arguments = `{"action":"` + action + `"` + extra + `}`
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.PermissionMode = "request_approval"
	// 资产工具只在短剧集画布披露：夹具在建 run 后补上作品绑定，这里同步定格标记并重算目录。
	state.Request.ShortplayEnabled = true
	rebuildFrozenCapabilities(t, s, &state, false)
	state.Calls = []cloudAgentCall{call}
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": state.Calls})
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, latest := reloadPiRun(t, s, run.ID)
	return s, db, run, latest, call
}

func countRunWrites(t *testing.T, db *gorm.DB, canvasID string) (int64, int64, int64) {
	t.Helper()
	var assets, jobs, cards int64
	if err := db.Model(&model.ShortplayAsset{}).Count(&assets).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ShortplayJob{}).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", canvasID).Error; err == nil {
		cards = int64(countAssetCards(t, db, canvas.ID))
	}
	return assets, jobs, cards
}

func TestCloudAgentShortplayPlaceTwiceRefreshesSameCard(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-place2", "sp-place2")
	seedShortplayCanvas(t, db, "owner", "p-place2", "sp-place2-c1", []map[string]any{storyInputNode()})
	seedAsset(t, svc, "sp-place2", 1, shortplay.AssetCharacter, "沈青禾", "")
	call := cloudAgentCall{ID: "call-place"}
	call.Function.Name = "shortplay_asset"
	call.Function.Arguments = `{"action":"place","assetIds":[1]}`
	for round := 0; round < 2; round++ {
		if _, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-place2-c1", call, RuntimePolicySetting{}); err != nil {
			t.Fatal(err)
		}
	}
	if cards := countAssetCards(t, db, "sp-place2-c1"); cards != 1 {
		t.Fatalf("place replay cards = %d", cards)
	}
	episode, err := svc.repo.ShortplayEpisodeByID("sp-place2", "sp-place2-ep1")
	if err != nil {
		t.Fatal(err)
	}
	if ids := shortplayIntList(episode.AssetIDsJSON); len(ids) != 1 {
		t.Fatalf("episode asset ids = %v", ids)
	}
}

func TestCloudAgentShortplayImageQueueReplayReusesJob(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-img2", "sp-img2")
	seedShortplayCanvas(t, db, "owner", "p-img2", "sp-img2-c1", []map[string]any{storyInputNode()})
	seedAsset(t, svc, "sp-img2", 1, shortplay.AssetCharacter, "沈青禾", "")
	call := cloudAgentCall{ID: "call-image"}
	call.Function.Name = "shortplay_asset"
	call.Function.Arguments = `{"action":"image","assetIds":[1]}`
	first, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-img2-c1", call, RuntimePolicySetting{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := applyCloudAgentShortplayAsset(svc.repo, "owner", "sp-img2-c1", call, RuntimePolicySetting{})
	if err != nil {
		t.Fatal(err)
	}
	if first.(map[string]any)["queued"] != 1 || second.(map[string]any)["reused"] != 1 {
		t.Fatalf("queue results = %#v / %#v", first, second)
	}
	var jobs int64
	if err := db.Model(&model.ShortplayJob{}).Where("kind = ?", shortplay.JobAssetImage).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("image jobs = %d, %v", jobs, err)
	}
}
