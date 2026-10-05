package app

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

func dynamicSubagentFixture(t *testing.T) (*Service, *gorm.DB, string) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	for _, row := range []any{
		&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`},
		&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"agentSubagentsEnabled":true}`},
		&model.AgentSubagentPolicy{ID: "consent", UserID: "user", CanvasID: "agent-canvas", Enabled: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := agentTestRequest()
	req.SubagentEnabled = true
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, db, root.ID
}

func dynamicToolCall(name, id, args string) cloudAgentCall {
	call := cloudAgentCall{ID: id}
	call.Function.Name, call.Function.Arguments = name, args
	return call
}

func dynamicSpawn(t *testing.T, s *Service, parent, id string) string {
	t.Helper()
	run, err := s.repo.CloudAgent("user", parent)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.spawnDynamicSubagent(run, &state, dynamicToolCall("spawn_subagent", id, `{"name":"研究员","role":"资料核对","objective":"回报三条事实"}`))
	if err != nil {
		t.Fatal(err)
	}
	return result["childRunId"].(string)
}

func dynamicExecute(t *testing.T, s *Service, runID string, call cloudAgentCall) cloudAgentRuntime {
	t.Helper()
	run, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Calls, state.CallIndex = []cloudAgentCall{call}, 0
	state.Phase = ""
	state.Step = 1
	state.TaskIDs = []string{"dynamic-fixture"}
	state.ActiveTaskID = ""
	state.LastStepTaskID = "dynamic-fixture"
	state.PiToolBatchTaskID = "dynamic-fixture"
	if err := s.repo.MutateCloudAgent("user", runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "running"
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = cloudAgentDecode(latest)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestDynamicSubagentSpawnSessionsReplayAndBounds(t *testing.T) {
	s, _, parent := dynamicSubagentFixture(t)
	child := dynamicSpawn(t, s, parent, "spawn-1")
	if duplicate := dynamicSpawn(t, s, parent, "spawn-1"); child != duplicate {
		t.Fatal("spawn retry duplicated child")
	}
	parentRun, _ := s.repo.CloudAgent("user", parent)
	childRun, _ := s.repo.CloudAgent("user", child)
	childState, err := cloudAgentDecode(childRun)
	if err != nil {
		t.Fatal(err)
	}
	if childRun.ConversationID == parentRun.ConversationID || childRun.ParentID != parent || childState.Subagent.LinkID == "" {
		t.Fatal("child is not an independent explicitly linked conversation")
	}
	if childState.Request.Budget.MaxSteps != 20 || childState.Request.PermissionMode != "read_only" {
		t.Fatal("child default budget or permission unbounded")
	}
	dynamicSpawn(t, s, parent, "spawn-2")
	dynamicSpawn(t, s, parent, "spawn-3")
	state, _ := cloudAgentDecode(parentRun)
	if _, err := s.spawnDynamicSubagent(parentRun, &state, dynamicToolCall("spawn_subagent", "spawn-4", `{"name":"第四个","role":"检查","objective":"不应越过并发限制"}`)); err == nil {
		t.Fatal("concurrency cap not enforced")
	}
}

func TestDynamicSubagentWaitMessageFinishAndReplay(t *testing.T) {
	s, db, parent := dynamicSubagentFixture(t)
	child := dynamicSpawn(t, s, parent, "spawn")
	waiting := dynamicExecute(t, s, parent, dynamicToolCall("wait_subagents", "wait", "{}"))
	if waiting.CallIndex != 0 {
		t.Fatal("wait_subagents acknowledged before child finished")
	}
	for i := 0; i < 2; i++ {
		dynamicExecute(t, s, child, dynamicToolCall("send_parent_message", "progress", `{"kind":"progress","text":"已确认第一条事实"}`))
	}
	var messages []model.AgentSubagentMessage
	if err := db.Where("parent_run_id = ?", parent).Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("message retry duplicated rows: %d", len(messages))
	}
	dynamicExecute(t, s, child, dynamicToolCall("finish_subagent", "finish", `{"summary":"三条事实核对完成"}`))
	latest, _ := s.repo.CloudAgent("user", child)
	if latest.Status != "completed" {
		t.Fatal("structured finish did not complete child")
	}
	ready := dynamicExecute(t, s, parent, dynamicToolCall("wait_subagents", "wait", "{}"))
	if ready.CallIndex != 1 {
		t.Fatal("durable wait did not release after final child result")
	}
	parentRun, _ := s.repo.CloudAgent("user", parent)
	state, _ := cloudAgentDecode(parentRun)
	count := 0
	for _, event := range state.Events {
		if event.Type == "subagent_message" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected one event per accepted message, got %d", count)
	}
	raw, _ := json.Marshal(state.PendingInterjections)
	if !strings.Contains(string(raw), "研究员") {
		t.Fatal("parent message lost child identity")
	}
}

func TestDynamicSubagentSpawnAdmissionRollsBackLinkFailure(t *testing.T) {
	s, db, parent := dynamicSubagentFixture(t)
	if err := db.Callback().Create().Before("gorm:create").Register("fail_dynamic_link", func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_subagent_links" {
			tx.AddError(gorm.ErrInvalidData)
		}
	}); err != nil {
		t.Fatal(err)
	}
	run, _ := s.repo.CloudAgent("user", parent)
	state, _ := cloudAgentDecode(run)
	if _, err := s.spawnDynamicSubagent(run, &state, dynamicToolCall("spawn_subagent", "rollback", `{"name":"检查员","role":"核查","objective":"不能产生孤儿"}`)); err == nil {
		t.Fatal("injected link failure ignored")
	}
	childID := cloudAgentID("user", "subagent:"+parent+":rollback")
	var count int64
	db.Model(&model.CloudAgentExecution{}).Where("id = ?", childID).Count(&count)
	if count != 0 {
		t.Fatal("link failure left an orphan executable child")
	}
	db.Model(&model.Task{}).Where("id = ?", childID).Count(&count)
	if count != 0 {
		t.Fatal("link failure left an orphan holding reservation")
	}
}

