package app

import (
	"fmt"
	"slices"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

type agentToolExecutor func(*Service, *model.CloudAgentExecution, *cloudAgentRuntime, cloudAgentCall, bool) error

type agentToolRegistration struct {
	version int
	execute agentToolExecutor
}

var agentToolRegistry map[string]agentToolRegistration

func builtinAgentToolRegistry() map[string]agentToolRegistration {
	registered := make(map[string]agentToolRegistration)
	for _, name := range cloudAgentToolNames(backendToolArtifact().Tools) {
		registered[name] = agentToolRegistration{version: 1, execute: (*Service).routeCloudAgentToolCall}
	}
	return registered
}

func validateAgentDefinition(definition AgentDefinition) error {
	if validateCloudAgentID(definition.ID, "Agent 配置 ID", 80) != nil || definition.Version <= 0 || definition.BusinessType == "" {
		return fmt.Errorf("invalid Agent definition identity %q v%d", definition.ID, definition.Version)
	}
	if definition.authorize == nil && agentBuiltinObjectAuthorizer(definition.BusinessType) == nil {
		return fmt.Errorf("missing object authorizer for %q", definition.BusinessType)
	}
	if definition.BusinessTools == nil {
		return fmt.Errorf("Agent definition %q must declare businessTools", definition.ID)
	}
	seen := make(map[string]bool)
	for _, name := range definition.BusinessTools {
		if seen[name] || agentToolRegistry[name].execute == nil || agentPlatformTool(name) {
			return fmt.Errorf("invalid or duplicate business tool %q in %q", name, definition.ID)
		}
		seen[name] = true
	}
	for _, name := range definition.ReadAdapters {
		if seen[name] || definition.readAdapters[name].business != definition.BusinessType {
			return fmt.Errorf("invalid or duplicate read adapter %q in %q", name, definition.ID)
		}
		seen[name] = true
	}
	if definition.MemoryPolicy != "" && definition.MemoryPolicy != "off" && definition.MemoryPolicy != "scope" {
		return fmt.Errorf("invalid Agent memory policy in %q", definition.ID)
	}
	if definition.InteractionPolicy != "" && definition.InteractionPolicy != "ask_user" {
		return fmt.Errorf("invalid Agent interaction policy in %q", definition.ID)
	}
	return nil
}

func agentPlatformTool(name string) bool {
	switch name {
	case "plan_update", "finish_run", "ask_user", "read", "memory_update":
		return true
	default:
		return false
	}
}

type AgentCapabilityAdmission struct {
	VisionEnabled bool
	HasSkills     bool
}

type AgentCapabilitySnapshot struct {
	DefinitionID        string         `json:"definitionId"`
	DefinitionVersion   int            `json:"definitionVersion"`
	BusinessType        string         `json:"businessType"`
	Tools               map[string]int `json:"tools"`
	ReadAdapters        []string       `json:"readAdapters"`
	ReadAdapterVersions map[string]int `json:"readAdapterVersions,omitempty"`
	SkillRead           bool           `json:"skillRead,omitempty"`
	ReadOnly            bool           `json:"readOnly,omitempty"`
}

func CompileAgentCapabilities(definition AgentDefinition, admission AgentCapabilityAdmission) (AgentCapabilitySnapshot, error) {
	snapshot := AgentCapabilitySnapshot{
		DefinitionID: definition.ID, DefinitionVersion: definition.Version, BusinessType: definition.BusinessType,
		Tools: make(map[string]int), ReadOnly: definition.ReadOnly, SkillRead: admission.HasSkills,
		ReadAdapterVersions: make(map[string]int),
	}
	names := append(slices.Clone(definition.BusinessTools), "plan_update", "finish_run")
	if definition.InteractionPolicy == "ask_user" {
		names = append(names, "ask_user")
	}
	if definition.MemoryPolicy == "scope" && !definition.ReadOnly {
		names = append(names, "memory_update")
	}
	for _, namespace := range definition.ReadAdapters {
		adapter := definition.readAdapters[namespace]
		if adapter.read == nil {
			return AgentCapabilitySnapshot{}, fmt.Errorf("missing executor for read adapter %q", namespace)
		}
		if !adapter.vision || admission.VisionEnabled {
			snapshot.ReadAdapters = append(snapshot.ReadAdapters, namespace)
			snapshot.ReadAdapterVersions[namespace] = adapter.version
		}
	}
	if admission.HasSkills || len(snapshot.ReadAdapters) > 0 {
		names = append(names, "read")
	}
	for _, name := range names {
		tool := agentToolRegistry[name]
		if tool.execute == nil {
			return AgentCapabilitySnapshot{}, fmt.Errorf("missing executor for Agent tool %q", name)
		}
		if name != "canvas_inspect_image" || admission.VisionEnabled {
			snapshot.Tools[name] = tool.version
		}
	}
	return snapshot, nil
}

func compileRegisteredAgentTools(req CloudAgentRequest, runtimeMode string, capabilities *AgentCapabilitySnapshot) []map[string]any {
	tools := compileCloudAgentToolsForRuntime(req, runtimeMode)
	if capabilities == nil {
		return tools
	}
	return slices.DeleteFunc(tools, func(tool map[string]any) bool {
		name := stringField(tool["function"].(map[string]any), "name")
		return capabilities.Tools[name] == 0
	})
}

func cloudAgentRunAllowsTool(state *cloudAgentRuntime, name string) bool {
	if state.Capabilities != nil && state.Capabilities.Tools[name] == 0 {
		return false
	}
	req := state.Request
	if state.Subagent != nil {
		req.subagent = state.Subagent
	}
	return cloudAgentToolAllowed(req, name)
}

func (s *Service) authorizeAgentRunTool(run *model.CloudAgentExecution, state *cloudAgentRuntime, name string) error {
	if !cloudAgentRunAllowsTool(state, name) {
		return kernel.Forbidden("工具未获本轮权限授权")
	}
	if state.Capabilities == nil {
		return nil
	}
	if state.Capabilities.Tools[name] != agentToolRegistry[name].version {
		return kernel.Forbidden("本轮工具执行器版本不可用")
	}
	return s.authorizeRegisteredAgentRun(run, state)
}

func (s *Service) checkRegisteredAgentToolBeforeDispatch(run *model.CloudAgentExecution, state *cloudAgentRuntime, name string) error {
	if state.Capabilities == nil || !cloudAgentRunAllowsTool(state, name) {
		return nil
	}
	return s.authorizeAgentRunTool(run, state, name)
}

func (s *Service) dispatchRegisteredAgentTool(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, allowed bool) error {
	if state.Capabilities != nil && allowed {
		return agentToolRegistry[call.Function.Name].execute(s, run, state, call, true)
	}
	return s.routeCloudAgentToolCall(run, state, call, allowed)
}

func agentRunPermissionPermitted(state *cloudAgentRuntime, mode string) bool {
	if mode == "read_only" {
		return true
	}
	if state.Capabilities != nil {
		return !state.Capabilities.ReadOnly
	}
	return agentBusinessScope(state.Request).Type != "drama"
}

func (s *Service) authorizeRegisteredAgentRun(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.Capabilities == nil {
		return nil
	}
	scope := agentBusinessScope(state.Request)
	if err := agentRequireIsolatedContext(state.Context, run.UserID, scope); err != nil {
		return err
	}
	if state.Capabilities.BusinessType != scope.Type {
		return kernel.Forbidden("本轮 Agent 配置不属于当前业务")
	}
	definition, err := s.resolveFrozenAgentDefinition(state.Capabilities)
	if err != nil {
		return err
	}
	_, err = definition.authorize(s, run.UserID, state.Request)
	return err
}
