package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// 首步合同（路线图 C2）专项：冻结 Harness 正文、原子换单、重复投递、退款门槛。
//
// 这些用例的共同前提是**走真实建 run 路径**：C2 的验收对象正是"建 run 只创建不可领取的
// 占位任务与合同快照"这一事实，手工拼出来的运行无法证明它。

// cloudAgentTestString 取字符串指针（快照里的可选层）。
func cloudAgentTestString(text string) *string { return &text }

// cloudAgentOrderDigest 把"该 run 的占位任务 + 它名下所有订单"打平成一行，用于失败信息。
//
// 只写"未退还"看不出是谁没退：占位任务的 billing_order_id、订单的真实状态与金额
// 必须一起看，才能区分"根本没退"和"退的是另一笔"。
func cloudAgentOrderDigest(t *testing.T, s *Service, db *gorm.DB, runID string) string {
	t.Helper()
	row, err := s.repo.TaskForUser("user", runID)
	taskInfo := "任务读取失败"
	if err == nil {
		taskInfo = fmt.Sprintf("任务=%s/%s/order=%s", row.ID, row.Operation, row.BillingOrderID)
	}
	var orders []model.BillingOrder
	db.Where("task_id = ?", runID).Find(&orders)
	parts := make([]string, 0, len(orders))
	for _, order := range orders {
		parts = append(parts, fmt.Sprintf("%s/%s/%d", order.ID, order.Status, order.AmountMicrocredits))
	}
	return fmt.Sprintf("%s；订单=%d 笔[%s]", taskInfo, len(orders), strings.Join(parts, ", "))
}

// piFirstStepFixture 建一条"等首个模型步"的运行，并完成一次真实领取。
func piFirstStepFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	s, db := agentRunFixture(t)
	view, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatalf("建 run 失败: %v", err)
	}
	claimed, err := s.ClaimPiAgent("worker-a")
	if err != nil || claimed == nil || claimed.RunID != view.ID {
		t.Fatalf("领取 pi 运行失败: %v", err)
	}
	run, state := reloadPiRun(t, s, view.ID)
	if !cloudAgentAwaitingFirstStep(state) {
		t.Fatalf("新建运行必须停在首步前：contract=%d phase=%q", state.ContractVersion, state.Phase)
	}
	return s, db, run, state
}

// startPiAgentFirstStep 让一个刚建好的 v2 运行走完真实的首个模型步（换单），返回启动后的运行与状态。
//
// 建 run 现在只落"占位 + 快照"，运行停在 awaiting_first_step；此时任何检查点或工具批次都会被
// 校验拒绝（"首步前不允许有任务历史"）。要检验首步**之后**业务的用例必须先合法启动运行 ——
// 与生产走同一入口（`PiModelStep`），而不是把待处理调用硬塞进一个还没启动的状态。
func startPiAgentFirstStep(t *testing.T, s *Service, runID string) (*model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	claimed, err := s.ClaimPiAgent("worker-a")
	if err != nil || claimed == nil || claimed.RunID != runID {
		t.Fatalf("领取 pi 运行失败: claimed=%+v err=%v", claimed, err)
	}
	run, state := reloadPiRun(t, s, runID)
	request, _ := piFirstStepRequest(state)
	if _, err := s.PiModelStep("user", runID, "worker-a", request); err != nil {
		t.Fatalf("首个模型步失败: %v", err)
	}
	run, state = reloadPiRun(t, s, runID)
	if cloudAgentAwaitingFirstStep(state) {
		t.Fatalf("首个模型步之后运行仍停在首步前：phase=%q tasks=%v", state.Phase, state.TaskIDs)
	}
	return run, state
}

