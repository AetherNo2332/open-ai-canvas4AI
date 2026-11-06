package app

import (
	"context"
	"encoding/json"
	"testing"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// B1-C: starting drama production through the Agent binds the production to
// the work tree; every later scheduler task is admitted against the tree's
// grant, and a stopped work or a closed grant submits nothing new.
func TestDramaAgentBudgetPropagation(t *testing.T) {
	f := newBusinessFixture(t)
	// 制作设置里固定三类模型选择，制作启动的模型校验直接通过。
	settings := DramaSettings{Models: DramaModels{
		Text:  DramaModelChoice{ChannelID: f.text.channel, ModelKey: f.text.modelKey},
		Image: DramaModelChoice{ChannelID: f.channelID, ModelKey: f.modelKey},
		Video: DramaModelChoice{ChannelID: f.channelID, ModelKey: f.modelKey},
	}}
	if err := f.db.Model(&model.DramaProduction{}).Where("project_id = ?", "drama-1").Update("settings_json", encodeDramaSettings(settings)).Error; err != nil {
		t.Fatal(err)
	}
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "drama", Goal: "推进制作", BudgetMicrocredits: 1_000_000, IdempotencyKey: "drama-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "drama", ActionKey: "d", DefinitionID: "drama.agent",
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	// Agent 启动制作：授权绑定落到制作行。
	startCall := as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"},
		WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
		CallID: "start-run", Name: "drama_set_running", Arguments: json.RawMessage(`{"running":true}`), OperationID: "turn:" + assignment.ExecutorSessionID + ":start:step:1:start-run"}
	if _, err := f.svc.executeBusinessTool(context.Background(), startCall); err != nil {
		t.Fatalf("agent drama start: %v", err)
	}
	production, err := f.svc.repo.DramaProduction("drama-1")
	if err != nil || production.Status != model.DramaStatusRunning || production.AgentWorkID != assignment.WorkItemID || production.AgentGrantID != assignment.BudgetGrantID {
		t.Fatalf("bound production: %+v %v", production, err)
	}
	// 后台任务按绑定授权准入：任务费用挂到工作树的 grant。
	run := &dramaRun{s: f.svc, project: &model.Project{ID: "drama-1", UserID: "user-a"}, production: production,
		settings: DramaSettings{Models: DramaModels{Image: DramaModelChoice{ChannelID: f.channelID, ModelKey: f.modelKey}}}}
	job := &model.DramaJob{ID: "agent-drama-job", ProjectID: "drama-1", Kind: model.DramaJobCastImage, CastID: "cast-1", Status: model.DramaJobQueued}
	run.submitting = job
	task, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女", map[string]any{"config": map[string]any{"size": "1024x1024"}})
	if err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	var charges []model.PlatformBudgetCharge
	if err := f.db.Where("owner_id = ? AND task_id = ?", "user-a", task.ID).Find(&charges).Error; err != nil || len(charges) != 1 {
		t.Fatalf("task charges: %+v %v", charges, err)
	}
	if charges[0].GrantID != assignment.BudgetGrantID || charges[0].RootID != work.BudgetGrantID {
		t.Fatalf("scheduler task not bound to the work grant: %+v", charges[0])
	}
	submissions := platformCount(t, f.db, &model.PlatformSubmission{}, "user_id = ? AND budget_grant_id = ?", "user-a", assignment.BudgetGrantID)
	if submissions == 0 {
		t.Fatal("submission did not carry the work grant")
	}
	// 工作停止：调度器暂停制作，不再提交新收费任务。
	child, err := f.svc.GetWork("user-a", assignment.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StopWork("user-a", WorkControlRequest{WorkItemID: child.ID, ExpectedVersion: child.Version, Reason: "用户停止"}); err != nil {
		t.Fatal(err)
	}
	before := platformCount(t, f.db, &model.PlatformSubmission{}, "user_id = ?", "user-a")
	if err := run.dispatch(); err != nil {
		t.Fatal(err)
	}
	production, err = f.svc.repo.DramaProduction("drama-1")
	if err != nil || production.Status != model.DramaStatusPaused || production.PauseReason == "" {
		t.Fatalf("production after stop: %+v %v", production, err)
	}
	run.submitting = job
	if _, err := run.createTaskWith(run.settings.Models.Image, "image", "text_to_image", "白衣少女再试", map[string]any{"config": map[string]any{"size": "1024x1024"}}); err == nil {
		t.Fatal("stopped work still admits scheduler submissions")
	}
	if after := platformCount(t, f.db, &model.PlatformSubmission{}, "user_id = ?", "user-a"); after != before {
		t.Fatalf("stopped work produced new submissions: %d -> %d", before, after)
	}
	// 授权关闭：同样不再准入（工作已停止已覆盖；这里核对关闭 grant 的直接路径）。
	rootGrant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, work.BudgetGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.BudgetGrants().Close(context.Background(), cp.Caller{UserID: "user-a"}, cp.CloseBudgetRequest{ID: work.BudgetGrantID, ExpectedVersion: rootGrant.Version}); err != nil {
		t.Fatal(err)
	}
	if live, err := f.svc.agentWorkAuthorizationLive("user-a", assignment.BudgetGrantID); err != nil || live {
		t.Fatalf("closed grant still live: %v %v", live, err)
	}
}
