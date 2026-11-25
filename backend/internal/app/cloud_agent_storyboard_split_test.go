package app

import (
	"encoding/json"
	"strings"
	"testing"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentStoryboardWritesConfirmedRowsWithoutSplitTask(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	doc := mustStoryboardDocument(t, s)
	before := splitCanvasRows(t, s)
	args := map[string]any{
		"action": "write_rows", "nodeId": "board", "snapshotHash": cloudAgentCanvasHash(doc), "operationId": "confirmed-plan-01",
		"updates": []map[string]any{{"rowId": "a", "patch": map[string]any{"durationSeconds": 10, "plotDescription": "确认后对视"}}},
		"newRows": []map[string]any{{"durationSeconds": 10, "plotDescription": "确认后离场"}},
	}
	call := cloudAgentStoryboardCall(t, "storyboard", "confirmed-rows", args)
	if err := validateCloudAgentToolArguments(workflowTool(t, "storyboard"), call.Function.Arguments); err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 0 || !strings.Contains(cloudAgentToolMessageForTest(t, state, call.ID), `"applied":true`) {
		t.Fatal("expected a write receipt without an async split submission")
	}
	rows := splitCanvasRows(t, s)
	if len(rows) != 4 || rows[0]["id"] != "a" || rows[0]["plotDescription"] != "确认后对视" || rows[0]["imageNodeId"] != before[0]["imageNodeId"] || rows[2]["locked"] != true || rows[3]["plotDescription"] != "确认后离场" {
		t.Fatalf("unexpected writeback: %#v", rows)
	}
	policy, _ := s.RuntimePolicy()
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", req.CanvasID, call, policy); err != nil {
		t.Fatalf("same operation should replay before snapshot validation: %v", err)
	}
	args["operationId"] = "confirmed-plan-02"
	stale := cloudAgentStoryboardCall(t, "storyboard", "stale-rows", args)
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", req.CanvasID, stale, policy); err == nil {
		t.Fatal("new operation accepted a stale snapshot")
	}
	snapshot, _ := json.Marshal(splitCanvasRows(t, s))
	args["snapshotHash"] = cloudAgentCanvasHash(mustStoryboardDocument(t, s))
	args["updates"] = []map[string]any{{"rowId": "c", "patch": map[string]any{"dialogue": "locked"}}}
	locked := cloudAgentStoryboardCall(t, "storyboard", "locked-rows", args)
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", req.CanvasID, locked, policy); err == nil {
		t.Fatal("locked update accepted")
	}
	after, _ := json.Marshal(splitCanvasRows(t, s))
	if string(snapshot) != string(after) {
		t.Fatal("replay or rejected call changed rows")
	}
}

