package app

// 收尾保护针对协议与交付事实；待办清单由模型自行管理。
import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// agentEventPayloads 取本轮某一类事件的全部载荷（内存窗口即可，本用例都很短）。
func agentEventPayloads(state cloudAgentRuntime, kind string) []map[string]any {
	payloads := make([]map[string]any, 0, len(state.Events))
	for _, event := range state.Events {
		if event.Type == kind {
			payloads = append(payloads, event.Payload)
		}
	}
	return payloads
}

// agentFinalReplies 是本轮**作为最终答复发布**的正文：final=true 的 assistant 消息。
// 候选正文（阶段性进度、模型边做边说的过程说明）必须不带 final=true。
func agentFinalReplies(state cloudAgentRuntime) []string {
	replies := make([]string, 0, 1)
	for _, payload := range agentEventPayloads(state, "assistant_message") {
		isFinal, ok := payload["final"].(bool)
		if !ok || !isFinal {
			continue
		}
		replies = append(replies, stringValue(payload["text"]))
	}
	return replies
}

func agentLastEvent(t *testing.T, state cloudAgentRuntime) CloudAgentEvent {
	t.Helper()
	if len(state.Events) == 0 {
		t.Fatal("本轮没有任何事件")
	}
	return state.Events[len(state.Events)-1]
}

func agentFinishRunForTest(t *testing.T, s *Service, db *gorm.DB, runID, callID, summary string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"summary": summary})
	if err != nil {
		t.Fatal(err)
	}
	return agentSettleStep(t, s, db, runID, summary, callID, "finish_run", string(arguments))
}

// agentWithPendingPlan 造一个"待办清单里还有未完成项"的运行。
func agentWithPendingPlan(t *testing.T, s *Service, db *gorm.DB, root *CloudAgentRun) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Plan = []cloudAgentPlanItem{{ID: "1", Title: "生成镜头1", Status: "doing"}}
	if err := cloudAgentSave(run, &state); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
	return run, state
}

// pending/doing/done 都只是模型维护的状态。
func TestCloudAgentCompletionIgnoresPlanStatus(t *testing.T) {
	for _, status := range []string{"pending", "doing", "done"} {
		state := &cloudAgentRuntime{Plan: []cloudAgentPlanItem{{ID: "1", Title: "后续工作", Status: status}}}
		if block := cloudAgentEvaluateCompletion(state); !block.Final || len(block.Blockers) != 0 {
			t.Fatalf("待办状态 %s 不应阻止收尾：%+v", status, block)
		}
	}
}

