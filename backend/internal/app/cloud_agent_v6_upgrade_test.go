package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentV6UpgradeReplaysFrozenModelStep(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPiAgent("worker-a"); err != nil {
		t.Fatal(err)
	}
	run, state := reloadPiRun(t, s, root.ID)
	request, _ := piFirstStepRequest(state)
	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
	if err != nil {
		t.Fatal(err)
	}
	run, _ = reloadPiRun(t, s, root.ID)
	// The previous compiler used the same policy documents and runtime
	// contract. Simulate its version marker on a checkpoint with frozen input.
	oldState := strings.Replace(run.StateJSON, `"compilerVersion":"cloud-agent-policy-compiler/v8"`, `"compilerVersion":"cloud-agent-policy-compiler/v6"`, 1)
	if oldState == run.StateJSON {
		t.Fatal("missing compiler version in checkpoint")
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("state_json", oldState).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
	if err != nil || replay.TaskID != step.TaskID {
		t.Fatalf("frozen step did not resume with its original task: %+v, %v", replay, err)
	}
	_, restored := reloadPiRun(t, s, root.ID)
	if restored.Policy.CompilerVersion != "cloud-agent-policy-compiler/v6" || restored.Canonical.SystemPrompt != state.Canonical.SystemPrompt {
		t.Fatal("resume rewrote the frozen policy")
	}
}

func TestCloudAgentV6UpgradeRejectsUnsupportedSnapshots(t *testing.T) {
	prompt, policy, err := compileCloudAgentPolicies(agentTestRequest(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	policy.CompilerVersion = "cloud-agent-policy-compiler/v6"
	_, general, err := compileCloudAgentPolicies(platformAgentTestRequest(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*cloudAgentRuntime){
		"changed frozen tool schema":  func(state *cloudAgentRuntime) { state.Snapshot.ToolsDigest = cloudAgentTextDigest("different tools") },
		"missing admitted harness":    func(state *cloudAgentRuntime) { state.Phase = "" },
		"missing first-step contract": func(state *cloudAgentRuntime) { state.Snapshot = nil },
		"changed frozen prompt":       func(state *cloudAgentRuntime) { state.Canonical.SystemPrompt += "changed" },
		"unsupported compiler":        func(state *cloudAgentRuntime) { state.Policy.CompilerVersion = "cloud-agent-policy-compiler/v7" },
		"changed media policy":        func(state *cloudAgentRuntime) { state.Policy.MediaPolicyHash = cloudAgentTextDigest("changed") },
		"changed canvas capabilities": func(state *cloudAgentRuntime) { state.Policy.CapabilitySetHash = cloudAgentTextDigest("changed") },
		"v8 policy under v6 marker": func(state *cloudAgentRuntime) {
			state.Policy.SystemPolicyID = general.SystemPolicyID
			state.Policy.SystemPolicyVersion = general.SystemPolicyVersion
			state.Policy.SystemPolicyHash = general.SystemPolicyHash
		},
		"platform introduced after v6": func(state *cloudAgentRuntime) { state.Request = platformAgentTestRequest() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			state := cloudAgentRuntime{
				Request: agentTestRequest(), Policy: policy,
				ContractVersion: cloudAgentContractVersionFirstStep,
				Phase:           cloudAgentPhaseAwaitingFirstStep,
				Canonical:       canonicalAgentRequest{SystemPrompt: prompt},
				Snapshot:        cloudAgentContractSnapshotFor(prompt, nil),
			}
			mutate(&state)
			if err := validateCloudAgentExecutionPolicy(&state); err == nil {
				t.Fatal("unsupported checkpoint was accepted for execution")
			}
		})
	}
}
