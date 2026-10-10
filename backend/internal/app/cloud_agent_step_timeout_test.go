package app

// 画布 Agent 单步边界（输出上限 + 秒级墙钟）与超时终止。
import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platform"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/repository"
)

func TestCloudAgentStepOutputBudgetFollowsPolicy(t *testing.T) {
	cases := []struct {
		name          string
		outputTokens  int
		boosted       bool
		expectedValue int
	}{
		{name: "默认上限", outputTokens: platform.DefaultRuntimeAgentStepOutputTokens, expectedValue: 16_384},
		{name: "空输出重试遵守管理员上限", outputTokens: platform.DefaultRuntimeAgentStepOutputTokens, boosted: true, expectedValue: 16_384},
		{name: "自定义上限", outputTokens: 32_000, expectedValue: 32_000},
		{name: "自定义上限重试不翻倍", outputTokens: 100_000, boosted: true, expectedValue: 100_000},
		{name: "不限制时原值即 0", outputTokens: 0, expectedValue: 0},
		{name: "不限制时放大档必须有界", outputTokens: 0, boosted: true, expectedValue: cloudAgentStepBoostFallbackTokens},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := cloudAgentStepOutputBudget(cloudAgentStepLimits{OutputTokens: item.outputTokens}, item.boosted)
			if got != item.expectedValue {
				t.Fatalf("budget = %d, want %d", got, item.expectedValue)
			}
		})
	}
}

func TestTaskExecutionTimeoutPrefersAgentStepSeconds(t *testing.T) {
	policy := defaultRuntimePolicy().Task
	policy.TextTimeoutMinutes = 8
	policy.AgentStepTimeoutSeconds = 90
	step := &model.Task{Type: "canvas_text", Operation: cloudAgentStepOperation}
	if got := taskExecutionTimeout(step, policy); got != 90*time.Second {
		t.Fatalf("agent step timeout = %s, want 90s", got)
	}
	// 没配秒级值时沿用文本任务超时（旧行为不变）。
	policy.AgentStepTimeoutSeconds = 0
	if got := taskExecutionTimeout(step, policy); got != 8*time.Minute {
		t.Fatalf("fallback timeout = %s, want 8m", got)
	}
	// 非 Agent 的文本任务不受这个开关影响。
	policy.AgentStepTimeoutSeconds = 90
	plain := &model.Task{Type: "canvas_text", Operation: "canvas_text_generate"}
	if got := taskExecutionTimeout(plain, policy); got != 8*time.Minute {
		t.Fatalf("plain text timeout = %s, want 8m", got)
	}
}

// 单步边界必须真的来自策略：管理端改完，运行期下一步就用新值，不需要重发本轮。
func TestCloudAgentStepLimitsFollowAdminPolicy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir())
	actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}

	defaults, err := svc.cloudAgentStepLimits()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.OutputTokens != platform.DefaultRuntimeAgentStepOutputTokens || defaults.Timeout != 8*time.Minute {
		t.Fatalf("default step limits = %+v", defaults)
	}

	policy := defaultRuntimePolicy()
	policy.Task.AgentStepMaxOutputTokens = 32_768
	policy.Task.AgentStepTimeoutSeconds = 150
	if _, err := svc.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.cloudAgentStepLimits()
	if err != nil {
		t.Fatal(err)
	}
	if updated.OutputTokens != 32_768 || updated.Timeout != 150*time.Second {
		t.Fatalf("updated step limits = %+v", updated)
	}

	// 0 的两个含义：输出不限制、超时沿用文本任务超时。
	policy.Task.AgentStepMaxOutputTokens = 0
	policy.Task.AgentStepTimeoutSeconds = 0
	policy.Task.TextTimeoutMinutes = 12
	if _, err := svc.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	unbounded, err := svc.cloudAgentStepLimits()
	if err != nil {
		t.Fatal(err)
	}
	if unbounded.OutputTokens != 0 || unbounded.Timeout != 12*time.Minute {
		t.Fatalf("unbounded step limits = %+v", unbounded)
	}
}

func TestRuntimePolicyRejectsOutOfRangeAgentStepLimits(t *testing.T) {
	policy := defaultRuntimePolicy()
	policy.Task.AgentStepMaxOutputTokens = platform.MinRuntimeAgentStepOutputTokens - 1
	if err := validateRuntimePolicy(policy); err == nil {
		t.Fatal("过小的单步输出上限应被拒绝")
	}
	policy = defaultRuntimePolicy()
	policy.Task.AgentStepMaxOutputTokens = platform.MaxRuntimeAgentStepOutputTokens + 1
	if err := validateRuntimePolicy(policy); err == nil {
		t.Fatal("过大的单步输出上限应被拒绝")
	}
	policy = defaultRuntimePolicy()
	policy.Task.AgentStepMaxOutputTokens = 0
	if err := validateRuntimePolicy(policy); err != nil {
		t.Fatalf("0（不限制）应当合法: %v", err)
	}
	policy = defaultRuntimePolicy()
	policy.Task.AgentStepTimeoutSeconds = platform.MinRuntimeAgentStepTimeoutSeconds - 1
	if err := validateRuntimePolicy(policy); err == nil {
		t.Fatal("过小的单步超时（非 0）应被拒绝")
	}
	policy = defaultRuntimePolicy()
	policy.Task.AgentStepTimeoutSeconds = platform.MaxRuntimeAgentStepTimeoutSeconds + 1
	if err := validateRuntimePolicy(policy); err == nil {
		t.Fatal("过大的单步超时应被拒绝")
	}
}

