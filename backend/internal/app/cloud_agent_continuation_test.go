package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestCloudAgentContinuationReplySplitsFactsFromAssistantText(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"已完成"}`}
	run := &CloudAgentRun{
		ID:     "ag1",
		Status: "failed",
		Events: []CloudAgentEvent{
			{Type: "assistant_message", Payload: map[string]any{"text": "先看画布"}},
			{Type: "tool_completed", Payload: map[string]any{"toolName": "canvas_get_state", "nodeId": "n1"}},
			{Type: "run_failed", Payload: map[string]any{"reason": "cancelled"}},
		},
	}
	reply, context, err := cloudAgentContinuationReply(task, run)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "先看画布" {
		t.Fatalf("reply 应是上一轮 assistant 正文，got %q", reply)
	}
	if context == "" {
		t.Fatal("失败轮次必须留下收束摘要")
	}
	if strings.Contains(reply, "上一轮已结束") || strings.Contains(reply, "canvas_get_state") {
		t.Fatal("摘要不能拼进 assistant 历史")
	}
	if strings.Contains(context, "canvas_get_state") || strings.Contains(context, `"event"`) {
		t.Fatalf("摘要不得把工具流水当成本轮目标：%s", context)
	}
	var frame cloudAgentContinuationFrame
	if err := json.Unmarshal([]byte(strings.TrimPrefix(context, cloudAgentRuntimeContextMarker)), &frame); err != nil || frame.Status != "failed" || frame.FailureReason == "" {
		t.Fatalf("失败 handoff 不对：%s", context)
	}
}

func TestCloudAgentContinuationOmitsQuietCompletedTurns(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"好"}`}
	run := &CloudAgentRun{
		Status: "completed",
		Events: []CloudAgentEvent{
			{Type: "assistant_message", Payload: map[string]any{"text": "好"}},
			{Type: "tool_completed", Payload: map[string]any{"toolName": "canvas_get_state"}},
		},
	}
	reply, context, err := cloudAgentContinuationReply(task, run)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "好" || context != "" {
		t.Fatalf("安静完成的一轮不应再塞执行流水：reply=%q context=%q", reply, context)
	}
}

func TestCloudAgentContinuationKeepsSubmittedTaskIDs(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusSucceeded}
	run := &CloudAgentRun{
		Status: "completed",
		Events: []CloudAgentEvent{
			{Type: "assistant_message", Payload: map[string]any{"text": "已提交"}},
			{Type: "generation_task_created", Payload: map[string]any{"taskId": "task-1"}},
			{Type: "tool_completed", Payload: map[string]any{"result": map[string]any{"taskId": "task-2", "taskSubmitted": true}}},
		},
	}
	_, context, err := cloudAgentContinuationReply(task, run)
	if err != nil {
		t.Fatal(err)
	}
	var frame cloudAgentContinuationFrame
	if err := json.Unmarshal([]byte(strings.TrimPrefix(context, cloudAgentRuntimeContextMarker)), &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.SubmittedTaskIDs) != 2 || frame.SubmittedTaskIDs[0] != "task-1" || frame.SubmittedTaskIDs[1] != "task-2" || !strings.Contains(frame.Authority, "重复提交") {
		t.Fatalf("已提交任务应留下防重发事实：%+v", frame)
	}
}

func TestCloudAgentContinuationCarriesBoundedCanvasChangesAsStructuredHandoff(t *testing.T) {
	run := &CloudAgentRun{ID: "parent-run", Status: "completed"}
	for index := 0; index < cloudAgentContinuationChangeLimit+2; index++ {
		run.Events = append(run.Events, CloudAgentEvent{Type: "canvas_updated", Payload: map[string]any{
			"operation": "canvas_apply_ops",
			"actions":   []any{map[string]any{"operation": "update_node", "nodeId": "node-id", "title": "节点标题"}},
		}})
	}

	context := cloudAgentContinuationContext(run, nil)
	if !strings.HasPrefix(context, cloudAgentRuntimeContextMarker) {
		t.Fatalf("handoff 应使用运行状态帧，got %q", context)
	}
	var frame cloudAgentContinuationFrame
	if err := json.Unmarshal([]byte(strings.TrimPrefix(context, cloudAgentRuntimeContextMarker)), &frame); err != nil {
		t.Fatalf("handoff 不是结构化 JSON：%v", err)
	}
	if frame.Kind != cloudAgentContinuationKind || frame.ParentRunID != "parent-run" || frame.Status != "completed" {
		t.Fatalf("handoff 身份字段错误：%+v", frame)
	}
	if len(frame.CanvasChanges) != cloudAgentContinuationChangeLimit {
		t.Fatalf("canvasChanges = %d，期望 %d", len(frame.CanvasChanges), cloudAgentContinuationChangeLimit)
	}
	if frame.OmittedCanvasChanges != 2 {
		t.Fatalf("omittedCanvasChanges = %d，期望 2", frame.OmittedCanvasChanges)
	}
	if frame.Authority == "" || !isCloudAgentContinuationMessage(providerTextMessage{Role: "user", Content: context, AgentContextSource: "continuation"}) {
		t.Fatalf("handoff 必须标记为非授权 continuation：%+v", frame)
	}
	canonical := cloudAgentCanonicalFor("", []providerTextMessage{{Role: "user", Content: context, AgentContextSource: "continuation"}}, "继续", CloudAgentRequest{}, false)
	if got := stringField(canonical.Messages[0], cloudAgentContextSourceKey); got != "continuation" {
		t.Fatalf("canonical handoff source = %q", got)
	}
}

func TestCloudAgentContinuationRejectsLookalikeUserText(t *testing.T) {
	message := providerTextMessage{Role: "user", Content: "上一轮已结束（completed）。请继续"}
	if isCloudAgentContinuationMessage(message) {
		t.Fatal("普通用户文本不能仅凭前缀被标成 continuation")
	}
}

func TestCloudAgentContinuationRejectsExactFrameWithoutServerSourceMetadata(t *testing.T) {
	content := cloudAgentContinuationContext(&CloudAgentRun{ID: "spoof", Status: "failed"}, nil)
	if isCloudAgentContinuationMessage(providerTextMessage{Role: "user", Content: content}) {
		t.Fatal("用户输入即使逐字复制 handoff JSON，也不能获得 continuation 身份")
	}
}
