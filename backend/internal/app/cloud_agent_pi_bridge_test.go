package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/repository"
)

// Pi 协议是 cloud_agent_executions 上唯一的 engine=pi 读路径，此前没有任何测试。
// 夹具直接落库一条已租出的 Pi 运行，让 bridge 的每个入口都跑真实持久化路径。
func piAgentTestFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	const (
		userID = "user"
		owner  = "worker-a"
	)
	req := agentTestRequest()
	req.IdempotencyKey = "pi-test-run"
	runID := cloudAgentID(userID, req.IdempotencyKey)
	// read_only 会裁掉所有写工具，协议测试要同时覆盖写入与只读工具。
	req.PermissionMode = "auto"
	profile := cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")}
	system, policy, err := compileCloudAgentPolicies(req, nil, "", profile)
	if err != nil {
		t.Fatal(err)
	}
	state := cloudAgentRuntime{Request: req, Policy: policy, Profile: profile, Decisions: map[string]string{}}
	// 运行必须至少登记一个已存在的任务 ID，否则恢复校验会拒绝这条状态。
	state.TaskIDs = []string{"pi-root-task"}
	if err := db.Create(&model.Task{ID: "pi-root-task", UserID: userID, ProjectID: req.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"text":""}`}).Error; err != nil {
		t.Fatal(err)
	}
	state.Canonical = cloudAgentCanonicalFor(system, nil, req.Prompt, req, false)
	state.Canonical.Tools = cloudAgentVisibleTools(state.Canonical.Tools, "", nil, nil)
	state.StepLimits, _ = s.cloudAgentStepLimits()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	// 未租出：领取是 Pi 协议的第一步，租约准入测试要覆盖它。
	run := &model.CloudAgentExecution{
		ID: runID, UserID: userID, Status: "running", Engine: "pi", Revision: 1,
		CanvasID: req.CanvasID, ConversationID: runID, StateJSON: string(encoded),
	}
	if err := db.Create(run).Error; err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]any{
		"type": "session", "version": 3, "id": runID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": "canvas://" + req.CanvasID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentPiSession{
		ID: runID, UserID: userID, ConversationID: runID, CanvasID: req.CanvasID,
		FormatVersion: 3, HeaderJSON: string(header), Revision: 1, ActiveRunID: runID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasProject{ID: req.CanvasID, UserID: userID, PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	return s, db, run
}

// piAgentTestLeasedFixture 走真实领取路径，返回已持有租约的运行（revision 已更新）。
func piAgentTestLeasedFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution) {
	t.Helper()
	s, db, _ := piAgentTestFixture(t)
	claimed, err := s.ClaimPiAgent("worker-a")
	if err != nil || claimed == nil {
		t.Fatalf("领取 pi 运行失败: %v", err)
	}
	run, err := s.repo.CloudAgent("user", claimed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.LeaseOwner != "worker-a" {
		t.Fatalf("领取后租约归属错误: %q", run.LeaseOwner)
	}
	return s, db, run
}

func TestPiAgentSnapshotCarriesGoResolvedModelLimits(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 64_000, 8_192)

	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelLimits.ContextWindowTokens != 64_000 || snapshot.ModelLimits.MaxOutputTokens != 8_192 ||
		!snapshot.ModelLimits.Configured || snapshot.ModelLimits.Source != "channel-model" {
		t.Fatalf("Pi model limits did not reflect Go's resolved channel capability: %+v", snapshot.ModelLimits)
	}
}

func TestPiAgentSnapshotCarriesUnifiedCompactionBudget(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 64_000, 8_192)

	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	budget := s.cloudAgentContextBudgetForRequest(snapshot.Request)
	raw, err := json.Marshal(snapshot.ModelLimits)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"overheadTokens":    budget.OverheadTokens,
		"inputBudgetTokens": budget.InputBudgetTokens,
		"compactAtTokens":   budget.CompactAtTokens,
	} {
		got, ok := wire[key].(float64)
		if !ok || int(got) != want {
			t.Fatalf("Pi snapshot %s = %#v, want %d; modelLimits=%s", key, wire[key], want, raw)
		}
	}
}

func TestPiAgentOutputReserveIsAutomaticAndPolicyBounded(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 128_000, 32_768)
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelLimits.MaxOutputTokens != 16_384 || snapshot.ModelLimits.ReservedOutputTokens != 16_384 {
		t.Fatalf("policy did not bound the output and reserve: %+v", snapshot.ModelLimits)
	}
	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: snapshot.Canonical})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.repo.TaskForUser("user", step.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		TextOptions struct {
			MaxOutputTokens int `json:"maxOutputTokens"`
		} `json:"textOptions"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input.TextOptions.MaxOutputTokens != snapshot.ModelLimits.MaxOutputTokens {
		t.Fatalf("request limit differs from Pi: %d", input.TextOptions.MaxOutputTokens)
	}
}

func TestPiAgentUndeclaredOutputUsesBoundedWindowReserve(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 32_000, 0)
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelLimits.MaxOutputTokens != 4_000 || snapshot.ModelLimits.ReservedOutputTokens != 4_000 {
		t.Fatalf("window-derived output budget mismatch: %+v", snapshot.ModelLimits)
	}
}

