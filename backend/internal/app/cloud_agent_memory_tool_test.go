package app

import (
	"errors"
	"strings"
	"testing"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func memoryUpdateCall(id, arguments string) cloudAgentCall {
	call := cloudAgentCall{ID: id}
	call.Function.Name, call.Function.Arguments = "memory_update", arguments
	return call
}

// 记忆写入不是画布写入：只在记忆开启且非只读时暴露，子代理不暴露；旧经验工具全部退役。
func TestMemoryUpdateToolVisibilityFollowsModeAndPermission(t *testing.T) {
	base := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}
	for _, test := range []struct {
		name    string
		mutate  func(*CloudAgentRequest)
		exposed bool
	}{
		{"auto", func(r *CloudAgentRequest) { r.MemoryMode = model.AgentMemoryDocumentModeAuto }, true},
		{"review", func(r *CloudAgentRequest) { r.MemoryMode = model.AgentMemoryDocumentModeReview }, true},
		{"request approval", func(r *CloudAgentRequest) {
			r.MemoryMode, r.PermissionMode = model.AgentMemoryDocumentModeAuto, "request_approval"
		}, true},
		{"off", func(r *CloudAgentRequest) { r.MemoryMode = model.AgentMemoryDocumentModeOff }, false},
		{"not derived", func(r *CloudAgentRequest) { r.MemoryMode = "" }, false},
		{"read only", func(r *CloudAgentRequest) {
			r.MemoryMode, r.PermissionMode = model.AgentMemoryDocumentModeAuto, "read_only"
		}, false},
		{"subagent", func(r *CloudAgentRequest) {
			r.MemoryMode, r.subagent = model.AgentMemoryDocumentModeAuto, &SubagentRuntime{Depth: 1}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := base
			test.mutate(&req)
			if got := cloudAgentToolAllowed(req, "memory_update"); got != test.exposed {
				t.Fatalf("memory_update exposed=%v, want %v", got, test.exposed)
			}
		})
	}
	if cloudAgentWrite("memory_update") || cloudAgentCanvasWriteTool("memory_update") {
		t.Fatal("memory_update must not enter the canvas write/approval chain")
	}
	if cloudAgentToolCategory("memory_update") != "agent_tools_memory" {
		t.Fatalf("memory_update category = %q", cloudAgentToolCategory("memory_update"))
	}
	supported := CloudAgentSupportedToolNames()
	if !containsToolName(supported, "memory_update") {
		t.Fatalf("platform tool set lacks memory_update: %v", supported)
	}
	for _, retired := range []string{"recall_lessons", "remember_lesson"} {
		if containsToolName(supported, retired) {
			t.Fatalf("retired lesson tool %s is still registered", retired)
		}
	}
}

func memoryRunMessages(t *testing.T, s *Service, runID string) (cloudAgentRuntime, []map[string]any) {
	t.Helper()
	execution, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	var memory []map[string]any
	for _, message := range state.Canonical.Messages {
		if stringField(message, cloudAgentContextSourceKey) == cloudAgentMemoryContextSource {
			memory = append(memory, message)
		}
	}
	return state, memory
}

