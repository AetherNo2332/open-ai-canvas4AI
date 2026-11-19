package app

import (
	"strings"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// AgentToolSchemaArtifact is the exported catalog used by offline documentation
// and parameter-contract fixtures. It does not participate in worker admission.
type AgentToolSchemaArtifact struct {
	SchemaVersion string           `json:"schemaVersion"`
	Tools         []map[string]any `json:"tools"`
}

func CloudAgentToolSchemaArtifact() AgentToolSchemaArtifact { return backendToolArtifact() }

func CloudAgentToolCount() int { return len(backendToolArtifact().Tools) }

func backendToolArtifact() AgentToolSchemaArtifact {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, VisionEnabled: true, MemoryMode: model.AgentMemoryDocumentModeAuto, WebSearchEnabled: true, SkillIDs: []string{"schema-artifact-skill"}}
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

func builtinAgentToolRegistry() map[string]agentToolRegistration {
	catalog := backendToolArtifact().Tools
	readOnly := agentSchemasByName(compileCloudAgentTools(CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}}))
	noStoryboard := agentSchemasByName(compileCloudAgentTools(CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, StoryboardDisabled: true}))
	registrations := make(map[string]agentToolRegistration, len(catalog))
	for index, schema := range catalog {
		name, _ := cloudAgentToolSchemaIdentity(schema)
		tool := builtinAgentToolPolicy(name)
		tool.name, tool.order, tool.version, tool.schema = name, index, 1, schema
		tool.readOnlySchema, tool.noStoryboardSchema = readOnly[name], noStoryboard[name]
		if err := registerAgentTool(registrations, tool); err != nil {
			panic(err)
		}
	}
	return registrations
}

func agentSchemasByName(tools []map[string]any) map[string]map[string]any {
	schemas := make(map[string]map[string]any, len(tools))
	for _, schema := range tools {
		name, _ := cloudAgentToolSchemaIdentity(schema)
		schemas[name] = schema
	}
	return schemas
}

func builtinAgentToolPolicy(name string) agentToolRegistration {
	tool := agentToolRegistration{businesses: []string{"canvas"}, execute: (*Service).routeCloudAgentToolCall}
	canvas := func(req CloudAgentRequest) bool { return len(req.ContextScope) > 0 }
	switch name {
	case "plan_update", "ask_user", "finish_run", "read":
		tool.platform = true
	case "memory_update":
		tool.platform, tool.writeOnly = true, true
		tool.allowed = func(req CloudAgentRequest) bool {
			return req.subagent == nil && (req.MemoryMode == model.AgentMemoryDocumentModeAuto || req.MemoryMode == model.AgentMemoryDocumentModeReview)
		}
	case "task_get":
		tool.businesses = []string{"canvas", "drama"}
	case "web_search":
		tool.allowed = func(req CloudAgentRequest) bool { return req.WebSearchEnabled }
	case "canvas_inspect_image":
		tool.allowed = func(req CloudAgentRequest) bool { return canvas(req) && req.VisionEnabled }
	case "storyboard":
		tool.allowed = func(req CloudAgentRequest) bool { return canvas(req) && !req.StoryboardDisabled }
	case "spawn_subagent", "wait_subagents", "message_subagent", "subagent_status":
		tool.allowed = func(req CloudAgentRequest) bool { return req.SubagentEnabled && req.subagent == nil }
	case "send_parent_message", "finish_subagent":
		tool.allowed = func(req CloudAgentRequest) bool { return req.subagent != nil }
	case "director", "canvas_list_node_types", "canvas_list_assets", "canvas_read_content",
		"canvas_search_nodes", "canvas_get_state", "canvas_read_batch_table":
		tool.allowed = canvas
	case "canvas_undo", "canvas_redo", "canvas_bind_asset", "model_list", "canvas_create_character",
		"canvas_edit_batch_table", "canvas_apply_ops", "canvas_arrange_nodes", "generate_media", "wait_generations",
		"generate_media_batch", "media_batch_control", "canvas_build_board", "canvas_preflight":
		tool.allowed, tool.writeOnly = canvas, true
	default:
		tool.execute = nil
	}
	return tool
}

func agentScopedReadToolSchema(capabilities *AgentCapabilitySnapshot) map[string]any {
	schema := nativeSkillReadToolSchema()
	function := schema["function"].(map[string]any)
	sources := make([]string, 0, len(capabilities.ReadAdapters)+1)
	for _, name := range capabilities.ReadAdapters {
		sources = append(sources, name+"/<id>")
	}
	if capabilities.SkillRead {
		sources = append(sources, "skills/<name>/SKILL.md 或其引用文件")
	}
	function["description"] = "读取本轮已授权内容：" + strings.Join(sources, "、") +
		"。offset/limit 按 Unicode 字符分页，仅用于文本。不支持任意 URL 或本机路径。"
	return schema
}

func agentCapabilityConfigurationError(message, tool string) error {
	err := kernel.NewAppError(422, message)
	err.Reason = "agent_capability_configuration_invalid"
	err.Details = map[string]any{"tool": tool}
	return err
}
