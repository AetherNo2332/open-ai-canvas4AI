package app

// 工作项 A：收尾闸门与 runtime 催办的时序语义。
// 依据 token-audit/local-only/HANDOFF-工作项A-完成闸门与催办.md 的五条验收：
// ① 有未对账待办时，候选收尾正文不得作为最终答复发布；
// ② runtime 控制消息不显示成真人 user 消息（事件层用 completion_blocked，前端渲染成控制行）；
// ③ 闸门通过后只发布一次最终 assistant 消息；
// ④ 终止态明确：催办用尽后以 completion_blocked 结束，不无限催办；
// ⑤ 同一个阻塞原因不会无限重复（指纹计数 + 变化即重置）。
import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// agentSettleStep 模拟"模型这一步返回了什么"：把排队中的模型任务标成成功并写入结果，
// 再推进一次运行。text 非空时按"无工具调用的正文"返回；tool 非空时按一次工具调用返回。
func agentSettleStep(t *testing.T, s *Service, db *gorm.DB, runID, text, callID, tool, args string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	_, state := agentInterjectionState(t, s, runID)
	if state.ActiveTaskID == "" {
		if err := s.advanceCloudAgentByID("user", runID); err != nil {
			t.Fatal(err)
		}
		_, state = agentInterjectionState(t, s, runID)
	}
	if state.ActiveTaskID == "" {
		t.Fatalf("本轮没有可完成的模型任务（run=%s）", runID)
	}
	body := map[string]any{"text": text}
	if tool != "" {
		call := cloudAgentCall{ID: callID}
		call.Function.Name, call.Function.Arguments = tool, args
		body["toolCalls"] = []cloudAgentCall{call}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", state.ActiveTaskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": string(encoded),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgentByID("user", runID); err != nil {
		t.Fatal(err)
	}
	if tool != "" {
		// 工具的执行是**下一次转移**：一次推进只处理模型结果并登记本批调用。
		if err := s.advanceCloudAgentByID("user", runID); err != nil {
			t.Fatal(err)
		}
	}
	return agentInterjectionState(t, s, runID)
}

func agentSettleTextStep(t *testing.T, s *Service, db *gorm.DB, runID, text string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	return agentSettleStep(t, s, db, runID, text, "", "", "")
}

func agentSettleToolStep(t *testing.T, s *Service, db *gorm.DB, runID, callID, tool, args string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	return agentSettleStep(t, s, db, runID, "", callID, tool, args)
}

// agentSettleBatch 模拟"一次模型输出里带多个工具调用"：逐个执行（执行是每个调用一次转移）。
func agentSettleBatch(t *testing.T, s *Service, db *gorm.DB, runID string, calls []cloudAgentCall) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	_, state := agentInterjectionState(t, s, runID)
	if state.ActiveTaskID == "" {
		if err := s.advanceCloudAgentByID("user", runID); err != nil {
			t.Fatal(err)
		}
		_, state = agentInterjectionState(t, s, runID)
	}
	if state.ActiveTaskID == "" {
		t.Fatalf("本轮没有可完成的模型任务（run=%s）", runID)
	}
	encoded, err := json.Marshal(map[string]any{"toolCalls": calls})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", state.ActiveTaskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": string(encoded),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 一次转移登记本批调用，之后每个调用各需要一次转移。
	for range len(calls) + 1 {
		if err := s.advanceCloudAgentByID("user", runID); err != nil {
			t.Fatal(err)
		}
	}
	return agentInterjectionState(t, s, runID)
}

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

// ① 有未对账待办时，模型"看起来收尾"的正文不得作为最终答复发布，本轮也不能结束。
func TestCloudAgentPendingPlanBlocksCandidateCompletion(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)

	run, state := agentSettleTextStep(t, s, db, root.ID, "镜头已经全部生成完毕，任务完成。")

	if run.Status != "running" {
		t.Fatalf("待办未对账时不该结束本轮，status=%s", run.Status)
	}
	if replies := agentFinalReplies(state); len(replies) != 0 {
		t.Fatalf("候选收尾正文不该作为最终答复发布：%v", replies)
	}
	// 用户仍然看得到这段内容，但它是"过程说明"而不是最终答复。
	var progress []map[string]any
	for _, payload := range agentEventPayloads(state, "assistant_message") {
		if payload["final"] == false {
			progress = append(progress, payload)
		}
	}
	if len(progress) != 1 || !strings.Contains(stringValue(progress[0]["text"]), "镜头已经全部生成完毕") {
		t.Fatalf("候选正文应以非最终（final=false）形式发布：%+v", progress)
	}
	// 被拦下的原因要作为控制事件落库：用户能看到"为什么它又继续跑了"。
	blocked := agentEventPayloads(state, "completion_blocked")
	if len(blocked) != 1 {
		t.Fatalf("应落一条 completion_blocked 控制事件：%+v", blocked)
	}
	if !strings.Contains(fmt.Sprint(blocked[0]["blockers"]), "pending_plan") {
		t.Fatalf("控制事件要带机器可读的阻塞原因：%+v", blocked[0])
	}
	if !strings.Contains(fmt.Sprint(state.Canonical.Messages), "生成镜头1") {
		t.Fatalf("模型侧仍要拿到未完成项催办：%+v", state.Canonical.Messages)
	}
}