// 记忆快照建 run 时冻结，作为有来源的数据消息放在当前用户消息之前；系统提示与记忆无关，
// 记忆不变时两轮的系统提示和记忆消息逐字节相同，续轮也不会把上一轮的快照带进历史。
func TestCloudAgentRunFreezesMemorySnapshotOutsideSystemPrompt(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	doc, err := s.AgentMemoryDocument("user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentMemoryDocument("user", AgentMemoryDocUpdateRequest{Revision: doc.Revision, Entries: []AgentMemoryEntryInput{{Section: model.AgentMemorySectionUser, Text: "称呼用户为长官"}}}); err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Prompt = "分析剧情"
	req.IdempotencyKey = "memory-run-1"
	first, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	firstState, firstMemory := memoryRunMessages(t, s, first.ID)
	if firstState.Request.MemoryMode != model.AgentMemoryDocumentModeAuto || !strings.Contains(firstState.Memory.Text, "[u1] 称呼用户为长官") {
		t.Fatalf("run did not freeze the memory snapshot: mode=%q memory=%+v", firstState.Request.MemoryMode, firstState.Memory)
	}
	if strings.Contains(firstState.Canonical.SystemPrompt, "称呼用户为长官") || strings.Contains(firstState.Canonical.SystemPrompt, "长期记忆】") {
		t.Fatal("memory leaked into the stable system prompt")
	}
	if len(firstMemory) != 1 || stringField(firstMemory[0], "role") != "user" || stringField(firstMemory[0], "content") != firstState.Memory.Text {
		t.Fatalf("memory snapshot must be exactly one user data message: %+v", firstMemory)
	}
	messages := firstState.Canonical.Messages
	memoryAt, promptAt := -1, -1
	for index, message := range messages {
		switch {
		case stringField(message, cloudAgentContextSourceKey) == cloudAgentMemoryContextSource:
			memoryAt = index
		case stringField(message, "role") == "user" && stringField(message, cloudAgentContextSourceKey) == "" && stringField(message, "content") == req.Prompt:
			promptAt = index
		}
	}
	if memoryAt < 0 || promptAt != len(messages)-1 || memoryAt > promptAt {
		t.Fatalf("memory must sit before the current user request: memory=%d prompt=%d of %d", memoryAt, promptAt, len(messages))
	}
	if !cloudAgentToolAllowed(firstState.Request, "memory_update") || !containsToolName(cloudAgentToolNames(firstState.Canonical.Tools), "memory_update") {
		t.Fatal("memory_update must be in the run's tool catalog while memory is on")
	}

	req.IdempotencyKey = "memory-run-2"
	second, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	secondState, secondMemory := memoryRunMessages(t, s, second.ID)
	if secondState.Canonical.SystemPrompt != firstState.Canonical.SystemPrompt {
		t.Fatal("system prompt changed between runs with unchanged memory")
	}
	if len(secondMemory) != 1 || stringField(secondMemory[0], "content") != stringField(firstMemory[0], "content") {
		t.Fatal("memory message is not byte-stable across runs with unchanged memory")
	}

	if _, err := s.UpdateAgentMemoryDocumentSettings("user", AgentMemoryDocSettingsRequest{Mode: model.AgentMemoryDocumentModeOff}); err != nil {
		t.Fatal(err)
	}
	req.IdempotencyKey = "memory-run-off"
	off, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	offState, offMemory := memoryRunMessages(t, s, off.ID)
	if offState.Canonical.SystemPrompt != firstState.Canonical.SystemPrompt {
		t.Fatal("system prompt must not depend on memory at all")
	}
	if len(offMemory) != 0 || offState.Request.MemoryMode != model.AgentMemoryDocumentModeOff || containsToolName(cloudAgentToolNames(offState.Canonical.Tools), "memory_update") {
		t.Fatalf("off mode must neither inject memory nor expose memory_update: memory=%d mode=%q", len(offMemory), offState.Request.MemoryMode)
	}

	if _, err := s.UpdateAgentMemoryDocumentSettings("user", AgentMemoryDocSettingsRequest{Mode: model.AgentMemoryDocumentModeAuto}); err != nil {
		t.Fatal(err)
	}
	completePiRunWithAssistantForTest(t, s, first.ID, "好的，长官")
	child := req
	child.IdempotencyKey, child.Prompt = "memory-run-child", "继续"
	next, err := s.CreateCloudAgentRun("user", child, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, childMemory := memoryRunMessages(t, s, next.ID)
	if len(childMemory) != 1 {
		t.Fatalf("continuation must carry only its own memory snapshot, got %d", len(childMemory))
	}
}

func memoryEvents(state cloudAgentRuntime) []CloudAgentEvent {
	var events []CloudAgentEvent
	for _, event := range state.Events {
		if event.Type == "memory_updated" {
			events = append(events, event)
		}
	}
	return events
}

// 运行期分派：auto 直接生效、review 只生成待确认修订、off 拒绝；同一调用重放不重复写入；
// 修订归属当前用户，别人不能撤销或采纳。
func TestMemoryUpdateRuntimeAutoReviewOffAndOwnership(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")
	if state.Request.MemoryMode != model.AgentMemoryDocumentModeAuto {
		t.Fatalf("server did not derive the memory mode: %q", state.Request.MemoryMode)
	}
	call := memoryUpdateCall("memory-call-1", `{"operations":[{"action":"add","section":"user","text":"用户偏好竖屏 9:16、电影感暗调"}],"reason":"用户说以后都用竖屏"}`)
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{call})
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail := lastAgentToolEvent(t, s, run.ID)
	if payload["toolName"] != "memory_update" || detail["error"] != nil || detail["status"] != agentMemoryOutcomeApplied || detail["usage"] != "17/5000" {
		t.Fatalf("auto write did not apply: payload=%v detail=%v", payload, detail)
	}
	_, state = reloadAgentRun(t, s, run.ID)
	events := memoryEvents(state)
	if len(events) != 1 || events[0].Payload["status"] != agentMemoryOutcomeApplied {
		t.Fatalf("memory_updated event missing: %+v", events)
	}
	revisionID, _ := events[0].Payload["revisionId"].(string)
	view, err := s.AgentMemoryDocument("user")
	if err != nil || len(view.Entries) != 1 || view.Entries[0].OriginRunID != run.ID || view.Entries[0].Source != model.AgentMemoryActorAgent {
		t.Fatalf("memory must be written once with run provenance: %+v err=%v", view, err)
	}

	// 同一调用 ID 重放（例如 worker 崩溃后重投）返回原结果，不重复写入。
	run, state = reloadAgentRun(t, s, run.ID)
	state.CallIndex = 0
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{call})
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	if again, err := s.AgentMemoryDocument("user"); err != nil || len(again.Entries) != 1 || again.Revision != view.Revision {
		t.Fatalf("replay wrote twice: %+v err=%v", again, err)
	}

	// 别人不能撤销或采纳这条修订，也读不到这份记忆。
	if _, err := s.UndoAgentMemoryRevision("other-user", revisionID); err == nil || !isNotFound(err) {
		t.Fatalf("foreign undo must be not found: %v", err)
	}
	if other, err := s.AgentMemoryDocument("other-user"); err != nil || len(other.Entries) != 0 {
		t.Fatalf("memory leaked across users: %+v err=%v", other, err)
	}
	undone, err := s.UndoAgentMemoryRevision("user", revisionID)
	if err != nil || len(undone.Entries) != 0 {
		t.Fatalf("owner undo failed: %+v err=%v", undone, err)
	}

	// 运行中切到需确认：工具仍在本轮目录里，但修改只生成待确认修订。
	if _, err := s.UpdateAgentMemoryDocumentSettings("user", AgentMemoryDocSettingsRequest{Mode: model.AgentMemoryDocumentModeReview}); err != nil {
		t.Fatal(err)
	}
	run, state = reloadAgentRun(t, s, run.ID)
	state.CallIndex = 0
	review := memoryUpdateCall("memory-call-2", `{"operations":[{"action":"add","section":"notes","text":"分镜默认每镜 3-5 秒"}]}`)
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{review})
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	_, detail = lastAgentToolEvent(t, s, run.ID)
	if detail["status"] != agentMemoryOutcomePending {
		t.Fatalf("review mode must stage the change: %v", detail)
	}
	_, state = reloadAgentRun(t, s, run.ID)
	events = memoryEvents(state)
	if len(events) == 0 || events[len(events)-1].Payload["status"] != agentMemoryOutcomePending {
		t.Fatalf("pending proposal event missing: %+v", events)
	}
	pendingID, _ := events[len(events)-1].Payload["revisionId"].(string)
	if staged, err := s.AgentMemoryDocument("user"); err != nil || len(staged.Entries) != 0 || staged.PendingCount != 1 {
		t.Fatalf("pending change must not take effect: %+v err=%v", staged, err)
	}
	if _, err := s.DecideAgentMemoryRevision("other-user", pendingID, AgentMemoryDocDecisionRequest{Decision: "approve"}); err == nil {
		t.Fatal("another user approved my memory proposal")
	}
	approved, err := s.DecideAgentMemoryRevision("user", pendingID, AgentMemoryDocDecisionRequest{Decision: "approve"})
	if err != nil || len(approved.Entries) != 1 || approved.PendingCount != 0 {
		t.Fatalf("approval must apply the staged change: %+v err=%v", approved, err)
	}

	// 用户在运行中关闭记忆：写入被拒，作为普通工具失败交回模型。
	if _, err := s.UpdateAgentMemoryDocumentSettings("user", AgentMemoryDocSettingsRequest{Mode: model.AgentMemoryDocumentModeOff}); err != nil {
		t.Fatal(err)
	}
	run, state = reloadAgentRun(t, s, run.ID)
	state.CallIndex = 0
	off := memoryUpdateCall("memory-call-3", `{"operations":[{"action":"add","section":"notes","text":"不应写入"}]}`)
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{off})
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail = lastAgentToolEvent(t, s, run.ID)
	if payload["toolName"] != "memory_update" || detail["error"] == nil {
		t.Fatalf("off mode accepted a write: payload=%v detail=%v", payload, detail)
	}
	if final, err := s.AgentMemoryDocument("user"); err != nil || len(final.Entries) != 1 {
		t.Fatalf("off mode changed memory: %+v err=%v", final, err)
	}
}

