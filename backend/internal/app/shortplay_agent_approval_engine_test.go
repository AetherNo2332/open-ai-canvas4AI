package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W07 引擎级审批闭环：从真实 executeCloudAgentToolCall 入口验证 shortplay_asset
// 在 request_approval 下挂起、拒绝无写入、批准只写一次、恢复/重放不重复入队与费用。
// 这些断言必须走 run 检查点与 DecideCloudAgentApproval 控制面，helper 直调不能替代。

func canvasPayload(t *testing.T, db *gorm.DB, canvasID string) string {
	t.Helper()
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", canvasID).Error; err != nil {
		t.Fatal(err)
	}
	return canvas.PayloadJSON
}

// shortplayToolReceipt 取回指定 callID 的工具回执正文，失败信息据此定位退化为回执的那一层。
func shortplayToolReceipt(state *cloudAgentRuntime, callID string) string {
	for _, message := range state.Canonical.Messages {
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == callID {
			return stringField(message, "content")
		}
	}
	return ""
}

// shortplayRunRows 统计资产、job、资产卡；任务表含夹具根任务，按增量单独核对。
func shortplayRunRows(t *testing.T, db *gorm.DB, canvasID string) (assets, jobs, cards int64) {
	t.Helper()
	if err := db.Model(&model.ShortplayAsset{}).Count(&assets).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ShortplayJob{}).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	cards = int64(countAssetCards(t, db, canvasID))
	return assets, jobs, cards
}

func shortplayTaskCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	return tasks
}

// mustWaitForShortplayApproval 断言本次调用挂起成审批卡而不是退化为回执或直接执行。
func mustWaitForShortplayApproval(t *testing.T, s *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime, callID string) *cloudAgentApproval {
	t.Helper()
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if run.Status != "waiting_approval" || state.Approval == nil {
		t.Fatalf("shortplay asset must wait for approval: status=%s approval=%v receipt=%s",
			run.Status, state.Approval, shortplayToolReceipt(state, callID))
	}
	return state.Approval
}

func TestCloudAgentShortplayAssetCreateWaitsForApproval(t *testing.T) {
	s, db, run, state, _ := shortplayRunFixture(t, "create", `,"kind":"character","name":"沈青禾"`)
	before := canvasPayload(t, db, run.CanvasID)
	approval := mustWaitForShortplayApproval(t, s, run, state, "call-create")
	if !strings.Contains(approval.Preview.Title, "新建短剧资产") {
		t.Fatalf("approval preview title = %q", approval.Preview.Title)
	}
	if after := canvasPayload(t, db, run.CanvasID); after != before {
		t.Fatal("canvas changed before approval")
	}
	assets, jobs, cards := shortplayRunRows(t, db, run.CanvasID)
	if assets != 0 || jobs != 0 || cards != 0 {
		t.Fatalf("approval preview wrote rows: assets=%d jobs=%d cards=%d", assets, jobs, cards)
	}
}

func TestCloudAgentShortplayAssetRejectLeavesNoWrites(t *testing.T) {
	s, db, run, state, _ := shortplayRunFixture(t, "create", `,"kind":"character","name":"沈青禾"`)
	before := canvasPayload(t, db, run.CanvasID)
	tasksBefore := shortplayTaskCount(t, db)
	approval := mustWaitForShortplayApproval(t, s, run, state, "call-create")
	if err := s.DecideCloudAgentApproval("user", run.ID, approval.ID, "reject", ""); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if run.Status != "rejected" {
		t.Fatalf("rejected run status = %s", run.Status)
	}
	if state.Approval != nil {
		t.Fatalf("rejected run kept approval: %#v", state.Approval)
	}
	// 拒绝后再推进同一个待执行调用也不能产生写入。
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatalf("rejected run advanced: %v", err)
	}
	if after := canvasPayload(t, db, run.CanvasID); after != before {
		t.Fatal("rejected approval changed the canvas")
	}
	assets, jobs, cards := shortplayRunRows(t, db, run.CanvasID)
	if assets != 0 || jobs != 0 || cards != 0 || shortplayTaskCount(t, db) != tasksBefore {
		t.Fatalf("rejected approval wrote rows: assets=%d jobs=%d cards=%d", assets, jobs, cards)
	}
}

