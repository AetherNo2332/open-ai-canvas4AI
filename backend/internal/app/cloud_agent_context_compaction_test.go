package app

// 自研：画布 Agent「上下文超预算时压缩成检查点后继续本轮」的针对性用例。
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// saveCloudAgentCompactionState 用真实保存路径持久化运行状态（消息、事件与检查点同事务），
// 而不是直接改写 state_json：检查点从 v2 起不再承载消息与事件，绕过持久化层会让行上的
// 计数与实际行数脱节。
func saveCloudAgentCompactionState(t *testing.T, s *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime, mutate ...func(*model.CloudAgentExecution)) *model.CloudAgentExecution {
	t.Helper()
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		for _, apply := range mutate {
			apply(current)
		}
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatalf("保存 Agent 状态失败: %v", err)
	}
	updated, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		t.Fatalf("重新读取 Agent 运行失败: %v", err)
	}
	return updated
}

func decodeCloudAgentCompactionState(t *testing.T, s *Service, runID string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	current, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(current)
	if err != nil {
		t.Fatal(err)
	}
	return current, state
}

func cloudAgentCompactionEvents(t *testing.T, s *Service, run *model.CloudAgentExecution, kind string) []CloudAgentEvent {
	t.Helper()
	_, state := decodeCloudAgentCompactionState(t, s, run.ID)
	events := make([]CloudAgentEvent, 0, 2)
	for _, event := range state.Events {
		if event.Type == kind {
			events = append(events, event)
		}
	}
	return events
}

// smallWindowCompactionFixture 造一个"配了小上下文窗口的渠道模型 + 已经堆到压缩线的会话"。
// 窗口取 8K/2K，压缩线（输入预算的 85%）只有一千多 token，用例不必依赖真实模型。
func smallWindowCompactionFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution, cloudAgentRuntime, cloudAgentContextBudget) {
	t.Helper()
	s, db, root := piAgentTestLeasedFixture(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	capability.Text.ContextWindowTokens = 8_000
	capability.Text.MaxOutputTokens = 2_000
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, capability)).Error; err != nil {
		t.Fatal(err)
	}
	run, state := decodeCloudAgentCompactionState(t, s, root.ID)
	budget := s.cloudAgentContextBudgetForRequest(state.Request)
	if budget.Source != "channel-model" {
		t.Fatalf("渠道模型窗口没有生效: %#v", budget)
	}
	state.Canonical.Messages = []map[string]any{
		{"role": "user", "content": strings.Repeat("剧情前提与人物关系", 300)},
		{"role": "assistant", "content": "已读取分镜"},
		{"role": "user", "content": "保持人物伤口连续性"},
		{"role": "assistant", "content": "已核对连续性"},
		{"role": "user", "content": "把第 4 行补上光影"},
		{"role": "assistant", "content": "好的，先看一下现有配色"},
		{"role": "user", "content": state.Request.Prompt},
	}
	seedPiCompactionMessages(t, db, run, state.Canonical.Messages)
	// 用例直接考"下一步该不该压"：这一步没有在跑的模型任务。
	state.ActiveTaskID = ""
	run = saveCloudAgentCompactionState(t, s, run, &state)
	return s, db, run, state, budget
}

// 下一步预计输入超过可用输入预算的压缩线时：暂停步进循环去压缩，而不是直接判死。
func TestCloudAgentCompactionRequestsWhenTokenLineReached(t *testing.T) {
	s, _, run, state, budget := smallWindowCompactionFixture(t)
	if requested, err := s.cloudAgentRequestCompaction(run, &state, budget, budget.CompactAtTokens-1); err != nil || requested {
		t.Fatalf("未到压缩线却请求了压缩: requested=%v err=%v", requested, err)
	}
	if state.ContextCompaction != nil {
		t.Fatalf("未到压缩线却改了状态: %+v", state.ContextCompaction)
	}

	requested, err := s.cloudAgentRequestCompaction(run, &state, budget, budget.CompactAtTokens)
	if err != nil || !requested {
		t.Fatalf("到压缩线没有暂停: requested=%v err=%v", requested, err)
	}
	if state.ContextCompaction == nil || state.ContextCompaction.Status != "requested" || !state.ContextCompaction.Resume {
		t.Fatalf("压缩态 = %+v，应为 requested + resume", state.ContextCompaction)
	}
	// 暂停这一步不能顺手结束本轮：压完还要继续。
	final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "running" {
		t.Fatalf("暂停把本轮改成了 %q", final.Status)
	}
	if persisted.ContextCompaction == nil || !persisted.ContextCompaction.Resume {
		t.Fatalf("压缩态没有落库: %+v", persisted.ContextCompaction)
	}
	events := cloudAgentCompactionEvents(t, s, final, "context_compaction_requested")
	if len(events) != 1 {
		t.Fatalf("context_compaction_requested 事件数 = %d", len(events))
	}
	payload := events[0].Payload
	// 事件从持久化状态里读回来，数字都是 float64。
	if payload["basis"] != "tokens" || payload["compactAtTokens"] != float64(budget.CompactAtTokens) {
		t.Fatalf("触发读数 = %+v", payload)
	}
	if ratio, ok := payload["pressureRatio"].(float64); !ok || ratio < 0.84 {
		t.Fatalf("压力读数 = %+v", payload["pressureRatio"])
	}
}

