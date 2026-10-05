package app

import (
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

func crewRunFixture(t *testing.T) (*Service, *CrewView, *gorm.DB) {
	t.Helper()
	s, db := agentRunFixture(t)
	previousDataDir := selectionDataDir
	selectionDataDir = s.dataDir
	t.Cleanup(func() { selectionDataDir = previousDataDir })
	initial, err := s.GetAgentWorkspace("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", initial.Revision, "共同规则"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		id := newID()
		seedSelectionSkill(t, s, db, id, 10, false, 1)
		seedDefault(t, db, id, i)
	}
	req := agentTestRequest()
	members := []CrewMemberInput{}
	for i, name := range []string{"导演", "编剧", "美术"} {
		member := crewMember(name, i)
		member.Model = CrewModelConfig{Model: req.Model, ChannelID: req.ChannelID, ChannelModelKey: req.ChannelModelKey}
		if i == 0 {
			member.Role = model.CrewMemberRoleCoordinator
		}
		members = append(members, member)
	}
	crew, err := s.CreateCrew("user", "agent-canvas", CreateCrewInput{Name: "剧组", Status: "enabled", Members: members})
	if err != nil {
		t.Fatal(err)
	}
	return s, crew, db
}

func TestAgentCrewRunIndependentSessionsAndFrozenDefaults(t *testing.T) {
	s, crew, _ := crewRunFixture(t)
	input := CreateCrewRunInput{Prompt: "拆解故事", IdempotencyKey: "crew-test"}
	run, err := s.CreateCrewRun("user", crew.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Members) != 3 {
		t.Fatalf("expected coordinator and two members, got %d", len(run.Members))
	}
	conversations, sessions := map[string]bool{}, map[string]bool{}
	for _, member := range run.Members {
		execution, err := s.repo.CloudAgent("user", member.AgentRunID)
		if err != nil {
			t.Fatal(err)
		}
		state, err := cloudAgentDecode(execution)
		if err != nil {
			t.Fatal(err)
		}
		if state.Workspace == nil || state.Workspace.AgentsMD != "共同规则" || state.Workspace.AgentsMDHash != run.WorkspaceHash || len(state.Skills) != 20 {
			t.Fatalf("member snapshot mismatch: skills=%d", len(state.Skills))
		}
		if state.Request.PermissionMode != "read_only" {
			t.Fatal("member has direct canvas write permission")
		}
		if member.Role == model.CrewMemberRoleMember && execution.ParentID != run.CoordinatorRunID {
			t.Fatal("member parent missing")
		}
		if conversations[execution.ConversationID] {
			t.Fatal("members share conversation")
		}
		conversations[execution.ConversationID] = true
		session, _, err := s.repo.CloudAgentPiSession("user", execution.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		if sessions[session.ID] {
			t.Fatal("members share Pi session")
		}
		sessions[session.ID] = true
	}
	same, err := s.CreateCrewRun("user", crew.ID, input)
	if err != nil || same.ID != run.ID {
		t.Fatalf("idempotency: %v", err)
	}
	if _, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "different", IdempotencyKey: "crew-test"}); err == nil {
		t.Fatal("key reused for different prompt")
	}
	if _, err := s.GetCrewRun("other", run.ID); err == nil {
		t.Fatal("cross-account run exposed")
	}
	view, err := s.GetAgentWorkspace("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", view.Revision, "后来的规则"); err != nil {
		t.Fatal(err)
	}
	first, _ := s.repo.CloudAgent("user", run.Members[1].AgentRunID)
	state, err := cloudAgentDecode(first)
	if err != nil || state.Workspace.AgentsMD != "共同规则" {
		t.Fatal("active run configuration drift")
	}
	public, _ := json.Marshal(run)
	for _, private := range []string{"共同规则", "canonical", "systemPrompt", "apiKey", "piSessionEntries"} {
		if strings.Contains(string(public), private) {
			t.Fatalf("public crew leaked %s", private)
		}
	}
}

