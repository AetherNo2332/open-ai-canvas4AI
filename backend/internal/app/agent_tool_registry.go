package app

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

type agentToolExecutor func(*Service, *model.CloudAgentExecution, *cloudAgentRuntime, cloudAgentCall, bool) error

type agentToolRegistration struct {
	name               string
	order              int
	version            int
	businesses         []string
	platform           bool
	writeOnly          bool
	allowed            func(CloudAgentRequest) bool
	schema             map[string]any
	readOnlySchema     map[string]any
	noStoryboardSchema map[string]any
	execute            agentToolExecutor
}

var agentToolRegistry map[string]agentToolRegistration

// agentRegisteredToolName 是新的工具命名合同：小写 snake_case、单下划线分段、
// 首字符必须是字母、总长不超过 64。业务工具的显式业务前缀让读取与写入在名字上
// 就能分辨，不必依赖运行期白名单。
var agentRegisteredToolName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

const agentRegisteredToolNameMaxRunes = 64

func registerAgentTool(tools map[string]agentToolRegistration, tool agentToolRegistration) error {
	if !agentRegisteredToolName.MatchString(tool.name) || len(tool.name) > agentRegisteredToolNameMaxRunes ||
		strings.HasPrefix(tool.name, "agent_tools_") ||
		!agentToolRegistrationSchemaValid(tool.name, tool.schema) ||
		tool.version <= 0 || tool.execute == nil || tools[tool.name].version != 0 ||
		(!tool.platform && len(tool.businesses) == 0) {
		return agentCapabilityConfigurationError("工具注册无效或重复", tool.name)
	}
	for _, variant := range []map[string]any{tool.readOnlySchema, tool.noStoryboardSchema} {
		if variant != nil && !agentToolRegistrationSchemaValid(tool.name, variant) {
			return agentCapabilityConfigurationError("工具 Schema 变体无效", tool.name)
		}
	}
	tools[tool.name] = tool
	return nil
}

func agentToolRegistrationSchemaValid(name string, schema map[string]any) bool {
	function, _ := schema["function"].(map[string]any)
	parameters, _ := function["parameters"].(map[string]any)
	_, description := function["description"].(string)
	_, identity := cloudAgentToolSchemaIdentity(schema)
	return schema["type"] == "function" && function["name"] == name && description &&
		parameters["type"] == "object" && identity != "invalid"
}

func (definition AgentDefinition) toolRegistrations() map[string]agentToolRegistration {
	if definition.tools != nil {
		return definition.tools
	}
	return agentToolRegistry
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
		tool := definition.toolRegistrations()[name]
		if seen[name] || !tool.appliesToDefinition(definition) {
			return agentCapabilityConfigurationError("业务工具未知、重复或不适用于该业务", name)
		}
		seen[name] = true
	}
	if definition.MemoryPolicy != "" && definition.MemoryPolicy != "off" && definition.MemoryPolicy != "scope" {
		return fmt.Errorf("invalid Agent memory policy in %q", definition.ID)
	}
	if definition.InteractionPolicy != "" && definition.InteractionPolicy != "ask_user" {
		return fmt.Errorf("invalid Agent interaction policy in %q", definition.ID)
	}
	return validateAgentDefaultSkillScopes(definition)
}

func validateAgentDefaultSkillScopes(definition AgentDefinition) error {
	for index, scope := range definition.DefaultSkillScopes {
		if (scope != "global" && scope != definition.BusinessType) || slices.Contains(definition.DefaultSkillScopes[:index], scope) {
			return fmt.Errorf("invalid default Skill scope %q in %q", scope, definition.ID)
		}
	}
	return nil
}

func (tool agentToolRegistration) appliesToDefinition(definition AgentDefinition) bool {
	return tool.execute != nil && !tool.platform && slices.Contains(tool.businesses, definition.BusinessType) &&
		(!definition.ReadOnly || !tool.writeOnly)
}

type AgentCapabilityAdmission struct {
	VisionEnabled bool
	HasSkills     bool
}

type AgentCapabilitySnapshot struct {
	DefinitionID      string         `json:"definitionId"`
	DefinitionVersion int            `json:"definitionVersion"`
	BusinessType      string         `json:"businessType"`
	Tools             map[string]int `json:"tools"`
	SkillRead         bool           `json:"skillRead,omitempty"`
	ReadOnly          bool           `json:"readOnly,omitempty"`
	DirectoryVersion  int            `json:"directoryVersion,omitempty"`
	DirectoryHash     string         `json:"directoryHash,omitempty"`
	WriteTools        []string       `json:"writeTools,omitempty"`
}

// CompileAgentCapabilities freezes the tool versions a run may execute. The
// generic read tool exists only when this run was admitted with enabled Skills;
// business content belongs to each business's own named tools.
func CompileAgentCapabilities(definition AgentDefinition, admission AgentCapabilityAdmission) (AgentCapabilitySnapshot, error) {
	snapshot := AgentCapabilitySnapshot{
		DefinitionID: definition.ID, DefinitionVersion: definition.Version, BusinessType: definition.BusinessType,
		Tools: make(map[string]int), ReadOnly: definition.ReadOnly, SkillRead: admission.HasSkills,
	}
	names := append(slices.Clone(definition.BusinessTools), "plan_update", "finish_run")
	if definition.InteractionPolicy == "ask_user" {
		names = append(names, "ask_user")
	}
	if definition.MemoryPolicy == "scope" && !definition.ReadOnly {
		names = append(names, "memory_update")
	}
	if admission.HasSkills {
		names = append(names, "read")
	}
	for _, name := range names {
		tool := definition.toolRegistrations()[name]
		if tool.execute == nil {
			return AgentCapabilitySnapshot{}, agentCapabilityConfigurationError("工具执行器不可用", name)
		}
		snapshot.Tools[name] = tool.version
	}
	return snapshot, nil
}

