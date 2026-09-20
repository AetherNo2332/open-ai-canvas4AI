package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// cloudAgentFactFrameFixture 造一个"有编排任务 + 两条媒体任务 + 一条账务订单"的运行现场。
// 事实帧的全部输入都必须来自数据库现读，因此这里直接写库而不是塞状态。
func cloudAgentFactFrameFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []any{
		&model.Task{
			ID: "task-video", UserID: "user", Type: "canvas_video", Status: model.TaskStatusRunning,
			Operation: "text_to_video", ProviderRequestID: "ark-task-9", PollStage: "polling",
			InputJSON: `{"metadata":{"nodeId":"video-node"}}`,
		},
		&model.Task{
			ID: "task-image", UserID: "user", Type: "canvas_image", Status: model.TaskStatusFailed,
			Operation: "text_to_image", Error: "上游返回 400：prompt 过长",
			InputJSON: `{"metadata":{"nodeId":"image-node"}}`,
		},
		&model.Task{
			ID: "task-orchestration", UserID: "user", Type: "canvas_text", Status: model.TaskStatusSucceeded,
			Operation: cloudAgentStepOperation,
		},
		&model.BillingOrder{
			ID: "order-video", UserID: "user", IdempotencyKey: "order-video", TaskID: "task-video",
			Status: model.BillingStatusReserved, AmountMicrocredits: 500,
			ReservedAmountMicrocredits: 400, ChargeLimitMicrocredits: 500,
		},
	} {
		if err = db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	state := &cloudAgentRuntime{TaskIDs: []string{run.ID, "task-video", "task-image", "task-orchestration"}}
	return s, db, run, state
}

func TestCloudAgentTaskFactFrameReportsTaskAndBillingFacts(t *testing.T) {
	s, _, run, state := cloudAgentFactFrameFixture(t)
	frames, older, ok := s.cloudAgentTaskFactFrame(run, state)
	if !ok || older != 0 {
		t.Fatalf("fact frame unavailable: ok=%v older=%d", ok, older)
	}
	byID := map[string]cloudAgentTaskFactFrame{}
	for _, frame := range frames {
		byID[frame.TaskID] = frame
	}
	// 本轮自己的编排任务不是生成结果，不得出现在事实帧里。
	if _, present := byID[run.ID]; present {
		t.Fatal("orchestration task leaked into the fact frame")
	}
	if _, present := byID["task-orchestration"]; present {
		t.Fatal("cloud_agent_step task leaked into the fact frame")
	}
	video := byID["task-video"]
	if video.TaskStatus != string(model.TaskStatusRunning) || video.TaskType != "canvas_video" || video.Operation != "text_to_video" {
		t.Fatalf("video facts = %+v", video)
	}
	if video.ProviderTaskID != "ark-task-9" || video.SubmissionOutcome != "accepted" || video.Phase != "polling" || video.NodeID != "video-node" {
		t.Fatalf("video submission facts = %+v", video)
	}
	if video.Billing == nil || video.Billing.OrderID != "order-video" || video.Billing.Status != string(model.BillingStatusReserved) {
		t.Fatalf("video billing facts = %+v", video.Billing)
	}
	if video.Billing.AuthorizedMicrocredits != 500 || video.Billing.ReservedMicrocredits != 400 || video.Billing.ChargeLimitMicrocredits != 500 {
		t.Fatalf("video billing amounts = %+v", video.Billing)
	}
	image := byID["task-image"]
	if image.SubmissionOutcome != "completed" || image.ProviderTaskID != "" || image.NodeID != "image-node" {
		t.Fatalf("failed image facts = %+v", image)
	}
	if image.Error != "上游返回 400：prompt 过长" {
		t.Fatalf("failed task error = %q", image.Error)
	}
	if image.Billing != nil {
		t.Fatalf("failed task invented billing facts: %+v", image.Billing)
	}
}

func TestCloudAgentTaskFactFrameRedactsUntrustedErrorsAndMissingTasks(t *testing.T) {
	s, db, run, state := cloudAgentFactFrameFixture(t)
	if err := db.Model(&model.Task{}).Where("id = ?", "task-image").
		Update("error", "POST https://ark.example.com/v1/tasks failed with authorization: Bearer sk-secret").Error; err != nil {
		t.Fatal(err)
	}
	state.TaskIDs = append(state.TaskIDs, "task-missing")
	frames, _, ok := s.cloudAgentTaskFactFrame(run, state)
	if !ok {
		t.Fatal("fact frame unavailable")
	}
	for _, frame := range frames {
		switch frame.TaskID {
		case "task-image":
			if strings.Contains(frame.Error, "ark.example.com") || strings.Contains(frame.Error, "sk-secret") || frame.Error == "" {
				t.Fatalf("upstream detail leaked into the fact frame: %q", frame.Error)
			}
		case "task-missing":
			// 行不在了就如实说"查不到"，不能编造状态。
			if frame.TaskStatus != "unavailable" || frame.SubmissionOutcome != "unavailable" || frame.ProviderTaskID != "" {
				t.Fatalf("missing task facts fabricated: %+v", frame)
			}
		}
	}
}