func TestDynamicSubagentParentCancellationStopsChildren(t *testing.T) {
	s, _, parent := dynamicSubagentFixture(t)
	child := dynamicSpawn(t, s, parent, "cancel")
	if err := s.CancelCloudAgent(context.Background(), "user", parent); err != nil {
		t.Fatal(err)
	}
	run, _ := s.repo.CloudAgent("user", child)
	if run.Status != "cancelled" {
		t.Fatalf("child survived parent cancellation: %s", run.Status)
	}
}

func TestDynamicSubagentFailureReportsOnceAndBlocksPrematureCompletion(t *testing.T) {
	s, _, parent := dynamicSubagentFixture(t)
	child := dynamicSpawn(t, s, parent, "failure")
	state := dynamicExecute(t, s, parent, dynamicToolCall("finish_run", "too-soon", `{"summary":"已完成"}`))
	if !cloudAgentCompletionHasBlocker(cloudAgentEvaluateCompletion(&state), "pending_subagents") {
		t.Fatal("parent could finish with active children")
	}
	childRun, _ := s.repo.CloudAgent("user", child)
	childState, _ := cloudAgentDecode(childRun)
	if cloudAgentEvaluateCompletion(&childState).Final {
		t.Fatal("child can finish without reporting its result")
	}
	if err := s.repo.MarkCloudAgentFailed("user", child, childRun.Revision, "模型不可用"); err != nil {
		t.Fatal(err)
	}
	dynamicSpawn(t, s, parent, "after-failure")
	for i := 0; i < 2; i++ {
		if err := s.ReconcileDynamicSubagents(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := s.repo.AgentSubagentMessages("user", parent)
	if err != nil || len(messages) != 1 || messages[0].Kind != "error" || messages[0].AcknowledgedAt == nil {
		t.Fatalf("failure outbox: %+v %v", messages, err)
	}
}

func TestDynamicSubagentPartialResultMatchesAdvertisedContract(t *testing.T) {
	s, _, parent := dynamicSubagentFixture(t)
	child := dynamicSpawn(t, s, parent, "partial")
	dynamicExecute(t, s, child, dynamicToolCall("send_parent_message", "partial-result", `{"kind":"partial_result","text":"已确认两条事实"}`))
	messages, err := s.repo.AgentSubagentMessages("user", parent)
	if err != nil || len(messages) != 1 || messages[0].Kind != "partial_result" {
		t.Fatalf("advertised kind rejected: %+v %v", messages, err)
	}
}

func TestDynamicSubagentWaitYieldsSingleCanvasCapacity(t *testing.T) {
	s, db, parent := dynamicSubagentFixture(t)
	policy := model.DefaultAgentSchedulerSetting()
	policy.MaxResidentPerCanvas = 1
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := s.repo.ClaimPiAgentFair("parent-worker", time.Now().Add(time.Minute), policy)
	if err != nil || claimed == nil || claimed.ID != parent {
		t.Fatalf("parent claim: %+v %v", claimed, err)
	}
	child := dynamicSpawn(t, s, parent, "capacity")
	dynamicExecute(t, s, parent, dynamicToolCall("wait_subagents", "capacity-wait", "{}"))
	session, _, err := s.repo.CloudAgentPiSession("user", claimed.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PiRelease("user", parent, fmt.Sprintf("parent-worker@%d", session.LeaseEpoch)); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.repo.ClaimPiAgentFair("child-worker", time.Now().Add(time.Minute), policy)
	if err != nil || claimed == nil || claimed.ID != child {
		got := "none"
		if claimed != nil {
			got = claimed.ID
		}
		t.Fatalf("waiting parent monopolizes capacity: got=%s expected=%s err=%v", got, child, err)
	}
}

func TestDynamicSubagentAsyncWaitSuspension(t *testing.T) {
	for _, scenario := range []string{"async_receipt", "phase_report"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, parent := dynamicSubagentFixture(t)
			if err := db.AutoMigrate(&model.AgentToolOperation{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{}); err != nil {
				t.Fatal(err)
			}
			claimed, err := s.repo.ClaimPiAgentFair("parent-worker", time.Now().Add(time.Minute), model.DefaultAgentSchedulerSetting())
			if err != nil || claimed == nil || claimed.ID != parent {
				t.Fatalf("parent claim: %+v %v", claimed, err)
			}
			dynamicSpawn(t, s, parent, "async-capacity")
			dynamicExecute(t, s, parent, dynamicToolCall("wait_subagents", "async-wait", "{}"))
			session, _, err := s.repo.CloudAgentPiSession("user", claimed.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			owner := fmt.Sprintf("parent-worker@%d", session.LeaseEpoch)
			if scenario == "async_receipt" {
				receipt, err := s.PiToolAdvanceAsync("user", parent, owner, "dynamic-fixture", "async-wait")
				if err != nil || receipt == nil || !receipt.Pending || !receipt.Suspended {
					t.Fatalf("async wait must release worker residency: %+v %v", receipt, err)
				}
			} else {
				if err := s.PiRuntimePhase("user", parent, owner, PiRuntimePhaseRequest{Phase: "waiting_tool", Kind: "tool", WaitID: "operation", Reason: "waiting"}); err != nil {
					t.Fatal(err)
				}
				run, err := s.repo.CloudAgent("user", parent)
				if err != nil {
					t.Fatal(err)
				}
				if run.WaitKind != "subagents" || run.WaitID != "async-wait" {
					t.Fatalf("generic tool phase erased durable dependency: kind=%s id=%s", run.WaitKind, run.WaitID)
				}
			}
		})
	}
}