// 只读模式按上游语义不暴露 memory_update：模型硬调也只得到未授权回执，记忆不变。
func TestMemoryUpdateRejectedInReadOnlyRun(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	req := agentTestRequest()
	req.IdempotencyKey = "memory-read-only-run"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, decoded := startPiAgentFirstStep(t, s, root.ID)
	state := *decoded
	state.ActiveTaskID = ""
	state.PiToolBatchTaskID = state.LastStepTaskID
	if state.Request.PermissionMode != "read_only" || state.Request.MemoryMode != model.AgentMemoryDocumentModeAuto {
		t.Fatalf("fixture: permission=%q memory=%q", state.Request.PermissionMode, state.Request.MemoryMode)
	}
	if cloudAgentToolAllowed(state.Request, "memory_update") {
		t.Fatal("read-only run exposed memory_update")
	}
	call := memoryUpdateCall("memory-read-only", `{"operations":[{"action":"add","section":"notes","text":"只读时不能写"}]}`)
	state.Calls = []cloudAgentCall{call}
	state.CallIndex = 0
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": state.Calls})
	state.CallAdmissions = cloudAgentPreflightBatch(&state, state.Calls)
	run, state = saveBatchState(t, s, run, &state)
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	if _, detail := lastAgentToolEvent(t, s, run.ID); detail["error"] == nil {
		t.Fatalf("read-only memory write was not rejected: %v", detail)
	}
	if view, err := s.AgentMemoryDocument("user"); err != nil || len(view.Entries) != 0 {
		t.Fatalf("read-only run changed memory: %+v err=%v", view, err)
	}
}

func isNotFound(err error) bool {
	var appErr *kernel.AppError
	return errors.As(err, &appErr) && appErr.Status == 404
}
