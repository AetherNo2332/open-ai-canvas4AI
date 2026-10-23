package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestAgentCallLimitsPolicyPersistenceDefaultsAndReset(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	policy := defaultRuntimePolicy()
	if policy.Task.AgentMaxToolCallsPerStep != 8 || policy.Task.AgentMaxReadToolCallsPerRun != 32 || policy.Task.AgentMaxStepsPerRun != 0 {
		t.Fatalf("unexpected defaults: %+v", policy.Task)
	}
	legacy, _ := json.Marshal(policy)
	var legacyObject map[string]any
	if err := json.Unmarshal(legacy, &legacyObject); err != nil {
		t.Fatal(err)
	}
	legacyTask := legacyObject["task"].(map[string]any)
	for _, key := range []string{"agentMaxToolCallsPerStep", "agentMaxReadToolCallsPerRun", "agentMaxStepsPerRun"} {
		delete(legacyTask, key)
	}
	legacy, _ = json.Marshal(legacyObject)
	if err := db.Create(&model.SystemSetting{Key: "runtime_policy", ValueJSON: string(legacy)}).Error; err != nil {
		t.Fatal(err)
	}
	// A fresh service observes persisted old JSON instead of the fixture's default cache.
	s.platform = nil
	limits, err := s.cloudAgentStepLimits()
	if err != nil || limits.CallLimits.ToolCallsPerStep != 8 || limits.CallLimits.ReadCallsPerRun != 32 {
		t.Fatalf("old policy defaults = %+v err=%v", limits, err)
	}
	policy.Task.AgentMaxToolCallsPerStep = 11
	policy.Task.AgentMaxReadToolCallsPerRun = 53
	policy.Task.AgentMaxStepsPerRun = 4
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	limits, err = s.cloudAgentStepLimits()
	if err != nil || limits.CallLimits != (cloudAgentCallLimits{ToolCallsPerStep: 11, ReadCallsPerRun: 53, StepsPerRun: 4}) {
		t.Fatalf("saved policy not effective: %+v err=%v", limits, err)
	}
	if _, err := s.ResetRuntimePolicySetting(actor); err != nil {
		t.Fatal(err)
	}
	limits, err = s.cloudAgentStepLimits()
	if err != nil || limits.CallLimits != (cloudAgentCallLimits{ToolCallsPerStep: 8, ReadCallsPerRun: 32}) {
		t.Fatalf("reset not effective: %+v err=%v", limits, err)
	}
}