func TestPiStreamPublisherUpdatesActiveDraft(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: snapshot.Canonical})
	if err != nil {
		t.Fatal(err)
	}
	publisher := newCloudAgentStreamPublisher(s, "user", step.TaskID, "assistant_delta")
	publisher.Publish("你")
	publisher.Publish("好")
	publisher.Close()

	view, err := s.PiModelStepView("user", run.ID, run.LeaseOwner, step.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if view.TextDraft != "你好" {
		t.Fatalf("active draft was not persisted in the runtime: %q", view.TextDraft)
	}
	reasoning := newCloudAgentStreamPublisher(s, "user", step.TaskID, "reasoning_delta")
	reasoning.Publish("思考")
	reasoning.Close()
	view, err = s.PiModelStepView("user", run.ID, run.LeaseOwner, step.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if view.TextDraft != "你好" {
		t.Fatalf("reasoning delta polluted the active draft: %q", view.TextDraft)
	}
}

func TestPiModelStepCarriesEscalatedOutputSettings(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	policy := platform.DefaultRuntimePolicy()
	policy.Task.AgentStepMaxOutputTokens = 0
	policy.Task.AgentStepTimeoutSeconds = 60
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SystemSetting{Key: "runtime_policy", ValueJSON: string(policyBytes)}).Error; err != nil {
		t.Fatal(err)
	}
	// The fixture initialized a default policy cache before inserting this override.
	s.platform = nil
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Policy.ReasoningMode = "deep"
		state.ForceThinkingOff = true
		state.BoostStepOutputBudget = true
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.PiAgentSnapshot("user", run.ID, leased.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	step, err := s.PiModelStep("user", run.ID, leased.LeaseOwner, PiModelStepRequest{Canonical: snapshot.Canonical})
	if err != nil {
		t.Fatalf("升级后的 Pi 模型步骤准入失败: %v", err)
	}
	task, err := s.repo.TaskForUser("user", step.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		TextOptions struct {
			Thinking        bool `json:"thinking"`
			MaxOutputTokens int  `json:"maxOutputTokens"`
		} `json:"textOptions"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input.TextOptions.Thinking {
		t.Fatal("ForceThinkingOff 未应用到 Pi 模型任务")
	}
	wantOutput := min(cloudAgentStepBoostFallbackTokens, s.cloudAgentContextBudgetForRequest(snapshot.Request).MaxOutputTokens)
	if input.TextOptions.MaxOutputTokens != wantOutput {
		t.Fatalf("Pi 忽略了升级输出预算：got=%d want=%d", input.TextOptions.MaxOutputTokens, wantOutput)
	}
}

func TestPiInterjectionIsDeliveredWithUserSessionCheckpoint(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	const messageID = "interjection-1"
	const interjectionText = "先停一下，保留当前构图"
	if _, err := s.InterjectCloudAgent("user", run.ID, messageID, interjectionText); err != nil {
		t.Fatalf("插话入队失败: %v", err)
	}
	updated, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.PiAgentSnapshot("user", run.ID, updated.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PendingInterjections) != 1 || snapshot.PendingInterjections[0].ID != messageID {
		t.Fatalf("Pi snapshot 未暴露待送插话：%+v", snapshot.PendingInterjections)
	}

	message := json.RawMessage(`{"role":"user","content":[{"type":"text","text":"【用户插话】先停一下，保留当前构图"}],"timestamp":1790550000000}`)
	entry := json.RawMessage(`{"type":"message","id":"pi-interjection-entry","parentId":null,"timestamp":"2026-09-28T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"【用户插话】先停一下，保留当前构图"}],"timestamp":1790550000000}}`)
	if err := s.PiCheckpointMessage("user", run.ID, updated.LeaseOwner, PiMessageCheckpoint{
		Sequence: 1, Message: message, InterjectionIDs: []string{messageID},
		SessionRevision: snapshot.PiSessionRevision, ActiveLeafID: "pi-interjection-entry", SessionEntries: []json.RawMessage{entry},
	}); err != nil {
		t.Fatalf("插话会话检查点失败: %v", err)
	}
	committed, state := reloadPiRun(t, s, run.ID)
	if len(state.PendingInterjections) != 0 || !containsString(state.InterjectionIDs, messageID) {
		t.Fatalf("插话检查点没有原子清队：pending=%+v seen=%v", state.PendingInterjections, state.InterjectionIDs)
	}
	if !agentHasEvent(*state, "user_interjection_delivered") {
		t.Fatal("送达检查点未记录 user_interjection_delivered 事件")
	}
	session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Revision != snapshot.PiSessionRevision+1 || session.ActiveLeafID != "pi-interjection-entry" {
		t.Fatalf("Pi 会话没有和插话一起推进：revision=%d leaf=%q runRevision=%d", session.Revision, session.ActiveLeafID, committed.Revision)
	}
	if err := s.PiCheckpointMessage("user", run.ID, updated.LeaseOwner, PiMessageCheckpoint{
		Sequence: 1, Message: message, InterjectionIDs: []string{messageID},
		SessionRevision: snapshot.PiSessionRevision, ActiveLeafID: "pi-interjection-entry", SessionEntries: []json.RawMessage{entry},
	}); err != nil {
		t.Fatalf("重复插话检查点必须幂等: %v", err)
	}
}

func TestPiInterjectionCheckpointRejectsUnmatchedMessage(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	if _, err := s.InterjectCloudAgent("user", run.ID, "interjection-2", "改成蓝色背景"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = s.PiCheckpointMessage("user", run.ID, updated.LeaseOwner, PiMessageCheckpoint{
		Sequence: 1, Message: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"普通用户消息"}]}`),
		InterjectionIDs: []string{"interjection-2"},
	})
	if err == nil {
		t.Fatal("未包含插话正文的 Pi 用户消息被记为插话送达")
	}
	_, state := reloadPiRun(t, s, run.ID)
	if len(state.PendingInterjections) != 1 || state.PendingInterjections[0].ID != "interjection-2" {
		t.Fatalf("不匹配的 Pi 消息不应清除插话：%+v", state.PendingInterjections)
	}
}

func TestPiModelStepFailsBeforeCreatingTaskAtStepBudget(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Request.Budget.MaxSteps = 1
		state.Step = 1 // The initial model call consumed the run's only allowed step.
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, state := reloadPiRun(t, s, run.ID)
	request, _ := piFirstStepRequest(state)
	var tasksBefore, tasksAfter int64
	if err := db.Model(&model.Task{}).Where("user_id = ?", "user").Count(&tasksBefore).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiModelStep("user", run.ID, leased.LeaseOwner, request); err == nil {
		t.Fatal("Pi 模型步骤在预算耗尽后仍被接受")
	}
	failed, _ := reloadPiRun(t, s, run.ID)
	if failed.Status != "failed" || !failed.CleanupPending {
		t.Fatalf("预算耗尽必须落为可清理的失败终态：status=%q cleanup=%v", failed.Status, failed.CleanupPending)
	}
	if err := db.Model(&model.Task{}).Where("user_id = ?", "user").Count(&tasksAfter).Error; err != nil {
		t.Fatal(err)
	}
	if tasksAfter != tasksBefore {
		t.Fatalf("预算耗尽仍创建了模型任务：%d -> %d", tasksBefore, tasksAfter)
	}
}

func TestPiAgentSnapshotCarriesPendingContextCompactionForRestart(t *testing.T) {
	s, _, leased := piAgentTestLeasedFixture(t)
	const operationID = "pi-compact-restart"
	err := s.repo.MutateCloudAgent("user", leased.ID, leased.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.ContextCompaction = &cloudAgentContextCompaction{
			Status: "running", PiOperationID: operationID, PiTaskID: "compact-task",
			PiSessionRevision: 7, PiSourceLeafID: "source-leaf", PiReason: "overflow",
			PiWillRetry: true, PiTokensBefore: 17_000,
		}
		return cloudAgentSave(current, &state)
	})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.CloudAgent("user", leased.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.PiAgentSnapshot("user", latest.ID, latest.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.PendingContextCompaction
	if got == nil || got.OperationID != operationID || got.SessionRevision != 7 || got.ActiveLeafID != "source-leaf" ||
		got.Reason != "overflow" || !got.WillRetry || got.TokensBefore != 17_000 {
		t.Fatalf("Pi snapshot did not carry the pending durable compaction: %+v", got)
	}
}

func piAgentTestCall(id, name, args string) cloudAgentCall {
	call := cloudAgentCall{ID: id}
	call.Function.Name, call.Function.Arguments = name, args
	return call
}

func reloadPiRun(t *testing.T, s *Service, runID string) (*model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	run, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	return run, &state
}

// 租约是 Pi 协议的唯一门禁：错误引擎、错误 owner、过期都必须拒绝，
// 否则任何 worker 都能推进别人的运行。
func TestPiAgentLeaseAdmission(t *testing.T) {
	s, db, run := piAgentTestFixture(t)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("status", "queued").Error; err != nil {
		t.Fatal(err)
	}

	// 未被租出的运行可以领取，且领取后租约归属该 worker。
	claimed, err := s.ClaimPiAgent("worker-a")
	if err != nil || claimed == nil || claimed.RunID != run.ID {
		t.Fatalf("租出的 pi 运行未被领取: %v", err)
	}
	if claimed.Status != "running" {
		t.Fatalf("领取 queued 运行后的状态 = %q，期望 running", claimed.Status)
	}
	if again, err := s.ClaimPiAgent("worker-a"); err != nil || again != nil {
		t.Fatalf("重复领取返回了运行: %v", err)
	}
	if _, err := s.ClaimPiAgent(""); err == nil {
		t.Fatal("空 worker ID 被接受")
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-b"); err == nil {
		t.Fatal("错误 owner 被接受")
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-a"); err != nil {
		t.Fatalf("有效租约被拒绝: %v", err)
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-a@2"); err == nil {
		t.Fatal("错误 session epoch 被接受")
	}
	// 用户归属独立于租约：别人的 userID 不能读到这条运行。
	if _, err := s.PiAgentSnapshot("other", run.ID, run.LeaseOwner); err == nil {
		t.Fatal("跨用户读取被接受")
	}
	if err := s.RenewPiAgentLease("user", run.ID, "worker-b"); err == nil {
		t.Fatal("错误 owner 续租被接受")
	}
	if err := s.RenewPiAgentLease("user", run.ID, "worker-a@2"); err == nil {
		t.Fatal("旧 worker 使用错误 epoch 续租被接受")
	}
	if err := s.RenewPiAgentLease("user", run.ID, "worker-a@1"); err != nil {
		t.Fatalf("带正确 session epoch 的续租失败: %v", err)
	}
	if err := s.RenewPiAgentLease("user", run.ID, "worker-a"); err != nil {
		t.Fatalf("兼容直接调用的有效续租失败: %v", err)
	}

	expired := time.Now().Add(-time.Second)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"status": "waiting_approval", "lease_expires_at": expired}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("conversation_id = ? AND user_id = ?", run.ConversationID, run.UserID).
		Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-a"); err == nil {
		t.Fatal("过期租约被接受")
	}
	reclaimed, err := s.ClaimPiAgent("worker-b")
	if err != nil || reclaimed == nil || reclaimed.PiSessionLeaseEpoch != 2 || reclaimed.Status != "waiting_approval" {
		t.Fatalf("过期会话未以新 epoch 重新领取: snapshot=%#v error=%v", reclaimed, err)
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-a@1"); err == nil {
		t.Fatal("旧 worker 在会话重新领取后仍可操作")
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-b@2"); err != nil {
		t.Fatalf("新 worker 的 session epoch 被拒绝: %v", err)
	}

	// 旧引擎的运行永远不能被 Pi 协议领取。
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).
		Updates(map[string]any{"engine": "", "lease_expires_at": time.Now().Add(time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, "worker-a"); err == nil {
		t.Fatal("非 pi 引擎的运行被 Pi 协议接受")
	}
}

func TestPiCheckpointCommitsMessageAndConversationEntryAtomically(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	message := json.RawMessage(`{"role":"user","content":"hello","timestamp":1}`)
	entry := json.RawMessage(`{"type":"message","id":"entry-1","parentId":null,"timestamp":"2026-09-27T00:00:00Z","message":{"role":"user","content":"hello","timestamp":1}}`)
	input := PiMessageCheckpoint{
		Sequence: 1, Message: message, SessionRevision: session.Revision, ActiveLeafID: "missing-leaf",
		SessionEntries: []json.RawMessage{entry},
	}
	if _, err := s.PiCheckpointMessageResult("user", run.ID, "worker-a", input); err == nil {
		t.Fatal("checkpoint with an unknown active leaf was accepted")
	}
	afterFailure, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range afterFailure.Transcript {
		if item.Kind == cloudAgentMessageKindPi {
			t.Fatal("run transcript committed even though conversation session append rolled back")
		}
	}
	var persisted int64
	if err := db.Model(&model.CloudAgentPiEntry{}).Where("session_id = ? AND user_id = ?", session.ID, run.UserID).Count(&persisted).Error; err != nil || persisted != 0 {
		t.Fatalf("failed transaction left %d Pi session entries, error %v", persisted, err)
	}

	input.ActiveLeafID = "entry-1"
	revision, err := s.PiCheckpointMessageResult("user", run.ID, "worker-a", input)
	wantRevision := session.Revision + 1
	if err != nil || revision != wantRevision {
		t.Fatalf("valid checkpoint returned revision %d, error %v; want %d", revision, err, wantRevision)
	}
	revision, err = s.PiCheckpointMessageResult("user", run.ID, "worker-a", input)
	if err != nil || revision != wantRevision {
		t.Fatalf("identical checkpoint retry returned revision %d, error %v", revision, err)
	}
	_, entries, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil || len(entries) != 1 || entries[0].EntryID != "entry-1" {
		t.Fatalf("persisted Pi session entries = %#v, error %v", entries, err)
	}
}

// 检查点必须严格连续、同序号同内容幂等、同序号不同内容冲突。
func TestPiCheckpointSequenceAndIdempotency(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)

	first := PiMessageCheckpoint{Sequence: 1, Message: json.RawMessage(`{"role":"user","content":"你好"}`)}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, first); err != nil {
		t.Fatalf("首个检查点失败: %v", err)
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, first); err != nil {
		t.Fatalf("同序号同内容重放应当幂等: %v", err)
	}
	conflict := PiMessageCheckpoint{Sequence: 1, Message: json.RawMessage(`{"role":"user","content":"换过了"}`)}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, conflict); err == nil {
		t.Fatal("同序号不同内容被接受")
	}
	gap := PiMessageCheckpoint{Sequence: 3, Message: json.RawMessage(`{"role":"user","content":"跳号"}`)}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, gap); err == nil {
		t.Fatal("跳号检查点被接受")
	}
	// 未知角色与非法 JSON 都要拒绝。
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 2, Message: json.RawMessage(`{"role":"tool"}`)}); err == nil {
		t.Fatal("未知角色被接受")
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 2, Message: json.RawMessage(`{`)}); err == nil {
		t.Fatal("非法 JSON 被接受")
	}
	if err := s.PiCheckpointMessage("user", run.ID, "worker-b", PiMessageCheckpoint{Sequence: 2, Message: json.RawMessage(`{"role":"user","content":"x"}`)}); err == nil {
		t.Fatal("错误 owner 写入检查点被接受")
	}

	updated, state := reloadPiRun(t, s, run.ID)
	if len(state.Canonical.Messages) == 0 {
		t.Fatal("检查点没有落到运行状态")
	}
	count := 0
	for _, record := range updated.Transcript {
		if record.Kind == cloudAgentMessageKindPi {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Pi 消息条数 = %d，期望 1", count)
	}
}

// 批次重放必须幂等；与上一步模型结果不一致的批次必须拒绝（否则 worker 能伪造工具调用）。
func TestPiToolBatchReplaySafety(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	taskID := "pi-model-step-1"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded,
		ResultJSON: `{"toolCalls":[{"id":"call-a","function":{"name":"finish_run","arguments":"{\"summary\":\"done\"}"}}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("revision", run.Revision+1).Error; err != nil {
		t.Fatal(err)
	}
	// 把模型步骤登记为已完成，供批次校验比对。
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision+1, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "开始"})
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}

	calls := []cloudAgentCall{piAgentTestCall("call-a", "finish_run", `{"summary":"done"}`)}
	batch := PiToolBatchRequest{TaskID: taskID, Calls: calls}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
		t.Fatalf("合法批次被拒绝: %v", err)
	}
	before, _ := reloadPiRun(t, s, run.ID)
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
		t.Fatalf("批次重放应当幂等: %v", err)
	}
	after, _ := reloadPiRun(t, s, run.ID)
	if before.Revision != after.Revision {
		t.Fatalf("批次重放推进了 revision: %d -> %d", before.Revision, after.Revision)
	}

	tampered := PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{piAgentTestCall("call-a", "canvas_delete_node", `{"nodeId":"n1"}`)}}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, tampered); err == nil {
		t.Fatal("与模型结果不一致的批次被接受")
	}
	if err := s.PiToolBatch("user", run.ID, "worker-b", batch); err == nil {
		t.Fatal("错误 owner 的批次被接受")
	}
}