func TestAgentCrewRunAdmissionIsAtomic(t *testing.T) {
	s, crew, db := crewRunFixture(t)
	bad := crew.Members[2].CrewMemberInput
	bad.Model.ChannelModelKey = "not-an-available-model"
	if _, err := s.UpdateCrewMember("user", crew.Members[2].ID, crew.Revision, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "不能部分启动", IdempotencyKey: "atomic"}); err == nil {
		t.Fatal("unavailable member model accepted")
	}
	for _, table := range []any{&model.AgentCrewRun{}, &model.AgentCrewMemberRun{}, &model.CloudAgentExecution{}, &model.BillingOrder{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("partial admission: %T count=%d err=%v", table, count, err)
		}
	}
}

// Queuing the wrong member or sharing a result transcript would break isolation.
func TestCrewDispatchStructuredResultAndReplay(t *testing.T) {
	s, crew, db := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "parallel", IdempotencyKey: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	for i, member := range run.Members[1:] {
		task := CrewTaskInput{TaskID: []string{"script", "art"}[i], Title: "任务", Instructions: "仅回传摘要"}
		if err := s.DispatchCrewTask(run.ID, member.MemberID, task); err != nil {
			t.Fatal(err)
		}
		if err := s.DispatchCrewTask(run.ID, member.MemberID, task); err != nil {
			t.Fatalf("replay: %v", err)
		}
		changed := task
		changed.Instructions = "changed"
		if err := s.DispatchCrewTask(run.ID, member.MemberID, changed); err == nil {
			t.Fatal("changed task accepted")
		}
		execution, _ := s.repo.CloudAgent("user", member.AgentRunID)
		state, err := cloudAgentDecode(execution)
		if err != nil || execution.Status != "queued" || state.Crew.Task.TaskID != task.TaskID {
			t.Fatalf("dispatch not durable: %v", err)
		}
		if cloudAgentToolAllowed(state.Request, "delegate_task") || cloudAgentToolAllowed(state.Request, "canvas_apply_ops") || !cloudAgentToolAllowed(state.Request, "task_result") {
			t.Fatal("member tools incorrect")
		}
		result := MemberTaskResult{TaskID: task.TaskID, Summary: []string{"编剧摘要", "美术摘要"}[i]}
		if err := s.CompleteMemberRun(member.ID, result); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteMemberRun(member.ID, result); err != nil {
			t.Fatal(err)
		}
		result.Summary = "changed"
		if err := s.CompleteMemberRun(member.ID, result); err == nil {
			t.Fatal("result replacement accepted")
		}
	}
	var count int64
	db.Model(&model.AgentCrewMessage{}).Where("crew_run_id = ?", run.ID).Count(&count)
	if count != 4 {
		t.Fatalf("duplicate messages: %d", count)
	}
	coordinator, _ := s.repo.CloudAgent("user", run.CoordinatorRunID)
	state, _ := cloudAgentDecode(coordinator)
	if !cloudAgentToolAllowed(state.Request, "delegate_task") || cloudAgentToolAllowed(state.Request, "task_result") {
		t.Fatal("coordinator tools incorrect")
	}
	for _, member := range run.Members[1:] {
		execution, _ := s.repo.CloudAgent("user", member.AgentRunID)
		if strings.Contains(execution.StateJSON, "编剧摘要") || strings.Contains(execution.StateJSON, "美术摘要") {
			t.Fatal("result injected into member transcript")
		}
	}
}

func TestCrewMemberCannotFinishWithoutStructuredResult(t *testing.T) {
	state := cloudAgentRuntime{Crew: &CrewMemberRuntime{Role: model.CrewMemberRoleMember}}
	if cloudAgentEvaluateCompletion(&state).Final {
		t.Fatal("member completed without task_result")
	}
}