func TestPiRunSnapshotAndFirstStepExposeEligibleConcreteTools(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, _ := agentMediaRun(t, s, args, "auto")
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	toolNames := make([]string, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	for _, child := range []string{"canvas_apply_ops", "canvas_get_state", "generate_media"} {
		if !containsToolName(toolNames, child) {
			t.Fatalf("Pi session registry lacks eligible child tool %q: %v", child, toolNames)
		}
	}
	if len(snapshot.Opened) != 0 {
		t.Fatalf("new run must not have opened child categories: %v", snapshot.Opened)
	}
	_, state := reloadPiRun(t, s, run.ID)
	first, _ := piFirstStepRequest(state)
	firstNames := cloudAgentToolNames(first.Canonical.Tools)
	if !containsToolName(firstNames, "canvas_apply_ops") || containsToolName(firstNames, "agent_tools_canvas_edit") {
		t.Fatalf("first model request must expose concrete tools without category entries: %v", firstNames)
	}
}

// piFirstStepRequest 组装一次合法的首步请求：系统提示 = 服务端策略 + Harness 装配结果。
func piFirstStepRequest(state *cloudAgentRuntime) (PiModelStepRequest, *cloudAgentHarnessSnapshot) {
	parts := &cloudAgentHarnessSnapshot{
		System:       cloudAgentTestString("工作区规则"),
		AppendSystem: cloudAgentTestString("追加说明"),
		Context:      []cloudAgentHarnessPart{{Name: "AGENTS.md", Text: "项目约定"}},
	}
	canonical := state.Canonical
	canonical.SystemPrompt = cloudAgentRenderTestPrompt(state.Canonical.SystemPrompt, parts)
	// 首步暴露本轮所有合格的具体工具：与 PiModelStep 的披露校验同一份算法。
	canonical.Tools = cloudAgentVisibleTools(state.Canonical.Tools, "", nil, nil)
	return PiModelStepRequest{Canonical: canonical, HarnessHash: cloudAgentHarnessBodyDigest(parts), Harness: parts}, parts
}

// cloudAgentRenderTestPrompt 复刻 Node 侧 renderSystemPrompt 的装配形状（策略在前，Harness 在后）。
func cloudAgentRenderTestPrompt(policy string, parts *cloudAgentHarnessSnapshot) string {
	text := policy
	if parts.System != nil {
		text += "\n\n" + *parts.System
	}
	for _, entry := range parts.Context {
		text += "\n\n## Workspace " + entry.Name + "\n" + entry.Text
	}
	if parts.AppendSystem != nil {
		text += "\n\n" + *parts.AppendSystem
	}
	return text
}

// TestPiFirstStepSwapsHoldingReservationForRealTask 是 C2 的核心断言：
// 建 run 时的占位预留被退掉，真实首步任务与它自己的预留在**同一事务**里落库。
func TestPiFirstStepSwapsHoldingReservationForRealTask(t *testing.T) {
	s, db, run, state := piFirstStepFixture(t)

	// 建 run 之后：占位行不可领取，账上恰好一笔占位预留。
	placeholder, err := s.repo.TaskForUser("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if placeholder.Status != model.TaskStatusHolding {
		t.Fatalf("占位任务状态 = %q，期望 %q", placeholder.Status, model.TaskStatusHolding)
	}
	if placeholder.Operation != cloudAgentHoldingOperation {
		t.Fatalf("占位任务操作名 = %q，期望 %q", placeholder.Operation, cloudAgentHoldingOperation)
	}
	if placeholder.BillingOrderID == "" {
		t.Fatal("占位任务必须承载本轮报价预留")
	}
	if orders := openOrdersFor(t, db, run.ID); len(orders) != 1 {
		t.Fatalf("建 run 后应有 1 笔占位预留，实际 %d 笔", len(orders))
	}
	// 占位任务不能占用"活动任务"额度：否则每一步都会把自己的占位算成并发占用。
	if count, err := s.repo.ActiveTaskCountForUser("user"); err != nil || count != 0 {
		t.Fatalf("占位任务不得计入活动任务：count=%d err=%v", count, err)
	}
	assertReservationInvariant(t, db, "user")

	req, parts := piFirstStepRequest(state)
	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, req)
	if err != nil {
		t.Fatalf("首个模型步换单失败: %v", err)
	}
	if step.TaskID == "" || step.TaskID == run.ID {
		t.Fatalf("首步任务 ID 异常: %q（占位行是 %q）", step.TaskID, run.ID)
	}

	// 真实首步任务：可执行、归属本运行、走的是步进操作名。
	first, err := s.repo.TaskForUser("user", step.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Operation != cloudAgentStepOperation {
		t.Fatalf("首步任务操作名 = %q，期望 %q", first.Operation, cloudAgentStepOperation)
	}
	if first.Status != model.TaskStatusQueued {
		t.Fatalf("首步任务状态 = %q，期望 queued", first.Status)
	}
	if first.AgentRunID != run.ID || first.BillingOrderID == "" {
		t.Fatalf("首步任务未绑定运行或缺少预授权：run=%q order=%q", first.AgentRunID, first.BillingOrderID)
	}

	// 阶段推进与任务同事务：检查点里不再停在 awaiting_first_step，且提示合同已冻结。
	_, rolled := reloadPiRun(t, s, run.ID)
	if cloudAgentAwaitingFirstStep(rolled) {
		t.Fatal("首步已提交，运行不该还停在 awaiting_first_step")
	}
	if rolled.ActiveTaskID != step.TaskID || len(rolled.TaskIDs) != 1 || rolled.TaskIDs[0] != step.TaskID {
		t.Fatalf("检查点任务历史异常：active=%q tasks=%v", rolled.ActiveTaskID, rolled.TaskIDs)
	}
	if rolled.Snapshot == nil || rolled.Snapshot.HarnessHash != cloudAgentHarnessBodyDigest(parts) {
		t.Fatal("首步必须把 Harness 正文身份固化进快照")
	}
	if rolled.Snapshot.Harness == nil || rolled.Snapshot.AssembledPromptHash == "" {
		t.Fatal("首步必须同时固化 Harness 正文与装配后的系统提示身份")
	}

	// 钱：占位预留已退，真实首步恰好一笔未结。
	var placeholderOrder model.BillingOrder
	if err := db.First(&placeholderOrder, "id = ?", placeholder.BillingOrderID).Error; err != nil {
		t.Fatal(err)
	}
	if placeholderOrder.Status != model.BillingStatusRefunded {
		t.Fatalf("占位预留状态 = %q，期望 refunded", placeholderOrder.Status)
	}
	if left := openOrdersFor(t, db, run.ID); len(left) != 0 {
		t.Fatalf("换单后占位行不得留下未结订单，实际 %d 笔", len(left))
	}
	if swapped := openOrdersFor(t, db, step.TaskID); len(swapped) != 1 {
		t.Fatalf("真实首步应有且仅有 1 笔预授权，实际 %d 笔", len(swapped))
	}
	assertReservationInvariant(t, db, "user")
}

func TestPiModelStepRetryIsIdempotentAndRejectsPayloadMismatch(t *testing.T) {
	s, _, run, state := piFirstStepFixture(t)
	request, _ := piFirstStepRequest(state)

	first, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
	if err != nil {
		t.Fatalf("first Pi model step failed: %v", err)
	}
	retry, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
	if err != nil || retry.TaskID != first.TaskID {
		t.Fatalf("identical model-step retry = task %q, error %v; want task %q", retry.TaskID, err, first.TaskID)
	}

	changed := request
	changed.HarnessHash += "changed"
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, changed); err == nil {
		t.Fatal("same active model step accepted a different request fingerprint")
	}
}

