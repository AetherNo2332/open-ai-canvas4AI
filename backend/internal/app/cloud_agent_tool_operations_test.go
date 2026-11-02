package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 工具 op 生命周期不变量：
//   - pending 调用补写失败回执成功，且不重复写；
//   - 已有回执的调用不再补写；
//   - tool_pending 生命周期事件追加进运行事件流。
func TestAgentToolOperationTimeoutReceiptInvariant(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("op-call", "storyboard", `{"action":"read","nodeId":"board"}`)
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if state.PiToolBatchTaskID == "" || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != call.ID {
		t.Fatalf("staged batch wrong: task=%q index=%d calls=%d", state.PiToolBatchTaskID, state.CallIndex, len(state.Calls))
	}
	op := &model.AgentToolOperation{ID: "ato-op-invariant", UserID: run.UserID, RunID: run.ID, TaskID: state.PiToolBatchTaskID, CallID: call.ID, Status: "queued"}
	if !s.recordCloudAgentOperationTimeoutReceipt(op) {
		t.Fatal("timeout receipt was not written for pending call")
	}
	current, state := agentInterjectionState(t, s, run.ID)
	receipt := cloudAgentToolMessageForTest(t, state, call.ID)
	if !strings.Contains(receipt, "工具执行超时") {
		t.Fatalf("timeout receipt missing message: %s", receipt)
	}
	if sound, terminal := current.Status, cloudAgentRunTerminal(current.Status); terminal && sound != "cancelled" && sound != "failed" {
		t.Fatalf("single timeout receipt must not terminate a healthy run: %s", current.Status)
	}
	if s.recordCloudAgentOperationTimeoutReceipt(op) {
		t.Fatal("duplicate timeout receipt must not be written")
	}
}

func TestAgentToolPendingEventAppend(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("op-call", "storyboard", `{"action":"read","nodeId":"board"}`)
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	s.cloudAgentAppendOperationEvent(run.UserID, run.ID, "tool_pending", map[string]any{
		"toolName": call.Function.Name, "callId": call.ID, "operationId": "ato-op-event",
		"queuedAt": "2026-11-02T00:00:00Z", "text": "工具已接单，正在后台执行",
	})
	_, state = agentInterjectionState(t, s, run.ID)
	found := false
	for _, event := range state.Events {
		if event.Type == "tool_pending" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tool_pending event lost")
	}
}