func TestPiFinishRunPublishesOneFinalReply(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	call := piAgentTestCall("finish-call", "finish_run", `{"summary":"分镜已经完成。"}`)
	taskID := "pi-finish-model-step"
	result, err := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID,
		Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(result)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "完成分镜"})
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err != nil {
		t.Fatalf("Pi finish_run 批次被拒绝: %v", err)
	}
	receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, call.ID)
	if err != nil {
		t.Fatalf("Pi finish_run 执行失败: %v", err)
	}
	if receipt.Pending || receipt.IsError || receipt.Terminated {
		t.Fatalf("finish_run 回执状态异常: %+v", receipt)
	}

	finished, state := reloadPiRun(t, s, run.ID)
	if finished.Status != "completed" {
		t.Fatalf("finish_run 后状态 = %q，期望 completed", finished.Status)
	}
	var finalReplies []string
	for _, event := range state.Events {
		if event.Type == "assistant_message" && event.Payload["final"] == true {
			finalReplies = append(finalReplies, stringField(event.Payload, "text"))
		}
	}
	if len(finalReplies) != 1 || finalReplies[0] != "分镜已经完成。" {
		t.Fatalf("最终答复应且只应发布一次，实际 %+v", finalReplies)
	}
}

func TestPiFinishRunIsBlockedByPendingPlan(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	call := piAgentTestCall("blocked-finish-call", "finish_run", `{"summary":"全部完成。"}`)
	taskID := "pi-blocked-finish-model-step"
	result, err := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID,
		Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(result)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.Plan = []cloudAgentPlanItem{{ID: "shot-1", Title: "生成镜头一", Status: "doing"}}
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "完成分镜"})
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err != nil {
		t.Fatalf("Pi finish_run 批次被拒绝: %v", err)
	}
	receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, call.ID)
	if err != nil {
		t.Fatalf("Pi finish_run 阻塞判定失败: %v", err)
	}
	if receipt.Pending || receipt.IsError || len(receipt.Result) == 0 {
		t.Fatalf("finish_run 阻塞应返回结构化工具回执: %+v", receipt)
	}
	var resultMap map[string]any
	if err := json.Unmarshal(receipt.Result, &resultMap); err != nil {
		t.Fatalf("finish_run 回执不是 JSON: %v", err)
	}
	if resultMap["completionBlocked"] != true || resultMap["requiredAction"] != "reconcile_plan" {
		t.Fatalf("待办未对账时未阻止收尾: %s", receipt.Result)
	}
	current, state := reloadPiRun(t, s, run.ID)
	if current.Status != "running" {
		t.Fatalf("待办未对账应留在运行中，实际 %q", current.Status)
	}
	blockedEvents := 0
	for _, event := range state.Events {
		if event.Type == "completion_blocked" {
			blockedEvents++
		}
	}
	if blockedEvents != 1 {
		t.Fatalf("finish_run 阻塞应写入一次 completion_blocked 事件，实际 %d", blockedEvents)
	}
	for _, event := range state.Events {
		if event.Type == "assistant_message" && event.Payload["final"] == true {
			t.Fatalf("被待办闸门拦截的 summary 不得作为最终答复发布: %+v", event.Payload)
		}
	}
}