// TestPiFirstStepRequiresPromptBodyMatchingItsHash：正文缺失或与报出的哈希不一致都必须拒绝。
//
// 只报哈希等于自证：写进快照的正文必须与它声称的身份是同一份事实，否则"恢复时按原样重建"
// 就成了空话。三类拒绝都不得改动运行或账目。
func TestPiFirstStepRequiresPromptBodyMatchingItsHash(t *testing.T) {
	s, db, run, state := piFirstStepFixture(t)

	noBody, _ := piFirstStepRequest(state)
	noBody.Harness = nil
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, noBody); err == nil {
		t.Fatal("缺少 Harness 正文的首步被接受")
	}

	mismatched, _ := piFirstStepRequest(state)
	mismatched.Harness = &cloudAgentHarnessSnapshot{
		System:  cloudAgentTestString("另一份正文"),
		Context: []cloudAgentHarnessPart{},
	}
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, mismatched); err == nil {
		t.Fatal("正文与哈希不一致的首步被接受")
	}

	_, unchanged := reloadPiRun(t, s, run.ID)
	if !cloudAgentAwaitingFirstStep(unchanged) {
		t.Fatal("被拒绝的首步不得推进阶段")
	}
	if updated, err := s.repo.TaskForUser("user", run.ID); err != nil || updated.Status != model.TaskStatusHolding {
		t.Fatalf("被拒绝的首步不得改动占位任务：status=%q err=%v", updated.Status, err)
	}
	if orders := openOrdersFor(t, db, run.ID); len(orders) != 1 {
		t.Fatalf("被拒绝的首步不得动占位预留，实际 %d 笔", len(orders))
	}
	assertReservationInvariant(t, db, "user")

	// 合法请求仍然成功：拒绝路径没有把运行弄坏。
	good, _ := piFirstStepRequest(state)
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, good); err != nil {
		t.Fatalf("合法首步被拒绝: %v", err)
	}
}

