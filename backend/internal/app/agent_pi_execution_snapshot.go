package app

import (
	"encoding/json"
	"sort"

	as "yingce/backend/internal/agentservice"
)

// applyPiExecutionSnapshot fills the generic execution identity on a snapshot.
// Canvas runs pass nil and keep the fields empty.
func applyPiExecutionSnapshot(snapshot *PiAgentSnapshot, execution *cloudAgentExecutionState) {
	if snapshot == nil || execution == nil {
		return
	}
	scope := execution.Scope
	snapshot.ProtocolVersion = execution.ProtocolVersion
	snapshot.SessionID = execution.SessionID
	snapshot.DefinitionID = execution.DefinitionID
	snapshot.DefinitionVersion = execution.DefinitionVersion
	snapshot.Role = execution.Role
	snapshot.Scope = &scope
	snapshot.TriggerMessageID = execution.TriggerMessageID
	snapshot.Execution = execution.Binding
	snapshot.EffectiveTools = piExecutionEffectiveTools(execution.Tools)
	snapshot.ToolCatalog = piExecutionToolCatalog(execution.Tools)
}

// piExecutionToolSpecs is the Pi-facing snapshot of the frozen generic tools.
// The backend still re-checks every invocation, so Allowed only mirrors what Go
// already froze for this run.
func piExecutionToolSpecs(tools []as.ToolSpec) []PiAgentToolSpec {
	specs := make([]PiAgentToolSpec, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		parameters := map[string]any{}
		if len(tool.Parameters) > 0 {
			_ = json.Unmarshal(tool.Parameters, &parameters)
		}
		if parameters == nil {
			parameters = map[string]any{}
		}
		specs = append(specs, PiAgentToolSpec{
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Allowed: true,
		})
	}
	return specs
}

func piExecutionCanonicalTools(tools []as.ToolSpec) []map[string]any {
	canonical := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		var parameters any
		if len(tool.Parameters) > 0 && json.Unmarshal(tool.Parameters, &parameters) != nil {
			continue
		}
		canonical = append(canonical, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": parameters,
			},
		})
	}
	return canonical
}

// piExecutionEffectiveTools keeps the raw JSON Schema the worker and gateway
// both consume.
func piExecutionEffectiveTools(tools []as.ToolSpec) []as.EffectiveTool {
	effective := make([]as.EffectiveTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		effective = append(effective, as.EffectiveTool{
			Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, Charges: tool.Charges,
		})
	}
	return effective
}

func piExecutionToolCatalog(tools []as.ToolSpec) []string {
	catalog := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			catalog = append(catalog, tool.Name)
		}
	}
	sort.Strings(catalog)
	return catalog
}