func TestPiToolBatchRejectsCallsAfterUnsupportedStopReason(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	const taskID = "pi-refusal-with-tools"
	call := piAgentTestCall("call-refusal", "finish_run", `{"summary":"should not finish"}`)
	result, err := json.Marshal(map[string]any{
		"text": "partial refusal", "stopReason": "refusal", "stopReasonKind": "refusal",
		"toolCalls": []cloudAgentCall{call},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: string(result)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, leased.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err == nil {
		t.Fatal("拒答终止原因下仍接受了 finish_run 工具调用")
	}
	current, state := reloadPiRun(t, s, run.ID)
	if current.Status != "running" || len(state.Calls) != 0 || state.CallIndex != 0 {
		t.Fatalf("拒答工具批次改变了运行或工具状态: status=%s calls=%+v index=%d", current.Status, state.Calls, state.CallIndex)
	}
}

// 推进顺序：越序 callId 必须拒绝；已记录回执的调用重放必须返回同一回执（恰好一次的可观测保证）。
func TestPiToolAdvanceOrderAndReceiptReplay(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	taskID := "pi-model-step-2"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded,
		ResultJSON: `{"toolCalls":[{"id":"call-1","function":{"name":"canvas_list_node_types","arguments":"{}"}}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{
		piAgentTestCall("call-1", "canvas_list_node_types", "{}"),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, "call-unknown"); err == nil {
		t.Fatal("未知 callId 被接受")
	}

	first, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, "call-1")
	if err != nil {
		t.Fatalf("首个工具推进失败: %v", err)
	}
	if first.Pending || first.CallID != "call-1" {
		t.Fatalf("首个回执不完整: %+v", first)
	}
	replayed, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, "call-1")
	if err != nil {
		t.Fatalf("回执重放失败: %v", err)
	}
	if string(replayed.Result) != string(first.Result) {
		t.Fatalf("重放回执内容不一致: %s vs %s", replayed.Result, first.Result)
	}
	if _, err := s.PiToolAdvance("user", run.ID, "worker-b", taskID, "call-1"); err == nil {
		t.Fatal("错误 owner 的推进被接受")
	}
}

func TestPiToolReceiptsAreScopedToTheirModelStepWhenCallIDsRepeat(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	const firstTaskID = "pi-reused-call-step-1"
	firstCall := piAgentTestCall("reused-call", "canvas_list_node_types", "{}")
	firstResult, err := json.Marshal(map[string]any{
		"toolCalls": []cloudAgentCall{firstCall}, "stopReasonKind": cloudAgentStopKindStop,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: firstTaskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: string(firstResult)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = firstTaskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: firstTaskID, Calls: []cloudAgentCall{firstCall}}); err != nil {
		t.Fatalf("首个工具批次失败: %v", err)
	}
	firstReceipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, firstTaskID, firstCall.ID)
	if err != nil || firstReceipt.Pending {
		t.Fatalf("首个工具执行失败: receipt=%+v err=%v", firstReceipt, err)
	}

	// 同一 provider 在下一轮复用了 call ID，但请求的是另一个工具和参数。
	// 第二批的合法回执必须来自第二批执行结果，不能复用第一轮的旧消息。
	const secondTaskID = "pi-reused-call-step-2"
	secondCall := piAgentTestCall("reused-call", "canvas_get_state", "{}")
	secondResult, err := json.Marshal(map[string]any{
		"toolCalls": []cloudAgentCall{secondCall}, "stopReasonKind": cloudAgentStopKindStop,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: secondTaskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: string(secondResult)}).Error; err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, latest.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = secondTaskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: secondTaskID, Calls: []cloudAgentCall{secondCall}}); err != nil {
		t.Fatalf("第二个工具批次不应被旧批次挡住: %v", err)
	}
	if _, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, firstTaskID, secondCall.ID); err == nil {
		t.Fatal("旧模型步骤仍能推进复用 call ID 的新工具")
	}
	secondReceipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, secondTaskID, secondCall.ID)
	if err != nil || secondReceipt.Pending {
		t.Fatalf("第二个工具执行失败: receipt=%+v err=%v", secondReceipt, err)
	}
	if string(secondReceipt.Result) == string(firstReceipt.Result) {
		t.Fatalf("复用 call ID 返回了旧步骤回执: first=%s second=%s", firstReceipt.Result, secondReceipt.Result)
	}
	replayed, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, secondTaskID, secondCall.ID)
	if err != nil || string(replayed.Result) != string(secondReceipt.Result) {
		t.Fatalf("当前模型步骤回执重放不稳定: replay=%+v first=%+v err=%v", replayed, secondReceipt, err)
	}
}

// 没有工具调用的收尾步骤只能走 PiNoToolTurn；重复询问必须返回同一判定，
// 否则重启后的 worker 会重复写 assistant 消息与完成事件。
func TestPiNoToolTurnIsIdempotent(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	taskID := "pi-no-tool-step"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded,
		ResultJSON: `{"text":"完成","toolCalls":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "开始"})
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}

	first, err := s.PiNoToolTurn("user", run.ID, run.LeaseOwner, taskID)
	if err != nil {
		t.Fatalf("收尾判定失败: %v", err)
	}
	if first.Status == "" {
		t.Fatal("收尾判定缺少状态")
	}
	// 重放：不再追加消息，也不再推进 revision。
	before, _ := reloadPiRun(t, s, run.ID)
	second, err := s.PiNoToolTurn("user", run.ID, run.LeaseOwner, taskID)
	if err != nil {
		t.Fatalf("收尾判定重放失败: %v", err)
	}
	after, _ := reloadPiRun(t, s, run.ID)
	if second.Status != first.Status {
		t.Fatalf("重放状态不一致: %s vs %s", second.Status, first.Status)
	}
	if before.Revision != after.Revision {
		t.Fatalf("收尾判定重放推进了 revision: %d -> %d", before.Revision, after.Revision)
	}
}