// TestPiLaterStepRejectsChangedPromptContract：首步之后提示只能比对，不能换。
//
// 两条路都要拦住：换了 Harness 本身（正文 + 哈希），以及 Harness 没变但装配出来的系统提示
// 变了（装配函数被改或请求被篡改）。
func TestPiLaterStepRejectsChangedPromptContract(t *testing.T) {
	s, db, run, state := piFirstStepFixture(t)

	req, _ := piFirstStepRequest(state)
	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, req)
	if err != nil {
		t.Fatalf("首个模型步失败: %v", err)
	}
	// 首步任务终结并确认后才允许进入第二步。
	if err := db.Model(&model.Task{}).Where("id = ?", step.TaskID).Update("status", model.TaskStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.PiModelStepAck("user", run.ID, run.LeaseOwner, step.TaskID); err != nil {
		t.Fatalf("确认首步失败: %v", err)
	}

	changed, changedParts := piFirstStepRequest(state)
	changedParts.System = cloudAgentTestString("被改过的系统层")
	changed.Harness = changedParts
	changed.HarnessHash = cloudAgentHarnessBodyDigest(changedParts)
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, changed); err == nil {
		t.Fatal("第二步接受了一份新的 Harness")
	}

	rewritten, _ := piFirstStepRequest(state)
	rewritten.Canonical.SystemPrompt = "另一段前置说明\n\n" + rewritten.Canonical.SystemPrompt
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, rewritten); err == nil {
		t.Fatal("第二步接受了被改写的系统提示")
	}

	// 原样重放仍然通过：上面的拒绝不能把运行推进到坏状态。
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, req); err != nil {
		t.Fatalf("第二步原样重放被拒绝: %v", err)
	}
}