// 没有配模型上下文窗口时退回字节/条数兜底判据，且兜底判据同样必须"暂停而不是判死"。
func TestCloudAgentCompactionFallsBackToBytesWithoutModelWindow(t *testing.T) {
	s, db, run, state, _ := smallWindowCompactionFixture(t)
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	profile.Text.ContextWindowTokens, profile.Text.MaxOutputTokens = 0, 0
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}
	budget := s.cloudAgentContextBudgetForRequest(state.Request)
	if budget.Source != "default" {
		t.Fatalf("unknown model window must use the bytes fallback: %+v", budget)
	}
	for index := len(state.Canonical.Messages); index < cloudAgentHistoryKeepRounds*2; index++ {
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "继续推进分镜"})
	}
	if !cloudAgentContextShouldCompact(len(state.Canonical.Messages), len([]byte(cloudAgentJSON(state.Canonical.Messages)))) {
		t.Fatal("message-count threshold did not request semantic compaction")
	}
	// The Pi hook owns initiation. Unknown windows stay unknown in its snapshot;
	// the Go endpoint nevertheless provides the original structured compactor.
	revision, leaf := seedPiCompactionMessages(t, db, run, state.Canonical.Messages)
	operation, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner,
		PiContextCompactionStart{SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold"})
	if err != nil {
		t.Fatal(err)
	}
	final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "running" || persisted.ContextCompaction == nil || operation.TaskID == "" {
		t.Fatalf("bytes-triggered Pi compaction did not keep the run active: status=%s operation=%+v", final.Status, operation)
	}
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil || snapshot.ModelLimits.Configured {
		t.Fatalf("unknown window was replaced with a fake model limit: snapshot=%+v err=%v", snapshot, err)
	}
}

