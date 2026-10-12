package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestCloudAgentPressureContinuationCreationUsesInheritedUsage(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	completePiRunWithAssistantForTest(t, s, root.ID, "previous answer")
	execution, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	state.TokenAnchor = &cloudAgentTokenAnchor{TaskID: "last-provider-task", Accepted: true, InputTokens: 80000,
		Model: "text-test", ChannelID: "channel", Step: 5}
	if err := s.repo.MutateCloudAgent("user", root.ID, execution.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	request := agentTestRequest()
	request.Prompt, request.IdempotencyKey = "continue", "provider-pressure-continuation"
	child, err := s.CreateCloudAgentRun("user", request, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	childExecution, err := s.repo.CloudAgent("user", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	childState, err := cloudAgentDecode(childExecution)
	if err != nil {
		t.Fatal(err)
	}
	if childState.TokenAnchor == nil || childState.TokenAnchor.InputTokens != 80000 {
		t.Fatalf("new run lost same-provider usage: %+v", childState.TokenAnchor)
	}
	payload := cloudAgentContextPressurePayload(cloudAgentContextPressure{EstimatedInputTokens: 99999}, &childState)
	if payload["projectedTokens"] != 80000 || payload["tokenSource"] != "provider" {
		t.Fatalf("continuation first pressure must retain provider usage: %+v", payload)
	}
}

func TestCloudAgentPressureLogicalSelectionRetainsResolvedUsage(t *testing.T) {
	state := &cloudAgentRuntime{Request: CloudAgentRequest{Model: "logical-name", LogicalModelID: "logical-id"},
		LastStepModel: "provider-model", LastStepChannelID: "actual-channel",
		TokenAnchor: &cloudAgentTokenAnchor{Accepted: true, InputTokens: 80000, Model: "provider-model", ChannelID: "actual-channel"}}
	cloudAgentExpireTokenAnchorForSelection("run", state)
	if !state.TokenAnchor.Accepted {
		t.Fatal("logical name must not invalidate usage for the same resolved model")
	}
}

func TestCloudAgentPressureAcceptsSuccessfulFallbackUsage(t *testing.T) {
	service, db, _, _ := creationTestService(t)
	for _, channel := range []string{"primary", "fallback"} {
		t.Run(channel, func(t *testing.T) {
			taskID := "fallback-task-" + channel
			if err := db.Create(&model.ApiCallLog{ID: taskID, UserID: "user", TaskID: taskID,
				Model: "fallback-model", ChannelID: channel, Capability: "text", Status: model.ApiCallStatusSucceeded,
				UsageAvailable: true, InputTokens: 80000}).Error; err != nil {
				t.Fatal(err)
			}
			state := &cloudAgentRuntime{RuntimeRunID: "run", Request: CloudAgentRequest{LogicalModelID: "logical"},
				LastStepTaskID: taskID, LastStepOperation: cloudAgentStepOperation,
				LastStepModel: "primary-model", LastStepChannelID: "primary", LastStepEstimate: 99999}
			service.recordCloudAgentTokenAnchor("user", state)
			if state.TokenAnchor == nil || !state.TokenAnchor.Accepted || state.TokenAnchor.InputTokens != 80000 ||
				state.TokenAnchor.Model != "fallback-model" || state.TokenAnchor.ChannelID != channel {
				t.Fatalf("successful fallback must own the latest measurement: %+v", state.TokenAnchor)
			}
			cloudAgentExpireTokenAnchorForSelection("run", state)
			if !state.TokenAnchor.Accepted {
				t.Fatal("fallback measurement must remain valid for the same logical selection")
			}
			if len(state.Events) == 0 || state.Events[len(state.Events)-1].Type != "context_pressure" {
				t.Fatal("fallback completion did not publish measured pressure")
			}
		})
	}
}

func TestCloudAgentPressureFallbackWithoutUsageInvalidatesPreviousRoute(t *testing.T) {
	service, db, _, _ := creationTestService(t)
	if err := db.Create(&model.Task{ID: "fallback-no-usage", UserID: "user", Type: "canvas_text",
		Status: model.TaskStatusSucceeded, InputJSON: `{"config":{"model":"fallback-model","channelId":"fallback"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	state := &cloudAgentRuntime{RuntimeRunID: "run", Request: CloudAgentRequest{LogicalModelID: "logical"},
		LastStepTaskID: "fallback-no-usage", LastStepOperation: cloudAgentStepOperation,
		LastStepModel: "primary-model", LastStepChannelID: "primary",
		TokenAnchor: &cloudAgentTokenAnchor{Accepted: true, InputTokens: 80000, Model: "primary-model", ChannelID: "primary"}}
	service.recordCloudAgentTokenAnchor("user", state)
	if state.TokenAnchor.Accepted {
		t.Fatal("a previous provider measurement cannot survive a fallback to another provider without usage")
	}
}

func TestCloudAgentPressureInheritsSameProviderAcrossRuns(t *testing.T) {
	request := CloudAgentRequest{Model: "m", ChannelID: "c", ChannelModelKey: "upstream-m", LogicalModelID: "logical"}
	parent := &cloudAgentRuntime{Request: request, TokenAnchor: &cloudAgentTokenAnchor{Accepted: true, InputTokens: 80000, Step: 5}}
	inherited := cloudAgentInheritedTokenAnchor(parent, request)
	if inherited == nil || inherited.InputTokens != 80000 || inherited.Step != 0 || inherited == parent.TokenAnchor {
		t.Fatalf("same conversation continuation must copy the latest usage: %+v", inherited)
	}
	changed := request
	changed.ChannelModelKey = "other-upstream-model"
	if cloudAgentInheritedTokenAnchor(parent, changed) != nil {
		t.Fatal("another model must start without the previous provider usage")
	}
}

func TestCloudAgentPressureUsesProviderWithoutLocalDelta(t *testing.T) {
	state := &cloudAgentRuntime{TokenAnchor: &cloudAgentTokenAnchor{Accepted: true, InputTokens: 80000, EstimatedTokens: 75000}}
	budget := cloudAgentContextBudget{Source: "channel-model", InputBudgetTokens: 100000, CompactAtTokens: 85000}
	for _, estimate := range []int{30000, 75000, 99000} {
		pressure := cloudAgentContextPressure{EstimatedInputTokens: estimate, ModelLimitConfigured: true, UsableInputTokens: 100000, InputBudgetTokens: 100000, CompactAtTokens: 85000}
		tokens, source := cloudAgentProjectedInputTokens(pressure, state)
		if tokens != 80000 || source != "provider" {
			t.Fatalf("local estimate %d changed provider pressure: %d / %s", estimate, tokens, source)
		}
		payload := cloudAgentContextPressurePayload(pressure, state)
		if payload["projectedTokens"] != 80000 || payload["pressureRatio"] != 0.8 || payload["compactionPressureRatio"] != 0.8 || payload["estimate"] != false {
			t.Fatalf("display and compaction must use the same measured pressure: %+v", payload)
		}
		reading, configured := cloudAgentCompactionReadingFor(budget, estimate, state)
		if !configured || reading.ProjectedTokens != tokens || reading.Ratio != 0.8 || reading.ProjectedTokens >= reading.CompactAtTokens {
			t.Fatalf("local estimate must not trigger compaction below measured threshold: %+v", reading)
		}
	}
	state.TokenAnchor.InputTokens = 85000
	reading, _ := cloudAgentCompactionReadingFor(budget, 10000, state)
	if reading.ProjectedTokens < reading.CompactAtTokens {
		t.Fatalf("measured threshold must trigger even with a lower local estimate: %+v", reading)
	}
	state.TokenAnchor = nil
	if tokens, source := cloudAgentProjectedInputTokens(cloudAgentContextPressure{EstimatedInputTokens: 12000}, state); tokens != 12000 || source != "estimate" {
		t.Fatalf("missing provider usage must use local estimate: %d / %s", tokens, source)
	}
}

func TestCloudAgentPressureRetainsUsageAcrossPromptWindowAndStepChanges(t *testing.T) {
	state := &cloudAgentRuntime{Step: 100, TokenAnchor: &cloudAgentTokenAnchor{Accepted: true, Step: 1, InputTokens: 80000, Signature: "old", Model: "m", ChannelID: "c", ContextWindowTokens: 128000}}
	cloudAgentExpireTokenAnchorForRequest("run", state, 256000, "new-system-and-tools", "m", "c")
	if !state.TokenAnchor.Accepted {
		t.Fatalf("latest same-provider measurement must remain authoritative: %+v", state.TokenAnchor)
	}
	cloudAgentExpireTokenAnchorForRequest("run", state, 256000, "new-system-and-tools", "other-model", "c")
	if state.TokenAnchor.Accepted {
		t.Fatal("a measurement from a different model must not be reused")
	}
}