// TestPiFirstStepAdmissionFailureRefundsPlaceholder：首步准入失败必须**当场**给终态并退预留。
//
// 只写 failed 会把退款推给看门狗（未领取 30 分钟），用户那一刻看到的是失败 + 一笔仍冻结的钱。
func TestPiFirstStepAdmissionFailureRefundsPlaceholder(t *testing.T) {
	s, db, run, state := piFirstStepFixture(t)
	// 让首步准入失败：占满并发任务额度（与"用户同时排了很多任务"同一条路径）。
	//
	// 刻意不用"清空余额"当触发：换单事务会先退还占位预留，退回的钱又足以支付首步报价，
	// 于是准入反而成功 —— 那是换单的正常语义，不是失败路径。
	placeholder, err := s.repo.TaskForUser("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < policy.Task.ActiveTaskLimit; index++ {
		// 克隆占位行只为了拿到合法的非空字段，避免临时拼一条缺列的任务。
		filler := *placeholder
		filler.ID = fmt.Sprintf("pi-admission-filler-%d", index)
		filler.Status = model.TaskStatusQueued
		filler.Operation = cloudAgentStepOperation
		filler.AgentRunID = ""
		filler.BillingOrderID = ""
		if err := db.Create(&filler).Error; err != nil {
			t.Fatal(err)
		}
	}

	req, _ := piFirstStepRequest(state)
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, req); err == nil {
		t.Fatal("余额不足的首步被接受")
	}

	latest, after := reloadPiRun(t, s, run.ID)
	if latest.Status != "failed" {
		t.Fatalf("首步准入失败必须给出确定终态，实际 %q", latest.Status)
	}
	if !latest.CleanupPending {
		t.Fatal("首步准入失败必须交给清理路径收尾")
	}
	if len(after.TaskIDs) != 0 {
		t.Fatalf("失败的首步不该留下任务历史: %v", after.TaskIDs)
	}
	if left := openOrdersFor(t, db, run.ID); len(left) != 0 {
		t.Fatal("首步准入失败必须退还占位预留：" + cloudAgentOrderDigest(t, s, db, run.ID))
	}
	assertReservationInvariant(t, db, "user")
}

