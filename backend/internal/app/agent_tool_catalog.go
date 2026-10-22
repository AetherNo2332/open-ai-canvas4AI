package app

import "strings"

// AgentToolSchemaArtifact is the exported catalog used by offline documentation
// and parameter-contract fixtures. It does not participate in worker admission.
type AgentToolSchemaArtifact struct {
	SchemaVersion string           `json:"schemaVersion"`
	Tools         []map[string]any `json:"tools"`
}

func CloudAgentToolSchemaArtifact() AgentToolSchemaArtifact { return backendToolArtifact() }

func CloudAgentToolCount() int { return len(backendToolArtifact().Tools) }

func backendToolArtifact() AgentToolSchemaArtifact {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, VisionEnabled: true, HasMemories: true, WebSearchEnabled: true, SkillIDs: []string{"schema-artifact-skill"}}
	req.Budget.MaxCredits = 1
	artifact := AgentToolSchemaArtifact{SchemaVersion: cloudAgentToolSchemaVersion, Tools: append(cloudAgentTools(req), nativeSkillReadToolSchema())}
	for _, request := range []CloudAgentRequest{{SubagentEnabled: true, PermissionMode: "read_only", ContextScope: []string{"canvas"}}, {PermissionMode: "read_only", ContextScope: []string{"canvas"}, subagent: &SubagentRuntime{Depth: 1}}} {
		for _, tool := range cloudAgentTools(request) {
			name := stringField(tool["function"].(map[string]any), "name")
			if strings.Contains(name, "subagent") || name == "send_parent_message" {
				artifact.Tools = append(artifact.Tools, tool)
			}
		}
	}
	return artifact
}
