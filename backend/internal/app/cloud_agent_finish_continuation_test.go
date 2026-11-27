package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentFinishRunContinuationArmsBatch(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	finish := `{"summary":"已提交视频生成。","continuation":{"when":"all_generations_terminal","instruction":"检查本批结果并绑定分镜，失败项只报告原因"}}`
	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-continuation", "finish_run", finish)
	if run.Status != "completed" {
		t.Fatalf("source run did not finish: %s %s", run.Status, run.FailureMessage)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchArmed || batch.TriggerWhen != model.AgentGenerationContinuationAllTerminal || !strings.Contains(batch.Instruction, "检查本批") {
		t.Fatalf("finish_run did not arm the batch: %+v", batch)
	}
	if batch.RequestJSON == "" || !strings.Contains(batch.RequestJSON, req.ChannelID) {
		t.Fatalf("batch did not freeze the source request: %s", batch.RequestJSON)
	}
	if receipt := cloudAgentToolMessageForTest(t, state, "finish-continuation"); !strings.Contains(receipt, `"terminalStatus":"completed"`) {
		t.Fatalf("finish receipt missing: %s", receipt)
	}
}

func TestCloudAgentFinishRunContinuationRequiresSubmittedBatch(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	finish := `{"summary":"稍后继续。","continuation":{"when":"all_generations_terminal","instruction":"检查结果"}}`
	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-bad-continuation", "finish_run", finish)
	if run.Status == "completed" {
		t.Fatal("finish_run accepted continuation without a submitted generation batch")
	}
	message := cloudAgentToolMessageForTest(t, state, "finish-bad-continuation")
	if !strings.Contains(message, "没有成功提交") || !strings.Contains(message, `"field":"continuation"`) {
		t.Fatalf("continuation rejection was not actionable: %s", message)
	}
}

func TestCloudAgentFinishRunWithoutContinuationDetachesBatch(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	run, _ = agentFinishRunForTest(t, s, db, root.ID, "finish-detached", "已提交生成，本轮结束。")
	if run.Status != "completed" {
		t.Fatalf("plain finish did not complete: %s", run.Status)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchDetached {
		t.Fatalf("plain finish must detach the batch: %+v", batch)
	}
}

func TestCloudAgentFinishRunAfterLastTerminalTaskArmsReady(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	if err := db.Model(&model.Task{}).Where("id = ?", state.PendingGenerations[0].TaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游拒绝"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePendingCloudAgentGenerations(); err != nil {
		t.Fatal(err)
	}
	finish := `{"summary":"已提交视频生成。","continuation":{"when":"all_generations_terminal","instruction":"检查结果"}}`
	run, _ = agentSettleToolStep(t, s, db, root.ID, "finish-ready", "finish_run", finish)
	if run.Status != "completed" {
		t.Fatalf("source run did not finish: %s", run.Status)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchReady {
		t.Fatalf("arming after the only task was terminal must be ready: %+v", batch)
	}
}