func TestAgentCallLimitsModelEnvelopeContainsEffectiveFacts(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	policy := defaultRuntimePolicy()
	policy.Task.AgentMaxToolCallsPerStep = 3
	policy.Task.AgentMaxReadToolCallsPerRun = 17
	policy.Task.AgentMaxStepsPerRun = 5
	if _, err := s.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
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
		AgentRequests struct {
			Canonical canonicalAgentRequest `json:"canonical"`
		} `json:"agentRequests"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range input.AgentRequests.Canonical.Messages {
		content := stringField(msg, "content")
		if !strings.Contains(content, `"kind":"call_limits"`) {
			continue
		}
		var facts cloudAgentRuntimeContext
		if err := json.Unmarshal([]byte(strings.TrimPrefix(content, cloudAgentRuntimeContextMarker)), &facts); err != nil {
			t.Fatal(err)
		}
		if facts.CallLimits == nil || *facts.CallLimits != (cloudAgentCallLimits{ToolCallsPerStep: 3, ReadCallsPerRun: 17, StepsPerRun: 5}) {
			t.Fatalf("wrong model limits: %+v", facts.CallLimits)
		}
		found = true
	}
	if !found {
		t.Fatal("model envelope did not contain live call limits")
	}
}

func TestAgentCallLimitsStepBudgetUsesStricterNonzeroLimit(t *testing.T) {
	for _, tc := range []struct{ admin, request, want int }{
		{0, 0, 0}, {0, 7, 7}, {5, 0, 5}, {5, 7, 5}, {7, 5, 5},
	} {
		t.Run(fmt.Sprintf("%d/%d", tc.admin, tc.request), func(t *testing.T) {
			state := &cloudAgentRuntime{CallLimits: cloudAgentCallLimits{StepsPerRun: tc.admin}}
			state.Request.Budget.MaxSteps = tc.request
			if got := state.callLimits().stepLimit(state.Request); got != tc.want {
				t.Fatalf("effective steps = %d, want %d", got, tc.want)
			}
			state.Step = tc.want
			if got := cloudAgentStepBudgetExhausted(state); got != (tc.want > 0) {
				t.Fatalf("exhausted = %v at limit %d", got, tc.want)
			}
		})
	}
}

func TestAgentCallLimitsNativeReadBudgetIsSharedAndIdempotent(t *testing.T) {
	s, _, run, skill := nativeSkillLeasedFixture(t)
	actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	policy := defaultRuntimePolicy()
	policy.Task.AgentMaxReadToolCallsPerRun = 2
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	request := PiSkillFileRequest{NativeName: skill.NativeName, Path: "SKILL.md", Limit: 1}
	for range 2 {
		if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err != nil {
			t.Fatal(err)
		}
	}
	_, state := reloadPiRun(t, s, run.ID)
	if state.ReadToolCalls != 1 {
		t.Fatalf("page retry consumed budget: %d", state.ReadToolCalls)
	}
	request.Offset = 1
	if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err != nil {
		t.Fatal(err)
	}
	// Raising the policy allows another page without starting a new run.
	policy.Task.AgentMaxReadToolCallsPerRun = 3
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	request.Offset = 2
	if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	if _, err := cloudAgentReadToolCached(s.repo, "user", state, piAgentTestCall("other-read", "canvas_list_node_types", `{}`)); err == nil {
		t.Fatal("canvas read bypassed native shared budget")
	}
	request.Offset = 3
	if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err == nil {
		t.Fatal("native page bypassed read budget")
	}
	failed, _ := reloadPiRun(t, s, run.ID)
	if failed.Status != "failed" {
		t.Fatal("native read budget must terminate run")
	}
}

func TestAgentCallLimitsModelResultCountsNativeTools(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			s, db, run, _ := nativeSkillLeasedFixture(t)
			calls := []cloudAgentCall{piAgentTestCall("read-a", "read", `{"path":"/skills/a/SKILL.md"}`), piAgentTestCall("read-b", "read", `{"path":"/skills/b/SKILL.md"}`)}
			if mixed {
				calls[1] = piAgentTestCall("canvas", "canvas_list_node_types", `{}`)
			}
			encoded, _ := json.Marshal(map[string]any{"toolCalls": calls})
			const taskID = "native-over-limit-step"
			if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(encoded)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				state.LastStepTaskID = taskID
				state.CallLimits.ToolCallsPerStep = 1
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			view, err := s.PiModelStepView("user", run.ID, run.LeaseOwner, taskID)
			if err != nil || view.Status != "failed" {
				t.Fatalf("native tools escaped single-step limit: view=%+v err=%v", view, err)
			}
		})
	}
}

func TestAgentCallLimitsNoToolTurnUsesRaisedBudget(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	const taskID = "raised-limit-no-tool-step"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"text":"working"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.Step = 1
		state.Request.Budget.MaxSteps = 0
		state.CallLimits.StepsPerRun = 1
		state.PendingInterjections = []cloudAgentInterjection{{ID: "pending-user-input", Text: "继续分析刚补充的内容"}}
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	policy := defaultRuntimePolicy()
	policy.Task.AgentMaxStepsPerRun = 5
	if _, err := s.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	decision, err := s.PiNoToolTurn("user", run.ID, run.LeaseOwner, taskID)
	if err != nil || decision.Status != "continue" {
		t.Fatalf("raised budget ignored in completion: decision=%+v err=%v", decision, err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	if state.CallLimits.StepsPerRun != 5 {
		t.Fatalf("updated step cap was not persisted: %+v", state.CallLimits)
	}
}

func TestAgentCallLimitsReadBudgetCoversCachedAndUncachedTools(t *testing.T) {
	s, _, _, canvasID := cloudAgentRulesFixtureDB(t)
	for _, tool := range []string{"canvas_list_node_types", "canvas_get_state"} {
		t.Run(tool, func(t *testing.T) {
			state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: canvasID}, CallLimits: cloudAgentCallLimits{ReadCallsPerRun: 2}}
			for index := 0; index < 2; index++ {
				args := `{}`
				if tool == "canvas_get_state" {
					args = fmt.Sprintf(`{"offset":%d}`, index)
				}
				if _, err := cloudAgentReadToolCached(s.repo, "user", state, piAgentTestCall(fmt.Sprint(index), tool, args)); err != nil {
					t.Fatal(err)
				}
			}
			_, err := cloudAgentReadToolCached(s.repo, "user", state, piAgentTestCall("over", tool, `{}`))
			var budgetErr *cloudAgentReadLoopError
			if !errors.As(err, &budgetErr) || !budgetErr.Budget || state.ReadToolCalls != 2 {
				t.Fatalf("configured read budget not enforced: count=%d err=%v", state.ReadToolCalls, err)
			}
		})
	}
}

func TestAgentCallLimitsReadBudgetWithoutDatabase(t *testing.T) {
	state := &cloudAgentRuntime{CallLimits: cloudAgentCallLimits{ReadCallsPerRun: 2}}
	for _, id := range []string{"first", "second"} {
		if _, err := cloudAgentReadToolCached(nil, "user", state, piAgentTestCall(id, "canvas_list_node_types", `{}`)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := cloudAgentReadToolCached(nil, "user", state, piAgentTestCall("over", "canvas_list_node_types", `{}`))
	var budgetErr *cloudAgentReadLoopError
	if !errors.As(err, &budgetErr) || !budgetErr.Budget || state.ReadToolCalls != 2 {
		t.Fatalf("read count=%d err=%v", state.ReadToolCalls, err)
	}
}

func TestAgentCallLimitsPiBatchRaiseLowerAndReplay(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	policy := defaultRuntimePolicy()
	policy.Task.AgentMaxToolCallsPerStep = 12
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	calls := make([]cloudAgentCall, 9)
	for i := range calls {
		calls[i] = piAgentTestCall(fmt.Sprintf("call-%d", i), "canvas_list_node_types", `{}`)
	}
	encoded, _ := json.Marshal(map[string]any{"toolCalls": calls})
	const taskID = "configured-batch-step"
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(encoded)}).Error; err != nil {
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
	batch := PiToolBatchRequest{TaskID: taskID, Calls: calls}
	policy.Task.AgentMaxToolCallsPerStep = 2
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err == nil {
		t.Fatal("batch exceeded configured limit")
	}
	policy.Task.AgentMaxToolCallsPerStep = 12
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
		t.Fatalf("raised limit did not allow batch: %v", err)
	}
	before, state := reloadPiRun(t, s, run.ID)
	if len(state.Calls) != 9 {
		t.Fatal("admitted batch was not recoverable")
	}
	policy.Task.AgentMaxToolCallsPerStep = 1
	if _, err := s.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil {
		t.Fatalf("lowering limit broke admitted batch replay: %v", err)
	}
	after, _ := reloadPiRun(t, s, run.ID)
	if after.Revision != before.Revision {
		t.Fatal("batch replay mutated run")
	}
}

func TestAgentCallLimitsPiModelCannotBypassAdminStepBudget(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	policy := defaultRuntimePolicy()
	policy.Task.AgentMaxStepsPerRun = 1
	if _, err := s.UpdateRuntimePolicySetting(&model.User{ID: "admin", Role: model.UserRoleAdmin}, policy); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Request.Budget.MaxSteps = 0
		state.Step = 1
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, state := reloadPiRun(t, s, run.ID)
	request, _ := piFirstStepRequest(state)
	var before, after int64
	if err := db.Model(&model.Task{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiModelStep("user", run.ID, leased.LeaseOwner, request); err == nil {
		t.Fatal("request unlimited budget bypassed admin limit")
	}
	failed, _ := reloadPiRun(t, s, run.ID)
	if failed.Status != "failed" {
		t.Fatalf("exhausted run status = %s", failed.Status)
	}
	if err := db.Model(&model.Task{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("exhausted budget created model task")
	}
}