// ③ 闸门通过后只发布一次最终消息，并且本轮就此结束。
func TestCloudAgentCompletionPublishesOneFinalReply(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, state := agentSettleTextStep(t, s, db, root.ID, "分析完成：画布上共有 3 个节点。")

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
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, root.ID)
	if replies := agentFinalReplies(state); len(replies) != 1 {
		t.Fatalf("收尾后不得再发布最终答复：%v", replies)
	}
}

// ④⑤ 同一个阻塞原因不会无限催办：到上限后以 completion_blocked 明确终止。
func TestCloudAgentCompletionNudgeIsBounded(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)

	for attempt := 1; attempt <= cloudAgentCompletionNudgeLimit; attempt++ {
		run, state := agentSettleTextStep(t, s, db, root.ID, fmt.Sprintf("第 %d 次声称完成。", attempt))
		if run.Status != "running" {
			t.Fatalf("第 %d 次仍应有催办机会，status=%s", attempt, run.Status)
		}
		if blocked := agentEventPayloads(state, "completion_blocked"); len(blocked) != attempt {
			t.Fatalf("第 %d 次应累计 %d 条控制事件：%+v", attempt, attempt, blocked)
		}
	}
	// 用尽之后：不再开新的模型调用，而是明确以 completion_blocked 收场。
	run, state := agentSettleTextStep(t, s, db, root.ID, "我认为已经完成。")
	if run.Status != "failed" {
		t.Fatalf("催办用尽后应终止本轮，status=%s", run.Status)
	}
	if replies := agentFinalReplies(state); len(replies) != 0 {
		t.Fatalf("闸门未通过时不得发布最终答复：%v", replies)
	}
	last := agentLastEvent(t, state)
	if last.Type != "run_failed" || stringValue(last.Payload["reason"]) != "completion_blocked" {
		t.Fatalf("终止原因应当是 completion_blocked：%s %+v", last.Type, last.Payload)
	}
	if !strings.Contains(run.FailureMessage, "生成镜头1") {
		t.Fatalf("失败说明要点出未对账的待办：%q", run.FailureMessage)
	}
}

// ⑤ 阻塞原因变了（模型真的推进了清单）时重新给机会：不算重复同一个错误。
func TestCloudAgentCompletionFingerprintResetsAfterPlanProgress(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)

	for attempt := 1; attempt <= cloudAgentCompletionNudgeLimit; attempt++ {
		if run, _ := agentSettleTextStep(t, s, db, root.ID, fmt.Sprintf("第 %d 次声称完成。", attempt)); run.Status != "running" {
			t.Fatalf("第 %d 次不该终止：%s", attempt, run.Status)
		}
	}
	// 模型把清单改成"只剩另一项"，阻塞原因因此变化 → 重新获得催办机会，而不是立刻终止。
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Plan = []cloudAgentPlanItem{
		{ID: "1", Title: "生成镜头1", Status: "done"},
		{ID: "2", Title: "拼接成片", Status: "pending"},
	}
	if err := cloudAgentSave(run, &state); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
	run, state = agentSettleTextStep(t, s, db, root.ID, "镜头1 已完成，继续拼接。")
	if run.Status != "running" {
		t.Fatalf("阻塞原因变化后应重新给一次机会：status=%s", run.Status)
	}
	if !strings.Contains(fmt.Sprint(state.Canonical.Messages), "拼接成片") {
		t.Fatalf("新一轮催办应指向新的未完成项：%+v", state.Canonical.Messages)
	}
}

// 待办全部对账（全部 done）后，闸门放行。
func TestCloudAgentCompletionGateAllowsReconciledPlan(t *testing.T) {
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
	run, state = agentSettleTextStep(t, s, db, root.ID, "镜头1 已生成，本轮完成。")
	if run.Status != "completed" {
		t.Fatalf("已对账的清单应当放行：status=%s", run.Status)
	}
	if replies := agentFinalReplies(state); len(replies) != 1 {
		t.Fatalf("应发布一次最终答复：%v", replies)
	}
}

