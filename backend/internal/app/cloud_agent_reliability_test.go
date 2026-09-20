package app

// Regression contracts for recovery, bounded scheduling and safe submission.
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func reliableAgentRoot(t *testing.T) (*Service, *gorm.DB, *CloudAgentRun) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	return s, db, root
}

func TestCloudAgentReliabilitySchedulerHeadOfLine500(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	now := time.Now().Add(-time.Hour)
	var tailID, tailUser string
	profile := cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")}
	_, policy, err := compileCloudAgentPolicies(agentTestRequest(), nil, "", profile)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		user := fmt.Sprintf("audit-user-%03d", i)
		req := agentTestRequest()
		req.IdempotencyKey = fmt.Sprintf("audit-key-%03d", i)
		id := cloudAgentID(user, req.IdempotencyKey)
		status := model.TaskStatusSucceeded
		if i < 50 {
			status = model.TaskStatusRunning
		}
		ts := now.Add(time.Duration(i) * time.Millisecond)
		task := model.Task{ID: id, UserID: user, ProjectID: req.CanvasID, Operation: cloudAgentOperation, Status: status, ResultJSON: `{"text":"ready"}`, CreatedAt: ts, UpdatedAt: ts}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		state := cloudAgentRuntime{Request: req, Policy: policy, Profile: profile, ActiveTaskID: id, TaskIDs: []string{id}, Step: 1, Decisions: map[string]string{}, Events: []CloudAgentEvent{}}
		run := model.CloudAgentExecution{ID: id, UserID: user, Status: "running", Revision: 1, CreatedAt: ts, UpdatedAt: ts}
		if err := cloudAgentSave(&run, &state); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
		tailID, tailUser = id, user
	}
	started := time.Now()
	for i := 0; i < 20; i++ {
		s.advanceCloudAgents()
	}
	var completed int64
	if err := db.Model(&model.CloudAgentExecution{}).Where("status = ?", "completed").Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("500 runs, first 50 waiting, 450 ready: completed after 20 scheduler passes=%d; elapsed=%s (not a capacity benchmark)", completed, time.Since(started))
	if completed != 450 {
		t.Fatalf("ready runs starved: %d/450 completed", completed)
	}
	tail, err := s.repo.CloudAgent(tailUser, tailID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceCloudAgent(tail); err != nil {
		t.Fatal(err)
	}
	tail, err = s.repo.CloudAgent(tailUser, tailID)
	if err != nil {
		t.Fatal(err)
	}
	if tail.Status != "completed" {
		t.Fatalf("tail should be ready independently, got %s", tail.Status)
	}
}

// 事件体积不再影响检查点：多个大事件照样保存成功，检查点只留控制面，
// 事件按原样落进事件表（不再需要"按级降级"这条传导链）。
func TestCloudAgentReliabilityLargeEventsStayOutOfCheckpoint(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	body := "BIG-EVENT-MARKER-" + strings.Repeat("x", 120000)
	for i := 0; i < 8; i++ {
		state.event(root.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "text": body, "callId": fmt.Sprintf("filler-%d", i)})
	}
	if err = s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatalf("事件应当进表而不是撑爆检查点: %v", err)
	}
	saved, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.StateJSON) > 64<<10 {
		t.Fatalf("检查点仍随事件膨胀: %d 字节", len(saved.StateJSON))
	}
	if strings.Contains(saved.StateJSON, "BIG-EVENT-MARKER-") {
		t.Fatal("检查点里还留着事件载荷")
	}
	if saved.EventCount != len(state.Events) {
		t.Fatalf("事件水位 = %d，期望 %d", saved.EventCount, len(state.Events))
	}
	stored, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := cloudAgentDecode(stored)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	wantWindow := saved.EventCount
	if wantWindow > repository.CloudAgentJournalWindow {
		wantWindow = repository.CloudAgentJournalWindow
	}
	if len(rebuilt.Events) != wantWindow {
		t.Fatalf("事件窗口 = %d，期望 %d", len(rebuilt.Events), wantWindow)
	}
	rows, err := s.repo.CloudAgentRunEvents("user", root.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != saved.EventCount {
		t.Fatalf("入库条数 = %d，期望 %d", len(rows), saved.EventCount)
	}
	if last := rows[len(rows)-1]; len(last.Payload) < 120000 || !strings.Contains(last.Payload, "BIG-EVENT-MARKER-") {
		t.Fatalf("大事件载荷在入库时被削弱: %d 字节", len(last.Payload))
	}
}

// 单条事件载荷超过校验上限（128KB）时压成"需要刷新"回执，而不是终止本轮。
//
// 实测线上就是一次整理几十个分镜节点、单条 canvas_updated 带满完整 before/after 顶穿了这条上限，
// 用户看到的是"超过安全限制，本轮已停止"。现在事件不进检查点，但单条仍要能过校验。
func TestCloudAgentReliabilityOversizedEventPayloadIsBounded(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.repo.CloudAgentRunEventCount("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		state.event(root.ID, "tool_completed", map[string]any{"text": strings.Repeat("x", 140000), "canvasId": "canvas-1"})
	}
	if err = s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatalf("超大单事件载荷应当封顶保存，实际 %v", err)
	}
	rows, err := s.repo.CloudAgentRunEvents("user", root.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(rows)) != before+4 {
		t.Fatalf("入库条数 = %d，期望 %d", len(rows), before+4)
	}
	for _, row := range rows[len(rows)-4:] {
		if len(row.Payload) > cloudAgentEventPayloadLimitBytes {
			t.Fatalf("治理后仍有超限载荷：%d 字节", len(row.Payload))
		}
		if !strings.Contains(row.Payload, "requiresRefresh") {
			t.Fatalf("应标记需要刷新：%s", truncateRunes(row.Payload, 120))
		}
	}
	stored, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status == "failed" {
		t.Fatalf("本轮不应被判死：%s", stored.FailureMessage)
	}
	if _, err := cloudAgentDecode(stored); err != nil {
		t.Fatalf("重建失败: %v", err)
	}
}