// 压缩结果合法时：历史被换成「检查点 + 回执 + 最近 2 对」，并 Resume 继续本轮的步进。
func TestCloudAgentCompactionResumesStepLoopWithCheckpoint(t *testing.T) {
	s, db, run, state, _ := smallWindowCompactionFixture(t)
	state.TokenAnchor = &cloudAgentTokenAnchor{TaskID: "previous-step", Step: state.Step, InputTokens: 10000, EstimatedTokens: 11000, Accepted: true}
	run = saveCloudAgentCompactionState(t, s, run, &state)
	stepBefore, pressureBefore := state.Step, len(cloudAgentCompactionEvents(t, s, run, "context_pressure"))
	operation := beginPiCompactionForTest(t, s, run, "threshold")
	_, pending := decodeCloudAgentCompactionState(t, s, run.ID)
	if pending.Step != stepBefore || pending.TokenAnchor == nil || !pending.TokenAnchor.Accepted {
		t.Fatalf("compaction changed model-step accounting: %+v", pending)
	}
	if got := len(cloudAgentCompactionEvents(t, s, run, "context_pressure")); got != pressureBefore {
		t.Fatalf("compaction emitted an ordinary model-step pressure event: %d -> %d", pressureBefore, got)
	}
	task, err := s.repo.TaskForUser("user", operation.TaskID)
	if err != nil || task.Operation != cloudAgentContextCompactionOperation || task.Type != "canvas_text" {
		t.Fatalf("not a Go model compaction task: task=%+v err=%v", task, err)
	}
	var taskInput map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &taskInput); err != nil {
		t.Fatal(err)
	}
	options, _ := taskInput["textOptions"].(map[string]any)
	if options["stream"] != false || options["thinking"] != false || !strings.Contains(task.Prompt, "scriptDesign") {
		t.Fatalf("original structured compaction contract was not routed to Go: %+v", taskInput)
	}
	checkpoint := agentcontext.Checkpoint{
		Version: agentcontext.Version, HistorySummary: "用户要求补齐分镜光影", ScriptDesign: "冷色调 + 左手伤口连续性",
		CurrentWork: "改写第 4 行", NextStep: "写入节点", PendingTasks: []string{"invented-task"},
		OperationHistory: []string{"invented-write"}, CompactedTurnCount: 999,
	}
	body, _ := json.Marshal(checkpoint)
	result, _ := json.Marshal(map[string]any{"text": string(body)})
	setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusSucceeded, string(result))
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	if ready.Mode != "model" || ready.Fallback {
		t.Fatalf("valid structured model output was not accepted: %+v", ready)
	}
	commitPiCompactionForTest(t, s, run, ready, "pi-model-compaction")
	final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "running" || persisted.ContextCompaction != nil || persisted.ContextCompactionCount != 1 || persisted.ActiveTaskID != "" {
		t.Fatalf("Pi commit did not resume the run: status=%s state=%+v", final.Status, persisted)
	}
	if persisted.ContextCheckpoint == nil || persisted.ContextCheckpoint.HistorySummary != checkpoint.HistorySummary ||
		persisted.ContextCheckpoint.CompactedTurnCount != 4 || len(persisted.ContextCheckpoint.PendingTasks) != 0 ||
		len(persisted.ContextCheckpoint.OperationHistory) != 0 {
		t.Fatalf("model invented execution facts or turn count survived: %+v", persisted.ContextCheckpoint)
	}
	if !persisted.HistoryIncludesCurrent || persisted.TokenAnchor != nil {
		t.Fatalf("compacted history state is stale: %+v", persisted)
	}
	messages := persisted.Canonical.Messages
	if len(messages) != 7 || !strings.Contains(stringField(messages[0], "content"), "<agent-context-checkpoint>") ||
		stringField(messages[1], "content") != agentcontext.Acknowledgement ||
		stringField(messages[len(messages)-1], "content") != state.Request.Prompt {
		t.Fatalf("checkpoint/recent complete turns/current request were not retained: %+v", messages)
	}
	events := cloudAgentCompactionEvents(t, s, final, "context_compacted")
	if len(events) != 1 || events[0].Payload["mode"] != "model" || events[0].Payload["resume"] != true ||
		events[0].Payload["droppedTurns"] != float64(1) {
		t.Fatalf("committed compaction event is incorrect: %+v", events)
	}
	// Pi, rather than the retired Go loop, explicitly starts the next model step.
	request := PiModelStepRequest{Canonical: persisted.Canonical}
	next, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
	if err != nil || next.TaskID == operation.TaskID {
		t.Fatalf("next Pi model step was not admitted after commit: next=%+v err=%v", next, err)
	}
}

// 压完仍然超阈值时不能无限暂停：同一轮的压缩次数有上限。
func TestCloudAgentCompactionStopsAtAttemptLimit(t *testing.T) {
	s, db, run, state, _ := smallWindowCompactionFixture(t)
	state.ContextCompactionCount = cloudAgentMaxCompactionsPerRun - 1
	run = saveCloudAgentCompactionState(t, s, run, &state)
	operation := beginPiCompactionForTest(t, s, run, "threshold")
	setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusFailed, "{}")
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	commitPiCompactionForTest(t, s, run, ready, "last-compaction")
	_, persisted := reloadPiRun(t, s, run.ID)
	if persisted.ContextCompactionCount != cloudAgentMaxCompactionsPerRun {
		t.Fatalf("last allowed commit did not consume the compaction budget: %d", persisted.ContextCompactionCount)
	}
	var tasksBefore int64
	db.Model(&model.Task{}).Where("operation = ?", cloudAgentContextCompactionOperation).Count(&tasksBefore)
	session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{
		SessionRevision: session.Revision, ActiveLeafID: session.ActiveLeafID, Reason: "manual",
	}); err == nil {
		t.Fatal("Pi admitted a fifth compaction in the same run")
	}
	var tasksAfter int64
	db.Model(&model.Task{}).Where("operation = ?", cloudAgentContextCompactionOperation).Count(&tasksAfter)
	if tasksAfter != tasksBefore {
		t.Fatalf("rejected compaction created a billed task: %d -> %d", tasksBefore, tasksAfter)
	}
}

