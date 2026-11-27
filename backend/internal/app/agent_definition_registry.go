package app

import (
	"fmt"
	"maps"
	"slices"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

type AgentDefinition struct {
	ID                 string
	Version            int
	BusinessType       string
	IsDefault          bool
	BusinessTools      []string
	SkillIDs           []string
	DefaultSkillScopes []string
	AllowUserSkills    bool
	MemoryPolicy       string
	InteractionPolicy  string
	ReadOnly           bool
	authorize          agentObjectAuthorizer
	tools              map[string]agentToolRegistration
}

type agentObjectAuthorizer func(*Service, string, CloudAgentRequest) (*model.CanvasProject, error)

type agentDefinitionVersion struct {
	ID      string
	Version int
}

type agentDefinitionRegistry struct {
	defaults map[string]string
	latest   map[string]AgentDefinition
	versions map[agentDefinitionVersion]AgentDefinition
	tools    map[string]agentToolRegistration
}

var defaultAgentDefinitionRegistry *agentDefinitionRegistry

func init() {
	agentToolRegistry = builtinAgentToolRegistry()
	drama := AgentDefinition{
		ID: "drama.analysis", Version: 1, BusinessType: "drama", IsDefault: true, ReadOnly: true,
		BusinessTools: []string{"task_get", "drama_read_episode"}, MemoryPolicy: "off",
		DefaultSkillScopes: []string{"global", "drama"},
	}
	// v2 是 Skill-only read 合同的短剧定义：分集改用 drama_read_episode，交互式升到 v3。
	scopedDrama := drama
	scopedDrama.Version = 2
	interactiveDrama := scopedDrama
	interactiveDrama.Version = 3
	interactiveDrama.InteractionPolicy = "ask_user"
	platform := AgentDefinition{
		ID: "platform.default", Version: 1, BusinessType: AgentPlatformBusinessType, IsDefault: true, ReadOnly: true,
		BusinessTools: []string{}, AllowUserSkills: false,
		InteractionPolicy: "ask_user", MemoryPolicy: "off",
	}
	registry, err := newAgentDefinitionRegistry([]AgentDefinition{
		{
			ID: "canvas.default", Version: 2, BusinessType: "canvas", IsDefault: true,
			BusinessTools: []string{
				"director", "canvas_list_node_types", "canvas_list_assets", "shortplay_list_assets", "canvas_read_content", "canvas_search_nodes",
				"canvas_get_state", "canvas_read_batch_table", "task_get", "canvas_inspect_image", "canvas_undo",
				"canvas",
				"canvas_redo", "canvas_bind_asset", "shortplay_asset", "model_list", "storyboard", "canvas_create_character",
				"canvas_edit_batch_table", "canvas_apply_ops", "canvas_arrange_nodes", "generate_media", "generate_media_batch", "media_batch_control", "canvas_build_board", "canvas_preflight",
				"web_search", "spawn_subagent", "wait_subagents", "message_subagent", "subagent_status", "send_parent_message", "finish_subagent",
			},
			AllowUserSkills: true, InteractionPolicy: "ask_user", MemoryPolicy: "off",
			DefaultSkillScopes: []string{"global", "canvas"},
		},
		drama, scopedDrama, interactiveDrama, platform,
	})
	if err != nil {
		panic(err)
	}
	defaultAgentDefinitionRegistry = registry
}

func newAgentDefinitionRegistry(definitions []AgentDefinition) (*agentDefinitionRegistry, error) {
	return newAgentRegistry(definitions, nil)
}

func newAgentRegistry(definitions []AgentDefinition, additionalTools []agentToolRegistration) (*agentDefinitionRegistry, error) {
	registry := &agentDefinitionRegistry{
		defaults: map[string]string{}, latest: map[string]AgentDefinition{}, versions: map[agentDefinitionVersion]AgentDefinition{},
		tools: maps.Clone(agentToolRegistry),
	}
	for _, tool := range additionalTools {
		tool.order = len(registry.tools)
		if err := registerAgentTool(registry.tools, tool); err != nil {
			return nil, err
		}
	}
	for _, definition := range definitions {
		if err := registry.add(definition); err != nil {
			return nil, err
		}
	}
	for _, definition := range registry.latest {
		if registry.defaults[definition.BusinessType] == "" {
			return nil, fmt.Errorf("missing default Agent definition for %q", definition.BusinessType)
		}
	}
	return registry, nil
}

func (registry *agentDefinitionRegistry) add(definition AgentDefinition) error {
	definition.tools = registry.tools
	if err := validateAgentDefinition(definition); err != nil {
		return err
	}
	if definition.authorize == nil {
		definition.authorize = agentBuiltinObjectAuthorizer(definition.BusinessType)
	}
	key := agentDefinitionVersion{ID: definition.ID, Version: definition.Version}
	previous := registry.latest[definition.ID]
	if registry.versions[key].ID != "" || (previous.ID != "" && previous.BusinessType != definition.BusinessType) {
		return fmt.Errorf("duplicate or conflicting Agent definition %q v%d", definition.ID, definition.Version)
	}
	if definition.IsDefault {
		if existing := registry.defaults[definition.BusinessType]; existing != "" && existing != definition.ID {
			return fmt.Errorf("duplicate default Agent definitions for %q", definition.BusinessType)
		}
		registry.defaults[definition.BusinessType] = definition.ID
	}
	definition = cloneAgentDefinition(definition)
	registry.versions[key] = definition
	if previous.Version < definition.Version {
		registry.latest[definition.ID] = definition
	}
	return nil
}

func cloneAgentDefinition(definition AgentDefinition) AgentDefinition {
	definition.BusinessTools = slices.Clone(definition.BusinessTools)
	definition.SkillIDs = slices.Clone(definition.SkillIDs)
	definition.DefaultSkillScopes = slices.Clone(definition.DefaultSkillScopes)
	return definition
}

func (s *Service) agentDefinitionRegistry() *agentDefinitionRegistry {
	if s.agentDefinitions != nil {
		return s.agentDefinitions
	}
	return defaultAgentDefinitionRegistry
}

func (s *Service) ResolveAgentDefinition(businessType, definitionID string) (AgentDefinition, error) {
	registry := s.agentDefinitionRegistry()
	if definitionID == "" {
		definitionID = registry.defaults[businessType]
	}
	definition := registry.latest[definitionID]
	if definition.ID == "" || definition.BusinessType != businessType {
		err := kernel.BadAuthRequest("该业务未注册所选 Agent 配置")
		err.Reason = "agent_definition_unavailable"
		return AgentDefinition{}, err
	}
	return cloneAgentDefinition(definition), nil
}

func (s *Service) resolveFrozenAgentDefinition(snapshot *AgentCapabilitySnapshot) (AgentDefinition, error) {
	key := agentDefinitionVersion{ID: snapshot.DefinitionID, Version: snapshot.DefinitionVersion}
	definition := s.agentDefinitionRegistry().versions[key]
	if definition.ID == "" || definition.BusinessType != snapshot.BusinessType {
		err := kernel.NewAppError(409, "本轮冻结的 Agent 配置版本不可用")
		err.Reason = "agent_definition_version_unavailable"
		return AgentDefinition{}, err
	}
	return cloneAgentDefinition(definition), nil
}