func TestCloudAgentReliabilityCancelReplayInterrupted(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	// Simulate process/request interruption after cancelled checkpoint commits, before child cancellation.
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", root.ID); err != nil {
		t.Fatal(err)
	}
	task, err := s.repo.TaskForUser("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cancel replay returned success; active child status remains=%s", task.Status)
	if task.Status != model.TaskStatusCancelled {
		t.Fatalf("child not cancelled: %s", task.Status)
	}
}

func TestCloudAgentReliabilityFailedContinuation(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	if err := db.Model(&model.Task{}).Where("id = ?", root.ID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "mock failure"}).Error; err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.Prompt = "继续刚才的任务"
	req.IdempotencyKey = "audit-continuation-key"
	child, err := s.CreateCloudAgentRun("user", req, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.repo.TaskForUser("user", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		TextHistory []providerTextMessage `json:"textHistory"`
	}
	if err = json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	t.Logf("failed first turn -> continue: inherited history messages=%d", len(input.TextHistory))
	if len(input.TextHistory) != 3 ||
		input.TextHistory[0].Content != agentTestRequest().Prompt ||
		input.TextHistory[1].Role != "assistant" ||
		input.TextHistory[2].Role != "user" ||
		!strings.Contains(input.TextHistory[2].Content, "failed") {
		t.Fatalf("continuation lost facts: %+v", input.TextHistory)
	}
}

func TestCloudAgentReliabilityIdleSnapshotReadCost(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	snapshot, err := s.CloudAgentRun("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	queries := 0
	if err = db.Callback().Query().After("gorm:query").Register("reliability:count_queries", func(tx *gorm.DB) {
		queries++
		if strings.Contains(tx.Statement.SQL.String(), "SELECT *") {
			t.Error("idle stream loaded full row")
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("reliability:count_queries")
	unchanged, err := s.CloudAgentRunIfChanged("user", root.ID, snapshot.Revision)
	if err != nil || unchanged != nil || queries != 1 {
		t.Fatalf("idle read should be one small query: queries=%d run=%v err=%v", queries, unchanged, err)
	}
	if _, err = s.CloudAgentRunIfChanged("another-user", root.ID, snapshot.Revision); err == nil {
		t.Fatal("cross-user stream allowed")
	}
}

func TestCloudAgentReliabilityDirectChannelDispatchGuard(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	task, err := s.repo.TaskForUser("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Attempts = 1
	attempt, err := s.beginTaskRouteAttempt(task)
	if err != nil || attempt == nil {
		t.Fatalf("missing attempt %v", err)
	}
	stale := *attempt
	if err = s.markRouteAttemptDispatching(attempt); err != nil {
		t.Fatal(err)
	}
	if err = s.markRouteAttemptDispatching(&stale); !isRouteDispatchUncertain(err) {
		t.Fatalf("stale dispatch allowed: %v", err)
	}
	task.Attempts = 2
	if _, err = s.beginTaskRouteAttempt(task); !isRouteDispatchUncertain(err) {
		t.Fatalf("ambiguous retry allowed: %v", err)
	}
	task.ProviderRequestID = "provider-original"
	recovered, err := s.beginTaskRouteAttempt(task)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != attempt.ID || recovered.DispatchState != "accepted" {
		t.Fatalf("did not recover original attempt: %+v", recovered)
	}
	ctx1 := withProviderSubmissionKey(context.Background(), attempt)
	ctx2 := withProviderSubmissionKey(context.Background(), recovered)
	if ctx1.Value(providerSubmissionKeyContext{}) != ctx2.Value(providerSubmissionKeyContext{}) {
		t.Fatal("recovery changed upstream key")
	}
}

func TestCloudAgentReliabilityPendingCancellationRecoveredByScheduler(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Updates(map[string]any{"status": "cancelled", "cleanup_pending": true}).Error; err != nil {
		t.Fatal(err)
	}
	s.advanceCloudAgents()
	task, err := s.repo.TaskForUser("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusCancelled || run.CleanupPending {
		t.Fatalf("cancellation not recovered: task=%s pending=%v", task.Status, run.CleanupPending)
	}
	if err = s.CancelCloudAgent(context.Background(), "user", root.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCloudAgentReliabilityCorruptedCancellationUsesControlTask(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	child := model.Task{ID: "cleanup-child", UserID: "user", Status: model.TaskStatusQueued, Operation: "cloud_agent_step"}
	if err := db.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Updates(map[string]any{"state_json": "broken", "active_task_id": child.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", root.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.repo.TaskForUser("user", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskStatusCancelled {
		t.Fatal("corrupted state orphaned active child")
	}
}
