package app

import (
	"strings"
	"testing"
)

// 分镜表工具关闭时 Agent 能力边界实证：
//   - canvas_apply_ops 建 script 节点（分镜稿容器）照常可用；
//   - storyboard 工具调用被强制闸拒绝。

func TestStoryboardDisabledStillAllowsScriptNodeCreation(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	req.StoryboardDisabled = true
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("board-call", "canvas_apply_ops", `{"snapshotHash":"h","ops":[{"type":"add_node","id":"st1","nodeType":"script","title":"第1场","content":"镜头卡探针"}]}`)
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatalf("script node creation with storyboard disabled errored: %v", err)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	receipt := cloudAgentToolMessageForTest(t, state, call.ID)
	if !strings.Contains(receipt, "snapshotHash") {
		t.Fatalf("script node creation not applied: %s", receipt)
	}
}

func TestStoryboardDisabledStillRejectsStoryboardCall(t *testing.T) {
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
	// 披露预检先用 permission_violation 拒绝（第一道闸）；执行强制闸是第二层。
	if !strings.Contains(receipt, "未获本轮权限或能力授权") && !strings.Contains(receipt, "分镜表工具已在 Agent 设置中关闭") {
		t.Fatalf("storyboard call not rejected: %s", receipt)
	}
}