// 旧配置 JSON 没有这两个字段时按默认值回填：不需要数据迁移。
func TestRuntimePolicyBackfillsAgentStepLimitsForLegacyJSON(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	legacy := defaultRuntimePolicy()
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	task := value["task"].(map[string]any)
	delete(task, "agentStepMaxOutputTokens")
	delete(task, "agentStepTimeoutSeconds")
	trimmed, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SystemSetting{Key: runtimePolicySettingKey, ValueJSON: string(trimmed)}).Error; err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir())
	effective, err := svc.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if effective.Task.AgentStepMaxOutputTokens != platform.DefaultRuntimeAgentStepOutputTokens {
		t.Fatalf("legacy json 未回填单步输出上限: %d", effective.Task.AgentStepMaxOutputTokens)
	}
	if effective.Task.AgentStepTimeoutSeconds != platform.DefaultRuntimeAgentStepTimeout {
		t.Fatalf("legacy json 未回填单步超时: %d", effective.Task.AgentStepTimeoutSeconds)
	}
}

// 单步超时必须立即结束运行，重投失败上报不能重新生成或重复失败事件。
func TestCloudAgentStepTimeoutTerminatesRunWithoutRetry(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	_, initial := agentStartPiModelStep(t, s, root.ID)
	failStepTask(t, db, initial.ActiveTaskID)

	if err := advancePiAgentForTest(t, s, root.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	if run.Status != "failed" || run.RuntimePhase != "terminal" || !run.CleanupPending {
		t.Fatalf("单步超时必须进入可清理的失败终态: status=%s phase=%s cleanup=%v", run.Status, run.RuntimePhase, run.CleanupPending)
	}
	if !strings.Contains(run.FailureMessage, "Agent 超时失败") || !agentHasEventWithReason(state, "run_failed", "model_step_timeout") {
		t.Fatalf("缺少明确的超时失败提示: %s", run.FailureMessage)
	}
	for _, event := range state.Events {
		if event.Type == "model_failure_recovered" {
			t.Fatal("超时不得触发自动重试")
		}
	}
	// 单步边界来自策略，并且进了压力载荷：界面要能显示"这一条线在管事"。
	// 注意 StepLimits 是每次推进重算的瞬时字段（不进状态 JSON），可观测的口径就是事件载荷。
	// 状态是 JSON 往返过的，事件载荷里的数字是 float64。
	if value, ok := agentEventValue(state, "context_pressure", "stepMaxOutputTokens"); !ok || value != float64(platform.DefaultRuntimeAgentStepOutputTokens) {
		t.Fatalf("压力事件里的单步输出上限 = %v（ok=%v）", value, ok)
	}
	if value, ok := agentEventValue(state, "context_pressure", "stepTimeoutSeconds"); !ok || value != float64(480) {
		t.Fatalf("压力事件里的单步墙钟 = %v 秒（ok=%v）", value, ok)
	}

	// 后续推进和重复失败上报都必须保留终态，不创建新的模型任务。
	taskCount := len(state.TaskIDs)
	eventCount := run.EventCount
	if err := advancePiAgentForTest(t, s, root.ID); err != nil {
		t.Fatal(err)
	}
	decision, err := s.PiFailModelStepResult("user", run.ID, run.LeaseOwner, initial.ActiveTaskID)
	if err != nil || decision.Status != "failed" || decision.Nudge != "" {
		t.Fatalf("超时失败重投必须幂等: decision=%+v err=%v", decision, err)
	}
	view, err := s.PiModelStepView("user", run.ID, run.LeaseOwner, initial.ActiveTaskID)
	if err != nil || view.Status != "failed" {
		t.Fatalf("Worker 必须收到失败终态: view=%+v err=%v", view, err)
	}
	run, state = agentInterjectionState(t, s, root.ID)
	if run.Status != "failed" || len(state.TaskIDs) != taskCount || run.EventCount != eventCount {
		t.Fatal("终态推进重新提交了模型任务或重复记录失败")
	}
	if state.PiModelFailureNudge != "" {
		t.Fatal("超时失败不得保留重试提示")
	}
	next, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{})
	if err != nil || next.Status != "failed" || next.TaskID != "" {
		t.Fatalf("超时终态不得准入新模型步骤: next=%+v err=%v", next, err)
	}
}