func compileAgentRunCapabilityDirectory(definition AgentDefinition, req CloudAgentRequest, hasSkills bool) (AgentCapabilitySnapshot, []map[string]any, error) {
	if err := validateAgentDefinition(definition); err != nil {
		return AgentCapabilitySnapshot{}, nil, err
	}
	capabilities, err := CompileAgentCapabilities(definition, AgentCapabilityAdmission{VisionEnabled: req.VisionEnabled, HasSkills: hasSkills})
	if err != nil {
		return AgentCapabilitySnapshot{}, nil, err
	}
	var selected []agentToolRegistration
	for name := range capabilities.Tools {
		tool := definition.toolRegistrations()[name]
		if tool.eligible(req) {
			selected = append(selected, tool)
		} else {
			delete(capabilities.Tools, name)
		}
	}
	sort.Slice(selected, func(left, right int) bool { return selected[left].order < selected[right].order })
	tools := make([]map[string]any, 0, len(selected))
	for _, tool := range selected {
		if tool.writeOnly || (req.PermissionMode != "read_only" && tool.readOnlySchema != nil && cloudAgentWrite(tool.name)) {
			capabilities.WriteTools = append(capabilities.WriteTools, tool.name)
		}
		tools = append(tools, tool.modelSchema(req))
	}
	return capabilities, tools, nil
}

func (tool agentToolRegistration) eligible(req CloudAgentRequest) bool {
	if req.PermissionMode == "read_only" && tool.writeOnly {
		return false
	}
	if req.subagent != nil && (tool.name == "ask_user" || tool.name == "finish_run" ||
		(!req.subagent.WriteGranted && cloudAgentWrite(tool.name))) {
		return false
	}
	return tool.allowed == nil || tool.allowed(req)
}

func (tool agentToolRegistration) modelSchema(req CloudAgentRequest) map[string]any {
	switch {
	case req.PermissionMode == "read_only" && tool.readOnlySchema != nil:
		return tool.readOnlySchema
	case req.StoryboardDisabled && tool.noStoryboardSchema != nil:
		return tool.noStoryboardSchema
	default:
		return tool.schema
	}
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
	req := state.Request
	if state.Subagent != nil {
		req.subagent = state.Subagent
	}
	if state.Capabilities != nil && state.Capabilities.DirectoryVersion != 0 {
		if state.Capabilities.Tools[name] == 0 {
			return false
		}
		if req.PermissionMode == "read_only" && slices.Contains(state.Capabilities.WriteTools, name) {
			return false
		}
		// 冻结目录已经按运行条件裁剪过一次；这里按当前角色再校验一次，避免目录与角色
		// 状态不一致时把写入或平台控制工具放给子代理。
		if req.subagent != nil && (name == "ask_user" || name == "finish_run" ||
			(!req.subagent.WriteGranted && cloudAgentWrite(name))) {
			return false
		}
		return true
	}
	if state.Capabilities != nil && state.Capabilities.Tools[name] == 0 {
		return false
	}
	return cloudAgentToolAllowed(req, name)
}

func (s *Service) authorizeAgentRunTool(run *model.CloudAgentExecution, state *cloudAgentRuntime, name string) error {
	if !cloudAgentRunAllowsTool(state, name) {
		return kernel.Forbidden("工具未获本轮权限授权")
	}
	registration := s.agentDefinitionRegistry().tools[name]
	if state.Capabilities == nil || registration.execute == nil || state.Capabilities.Tools[name] != registration.version {
		return kernel.Forbidden("本轮工具执行器版本不可用")
	}
	return s.authorizeRegisteredAgentRun(run, state)
}

func (s *Service) checkRegisteredAgentToolBeforeDispatch(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	if state.Capabilities == nil {
		return nil
	}
	name := call.Function.Name
	parameters, disclosed := cloudAgentAdvertisedTool(state, name)
	if !disclosed || !cloudAgentRunAllowsTool(state, name) {
		return kernel.Forbidden("工具未获本轮权限授权")
	}
	if err := s.authorizeAgentRunTool(run, state, name); err != nil {
		return err
	}
	return validateCloudAgentToolArguments(parameters, call.Function.Arguments)
}

func (s *Service) dispatchRegisteredAgentTool(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, allowed bool) error {
	if state.Capabilities != nil && allowed {
		registration := s.agentDefinitionRegistry().tools[call.Function.Name]
		if registration.execute == nil || registration.version != state.Capabilities.Tools[call.Function.Name] {
			return s.recordCloudAgentToolReceipt(run, state, call, nil, agentCapabilityConfigurationError("冻结工具执行器不可用", call.Function.Name))
		}
		return registration.execute(s, run, state, call, true)
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
	if err := s.verifyAgentCapabilityDirectory(state); err != nil {
		return err
	}
	scope := agentBusinessScope(state.Request)
	if err := agentRequireIsolatedContext(state.Context, run.UserID, scope); err != nil {
		return err
	}
	if state.Capabilities.BusinessType != scope.Type {
		return kernel.Forbidden("本轮 Agent 配置不属于当前业务")
	}
	definition := s.agentDefinitionRegistry().versions[agentDefinitionVersion{ID: state.Capabilities.DefinitionID, Version: state.Capabilities.DefinitionVersion}]
	_, err := definition.authorize(s, run.UserID, state.Request)
	return err
}