func TestCloudAgentTaskFactFrameReportsOlderTasksOutsideTheWindow(t *testing.T) {
	s, _, run, state := cloudAgentFactFrameFixture(t)
	state.TaskIDs = []string{}
	for i := 0; i < cloudAgentContextFrameTaskWindow+8; i++ {
		state.TaskIDs = append(state.TaskIDs, "task-absent-"+strconv.Itoa(i))
	}
	frames, older, ok := s.cloudAgentTaskFactFrame(run, state)
	if !ok {
		t.Fatal("fact frame unavailable")
	}
	if older != 8 || len(frames) != cloudAgentContextFrameTaskWindow {
		t.Fatalf("window = %d frames with %d older, want %d/8", len(frames), older, cloudAgentContextFrameTaskWindow)
	}
}

func TestCloudAgentTaskFactFrameUnavailableWithoutTasks(t *testing.T) {
	s, _, run, _ := cloudAgentFactFrameFixture(t)
	if _, _, ok := s.cloudAgentTaskFactFrame(run, &cloudAgentRuntime{}); ok {
		t.Fatal("empty task list produced a fact frame")
	}
	// 只有编排任务（自己的 run）时也不该下发一帧空事实。
	if _, _, ok := s.cloudAgentTaskFactFrame(run, &cloudAgentRuntime{TaskIDs: []string{run.ID}}); ok {
		t.Fatal("orchestration-only task list produced a fact frame")
	}
	if _, _, ok := s.cloudAgentTaskFactFrame(nil, &cloudAgentRuntime{TaskIDs: []string{"task-video"}}); ok {
		t.Fatal("nil run produced a fact frame")
	}
}

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

func TestAttachCloudAgentTaskFactsAppendsOneRuntimeMessage(t *testing.T) {
	s, _, run, state := cloudAgentFactFrameFixture(t)
	canonical := canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": "继续"}}}
	s.attachCloudAgentTaskFacts(run, state, &canonical)
	if len(canonical.Messages) != 2 {
		t.Fatalf("canonical messages = %d", len(canonical.Messages))
	}
	message := canonical.Messages[1]
	if stringField(message, cloudAgentContextSourceKey) != "runtime" {
		t.Fatalf("fact frame is not a runtime message: %+v", message)
	}
	var context cloudAgentRuntimeContext
	content := strings.TrimPrefix(stringField(message, "content"), cloudAgentRuntimeContextMarker)
	if err := json.Unmarshal([]byte(content), &context); err != nil {
		t.Fatal(err)
	}
	if context.Kind != cloudAgentContextTaskFacts || len(context.Tasks) != 2 || context.ObservedAt == "" {
		t.Fatalf("fact frame payload = %+v", context)
	}
	if context.Authority != cloudAgentTaskFactsAuthority {
		t.Fatalf("fact frame authority = %q", context.Authority)
	}
	// 再挂一次不能叠加：attachCloudAgentPlan 会先清掉尾部重拼的运行时消息。
	canonical = cloudAgentCanonicalWithPlan(&cloudAgentRuntime{Canonical: canonical})
	s.attachCloudAgentTaskFacts(run, state, &canonical)
	if len(canonical.Messages) != 2 {
		t.Fatalf("fact frame stacked across steps: %d messages", len(canonical.Messages))
	}
	// 没有任务时不要塞一条空帧。
	empty := canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": "继续"}}}
	s.attachCloudAgentTaskFacts(run, &cloudAgentRuntime{}, &empty)
	if len(empty.Messages) != 1 {
		t.Fatalf("empty fact frame appended: %+v", empty.Messages)
	}
}

func TestStripCloudAgentRuntimeContextKeepsOneShotCorrections(t *testing.T) {
	reattached := cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextPlan, Items: []cloudAgentPlanItem{{ID: "1", Title: "整理画布"}}})
	facts := cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextTaskFacts, Tasks: []cloudAgentTaskFactFrame{{TaskID: "task-video"}}})
	nudge := cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextEmptyOutput})
	correction := cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextInvalidOutput, Detail: "正文超过上限"})
	truncated := cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextTruncatedArguments})

	messages := []map[string]any{
		{"role": "user", "content": "整理画布"},
		{"role": "assistant", "content": "好的"},
		correction,
		reattached,
		facts,
	}
	stripped := stripCloudAgentRuntimeContext(messages)
	if len(stripped) != 3 || stripped[2]["content"] != correction["content"] {
		t.Fatalf("reattached messages not stripped or correction lost: %+v", stripped)
	}
	// 空输出催办 / 参数纠错是一次性指令：必须留在会话里，否则重试时模型看不到纠正要求。
	for _, oneShot := range []map[string]any{nudge, truncated} {
		kept := stripCloudAgentRuntimeContext([]map[string]any{{"role": "user", "content": "继续"}, oneShot})
		if len(kept) != 2 {
			t.Fatalf("one-shot instruction dropped: %+v", kept)
		}
	}
	// 解析不出 kind 的运行时消息保持原样（宁可多留一条，也不要误删指令）。
	broken := map[string]any{"role": "user", "content": cloudAgentRuntimeContextMarker + "{", cloudAgentContextSourceKey: "runtime"}
	if kept := stripCloudAgentRuntimeContext([]map[string]any{broken}); len(kept) != 1 {
		t.Fatalf("unparseable runtime message dropped: %+v", kept)
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