func TestCloudAgentCompletionPublishesOneFinalReply(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, state := agentFinishRunForTest(t, s, db, root.ID, "finish-complete", "分析完成：画布上共有 3 个节点。")

	if run.Status != "completed" {
		t.Fatalf("没有阻塞时应当收尾，status=%s", run.Status)
	}
	replies := agentFinalReplies(state)
	if len(replies) != 1 || !strings.Contains(replies[0], "画布上共有 3 个节点") {
		t.Fatalf("只应发布一次最终答复：%v", replies)
	}
	if blocked := agentEventPayloads(state, "completion_blocked"); len(blocked) != 0 {
		t.Fatalf("闸门通过时不该有控制事件：%+v", blocked)
	}
	// 收尾后不再开新的模型请求。
	decision, err := s.PiNoToolTurn("user", root.ID, run.LeaseOwner, state.LastStepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != "completed" {
		t.Fatalf("收尾重放必须仍为 completed: %+v", decision)
	}
	_, state = agentInterjectionState(t, s, root.ID)
	if replies := agentFinalReplies(state); len(replies) != 1 {
		t.Fatalf("收尾后不得再发布最终答复：%v", replies)
	}
}

// 子代理结果尚未收集时仍需报告真实阻塞。
func TestCloudAgentCompletionReportsProtocolBlockerWithoutPlanNudge(t *testing.T) {
	state := &cloudAgentRuntime{
		ActiveSubagents: 1,
		Plan:            []cloudAgentPlanItem{{ID: "1", Title: "后续工作", Status: "doing"}},
		Canonical:       canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": "先汇总子代理结果"}}},
	}
	block := cloudAgentEvaluateCompletion(state)
	if block.Final || !cloudAgentCompletionHasBlocker(block, "pending_subagents") {
		t.Fatalf("应保留子代理结果保护：%+v", block)
	}
	cloudAgentCompletionBlockedNudge("run", state, block)
	last := state.Canonical.Messages[len(state.Canonical.Messages)-1]
	var context cloudAgentRuntimeContext
	if err := json.Unmarshal([]byte(strings.TrimPrefix(stringField(last, "content"), cloudAgentRuntimeContextMarker)), &context); err != nil {
		t.Fatal(err)
	}
	if context.Kind != cloudAgentContextCompletionBlocked || !strings.Contains(context.Detail, "wait_subagents") || strings.Contains(context.Detail, "后续工作") {
		t.Fatalf("只应报告实际协议阻塞：%+v", context)
	}
	if !isCloudAgentRuntimeContextMessage(last) || cloudAgentLatestUserInstruction(state.Canonical.Messages) != "先汇总子代理结果" {
		t.Fatalf("协议状态不得冒充用户要求：%+v", last)
	}
	if len(agentEventPayloads(*state, "completion_blocked")) != 1 {
		t.Fatal("缺少界面控制事件")
	}
}

func TestCloudAgentPlanUpdateDoesNotResetProtocolRetries(t *testing.T) {
	state := &cloudAgentRuntime{ActiveSubagents: 1}
	block := cloudAgentEvaluateCompletion(state)
	for i := 0; i < cloudAgentCompletionNudgeLimit; i++ {
		cloudAgentNoteCompletionBlocked(state, block.Fingerprint)
	}
	call := cloudAgentCall{ID: "plan"}
	call.Function.Arguments = `{"items":[{"id":"1","title":"模型自行调整清单","status":"pending"}]}`
	if _, err := cloudAgentApplyPlanUpdate(state, call); err != nil {
		t.Fatal(err)
	}
	after := cloudAgentEvaluateCompletion(state)
	if after.Fingerprint != block.Fingerprint {
		t.Fatal("待办修改不应影响协议阻塞指纹")
	}
	if attempt, exhausted := cloudAgentNoteCompletionBlocked(state, after.Fingerprint); attempt != cloudAgentCompletionNudgeLimit+1 || !exhausted {
		t.Fatalf("待办修改不应重置协议恢复次数：attempt=%d exhausted=%v", attempt, exhausted)
	}
}

func TestCloudAgentCompletionPreservesCompletedPlan(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Plan = []cloudAgentPlanItem{{ID: "1", Title: "生成镜头1", Status: "done"}}
	if err := cloudAgentSave(run, &state); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
	run, state = agentFinishRunForTest(t, s, db, root.ID, "finish-reconciled", "镜头1 已生成，本轮完成。")
	if run.Status != "completed" {
		t.Fatalf("已对账的清单应当放行：status=%s", run.Status)
	}
	if replies := agentFinalReplies(state); len(replies) != 1 {
		t.Fatalf("应发布一次最终答复：%v", replies)
	}
}

func TestCloudAgentFinishRunCompletesRun(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-1", "finish_run", `{"summary":"画布分析完成。"}`)

	if run.Status != "completed" {
		t.Fatalf("闸门通过时 finish_run 应收尾：status=%s", run.Status)
	}
	replies := agentFinalReplies(state)
	if len(replies) != 1 || !strings.Contains(replies[0], "画布分析完成") {
		t.Fatalf("最终答复应来自 finish_run 的 summary：%v", replies)
	}
	completed := agentEventPayloads(state, "tool_completed")
	if len(completed) != 1 {
		t.Fatalf("finish_run 应返回一条回执：%+v", completed)
	}
	result, _ := completed[0]["result"].(map[string]any)
	if result["completionBlocked"] == true {
		t.Fatalf("通过的收尾不该标记为被拦下：%+v", result)
	}
}

// finish_run 的参数契约：summary 必填，缺了要能自纠（字段级回执）。
func TestCloudAgentFinishRunRequiresSummary(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-1", "finish_run", `{}`)
	if run.Status != "running" {
		t.Fatalf("参数错误不该结束本轮：status=%s", run.Status)
	}
	failed := agentEventPayloads(state, "tool_failed")
	if len(failed) != 1 {
		t.Fatalf("缺 summary 应回一条字段级失败：%+v", failed)
	}
	detail, _ := failed[0]["result"].(map[string]any)
	if stringValue(detail["field"]) != "summary" {
		t.Fatalf("失败回执要点出缺失字段：%+v", failed[0])
	}
}

// 子代理协议阻塞仍保留有限恢复。
func TestCloudAgentFinishRunProtocolRetriesAreBounded(t *testing.T) {
	state := &cloudAgentRuntime{ActiveSubagents: 1}
	call := cloudAgentCall{ID: "finish"}
	call.Function.Arguments = `{"summary":"汇总完成。"}`
	for attempt := 1; attempt <= cloudAgentCompletionNudgeLimit+1; attempt++ {
		result, err := cloudAgentFinishRun("run", state, call)
		if err != nil {
			t.Fatal(err)
		}
		detail := result.(map[string]any)
		if detail["completionBlocked"] != true || detail["requiredAction"] != "resolve_blockers" || !strings.Contains(fmt.Sprint(detail["blockers"]), "pending_subagents") {
			t.Fatalf("未返回实际协议阻塞：%+v", detail)
		}
		if cloudAgentFinishRunExhausted(result) != (attempt > cloudAgentCompletionNudgeLimit) {
			t.Fatalf("协议恢复次数未受限：attempt=%d result=%+v", attempt, detail)
		}
		if strings.Contains(fmt.Sprint(detail), "清单") {
			t.Fatalf("协议失败不得要求对账待办：%+v", detail)
		}
	}
}

func TestCloudAgentContinuationPrefersFinalReply(t *testing.T) {
	task := &model.Task{Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"过程草稿"}`}
	run := &CloudAgentRun{ID: "ag-final", Status: "completed", Events: []CloudAgentEvent{
		{Type: "assistant_message", Payload: map[string]any{"text": "我先读一下画布。", "final": false}},
		{Type: "tool_completed", Payload: map[string]any{"toolName": "canvas_get_state"}},
		{Type: "assistant_message", Payload: map[string]any{"text": "画布分析完成：3 个节点。", "final": true}},
	}}
	reply, _, err := cloudAgentContinuationReply(task, run)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "画布分析完成：3 个节点。" {
		t.Fatalf("上一轮答复应取最终答复，got %q", reply)
	}
}

// 同一批里重复申请收尾只处理第一个：否则一个模型步骤就能把整轮催办额度烧光。
func TestCloudAgentFinishRunCountsOncePerBatch(t *testing.T) {
	first, second := cloudAgentCall{ID: "finish-1"}, cloudAgentCall{ID: "finish-2"}
	first.Function.Name, first.Function.Arguments = "finish_run", `{"summary":"汇总完成。"}`
	second.Function = first.Function
	state := &cloudAgentRuntime{ActiveSubagents: 1, Calls: []cloudAgentCall{first, second}}
	if _, err := cloudAgentFinishRun("run", state, first); err != nil {
		t.Fatal(err)
	}
	state.CallIndex = 1
	result, err := cloudAgentFinishRun("run", state, second)
	if err != nil {
		t.Fatal(err)
	}
	if state.CompletionNudgeAttempt != 1 || state.CompletionNudges != 1 || len(agentEventPayloads(*state, "completion_blocked")) != 1 {
		t.Fatalf("同一批只应处理一次协议阻塞：%+v", state)
	}
	if result.(map[string]any)["duplicate"] != true {
		t.Fatalf("重复申请应明确未执行：%+v", result)
	}
}

func TestCloudAgentCompletionStopMessagesDiffer(t *testing.T) {
	block := cloudAgentCompletionBlock{Blockers: []cloudAgentCompletionBlocker{{Kind: "pending_subagents", Detail: "使用 wait_subagents 收集结果"}}}
	budget := cloudAgentCompletionExhaustedMessage(block)
	if !strings.Contains(budget, "预算") || strings.Contains(budget, "连续") {
		t.Fatalf("没有步数预算时要说明预算而不是催办次数：%q", budget)
	}
	block.Attempt = cloudAgentCompletionNudgeLimit
	nudged := cloudAgentCompletionExhaustedMessage(block)
	if !strings.Contains(nudged, fmt.Sprintf("连续 %d 次", cloudAgentCompletionNudgeLimit)) || !strings.Contains(nudged, "wait_subagents") {
		t.Fatalf("催办用尽时要说明次数与未完成项：%q", nudged)
	}
}

// 用户刚插话是"软阻塞"：finish_run 不催办、不计额度，先让插话进上下文（与纯文本收尾同口径）。
func TestCloudAgentFinishRunDefersToPendingInterjection(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	if _, err := s.InterjectCloudAgent("user", root.ID, "msg-1", "先别收尾，还要再看一眼"); err != nil {
		t.Fatal(err)
	}
	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-1", "finish_run", `{"summary":"我认为完成了。"}`)
	if run.Status != "running" {
		t.Fatalf("有待送达插话时不该收尾：status=%s", run.Status)
	}
	if state.CompletionNudges != 0 || state.CompletionNudgeAttempt != 0 {
		t.Fatalf("软阻塞不该消耗催办额度：total=%d attempt=%d", state.CompletionNudges, state.CompletionNudgeAttempt)
	}
	if blocked := agentEventPayloads(state, "completion_blocked"); len(blocked) != 0 {
		t.Fatalf("软阻塞不该落控制事件（插话本身在界面上已可见）：%+v", blocked)
	}
	completed := agentEventPayloads(state, "tool_completed")
	if len(completed) != 1 {
		t.Fatalf("finish_run 应回一条回执：%+v", completed)
	}
	detail, _ := completed[0]["result"].(map[string]any)
	if detail["requiredAction"] != "answer_interjection" {
		t.Fatalf("回执要指向插话：%+v", completed[0])
	}
}

// 收尾时"本批剩余调用"必须逐个拿到回执：漏一个就会让落库的 canonical 自相矛盾
// （assistant(tool_calls) 里的 tool_call_id 没有对应的 tool 消息）。
func TestCloudAgentCompletionReceiptsEveryRemainingCall(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	finish := cloudAgentCall{ID: "finish-1"}
	finish.Function.Name, finish.Function.Arguments = "finish_run", `{"summary":"分析完成。"}`
	read := cloudAgentCall{ID: "read-1"}
	read.Function.Name, read.Function.Arguments = "canvas_get_state", `{}`
	run, state := agentSettleBatch(t, s, db, root.ID, []cloudAgentCall{finish, read})
	if run.Status != "completed" {
		t.Fatalf("闸门通过时应收尾：status=%s", run.Status)
	}
	if state.CallIndex != len(state.Calls) {
		t.Fatalf("本批剩余调用没有被逐个收尾：callIndex=%d calls=%d", state.CallIndex, len(state.Calls))
	}
	if completed := agentEventPayloads(state, "tool_completed"); len(completed) != 1 || completed[0]["toolName"] != "finish_run" {
		t.Fatalf("收尾调用应有成功回执：%+v", completed)
	}
	// 剩余调用按既有口径收成"本轮已结束"回执（tool_failed + skipped）。
	skipped := agentEventPayloads(state, "tool_failed")
	if len(skipped) != 1 || skipped[0]["toolName"] != "canvas_get_state" {
		t.Fatalf("剩余调用应有一条未执行回执：%+v", skipped)
	}
	detail, _ := skipped[0]["result"].(map[string]any)
	if detail["skipped"] != true {
		t.Fatalf("剩余调用的回执要标 skipped：%+v", skipped[0])
	}
	paired := map[string]bool{}
	for _, message := range state.Canonical.Messages {
		if stringField(message, "role") == "tool" {
			paired[stringField(message, "tool_call_id")] = true
		}
	}
	for _, id := range []string{"finish-1", "read-1"} {
		if !paired[id] {
			t.Fatalf("tool_call_id %s 没有回执：%+v", id, state.Canonical.Messages)
		}
	}
}
