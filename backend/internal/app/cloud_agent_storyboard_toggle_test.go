package app

import (
	"strings"
	"testing"
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
	// 第一道闸是工具披露预检（permission_violation）；执行前的显式拒绝
	// （"分镜表工具已在 Agent 设置中关闭"）是绕过披露路径的纵深防御。
	if !strings.Contains(receipt, "未获本轮权限或能力授权") && !strings.Contains(receipt, "分镜表工具已在 Agent 设置中关闭") {
		t.Fatalf("disabled storyboard execution not rejected: %s", receipt)
	}
	if strings.Contains(receipt, `"status":"submitted"`) || strings.Contains(receipt, "markdown") {
		t.Fatalf("disabled storyboard must not execute: %s", receipt)
	}
}
