package app

import (
	"fmt"
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

// 现场：账号活动任务已满时 generate_media 被拒。生成没有提交，结果必须作为可纠正的工具回执交给 Agent
// （等已有生成结束再重发），而不是让整轮失败。
func TestCloudAgentGenerationActiveTaskLimitReturnsToAgent(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 2, 24
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < policy.Task.ActiveTaskLimit; index++ {
		filler := model.Task{ID: fmt.Sprintf("full-%d", index), UserID: "user", Type: "canvas_image", Operation: "text_to_image", Status: model.TaskStatusRunning}
		if err := db.Create(&filler).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatalf("活动任务已满不应作为运行错误抛出: %v", err)
	}
	latest, state := agentInterjectionState(t, s, run.ID)
	if latest.Status == "failed" {
		t.Fatalf("活动任务已满不应让整轮失败: %s", latest.FailureMessage)
	}
	if len(state.PendingGenerations) != 0 {
		t.Fatal("被拒的生成不应登记为待回写")
	}
	message := cloudAgentToolMessageForTest(t, state, "media-call")
	if !strings.Contains(message, "同时排队或运行的任务") || !strings.Contains(message, "wait_for_generations") {
		t.Fatalf("工具回执应说明额度已满并指引等待：%s", message)
	}
}