func TestPiNoToolTurnEscalatesTruncationWithoutPublishingPartialText(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	const taskID = "pi-truncated-step"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"未完成的半句","stopReason":"length","stopReasonKind":"length"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.PiNoToolTurn("user", run.ID, leased.LeaseOwner, taskID)
	if err != nil {
		t.Fatalf("Pi 截断重试判定失败: %v", err)
	}
	if decision.Status != "continue" || strings.TrimSpace(decision.Nudge) == "" {
		t.Fatalf("截断步骤应升级预算后续跑: %+v", decision)
	}
	_, state := reloadPiRun(t, s, run.ID)
	if state.TruncatedStepEscalated != 1 || !state.ForceThinkingOff || !state.BoostStepOutputBudget {
		t.Fatalf("Pi 截断恢复开关未升级: count=%d thinkingOff=%v boosted=%v", state.TruncatedStepEscalated, state.ForceThinkingOff, state.BoostStepOutputBudget)
	}
	if strings.Contains(fmt.Sprint(state.Canonical.Messages), "未完成的半句") || agentHasEvent(*state, "assistant_message") {
		t.Fatal("截断正文不应进入 Go 的最终上下文或助手消息账本")
	}
}

func TestPiNoToolTurnFailsRefusalWithoutPublishingPartialText(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	const taskID = "pi-refusal-step"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"不可发布的部分正文","stopReason":"refusal","stopReasonKind":"refusal"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.PiNoToolTurn("user", run.ID, leased.LeaseOwner, taskID)
	if err != nil {
		t.Fatalf("Pi 拒答判定失败: %v", err)
	}
	if decision.Status != "failed" {
		t.Fatalf("拒答终止原因应结束本轮: %+v", decision)
	}
	_, state := reloadPiRun(t, s, run.ID)
	if strings.Contains(fmt.Sprint(state.Canonical.Messages), "不可发布的部分正文") || agentHasEvent(*state, "assistant_message") {
		t.Fatal("拒答正文不应进入 Go 的最终上下文或助手消息账本")
	}
	if !agentHasEvent(*state, "run_failed") {
		t.Fatal("拒答终止没有写入 run_failed")
	}
}