// TestPiCleanupDrainRefundsUnstartedRun：看门狗只写终态，真正的收尾由排空入口补做。
//
// 没有这一步，被清扫掉的运行会把占位预留永远冻在账上 —— 没有任何一方会再回来退它。
// 用例刻意走**真实清扫原语**：手工改状态会绕过 cloudAgentSave，造出一个解码失败的行，
// 那既不是生产形态，也会把"退款是否发生"这个问题和"状态能不能解码"混在一起。
func TestPiCleanupDrainRefundsUnstartedRun(t *testing.T) {
	s, db := agentRunFixture(t)
	view, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatalf("建 run 失败: %v", err)
	}
	// 回拨创建时间，模拟"建 run 后长时间没有任何 Pi worker 领取"。
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", view.ID).
		Update("created_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	run, state := reloadPiRun(t, s, view.ID)
	if !cloudAgentAwaitingFirstStep(state) {
		t.Fatalf("前置：新建运行必须停在首步前，实际 phase=%q", state.Phase)
	}
	if orders := openOrdersFor(t, db, run.ID); len(orders) != 1 {
		t.Fatalf("前置：占位预留应仍未结，实际 %d 笔", len(orders))
	}

	swept, err := s.SweepUnclaimedPiAgentRuns()
	if err != nil {
		t.Fatalf("清扫未领取运行失败: %v", err)
	}
	if swept != 1 {
		t.Fatalf("应清扫 1 条未领取运行，实际 %d", swept)
	}
	// 清扫只写终态与收尾标记：这一步不该动账。
	if left := openOrdersFor(t, db, run.ID); len(left) != 1 {
		t.Fatalf("清扫本身不该退还预留，实际剩 %d 笔", len(left))
	}

	cleaned, err := s.DrainPendingPiAgentCleanups()
	if err != nil {
		t.Fatalf("排空待清理运行失败: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("应排空 1 条待清理运行，实际 %d", cleaned)
	}

	latest, _ := reloadPiRun(t, s, run.ID)
	if latest.Status != "failed" || !cloudAgentRunTerminal(latest.Status) {
		t.Fatalf("清扫后的运行必须有确定终态，实际 %q", latest.Status)
	}
	if latest.CleanupPending {
		t.Fatal("清理完成后不得再挂着 CleanupPending")
	}
	if left := openOrdersFor(t, db, run.ID); len(left) != 0 {
		t.Fatal("从未开始的运行必须退还占位预留：" + cloudAgentOrderDigest(t, s, db, run.ID))
	}
	assertReservationInvariant(t, db, "user")
}

// TestCloudAgentHarnessBodyDigestMatchesNodeVector：Go 复算的 Harness 身份必须与 Node 一致。
//
// 期望值取自 `agent/test/prompt-contract.test.ts` 的同名固定向量：两边各写一份常量，
// 任何一侧改了编码（分层顺序、长度前缀、UTF-8 处理）都会有一侧断言变红，
// 而不是等真实运行到首步才以 403 暴露。
func TestCloudAgentHarnessBodyDigestMatchesNodeVector(t *testing.T) {
	parts := &cloudAgentHarnessSnapshot{
		System:       cloudAgentTestString("策略"),
		AppendSystem: cloudAgentTestString("追加"),
		Context:      []cloudAgentHarnessPart{{Name: "AGENTS.md", Text: "约定"}},
	}
	const want = "7c60d043bb93f77da9b71fc218fc9ab1866e29bd308a8ef9b94fe2e60f267d61"
	if got := cloudAgentHarnessBodyDigest(parts); got != want {
		t.Fatalf("Harness 内容身份与 Node 不一致：got=%s want=%s", got, want)
	}
	// 缺失层与显式空串必须得到同一个身份，否则同一份装配会有两个哈希。
	if cloudAgentHarnessBodyDigest(nil) != cloudAgentHarnessBodyDigest(&cloudAgentHarnessSnapshot{Context: []cloudAgentHarnessPart{}}) {
		t.Fatal("nil 与空 Harness 必须得到同一个内容身份")
	}
}

// 压缩摘要任务不是"首个模型步"：它不能消耗占位预留，也不能把运行从 awaiting_first_step 推走。
// 否则压缩后的续跑会走"已冻结合同"分支，而合同从未冻结（AssembledPromptHash 为空），
// 服务端以 403 "Agent runtime first-step contract snapshot is missing" 拒绝（真实 3000 部署复现）。
func TestContextCompactionTaskKeepsFirstStepReservation(t *testing.T) {
	s, db, run, state := piFirstStepFixture(t)
	// 真实流程里 PiBeginContextCompaction 先登记操作，摘要模型调用再入队；
	// 这里保持同样的前置状态，避免摘要任务走普通步进记账分支。
	state.ContextCompaction = &cloudAgentContextCompaction{Status: "requested", PiOperationID: "pi-test-op"}
	if err := s.enqueueCloudAgentContextCompaction(run, state); err != nil {
		t.Fatalf("压缩任务入队失败: %v", err)
	}
	var summary model.Task
	if err := db.Where("agent_run_id = ? AND operation = ?", run.ID, cloudAgentContextCompactionOperation).First(&summary).Error; err != nil {
		t.Fatalf("压缩摘要任务未创建: %v", err)
	}
	_, after := reloadPiRun(t, s, run.ID)
	if !cloudAgentAwaitingFirstStep(after) {
		t.Fatalf("压缩任务推进了首步相位: phase=%q tasks=%v", after.Phase, after.TaskIDs)
	}
	placeholder, err := s.repo.TaskForUser("user", run.ID)
	if err != nil || placeholder.Operation != cloudAgentHoldingOperation || placeholder.Status != model.TaskStatusHolding {
		t.Fatalf("占位预留被压缩任务消耗: %+v err=%v", placeholder, err)
	}
	if after.Snapshot == nil || after.Snapshot.AssembledPromptHash != "" {
		t.Fatalf("首步前不应出现已冻结合同: %+v", after.Snapshot)
	}
}
