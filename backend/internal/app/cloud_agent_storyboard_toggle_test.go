package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 分镜表工具开关：运行请求冻结 StoryboardDisabled，关闭时工具不披露、
// 提示缓存键区分、执行前强制拒绝；读取类工具不受影响。

func TestStoryboardDisabledRemovesToolFromCatalog(t *testing.T) {
	enabled := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SubagentEnabled: true, SubagentWriteEnabled: true}
	if !cloudAgentToolAllowed(enabled, "storyboard") {
		t.Fatal("storyboard tool missing for enabled run")
	}
	disabled := enabled
	disabled.StoryboardDisabled = true
	if cloudAgentToolAllowed(disabled, "storyboard") {
		t.Fatal("disabled run must not disclose storyboard")
	}
	for _, tool := range []string{"canvas_read_content", "canvas_read_batch_table", "canvas_get_state", "canvas_apply_ops"} {
		if !cloudAgentToolAllowed(disabled, tool) {
			t.Fatalf("disabled run must keep %s", tool)
		}
	}
}

func TestStoryboardDisabledChangesPromptCacheKey(t *testing.T) {
	base := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, CanvasID: "c"}
	off := base
	off.StoryboardDisabled = true
	if cloudAgentPromptCacheKey(base) == cloudAgentPromptCacheKey(off) {
		t.Fatal("storyboard toggle must split the prompt cache key")
	}
}

func TestStoryboardDisabledRejectsExecution(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	req.StoryboardDisabled = true
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("board-call", "storyboard", `{"action":"read","nodeId":"board"}`)
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	receipt := cloudAgentToolMessageForTest(t, state, call.ID)
	// 第一道闸是工具披露预检（permission_violation）。
	if !strings.Contains(receipt, "未获本轮权限或能力授权") {
		t.Fatalf("disabled storyboard disclosure gate missing: %s", receipt)
	}
	// 第二道闸：直接调用执行入口（绕过披露预检的内部路径）必须被强制闸拒绝。
	// 先刷新 run/state——第一道闸的拒绝回执已推进 revision；把调用游标归零，
	// 让强制闸像"本批仍待执行"一样走完整记录路径。
	run, state = agentInterjectionState(t, s, run.ID)
	state.CallIndex = 0
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, state = agentInterjectionState(t, s, run.ID)
	if err := s.executeCloudAgentStoryboard(run, &state, call); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	// 同 callId 有两条工具消息（预检拒绝 + 强制闸拒绝）；取最后一条核对强制闸。
	var forced string
	for _, message := range state.Canonical.Messages {
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == call.ID {
			forced = stringField(message, "content")
		}
	}
	if !strings.Contains(forced, "分镜表工具已在 Agent 设置中关闭") {
		t.Fatalf("forced gate missing: %s", forced)
	}
	if strings.Contains(forced, `"status":"submitted"`) || strings.Contains(forced, "markdown") {
		t.Fatalf("disabled storyboard must not execute: %s", forced)
	}
}