// 失败的模型步骤不能被当成收尾依据：它必须报错，而不是把失败步骤写成 final 消息。
func TestPiNoToolTurnRejectsFailedStep(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	failed := "pi-no-tool-failed"
	if err := db.Create(&model.Task{ID: failed, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusFailed, ResultJSON: `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = failed
		state.PiNoToolTaskID = ""
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "开始"})
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiNoToolTurn("user", run.ID, run.LeaseOwner, failed); err == nil {
		t.Fatal("失败的模型步骤被当作收尾依据")
	}
	// 别人不能拿这条运行做收尾判定。
	if _, err := s.PiNoToolTurn("user", run.ID, "worker-b", failed); err == nil {
		t.Fatal("错误 owner 的收尾判定被接受")
	}
}

// 失败上报必须幂等：worker 在"已记录失败、未收到响应"之间崩溃后会重投，
// 第二次必须成功返回，否则 Node 把它当协议错误并整轮退出。
func TestPiFailModelStepIdempotent(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	failed := "pi-model-failed"
	if err := db.Create(&model.Task{ID: failed, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusFailed, Error: "上游超时"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.ActiveTaskID = failed
		state.TaskIDs = append(state.TaskIDs, failed)
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.PiFailModelStep("user", run.ID, run.LeaseOwner, failed); err != nil {
		t.Fatalf("首次失败上报失败: %v", err)
	}
	after, _ := reloadPiRun(t, s, run.ID)
	if after.Status != "failed" {
		t.Fatalf("失败上报后状态 = %q", after.Status)
	}
	events := 0
	for _, record := range after.Journal {
		if strings.Contains(record.EventJSON, "model_step_failed") {
			events++
		}
	}
	if err := s.PiFailModelStep("user", run.ID, run.LeaseOwner, failed); err != nil {
		t.Fatalf("失败上报重投应当幂等成功: %v", err)
	}
	replayed, _ := reloadPiRun(t, s, run.ID)
	replayedEvents := 0
	for _, record := range replayed.Journal {
		if strings.Contains(record.EventJSON, "model_step_failed") {
			replayedEvents++
		}
	}
	if events != replayedEvents {
		t.Fatalf("重投重复记录了失败事件: %d -> %d", events, replayedEvents)
	}
	// 尚未失败的任务不能被上报为失败。
	succeeded := "pi-model-ok"
	if err := db.Create(&model.Task{ID: succeeded, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, replayed.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.ActiveTaskID = succeeded
		state.TaskIDs = append(state.TaskIDs, succeeded)
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiFailModelStep("user", run.ID, run.LeaseOwner, succeeded); err == nil {
		t.Fatal("成功的模型任务被上报为失败")
	}
	if err := s.PiFailModelStep("user", run.ID, "worker-b", failed); err == nil {
		t.Fatal("错误 owner 的失败上报被接受")
	}
}

// 旧 SSE 合同在模型每次返回正文时发 assistant_message；Pi 路径原先只在无工具调用的
// 收尾步骤发，模型"边做边说"时前端会丢掉过程消息。这条用例守住补发行为。
func TestPiCheckpointEmitsAssistantMessageEvent(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)

	// 正文 + 工具调用的过程说明：必须发 final=false 的 assistant_message。
	withCalls := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"我先读取画布"}],"toolCalls":[]}`)
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 1, Message: withCalls}); err != nil {
		t.Fatalf("检查点失败: %v", err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	events := 0
	for _, event := range state.Events {
		if event.Type == "assistant_message" {
			events++
			if text, _ := event.Payload["text"].(string); text != "我先读取画布" {
				t.Fatalf("过程消息正文错误: %+v", event.Payload)
			}
			if final, _ := event.Payload["final"].(bool); final {
				t.Fatalf("过程说明不应标记 final: %+v", event.Payload)
			}
		}
	}
	if events != 1 {
		t.Fatalf("assistant_message 事件数 = %d，期望 1", events)
	}

	// 推理正文（thinking 块）同样要发既有 reasoning_message，且不进正文事件。
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 2,
		Message: json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","thinking":"先核对画布"},{"type":"text","text":"结论"}]}`)}); err != nil {
		t.Fatal(err)
	}
	_, withReasoning := reloadPiRun(t, s, run.ID)
	reasoning := 0
	for _, event := range withReasoning.Events {
		if event.Type == "reasoning_message" {
			reasoning++
			if text, _ := event.Payload["text"].(string); text != "先核对画布" {
				t.Fatalf("推理正文错误: %+v", event.Payload)
			}
			if id, _ := event.Payload["messageId"].(string); !strings.HasSuffix(id, ":reasoning") {
				t.Fatalf("推理事件 messageId 应带 :reasoning 后缀: %+v", event.Payload)
			}
		}
	}
	if reasoning != 1 {
		t.Fatalf("reasoning_message 事件数 = %d，期望 1", reasoning)
	}
	for _, event := range withReasoning.Events {
		if event.Type == "assistant_message" {
			if text, _ := event.Payload["text"].(string); strings.Contains(text, "先核对画布") {
				t.Fatalf("推理正文不得混进正文事件: %+v", event.Payload)
			}
		}
	}

	// 纯字符串正文与空正文：前者补发，后者不产生事件。
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 3,
		Message: json.RawMessage(`{"role":"assistant","content":"直接答复"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 4,
		Message: json.RawMessage(`{"role":"user","content":"继续"}`)}); err != nil {
		t.Fatal(err)
	}
	_, after := reloadPiRun(t, s, run.ID)
	total, texts := 0, map[string]bool{}
	for _, event := range after.Events {
		if event.Type == "assistant_message" {
			total++
			text, _ := event.Payload["text"].(string)
			texts[text] = true
		}
	}
	if total != 3 || !texts["直接答复"] || !texts["结论"] {
		t.Fatalf("字符串正文应补发一条、user 消息不发：total=%d texts=%v", total, texts)
	}
}

// 故障注入：业务读取已经执行、回执尚未落库时进程崩溃。重试必须拿到确定性回执
// （而不是重复执行副作用），并且运行 revision 不被推进第二次。
func TestPiToolAdvanceIsReplaySafeAfterCrash(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)

	taskID := "pi-crash-step"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded,
		ResultJSON: `{"toolCalls":[{"id":"crash-call","function":{"name":"canvas_list_node_types","arguments":"{}"}}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{
		piAgentTestCall("crash-call", "canvas_list_node_types", "{}"),
	}}); err != nil {
		t.Fatal(err)
	}

	// 第一次推进：副作用与回执在同一个 MutateCloudAgent 事务里提交。
	first, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, "crash-call")
	if err != nil || first.Pending {
		t.Fatalf("首次推进失败: receipt=%+v err=%v", first, err)
	}
	afterFirstRun, afterFirstState := reloadPiRun(t, s, run.ID)

	// 模拟"回执已提交、但 worker 没收到响应"后的重投：必须返回同一回执。
	replayed, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, "crash-call")
	if err != nil {
		t.Fatalf("重投失败: %v", err)
	}
	if replayed.Pending || string(replayed.Result) != string(first.Result) {
		t.Fatalf("重投回执与首次不一致: %+v vs %+v", replayed, first)
	}
	afterReplayRun, afterReplayState := reloadPiRun(t, s, run.ID)

	// 关键断言：重投不推进 revision，也不产生第二条 tool 消息。
	if afterFirstRun.Revision != afterReplayRun.Revision {
		t.Fatalf("重投推进了 revision: %d -> %d", afterFirstRun.Revision, afterReplayRun.Revision)
	}
	toolMessages := 0
	for _, message := range afterReplayState.Canonical.Messages {
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == "crash-call" {
			toolMessages++
		}
	}
	if toolMessages != 1 {
		t.Fatalf("tool 回执消息数 = %d，期望 1（副作用恰好一次）", toolMessages)
	}

	// 陈旧 revision 的写入必须被 CAS 拒绝，而不是覆盖已提交状态。
	if err := s.repo.MutateCloudAgent("user", run.ID, afterFirstRun.Revision-1, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, afterFirstState)
	}); err == nil {
		t.Fatal("陈旧 revision 的写入被接受了")
	}
}