// 压缩调用失败时走服务端保底检查点：整轮既不判死，也不会因为压缩失败丢掉上下文。
func TestCloudAgentCompactionUsesFallbackCheckpointOnModelFailure(t *testing.T) {
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	operation := beginPiCompactionForTest(t, s, run, "threshold")
	setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusFailed, "{}")
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	if !ready.Fallback || ready.Reason == "" {
		t.Fatalf("model failure did not retain Go fallback semantics: %+v", ready)
	}
	commitPiCompactionForTest(t, s, run, ready, "pi-fallback")
	final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "running" || persisted.ContextCheckpoint == nil || persisted.ContextCompaction != nil || persisted.ContextCompactionCount != 1 {
		t.Fatalf("fallback did not safely resume the run: status=%s state=%+v", final.Status, persisted)
	}
	events := cloudAgentCompactionEvents(t, s, final, "context_compacted")
	if len(events) != 1 || events[0].Payload["mode"] != "fallback" || events[0].Payload["resume"] != true {
		t.Fatalf("fallback event not durable: %+v", events)
	}
}

// 压缩模型输出不合规（不是 JSON / 版本不对）时同样退到保底检查点。
func TestCloudAgentCompactionRejectsInvalidModelCheckpoint(t *testing.T) {
	for _, invalid := range []string{`{"text":"not JSON"}`, `{"text":"{\\"version\\":99,\\"historySummary\\":\\"invented\\"}"}`, "{"} {
		t.Run(invalid, func(t *testing.T) {
			s, db, run, _, _ := smallWindowCompactionFixture(t)
			operation := beginPiCompactionForTest(t, s, run, "threshold")
			setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusSucceeded, invalid)
			ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
			if !ready.Fallback || ready.Reason == "" {
				t.Fatalf("malformed result bypassed structured fallback: %+v", ready)
			}
			commitPiCompactionForTest(t, s, run, ready, "pi-invalid-fallback")
			final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
			if final.Status != "running" || persisted.ContextCheckpoint == nil {
				t.Fatalf("invalid model checkpoint lost usable history: status=%s state=%+v", final.Status, persisted)
			}
		})
	}
}

// 压缩期间被取消：收尾要落保底检查点 + context_compacted(mode=fallback)，且终态不被改写。
func TestCloudAgentCancelledRunFinalizesInterruptedCompaction(t *testing.T) {
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	operation := beginPiCompactionForTest(t, s, run, "threshold")
	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := decodeCloudAgentCompactionState(t, s, run.ID)
	if _, err := s.PiContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID); err == nil {
		t.Fatal("cancelled run permitted a worker to continue its compaction")
	}
	if err := s.finishCloudAgentCleanup(context.Background(), cancelled); err != nil {
		t.Fatal(err)
	}
	final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "cancelled" || final.CleanupPending || persisted.ContextCompaction != nil ||
		persisted.ContextCheckpoint == nil || persisted.ContextCheckpoint.CompactedTurnCount != 4 {
		t.Fatalf("cancelled Pi operation did not retain terminal fallback checkpoint: status=%s state=%+v", final.Status, persisted)
	}
	task, err := s.repo.TaskForUser("user", operation.TaskID)
	if err != nil || task.Status != model.TaskStatusCancelled {
		t.Fatalf("cancelled compaction task remains claimable: task=%+v err=%v", task, err)
	}
	var order model.BillingOrder
	if err := db.Where("task_id = ?", operation.TaskID).First(&order).Error; err != nil || order.Status != model.BillingStatusRefunded {
		t.Fatalf("cancelled compaction did not release its reservation: order=%+v err=%v", order, err)
	}
	var account model.CreditAccount
	if err := db.Where("user_id = ?", "user").First(&account).Error; err != nil || account.ReservedMicrocredits != 0 {
		t.Fatalf("cancelled compaction left an account reservation: account=%+v err=%v", account, err)
	}
	if err := s.finishCloudAgentCleanup(context.Background(), final); err != nil {
		t.Fatal(err)
	}
	events := cloudAgentCompactionEvents(t, s, final, "context_compacted")
	if len(events) != 1 || events[0].Payload["mode"] != "fallback" || events[0].Payload["resume"] != true || events[0].Payload["reason"] == nil {
		t.Fatalf("cancel cleanup repeated or omitted the fallback event: %+v", events)
	}
}