func mustStoryboardDocument(t *testing.T, s *Service) map[string]any {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "split-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestCloudAgentStoryboardAsyncSubmitContinuation(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks = 2
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("split-call", "storyboard", `{"action":"resplit","nodeId":"board","rowIds":["b"],"request":"延长对视","channelId":"channel","channelModelKey":"text-test"}`)
	finish := piAgentTestCall("finish-call", "finish_run", `{"summary":"已提交拆镜，全部任务终态后继续。","continuation":{"when":"all_generations_terminal","instruction":"检查拆镜结果并写回，失败项只报告原因。"}}`)
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call, finish})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 1 || state.CallIndex != 1 {
		t.Fatalf("split must return immediately: %+v %d", state.PendingGenerations, state.CallIndex)
	}
	taskID := state.PendingGenerations[0].TaskID
	if state.PendingGenerations[0].BatchID == "" {
		t.Fatal("split receipt did not register a generation batch")
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil || batch.ExpectedTasks != 1 || batch.Status != model.AgentGenerationBatchCollecting {
		t.Fatalf("split batch barrier is wrong: %+v %v", batch, err)
	}
	task, _ := s.repo.TaskForUser("user", taskID)
	if task.AgentRunID != run.ID || !cloudAgentContainsString(state.TaskIDs, taskID) {
		t.Fatal("split bypassed run admission")
	}
	before, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range before.PendingInterjections {
		if item.Source == cloudAgentGenerationSource {
			t.Fatal("completion notice before writeback")
		}
	}
	if err := agentPiExecuteCallForTest(t, s, run.ID, finish.ID); err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", run.ID)
	if err != nil || run.Status != "completed" {
		t.Fatalf("finish_run must complete the source run: %+v %v", run, err)
	}
	batch, err = s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil || batch.Status != model.AgentGenerationBatchArmed {
		t.Fatalf("continuation was not armed: %+v %v", batch, err)
	}
	if queued, err := s.QueueReadyGenerationContinuations(10); err != nil || queued != 0 {
		t.Fatalf("armed batch must not queue before every task is terminal: %d %v", queued, err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"rows":[{"plotDescription":"新的对视","durationSeconds":5}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if settled, err := s.SettlePendingCloudAgentGenerations(); err != nil || settled != 1 {
		t.Fatalf("settle=%d %v", settled, err)
	}
	batch, err = s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil || batch.Status != model.AgentGenerationBatchReady {
		t.Fatalf("last terminal task must reach the barrier: %+v %v", batch, err)
	}
	if !strings.Contains(batch.ResultsJSON, `"splitResult"`) || !strings.Contains(batch.ResultsJSON, `"rowId":"b"`) {
		t.Fatalf("batch results lost the split mapping: %s", batch.ResultsJSON)
	}
	if queued, err := s.QueueReadyGenerationContinuations(10); err != nil || queued != 1 {
		t.Fatalf("ready batch must queue exactly one continuation: %d %v", queued, err)
	}
	if queued, err := s.QueueReadyGenerationContinuations(10); err != nil || queued != 0 {
		t.Fatalf("repeated scan duplicated the continuation: %d %v", queued, err)
	}
	var internal []model.AgentQueuedMessage
	if err := db.Where("user_id = ? AND kind = ?", "user", model.AgentQueuedMessageGenerationContinuation).Find(&internal).Error; err != nil {
		t.Fatal(err)
	}
	if len(internal) != 1 || internal[0].IdempotencyKey != "generation-batch:"+batch.ID || !strings.Contains(internal[0].Prompt, "系统生成结果") {
		t.Fatalf("internal continuation message is wrong: %+v", internal)
	}
	if visible, err := s.repo.AgentQueuedMessages("user", run.ConversationID); err != nil || len(visible) != 0 {
		t.Fatalf("internal continuation leaked into the user queue: %+v %v", visible, err)
	}
	task, _ = s.repo.TaskForUser("user", taskID)
	if facts := cloudAgentTaskDiagnostic(s.repo, task); facts["splitResult"] == nil {
		t.Fatal("task_get lost mapping")
	}
	rows := splitCanvasRows(t, s)
	if rows[1]["id"] != "b" || rows[1]["mediaReview"] != "pending" || rows[2]["locked"] != true {
		t.Fatalf("wrong resplit rows: %+v", rows)
	}
}

func TestCloudAgentStoryboardLockedWrites(t *testing.T) {
	s, _ := storyboardSplitTestService(t)
	for _, raw := range []string{
		`{"action":"update","nodeId":"board","rowIds":["a","c"],"patch":{"dialogue":"change"}}`,
		`{"action":"update","nodeId":"board","updates":[{"rowId":"c","patch":{"dialogue":"change"}}]}`,
		`{"action":"set_assets","nodeId":"board","rowId":"c","assets":[]}`,
		`{"action":"set_references","nodeId":"board","references":[]}`,
		`{"action":"bind_output","nodeId":"board","rowId":"c","outputMode":"image","outputNodeId":"img-a"}`,
	} {
		call := piAgentTestCall("locked", "storyboard", raw)
		fields, action, err := storyboardArguments(call)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepareStoryboardWriteCall(s.repo, "user", "split-canvas", call, fields, action); err == nil || !strings.Contains(err.Error(), "已锁定") {
			t.Fatalf("locked %s accepted: %v", action, err)
		}
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "split-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	call := piAgentTestCall("unlock", "storyboard", `{"action":"unlock","nodeId":"board","rowIds":["c"]}`)
	fields, action, _ := storyboardArguments(call)
	fields["snapshotHash"] = cloudAgentCanvasHash(doc)
	internal, err := prepareStoryboardWriteCall(s.repo, "user", "split-canvas", call, fields, action)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", "split-canvas", internal, policy); err != nil {
		t.Fatal(err)
	}
	read := piAgentTestCall("read", "storyboard", `{"nodeId":"board"}`)
	result, err := cloudAgentReadStoryboard(s.repo, "user", "split-canvas", read)
	if err != nil {
		t.Fatal(err)
	}
	if markdown := cloudAgentStoryboardMarkdown(result.(map[string]any)); !strings.Contains(markdown, "| b |") || !strings.Contains(markdown, "待复核") {
		t.Fatalf("missing readable table: %s", markdown)
	}
}
func TestCloudAgentStoryboardApprovalAndBudget(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "approval"}[approved], func(t *testing.T) {
			s, db := storyboardSplitTestService(t)
			req := agentTestRequest()
			req.CanvasID = "split-canvas"
			req.PermissionMode = "auto"
			if approved {
				req.PermissionMode = "request_approval"
			}
			root, err := s.CreateCloudAgentRun("user", req, "")
			if err != nil {
				t.Fatal(err)
			}
			call := piAgentTestCall("split-call", "storyboard", `{"action":"split","nodeId":"board","channelId":"channel","channelModelKey":"text-test"}`)
			run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
			if !approved {
				orders, err := s.repo.BillingOrdersByTaskIDs("user", state.TaskIDs)
				if err != nil {
					t.Fatal(err)
				}
				var spent int64
				for _, order := range orders {
					spent += order.AmountMicrocredits
				}
				state.Request.Budget.MaxCredits = float64(spent+50) / float64(CreditScale)
				if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
					return cloudAgentSave(current, &state)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
				t.Fatal(err)
			}
			run, state = agentInterjectionState(t, s, run.ID)
			var count int64
			db.Model(&model.Task{}).Where("operation = ?", "storyboard").Count(&count)
			if count != 0 {
				t.Fatal("task billed before approval or over budget")
			}
			if !approved {
				if len(state.PendingGenerations) != 0 || !strings.Contains(cloudAgentToolMessageForTest(t, state, call.ID), "积分上限") {
					t.Fatal("budget refusal not returned to model")
				}
				return
			}
			if run.Status != "waiting_approval" || state.Approval == nil || state.Approval.QuoteHash == "" {
				t.Fatalf("missing approval: %s %+v", run.Status, state.Approval)
			}
			if err := s.DecideCloudAgentApproval("user", run.ID, state.Approval.ID, "approve", ""); err != nil {
				t.Fatal(err)
			}
			if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
				t.Fatal(err)
			}
			_, state = agentInterjectionState(t, s, run.ID)
			if len(state.PendingGenerations) != 1 {
				t.Fatal("approved split not submitted")
			}
		})
	}
}