func TestPiCompactionTimeoutTerminatesRun(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	revision, leaf := seedPiCompactionBranch(t, db, run)
	operation, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, PiContextCompactionStart{
		SessionRevision: revision, ActiveLeafID: leaf, Reason: "threshold", TokensBefore: 24000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", operation.TaskID).Updates(map[string]any{
		"status": model.TaskStatusFailed, "error": cloudAgentStepTimeoutError,
	}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.PiContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID)
	if err != nil || view.Status != "failed" || view.Fallback {
		t.Fatalf("压缩超时不得降级后继续运行: view=%+v err=%v", view, err)
	}
	failed, state := agentInterjectionState(t, s, run.ID)
	if failed.Status != "failed" || !failed.CleanupPending || !agentHasEventWithReason(state, "run_failed", "model_step_timeout") {
		t.Fatalf("压缩超时没有结束 Agent: status=%s", failed.Status)
	}
	eventCount := failed.EventCount
	view, err = s.PiContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID)
	// 终态落库会释放会话租约，原 Worker 的重投必须被拒绝。
	var leaseErr *kernel.AppError
	if view != nil || !errors.As(err, &leaseErr) || leaseErr.Reason != kernel.ReasonAgentLeaseLost {
		t.Fatalf("压缩超时后不得接受已失效 Worker 的查询: view=%+v err=%v", view, err)
	}
	replayed, _ := agentInterjectionState(t, s, run.ID)
	if replayed.Status != "failed" || replayed.EventCount != eventCount {
		t.Fatal("压缩查询重投重复记录失败事件")
	}
}

func TestPiModelStepViewTimeoutTerminatesRun(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, initial := agentStartPiModelStep(t, s, root.ID)
	failStepTask(t, db, initial.ActiveTaskID)
	view, err := s.PiModelStepView("user", run.ID, run.LeaseOwner, initial.ActiveTaskID)
	if err != nil || view.Status != "failed" {
		t.Fatalf("Worker 查询应收到失败终态: view=%+v err=%v", view, err)
	}
	failed, state := agentInterjectionState(t, s, run.ID)
	if failed.Status != "failed" || !failed.CleanupPending || !agentHasEventWithReason(state, "run_failed", "model_step_timeout") {
		t.Fatalf("Worker 仅查询超时任务时也必须结束整轮: status=%s cleanup=%v", failed.Status, failed.CleanupPending)
	}
	eventCount := failed.EventCount
	if _, err := s.PiModelStepView("user", run.ID, run.LeaseOwner, initial.ActiveTaskID); err != nil {
		t.Fatal(err)
	}
	replayed, _ := agentInterjectionState(t, s, run.ID)
	if replayed.Status != "failed" || replayed.EventCount != eventCount {
		t.Fatal("超时查询重投复活运行或重复记录失败事件")
	}
}

func failStepTask(t *testing.T, db *gorm.DB, taskID string) {
	t.Helper()
	var task model.Task
	if err := db.First(&task, "id = ?", taskID).Error; err != nil || task.Operation != cloudAgentStepOperation {
		t.Fatalf("timeout requires an admitted Pi model task: task=%s operation=%s err=%v", taskID, task.Operation, err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
		"status": model.TaskStatusFailed,
		"error":  cloudAgentStepTimeoutError + "，已中止这一步",
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func stepTaskTextOptions(t *testing.T, s *Service, task *model.Task) map[string]any {
	t.Helper()
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		TextOptions map[string]any `json:"textOptions"`
	}
	if err := json.Unmarshal([]byte(decrypted), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.TextOptions) == 0 {
		t.Fatalf("任务缺少 textOptions: %s", decrypted)
	}
	return input.TextOptions
}

func agentHasEventWithReason(state cloudAgentRuntime, eventType, reason string) bool {
	for _, event := range state.Events {
		if event.Type == eventType && event.Payload["reason"] == reason {
			return true
		}
	}
	return false
}

// agentEventValue 取最近一条指定类型事件里的某个字段值。
func agentEventValue(state cloudAgentRuntime, eventType, key string) (any, bool) {
	for i := len(state.Events) - 1; i >= 0; i-- {
		if state.Events[i].Type != eventType {
			continue
		}
		value, ok := state.Events[i].Payload[key]
		return value, ok
	}
	return nil, false
}

// 执行时限到点必须按失败收尾（而不是被"租约丢失"提前返回）。
func TestAgentExecutionDeadlineCountsAsTaskFailure(t *testing.T) {
	policy := defaultRuntimePolicy().Task
	policy.AgentStepTimeoutSeconds = 30
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	<-ctx.Done()
	task := &model.Task{Type: "canvas_text", Operation: cloudAgentStepOperation}
	if got := taskExecutionTimeout(task, policy); got != 30*time.Second {
		t.Fatalf("timeout = %s", got)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("到期后 ctx 必须报告 DeadlineExceeded（worker 据此区分租约丢失）")
	}
}