func TestCloudAgentShortplayAssetApproveWritesOnceOnRecovery(t *testing.T) {
	s, db, run, state, _ := shortplayRunFixture(t, "create", `,"kind":"character","name":"沈青禾","voice":"青年女声"`)
	tasksBefore := shortplayTaskCount(t, db)
	approval := mustWaitForShortplayApproval(t, s, run, state, "call-create")
	if err := s.DecideCloudAgentApproval("user", run.ID, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	// 同一决定重放必须幂等：不改变决定、不重复执行。
	if err := s.DecideCloudAgentApproval("user", run.ID, approval.ID, "approve", ""); err != nil {
		t.Fatalf("approve replay = %v", err)
	}
	// 模拟 worker 重启：重新读取已批准的检查点再执行。
	run, state = reloadPiRun(t, s, run.ID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatalf("approved execution failed: %v", err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if run.Status == "waiting_approval" || (state.Approval != nil && state.Approval.Decision == "") {
		t.Fatalf("approved call stayed pending: status=%s approval=%v", run.Status, state.Approval)
	}
	assets, jobs, cards := shortplayRunRows(t, db, run.CanvasID)
	if assets != 1 || jobs != 0 || cards != 1 || shortplayTaskCount(t, db) != tasksBefore {
		t.Fatalf("approved create rows: assets=%d jobs=%d cards=%d", assets, jobs, cards)
	}
	// 重复派发同一已执行调用：检查点已推进，重复派发按无效调用状态终止，
	// 不会因为重放再写一次资产或第二张卡。
	if replayErr := s.executeCloudAgentToolCall(run, state); replayErr != nil {
		t.Logf("duplicate dispatch rejected: %v", replayErr)
	}
	replayed, _ := reloadPiRun(t, s, run.ID)
	if replayed.Status == "waiting_approval" {
		t.Fatal("replay re-opened an approval")
	}
	if assets, jobs, cards = shortplayRunRows(t, db, run.CanvasID); assets != 1 || jobs != 0 || cards != 1 {
		t.Fatalf("replay duplicated rows: assets=%d jobs=%d cards=%d", assets, jobs, cards)
	}
}

func TestCloudAgentShortplayAssetRefusesForeignAndFreeCanvas(t *testing.T) {
	cases := []struct {
		name     string
		canvasID string
		owner    string
		project  string
	}{
		{name: "cross-account", canvasID: "foreign-canvas", owner: "other", project: "p-other"},
		{name: "free-canvas", canvasID: "free-canvas", owner: "user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, db, run, state, _ := shortplayRunFixture(t, "create", `,"kind":"character","name":"沈青禾"`)
			canvas := model.CanvasProject{ID: tc.canvasID, UserID: tc.owner, ProjectID: tc.project, PayloadJSON: `{"nodes":[]}`}
			if err := db.Create(&canvas).Error; err != nil {
				t.Fatal(err)
			}
			state.Request.CanvasID = tc.canvasID
			if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				return cloudAgentSave(current, state)
			}); err != nil {
				t.Fatal(err)
			}
			run, state = reloadPiRun(t, s, run.ID)
			if err := s.executeCloudAgentToolCall(run, state); err != nil {
				t.Fatalf("refusal must be a run decision, got infra error: %v", err)
			}
			run, state = reloadPiRun(t, s, run.ID)
			if state.Approval != nil || run.Status == "waiting_approval" {
				t.Fatalf("refused canvas opened an approval: status=%s", run.Status)
			}
			assets, jobs, cards := shortplayRunRows(t, db, tc.canvasID)
			if assets != 0 || jobs != 0 || cards != 0 {
				t.Fatalf("refused canvas wrote rows: assets=%d jobs=%d cards=%d", assets, jobs, cards)
			}
		})
	}
}

func TestCloudAgentShortplayAssetAudioRecoveryQueuesOneJob(t *testing.T) {
	s, db, run, state, _ := shortplayRunFixture(t, "audio", "")
	seedVoicedAsset(t, s, "sp-run", 1, shortplay.AssetCharacter, "沈青禾", "青年女声", "")
	tasksBefore := shortplayTaskCount(t, db)
	approval := mustWaitForShortplayApproval(t, s, run, state, "call-audio")
	if err := s.DecideCloudAgentApproval("user", run.ID, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	// 重启后执行一次：只入队一个 job，不提交付费任务。
	run, state = reloadPiRun(t, s, run.ID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatalf("approved audio failed: %v", err)
	}
	var jobs int64
	if err := db.Model(&model.ShortplayJob{}).Where("kind = ?", shortplay.JobAssetAudio).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("audio jobs = %d, %v", jobs, err)
	}
	if shortplayTaskCount(t, db) != tasksBefore {
		t.Fatal("audio queue提交了付费任务")
	}
	// 重复派发同一已执行调用：job 去重键让恢复/重放不产生第二个音频任务或预留。
	if replayErr := s.executeCloudAgentToolCall(run, state); replayErr != nil {
		t.Logf("duplicate dispatch rejected: %v", replayErr)
	}
	jobs = 0
	if err := db.Model(&model.ShortplayJob{}).Where("kind = ?", shortplay.JobAssetAudio).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("audio replay jobs = %d, %v", jobs, err)
	}
	if shortplayTaskCount(t, db) != tasksBefore {
		t.Fatal("audio replay提交了付费任务")
	}
}

func TestCloudAgentShortplayAssetPlaceReplayKeepsOneCard(t *testing.T) {
	s, db, run, state, _ := shortplayRunFixture(t, "place", `,"assetIds":[1]`)
	seedAsset(t, s, "sp-run", 1, shortplay.AssetCharacter, "沈青禾", "")
	approval := mustWaitForShortplayApproval(t, s, run, state, "call-place")
	if err := s.DecideCloudAgentApproval("user", run.ID, approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatalf("approved place failed: %v", err)
	}
	if _, _, cards := shortplayRunRows(t, db, run.CanvasID); cards != 1 {
		t.Fatalf("place cards = %d", cards)
	}
	// 投送是幂等刷新：重复派发同一调用不叠第二张卡，也不重复绑定集资产。
	if replayErr := s.executeCloudAgentToolCall(run, state); replayErr != nil {
		t.Logf("duplicate dispatch rejected: %v", replayErr)
	}
	if _, _, cards := shortplayRunRows(t, db, run.CanvasID); cards != 1 {
		t.Fatalf("place replay cards = %d", cards)
	}
	episode, err := s.repo.ShortplayEpisodeByID("sp-run", "sp-run-ep1")
	if err != nil {
		t.Fatal(err)
	}
	if ids := shortplayIntList(episode.AssetIDsJSON); len(ids) != 1 {
		t.Fatalf("episode asset ids = %v", ids)
	}
}