// 本轮终态（拒绝/取消/失败/完成）后该调用不会有回执：必须返回明确的终止信号，
// 否则 Node 的 executeTool 会无限轮询 pending —— 占死 worker 并持续续租。
func TestPiToolAdvanceSignalsTerminationInsteadOfPending(t *testing.T) {
	for _, status := range []string{"rejected", "cancelled", "failed", "completed"} {
		s, db, run := piAgentTestLeasedFixture(t)
		taskID := "pi-term-step"
		if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded,
			ResultJSON: `{"toolCalls":[{"id":"term-call","function":{"name":"canvas_list_node_types","arguments":"{}"}}]}`}).Error; err != nil {
			t.Fatal(err)
		}
		if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			state.LastStepTaskID = taskID
			state.Calls = []cloudAgentCall{piAgentTestCall("term-call", "canvas_list_node_types", "{}")}
			state.CallIndex = 0
			current.Status = status
			return cloudAgentSave(current, &state)
		}); err != nil {
			t.Fatal(err)
		}
		receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, taskID, "term-call")
		if err != nil {
			t.Fatalf("status=%s 推进报错: %v", status, err)
		}
		if !receipt.Terminated || receipt.Pending {
			t.Fatalf("status=%s 应返回终止信号而不是 pending: %+v", status, receipt)
		}
	}
}