// 失败轮同样是终态：中断收尾只能落检查点，不能把 failed 写成 completed。
func TestCloudAgentFailedRunStaysFailedWhenCompactionFinalized(t *testing.T) {
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	beginPiCompactionForTest(t, s, run, "threshold")
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	run, state := decodeCloudAgentCompactionState(t, s, run.ID)

	if err := s.finalizeCloudAgentInterruptedCompaction(run, &state, "本轮在压缩期间结束，已使用服务端保底检查点"); err != nil {
		t.Fatal(err)
	}
	final, _ := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "failed" {
		t.Fatalf("失败轮被复活成 %q", final.Status)
	}
	if len(cloudAgentCompactionEvents(t, s, final, "context_compacted")) != 1 {
		t.Fatal("失败轮没有落保底检查点")
	}
}

// 没有停在压缩上的轮次：收尾不该凭空写一条压缩事件。
func TestCloudAgentCleanupWithoutPendingCompactionWritesNoCheckpointEvent(t *testing.T) {
	s, db, root := piAgentTestLeasedFixture(t)
	run, _ := decodeCloudAgentCompactionState(t, s, root.ID)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).
		Updates(map[string]any{"status": "cancelled", "cleanup_pending": true}).Error; err != nil {
		t.Fatal(err)
	}
	run, _ = decodeCloudAgentCompactionState(t, s, run.ID)
	if err := s.finishCloudAgentCleanup(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if got := len(cloudAgentCompactionEvents(t, s, run, "context_compacted")); got != 0 {
		t.Fatalf("无压缩态的收尾写了 %d 条压缩事件", got)
	}
}

// 保留最近 N 对消息的不变量：不以 tool/带工具调用的 assistant 开头，不把摘要当轮次。
func TestCloudAgentCompactionKeepsRecentCompleteTurns(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "第一轮要求"},
		{"role": "assistant", "content": "第一轮回复"},
		{"role": "user", "content": "第二轮要求"},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-1"}}},
		{"role": "tool", "content": `{"ok":true}`},
		{"role": "assistant", "content": "第二轮回复"},
		{"role": "user", "content": "<agent-context-checkpoint>\n{\"version\":1}\n</agent-context-checkpoint>", cloudAgentContextSourceKey: "checkpoint"},
		{"role": "user", "content": "第三轮要求"},
		{"role": "assistant", "content": "第三轮回复"},
	}
	recent := cloudAgentCompleteTurnTail(messages, cloudAgentContextKeepPairs)
	if len(recent) != 4 {
		t.Fatalf("最近对话 = %+v", recent)
	}
	if recent[0].Role != "user" || recent[0].Content != "第二轮要求" || recent[len(recent)-1].Content != "第三轮回复" {
		t.Fatalf("保留的不是最近两对完整对话: %+v", recent)
	}
	for _, message := range recent {
		if message.Role == "tool" {
			t.Fatal("保留了一条孤立的工具回执")
		}
	}
	// 只有一对时不应把更早的一对带进来。
	if got := cloudAgentCompleteTurnTail(messages, 1); len(got) != 2 || got[0].Content != "第三轮要求" {
		t.Fatalf("保留一对 = %+v", got)
	}
}

// 压缩调用与步进调用一样属于"一次模型调用"，必须同样受单步墙钟约束。
func TestCloudAgentModelOperationCoversContextCompaction(t *testing.T) {
	if !cloudAgentModelOperation(&model.Task{Operation: cloudAgentContextCompactionOperation}) {
		t.Fatal("压缩调用没有按单步口径计时")
	}
	if cloudAgentModelOperation(&model.Task{Operation: "canvas_text_generate"}) {
		t.Fatal("普通文本任务被当成了 Agent 单步")
	}
}