// A-1 显式完成工具：闸门不通过时返回结构化回执并继续本轮，不发布最终答复。
func TestCloudAgentFinishRunIsBlockedByPendingPlan(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)

	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-1", "finish_run", `{"summary":"镜头已全部生成完毕。"}`)

	if run.Status != "running" {
		t.Fatalf("闸门未通过时 finish_run 不该结束本轮：status=%s", run.Status)
	}
	if replies := agentFinalReplies(state); len(replies) != 0 {
		t.Fatalf("闸门未通过时不得发布最终答复：%v", replies)
	}
	completed := agentEventPayloads(state, "tool_completed")
	if len(completed) != 1 {
		t.Fatalf("finish_run 应返回一条结构化回执：%+v", completed)
	}
	result, _ := completed[0]["result"].(map[string]any)
	if result["completionBlocked"] != true {
		t.Fatalf("回执要说明被闸门拦下：%+v", completed[0])
	}
	if !strings.Contains(fmt.Sprint(result["blockers"]), "pending_plan") {
		t.Fatalf("回执要带机器可读的阻塞原因：%+v", result)
	}
	if blocked := agentEventPayloads(state, "completion_blocked"); len(blocked) != 1 {
		t.Fatalf("应落一条 completion_blocked 控制事件：%+v", blocked)
	}
	// 回执必须进模型上下文，它下一步才知道要做什么。
	if !strings.Contains(fmt.Sprint(state.Canonical.Messages), "completionBlocked") {
		t.Fatalf("结构化回执要进 canonical：%+v", state.Canonical.Messages)
	}
}

// A-1 显式完成工具：闸门通过时发布一次最终答复并收尾。
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

// finish_run 连续被拦下同样受催办额度约束：用尽即以 completion_blocked 终止，
// 不让模型一直重复"申请收尾"（每一次都是真实计费的模型调用）。
func TestCloudAgentFinishRunNudgeIsBounded(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)
	for attempt := 1; attempt <= cloudAgentCompletionNudgeLimit; attempt++ {
		run, _ := agentSettleToolStep(t, s, db, root.ID, fmt.Sprintf("finish-%d", attempt), "finish_run", `{"summary":"我认为完成了。"}`)
		if run.Status != "running" {
			t.Fatalf("第 %d 次仍应有催办机会：status=%s", attempt, run.Status)
		}
	}
	run, state := agentSettleToolStep(t, s, db, root.ID, "finish-final", "finish_run", `{"summary":"我认为完成了。"}`)
	if run.Status != "failed" {
		t.Fatalf("催办用尽后应终止本轮，status=%s", run.Status)
	}
	last := agentLastEvent(t, state)
	if last.Type != "run_failed" || stringValue(last.Payload["reason"]) != "completion_blocked" {
		t.Fatalf("终止原因应当是 completion_blocked：%s %+v", last.Type, last.Payload)
	}
}

// 上一轮的"答复"以最终答复为准：过程说明（final=false）不能被当成上一轮的回答交给下一轮。
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
	s, db, root := reliableAgentRoot(t)
	agentWithPendingPlan(t, s, db, root)
	call := func(id string) cloudAgentCall {
		entry := cloudAgentCall{ID: id}
		entry.Function.Name, entry.Function.Arguments = "finish_run", `{"summary":"我认为完成了。"}`
		return entry
	}
	run, state := agentSettleBatch(t, s, db, root.ID, []cloudAgentCall{call("finish-1"), call("finish-2")})
	if run.Status != "running" {
		t.Fatalf("被闸门拦下时不该收尾：status=%s", run.Status)
	}
	if state.CompletionNudgeAttempt != 1 || state.CompletionNudges != 1 {
		t.Fatalf("同一批只该记一次催办：attempt=%d total=%d", state.CompletionNudgeAttempt, state.CompletionNudges)
	}
	if blocked := agentEventPayloads(state, "completion_blocked"); len(blocked) != 1 {
		t.Fatalf("同一批只该落一条控制事件：%+v", blocked)
	}
	completed := agentEventPayloads(state, "tool_completed")
	if len(completed) != 2 {
		t.Fatalf("两次调用都要有回执：%+v", completed)
	}
	second, _ := completed[1]["result"].(map[string]any)
	if second["duplicate"] != true {
		t.Fatalf("第二次申请应如实说明未重复处理：%+v", completed[1])
	}
}

// 两种停止原因要给出不同的说明：催办用尽 vs 开不起下一次模型调用。
func TestCloudAgentCompletionStopMessagesDiffer(t *testing.T) {
	block := cloudAgentCompletionBlock{Blockers: []cloudAgentCompletionBlocker{{Kind: "pending_plan", Detail: "生成镜头1"}}}
	budget := cloudAgentCompletionExhaustedMessage(block)
	if !strings.Contains(budget, "预算") || strings.Contains(budget, "连续") {
		t.Fatalf("没有步数预算时要说明预算而不是催办次数：%q", budget)
	}
	block.Attempt = cloudAgentCompletionNudgeLimit
	nudged := cloudAgentCompletionExhaustedMessage(block)
	if !strings.Contains(nudged, fmt.Sprintf("连续 %d 次", cloudAgentCompletionNudgeLimit)) || !strings.Contains(nudged, "生成镜头1") {
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
