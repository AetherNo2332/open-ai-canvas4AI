package app

import "testing"

func TestDynamicSubagentToolsAreParentOnlyAndBounded(t *testing.T) {
	parent := CloudAgentRequest{SubagentEnabled: true, PermissionMode: "auto", ContextScope: []string{"canvas"}}
	if !cloudAgentToolAllowed(parent, "spawn_subagent") || !cloudAgentToolAllowed(parent, "wait_subagents") {
		t.Fatal("parent tools are not exposed when consent is enabled")
	}
	child := parent
	child.SubagentEnabled = false
	child.subagent = &SubagentRuntime{LinkID: "link", ParentRunID: "parent", ChildRunID: "child", Depth: 1}
	if !cloudAgentToolAllowed(child, "send_parent_message") || !cloudAgentToolAllowed(child, "finish_subagent") {
		t.Fatal("child reporting tools are not exposed")
	}
	if cloudAgentToolAllowed(child, "spawn_subagent") {
		t.Fatal("child must not receive spawn_subagent")
	}
}

func TestDynamicSubagentInputLimits(t *testing.T) {
	if err := validateSpawnSubagentInput(spawnSubagentInput{Name: "研究员", Role: "资料整理", Objective: "整理三条事实", MaxSteps: 20}); err != nil {
		t.Fatal(err)
	}
	if err := validateSpawnSubagentInput(spawnSubagentInput{Name: "", Role: "资料整理", Objective: "整理事实"}); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := validateSpawnSubagentInput(spawnSubagentInput{Name: "研究员", Role: "资料整理", Objective: "整理事实", MaxSteps: 21}); err == nil {
		t.Fatal("maxSteps above server limit accepted")
	}
}
