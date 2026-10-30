package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 异步生成：generate_media 提交后立即返回回执，同一批里后面的调用照常执行，
// 生成结束时以一条系统通知告诉 Agent。
func TestCloudAgentGenerationReturnsImmediatelyAndNotifiesOnCompletion(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 2, 24
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	read := piAgentTestCall("read-after", "canvas_get_state", `{}`)
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args), read})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 1 || state.CallIndex != 1 {
		t.Fatalf("generate_media must return at once and move to the next call: pending=%d callIndex=%d", len(state.PendingGenerations), state.CallIndex)
	}
	if !strings.Contains(cloudAgentToolMessageForTest(t, state, "media-call"), `"status":"submitted"`) {
		t.Fatal("generate_media receipt must say the generation was submitted")
	}
	if err := agentPiExecuteCallForTest(t, s, run.ID, "read-after"); err != nil {
		t.Fatalf("the next call in the batch must run while the generation is pending: %v", err)
	}
	taskID := state.PendingGenerations[0].TaskID
	if err := db.Create(&model.Resource{ID: "async-output", UserID: "user", Kind: "video", Status: "ready", MimeType: "video/mp4", Width: 720, Height: 1280}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"mode":"video","video":{"storageKey":"resource:async-output"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	// Worker 读取快照时回写：通知随快照的待送插话一起交给模型。
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, item := range snapshot.PendingInterjections {
		if strings.HasPrefix(item.Text, "[系统通知] 生成结果") && strings.Contains(item.Text, "已生成并写入画布") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("snapshot must carry exactly one generation notice: %+v", snapshot.PendingInterjections)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 0 {
		t.Fatal("settled generation is still pending")
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	nodes, _ := creationObjects(doc["nodes"])
	if meta := nodes[args.NodeID]["metadata"].(map[string]any); meta["storageKey"] != "resource:async-output" {
		t.Fatalf("result was not written to the canvas: %+v", meta)
	}
}

// 多个生成在同一步前结束时合并成一条通知。
func TestCloudAgentGenerationNoticesMerge(t *testing.T) {
	state := &cloudAgentRuntime{}
	cloudAgentQueueGenerationNotice(state, []cloudAgentGenerationOutcome{{TaskID: "a", NodeID: "n1", Title: "镜头1", Summary: "已生成并写入画布"}})
	cloudAgentQueueGenerationNotice(state, []cloudAgentGenerationOutcome{{TaskID: "b", NodeID: "n2", Title: "镜头2", Summary: "生成未成功", Error: "上游拒绝"}})
	if len(state.PendingInterjections) != 1 {
		t.Fatalf("notices must merge: %+v", state.PendingInterjections)
	}
	text := state.PendingInterjections[0].Text
	if !strings.Contains(text, "镜头1") || !strings.Contains(text, "镜头2") || !strings.Contains(text, "上游拒绝") {
		t.Fatalf("merged notice lost an outcome: %s", text)
	}
	if blockers := cloudAgentCompletionBlockersForTest(state); len(blockers) != 0 {
		t.Fatalf("generation notices must not block finishing: %+v", blockers)
	}
}

// wait_generations：有进行中的生成时挂起并让出 Worker，最后一个结束时带结果恢复；没有时立即返回。
func TestCloudAgentWaitGenerationsSuspendsAndResumes(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 2, 24
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	wait := piAgentTestCall("wait-1", "wait_generations", `{}`)
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args), wait})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, run.ID)
	receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, "wait-1")
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Pending || !receipt.Suspended {
		t.Fatalf("wait_generations must suspend while a generation is pending: %+v", receipt)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	if run.WaitKind != cloudAgentGenerationsWaitKind || run.WaitID != "wait-1" {
		t.Fatalf("run must wait on generations: kind=%s id=%s", run.WaitKind, run.WaitID)
	}
	if protected, err := s.repo.WorkerRecoveryProtectedWait(*run); err != nil || !protected {
		t.Fatalf("a generations wait must not spend worker recovery budget: %v %v", protected, err)
	}
	taskID := state.PendingGenerations[0].TaskID
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游拒绝"}).Error; err != nil {
		t.Fatal(err)
	}
	if settled, err := s.SettlePendingCloudAgentGenerations(); err != nil || settled != 1 {
		t.Fatalf("settle = %d, %v", settled, err)
	}
	run, state = agentInterjectionState(t, s, run.ID)
	if run.WaitKind != "" || state.CallIndex != 2 {
		t.Fatalf("last settled generation must resume the wait: kind=%s callIndex=%d", run.WaitKind, state.CallIndex)
	}
	message := cloudAgentToolMessageForTest(t, state, "wait-1")
	if !strings.Contains(message, `"pending":0`) || !strings.Contains(message, "上游拒绝") {
		t.Fatalf("wait_generations result must carry the outcome: %s", message)
	}
	for _, item := range state.PendingInterjections {
		if item.Source == cloudAgentGenerationSource {
			t.Fatal("results already in the wait receipt must not be sent again as a notice")
		}
	}

}

func TestCloudAgentWaitGenerationsReturnsAtOnceWithoutPending(t *testing.T) {
	current := &model.CloudAgentExecution{}
	result, suspended := cloudAgentWaitGenerations(current, &cloudAgentRuntime{}, piAgentTestCall("wait-0", "wait_generations", `{}`))
	if suspended || current.WaitKind != "" || result.(map[string]any)["pending"] != 0 {
		t.Fatalf("no pending generation must return at once: %+v suspended=%v", result, suspended)
	}
}

func cloudAgentToolMessageForTest(t *testing.T, state cloudAgentRuntime, callID string) string {
	t.Helper()
	for _, message := range state.Canonical.Messages {
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == callID {
			return stringField(message, "content")
		}
	}
	t.Fatalf("no tool result for %s", callID)
	return ""
}

func cloudAgentCompletionBlockersForTest(state *cloudAgentRuntime) []cloudAgentCompletionBlocker {
	return cloudAgentCompletionBlockers(state)
}
