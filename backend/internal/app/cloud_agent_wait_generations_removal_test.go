package app

import (
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentWaitGenerationsRemovedFromToolContracts(t *testing.T) {
	request := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, VisionEnabled: true,
		MemoryMode: model.AgentMemoryDocumentModeAuto, WebSearchEnabled: true, SkillIDs: []string{"schema-artifact-skill"}}
	request.Budget.MaxCredits, request.Budget.MaxGenerationTasks = 1, 1
	for _, tool := range compileCloudAgentToolsForRuntime(request, cloudAgentSkillRuntimeNative) {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") == "wait_generations" {
			t.Fatalf("wait_generations is still compiled for %+v", request)
		}
	}
	for _, tool := range backendToolArtifact().Tools {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") == "wait_generations" {
			t.Fatal("wait_generations is still in the schema artifact")
		}
	}
	if slices.Contains(CloudAgentSupportedToolNames(), "wait_generations") {
		t.Fatal("wait_generations is still part of the supported tool directory")
	}
	definition := defaultAgentDefinitionRegistry.latest["canvas.default"]
	if slices.Contains(definition.BusinessTools, "wait_generations") {
		t.Fatal("canvas.default still lists wait_generations")
	}
	if category := cloudAgentToolCategory("wait_generations"); category != "" {
		t.Fatalf("wait_generations still has a category: %s", category)
	}
}

func TestLegacyGenerationWaitCheckpointIsRepairedOnce(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	wait := piAgentTestCall("legacy-wait", "wait_generations", `{}`)
	run, err = s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Calls = []cloudAgentCall{wait}
	state.CallIndex = 0
	state.PiToolBatchTaskID = firstNonEmpty(state.LastStepTaskID, state.ActiveTaskID)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": state.Calls})
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "waiting_tool", "generations", wait.ID, "等待生成结果"
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.piAgentSnapshot(run)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.WaitKind != "" || repaired.WaitID != "" || repaired.RuntimePhase == "waiting_tool" {
		t.Fatalf("legacy generation wait was not cleared: %+v", repaired)
	}
	decoded, err := cloudAgentDecode(repaired)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CallIndex != 1 {
		t.Fatalf("legacy wait call did not advance: callIndex=%d", decoded.CallIndex)
	}
	message := cloudAgentToolMessageForTest(t, decoded, wait.ID)
	if !strings.Contains(message, "已移除") || !strings.Contains(message, "finish_run") {
		t.Fatalf("legacy tool receipt is not actionable: %s", message)
	}
	if len(decoded.PendingGenerations) != 1 {
		t.Fatalf("repair resubmitted or dropped the generation: %+v", decoded.PendingGenerations)
	}
	if snapshot.PendingToolBatch != nil {
		t.Fatalf("repaired snapshot still replays the removed wait: %+v", snapshot.PendingToolBatch)
	}
	// A second read is idempotent: no duplicate compatibility receipt.
	again, err := s.piAgentSnapshot(repaired)
	if err != nil {
		t.Fatal(err)
	}
	if again.PendingToolBatch != nil {
		t.Fatalf("second snapshot replayed the removed wait: %+v", again.PendingToolBatch)
	}
	latest, _ := s.repo.CloudAgent("user", run.ID)
	latestState, _ := cloudAgentDecode(latest)
	if latestState.CallIndex != 1 {
		t.Fatalf("second repair advanced again: %d", latestState.CallIndex)
	}
}