func TestCrewRuntimeToolsWaitAndDurableReceipt(t *testing.T) {
	s, crew, db := crewRunFixture(t)
	view, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "runtime", IdempotencyKey: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, startedState := startPiAgentFirstStep(t, s, view.CoordinatorRunID)
	state := *startedState
	claimedView, err := s.GetCrewRun("user", view.ID)
	if err != nil || claimedView.Members[0].Status != model.MemberRunRunning {
		t.Fatal("worker claim did not project member running")
	}
	delegate := cloudAgentCall{ID: "delegate", Type: "function"}
	delegate.Function.Name = "delegate_task"
	args, _ := json.Marshal(map[string]any{"memberId": view.Members[1].MemberID, "taskId": "story", "title": "故事", "instructions": "返回摘要"})
	delegate.Function.Arguments = string(args)
	coordinator, state = writeBatch(t, s, coordinator, &state, []cloudAgentCall{delegate})
	if !state.CallAdmissions[0].Allowed {
		t.Fatalf("not admitted: %+v", state.CallAdmissions[0])
	}
	if err := s.executeCloudAgentToolCall(coordinator, &state); err != nil {
		t.Fatal(err)
	}
	coordinator, state = reloadAgentRun(t, s, coordinator.ID)
	wait := cloudAgentCall{ID: "wait", Type: "function"}
	wait.Function.Name = "crew_wait"
	wait.Function.Arguments = "{}"
	state.CallIndex = 0
	coordinator, state = writeBatch(t, s, coordinator, &state, []cloudAgentCall{wait})
	if err := s.executeCloudAgentToolCall(coordinator, &state); err != nil {
		t.Fatal(err)
	}
	stillWaiting, _ := s.repo.CloudAgent("user", coordinator.ID)
	if stillWaiting.Revision != coordinator.Revision {
		t.Fatal("pending wait wrote a receipt")
	}
	member, memberStartedState := startPiAgentFirstStep(t, s, view.Members[1].AgentRunID)
	memberState := *memberStartedState
	result := cloudAgentCall{ID: "result", Type: "function"}
	result.Function.Name = "task_result"
	result.Function.Arguments = `{"taskId":"story","summary":"独立摘要"}`
	member, memberState = writeBatch(t, s, member, &memberState, []cloudAgentCall{result})
	if !memberState.CallAdmissions[0].Allowed {
		t.Fatalf("result not admitted: %+v", memberState.CallAdmissions[0])
	}
	if err := s.executeCloudAgentToolCall(member, &memberState); err != nil {
		t.Fatal(err)
	}
	member, memberState = reloadAgentRun(t, s, member.ID)
	if member.Status != "completed" {
		t.Fatal("member not terminal")
	}
	if err := s.executeCloudAgentToolCall(member, &memberState); err != nil {
		t.Fatal(err)
	}
	if err := s.executeCloudAgentToolCall(coordinator, &state); err != nil {
		t.Fatal(err)
	}
	_, state = reloadAgentRun(t, s, coordinator.ID)
	if !state.Crew.ResultsCollected || !strings.Contains(stringField(state.Canonical.Messages[len(state.Canonical.Messages)-1], "content"), "独立摘要") {
		t.Fatal("coordinator did not receive structured result")
	}
	var count int64
	db.Model(&model.AgentCrewMessage{}).Where("crew_run_id = ? AND kind = ?", view.ID, "result").Count(&count)
	if count != 1 {
		t.Fatalf("replayed result: %d", count)
	}
}

func TestCrewWaitReturnsTerminalFailureWithoutTranscript(t *testing.T) {
	s, crew, _ := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "failure", IdempotencyKey: "failure"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchCrewTask(run.ID, run.Members[1].MemberID, CrewTaskInput{TaskID: "failed", Title: "失败任务", Instructions: "private"}); err != nil {
		t.Fatal(err)
	}
	execution, _ := s.repo.CloudAgent("user", run.Members[1].AgentRunID)
	if err := s.terminateCloudAgent(execution, "private-upstream-error"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.repo.AgentCrewRunSnapshot("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	results, pending, err := crewResults(snapshot)
	if err != nil || pending || len(results) != 1 || results[0].ErrorCode != "member_execution_failed" || strings.Contains(results[0].Summary, "private") {
		t.Fatalf("terminal result: %+v pending=%v err=%v", results, pending, err)
	}
}