// worker 遇到重试无用的配置/协议错误时必须能上报运行失败，否则运行会一直停在
// running，前端表现为"Agent 输出完了却永远显示运行中"（本轮真实事故）。
func TestPiFailRunMarksRunFailedWithReason(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)

	if err := s.PiFailRun("user", run.ID, run.LeaseOwner, "server snapshot has tools missing from cloud-agent-tools/v3"); err != nil {
		t.Fatalf("上报失败: %v", err)
	}
	failed, state := reloadPiRun(t, s, run.ID)
	if failed.Status != "failed" {
		t.Fatalf("状态 = %q，期望 failed", failed.Status)
	}
	if !strings.Contains(failed.FailureMessage, "tools missing") {
		t.Fatalf("失败原因未落库: %q", failed.FailureMessage)
	}
	hasEvent := false
	for _, event := range state.Events {
		if event.Type == "run_failed" {
			hasEvent = true
			if reason, _ := event.Payload["reason"].(string); reason != "pi_worker_fatal" {
				t.Fatalf("run_failed 原因标签错误: %+v", event.Payload)
			}
		}
	}
	if !hasEvent {
		t.Fatal("缺少 run_failed 事件")
	}

	// 已终态时重复上报必须是 no-op（幂等），不能覆盖原失败原因。
	if err := s.PiFailRun("user", run.ID, run.LeaseOwner, "第二次上报"); err != nil {
		t.Fatalf("重复上报应幂等: %v", err)
	}
	again, _ := reloadPiRun(t, s, run.ID)
	if !strings.Contains(again.FailureMessage, "tools missing") {
		t.Fatalf("重复上报覆盖了原原因: %q", again.FailureMessage)
	}
	// 错误 owner 不能上报。
	if err := s.PiFailRun("user", run.ID, "worker-b", "越权"); err == nil {
		t.Fatal("错误 owner 的上报被接受")
	}
}
