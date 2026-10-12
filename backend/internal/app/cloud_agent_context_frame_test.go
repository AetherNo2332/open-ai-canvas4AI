package app

import (
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// 本文件是从 dev 的 cloud_agent_context_frame_test.go 恢复过来的**可恢复子集**。
//
// 合并把云端 Agent 主链路整体取上游，上游用自己的 cloudAgentContextFrame /
// cloudAgentModelContext 表达"任务事实帧"，没有我们对等的
// cloudAgentTaskFactFrame / attachCloudAgentTaskFacts / cloudAgentRuntimeContext /
// cloudAgentRuntimeMessage 那一套类型与函数。因此以下用例依赖的符号在合并树里不存在，
// 未随本文件恢复（详见报告 UPSTREAM-PORT-GAP-CLOSURE.md 的"需要人工确认"）：
//
//	TestCloudAgentTaskFactFrameReportsTaskAndBillingFacts
//	TestCloudAgentTaskFactFrameRedactsUntrustedErrorsAndMissingTasks
//	TestCloudAgentTaskFactFrameReportsOlderTasksOutsideTheWindow
//	TestCloudAgentTaskFactFrameUnavailableWithoutTasks
//	TestAttachCloudAgentTaskFactsAppendsOneRuntimeMessage
//	TestStripCloudAgentRuntimeContextKeepsOneShotCorrections
//
// 其余两条测的是合并树里确实存在的语义（提交结果口径、完整轮次尾部），故恢复。

// 提交结果要区分"送没送到上游"：有上游任务 ID 才是 accepted；已经跑完的任务必然提交过，
// 只是上游没回传 ID，报 completed；还在排队/执行的按本地状态报 queued/submitted。
func TestCloudAgentTaskSubmissionOutcome(t *testing.T) {
	tests := []struct {
		task   *model.Task
		expect string
	}{
		{nil, "unavailable"},
		{&model.Task{Status: model.TaskStatusQueued}, "queued"},
		{&model.Task{Status: model.TaskStatusRunning}, "submitted"},
		{&model.Task{Status: model.TaskStatusSucceeded}, "completed"},
		{&model.Task{Status: model.TaskStatusFailed}, "completed"},
		{&model.Task{Status: model.TaskStatusCancelled}, "completed"},
		{&model.Task{Status: "unknown-status"}, "unknown"},
		{&model.Task{Status: model.TaskStatusQueued, ProviderRequestID: "ark-1"}, "accepted"},
		{&model.Task{Status: model.TaskStatusSucceeded, ProviderRequestID: "ark-1"}, "accepted"},
	}
	for _, tc := range tests {
		if got := cloudAgentTaskSubmissionOutcome(tc.task); got != tc.expect {
			t.Fatalf("outcome for %+v = %q, want %q", tc.task, got, tc.expect)
		}
	}
}

func TestCloudAgentCompleteTurnTailKeepsWholePairsOnly(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "第一问"},
		{"role": "assistant", "content": "第一答"},
		{"role": "user", "content": "第二问"},
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call-1"}}},
		{"role": "tool", "tool_call_id": "call-1", "content": "大体积正文"},
		{"role": "user", "content": "第三问"},
		{"role": "assistant", "content": "第三答"},
	}
	recent := cloudAgentCompleteTurnTail(messages, 2)
	if len(recent) != 4 {
		t.Fatalf("recent tail = %+v", recent)
	}
	// 带工具调用的 assistant 与 tool 回执都不进尾部：不能制造"有调用没结果"的半截轮次。
	for _, message := range recent {
		if message.Role == "tool" || strings.Contains(message.Content, "大体积正文") {
			t.Fatalf("half tool turn kept: %+v", recent)
		}
	}
	if recent[0].Content != "第一问" || recent[3].Content != "第三答" {
		t.Fatalf("wrong turns retained: %+v", recent)
	}
	// 检查点正文不是用户原话，不能当成一轮。
	checkpointed := []map[string]any{
		{"role": "user", "content": "<agent-context-checkpoint>{\"version\":1}</agent-context-checkpoint>"},
		{"role": "assistant", "content": "已收到检查点"},
		{"role": "user", "content": "真实提问"},
		{"role": "assistant", "content": "真实回答"},
	}
	recent = cloudAgentCompleteTurnTail(checkpointed, 1)
	if len(recent) != 2 || recent[0].Content != "真实提问" {
		t.Fatalf("checkpoint counted as a turn: %+v", recent)
	}
}