func TestCloudAgentCompactionContinuationIncludesFinalReplyWithoutReplayingPrompt(t *testing.T) {
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	operation := beginPiCompactionForTest(t, s, run, "threshold")
	setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusFailed, "{}")
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	commitPiCompactionForTest(t, s, run, ready, "continuation-compaction")
	if _, err := s.InterjectCloudAgent("user", run.ID, "compacted-interjection", "保持冷色调"); err != nil {
		t.Fatal(err)
	}
	for index, text := range []string{"【用户插话】保持冷色调", "镜头已按冷色调调整"} {
		role := "user"
		if index == 1 {
			role = "assistant"
		}
		session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		message, _ := json.Marshal(map[string]any{"role": role, "content": text, "timestamp": 1})
		id := fmt.Sprintf("continuation-%s", role)
		entry, _ := json.Marshal(map[string]any{"type": "message", "id": id, "parentId": session.ActiveLeafID,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": json.RawMessage(message)})
		var interjectionIDs []string
		if index == 0 {
			interjectionIDs = []string{"compacted-interjection"}
		}
		if _, err := s.PiCheckpointMessageResult("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{
			Sequence: index + 1, Message: message, SessionRevision: session.Revision, ActiveLeafID: id,
			SessionEntries: []json.RawMessage{entry}, InterjectionIDs: interjectionIDs,
		}); err != nil {
			t.Fatal(err)
		}
	}
	run, state := decodeCloudAgentCompactionState(t, s, run.ID)
	persistedSession, persistedEntries, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := cloudAgentPiActiveBranch(persistedEntries, persistedSession.ActiveLeafID)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := cloudAgentPiCompactionSourceForBranch(branch, persistedSession.ActiveLeafID)
	if err != nil {
		t.Fatal(err)
	}
	state.Canonical.Messages = projected.Messages
	state.event(run.ID, "assistant_message", map[string]any{"messageId": "final", "text": "镜头已按冷色调调整", "final": true})
	run = saveCloudAgentCompactionState(t, s, run, &state, func(current *model.CloudAgentExecution) {
		current.Status = "completed"
	})
	req := agentTestRequest()
	req.Prompt, req.IdempotencyKey = "继续下一镜", "compaction-continuation-final"
	child, err := s.CreateCloudAgentRun("user", req, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.repo.CloudAgent("user", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	childState, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	var replies, originalPrompts, interjections int
	for _, message := range childState.TextHistory {
		switch message.Content {
		case "镜头已按冷色调调整":
			replies++
		case state.Request.Prompt:
			originalPrompts++
		case "【用户插话】保持冷色调":
			interjections++
		}
	}
	if replies != 1 || originalPrompts != 1 || interjections != 1 {
		t.Fatalf("compacted continuation duplicated or lost current instructions: replies=%d original=%d interjections=%d",
			replies, originalPrompts, interjections)
	}
	session, entries, err := s.repo.CloudAgentPiSession("user", execution.ConversationID)
	if err != nil || session.ConversationID != run.ConversationID || len(entries) != 10 {
		t.Fatalf("continuation did not reuse its persisted Pi tree: session=%+v entries=%d err=%v", session, len(entries), err)
	}
}

func TestCloudAgentCompactionCanRecoverContextFrameBudgetFailure(t *testing.T) {
	s, db, run, state, budget := smallWindowCompactionFixture(t)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{
		"role": "user", "content": strings.Repeat("新的剧本细节与人物设定", 2000),
	})
	if err := fitCloudAgentModelContext(&state.Canonical, budget.InputBudgetTokens); err == nil {
		t.Fatal("fixture must exceed the declared input capacity")
	}
	seedPiCompactionMessages(t, db, run, state.Canonical.Messages)
	operation := beginPiCompactionForTest(t, s, run, "overflow")
	setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusFailed, "{}")
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	commitPiCompactionForTest(t, s, run, ready, "pi-overflow-fallback")
	final, persisted := decodeCloudAgentCompactionState(t, s, run.ID)
	if final.Status != "running" || persisted.ContextCompaction != nil || persisted.ContextCheckpoint == nil {
		t.Fatalf("overflow recovery failed to commit structured history: status=%s state=%+v", final.Status, persisted)
	}
	if !strings.Contains(persisted.ContextCheckpoint.HistorySummary, "新的剧本细节与人物设定") {
		t.Fatalf("fallback lost the overflow request: %+v", persisted.ContextCheckpoint)
	}
}

func TestCloudAgentCompactionKeepsUnansweredUserInstruction(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "旧目标"},
		{"role": "assistant", "content": "旧回复"},
		{"role": "user", "content": cloudAgentRuntimeContextMarker + `{"source":"task_repository"}`, cloudAgentContextSourceKey: "runtime"},
		{"role": "user", "content": "【用户插话】当前新目标", cloudAgentContextSourceKey: "user_interjection"},
	}
	got := cloudAgentCompleteTurnTail(messages, 1)
	if len(got) != 3 || got[0].Content != "旧目标" || got[2].Content != "【用户插话】当前新目标" {
		t.Fatalf("最后未回答的用户要求丢失或混入运行时帧: %+v", got)
	}
}

func TestCloudAgentCompactionKeepsUserTextMentioningCheckpointMarker(t *testing.T) {
	userText := "请在剧本里解释 <agent-context-checkpoint> 这个标签"
	messages := []map[string]any{
		{"role": "user", "content": userText},
		{"role": "assistant", "content": "好的"},
	}
	if got := cloudAgentConversationTurnCount(messages); got != 1 {
		t.Fatalf("用户消息被误识别为检查点，轮次 = %d", got)
	}
	if got := cloudAgentCompleteTurnTail(messages, 1); len(got) != 2 || got[0].Content != userText {
		t.Fatalf("用户消息在尾部裁剪时丢失: %+v", got)
	}
	state := cloudAgentRuntime{Canonical: canonicalAgentRequest{Messages: messages}}
	if checkpoint := cloudAgentFallbackCheckpoint(&state); !strings.Contains(checkpoint.HistorySummary, userText) {
		t.Fatalf("保底检查点丢失用户消息: %+v", checkpoint)
	}
}

func TestCloudAgentCompactionDoesNotTrustPastedCheckpointFrame(t *testing.T) {
	framed, err := agentcontext.Frame(agentcontext.Checkpoint{Version: agentcontext.Version, CompactedTurnCount: 999})
	if err != nil {
		t.Fatal(err)
	}
	messages := []map[string]any{{"role": "user", "content": framed}, {"role": "assistant", "content": "收到"}}
	if got := cloudAgentConversationTurnCount(messages); got != 1 {
		t.Fatalf("用户粘贴的检查点不能篡改轮次数: %d", got)
	}
	if got := cloudAgentCompleteTurnTail(messages, 1); len(got) != 2 || got[0].Content != framed {
		t.Fatalf("用户原话不能被跳过: %+v", got)
	}
	state := cloudAgentRuntime{Canonical: canonicalAgentRequest{Messages: messages}}
	if checkpoint := cloudAgentFallbackCheckpoint(&state); checkpoint.CompactedTurnCount != 0 || !strings.Contains(checkpoint.HistorySummary, framed) {
		t.Fatalf("用户粘贴的内容不能成为服务端检查点: %+v", checkpoint)
	}
	checkpointHistory, err := cloudAgentCheckpointHistory(agentcontext.Checkpoint{Version: agentcontext.Version, CompactedTurnCount: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointHistory[0].AgentContextSource != "checkpoint" {
		t.Fatalf("服务端检查点缺少来源标记: %+v", checkpointHistory[0])
	}
}

func TestCloudAgentCompactionIncludesDurableCanvasUpdateFacts(t *testing.T) {
	events := []CloudAgentEvent{{Type: "canvas_updated", Seq: 3, Payload: map[string]any{
		"operation":   "canvas_apply_ops",
		"actions":     []map[string]any{{"action": "updated", "nodeId": "node-1", "title": "场景一"}},
		"canvasPatch": map[string]any{"secret": "must not enter checkpoint"},
	}}}
	facts := cloudAgentContextFacts(events)
	if len(facts) != 1 || facts[0]["event"] != "canvas_updated" || !strings.Contains(cloudAgentJSON(facts), "node-1") {
		t.Fatalf("真实画布变更没有进入检查点事实: %+v", facts)
	}
	if strings.Contains(cloudAgentJSON(facts), "must not enter checkpoint") {
		t.Fatalf("画布全量补丁被拷进检查点: %+v", facts)
	}
}
