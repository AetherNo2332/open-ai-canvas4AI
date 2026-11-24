package app

import (
	"slices"

	"yingce/backend/internal/kernel"
)

type AgentModelRequirements struct {
	Text         bool `json:"text"`
	ToolCalling  bool `json:"toolCalling"`
	Vision       bool `json:"vision"`
	StreamOutput bool `json:"streamOutput"`
}

type AgentCapabilitiesView struct {
	Version                 int                        `json:"version"`
	Scope                   AgentBusinessScope         `json:"scope"`
	AgentDefinitionID       string                     `json:"agentDefinitionId"`
	AgentDefinitionVersion  int                        `json:"agentDefinitionVersion"`
	PermissionModes         []string                   `json:"permissionModes"`
	ContextScopes           []string                   `json:"contextScopes"`
	Skills                  bool                       `json:"skills"`
	AllowUserSkills         bool                       `json:"allowUserSkills"`
	WriteTools              bool                       `json:"writeTools"`
	BusinessTools           []string                   `json:"businessTools"`
	Tools                   []string                   `json:"tools"`
	DefaultSkills           AgentSkillDefaultsSummary  `json:"defaultSkills"`
	ModelRequirements       AgentModelRequirements     `json:"modelRequirements"`
	InteractionPolicy       string                     `json:"interactionPolicy,omitempty"`
	MemoryPolicy            string                     `json:"memoryPolicy"`
	Availability            AgentAvailability          `json:"availability"`
	CapabilitySetVersion    string                     `json:"capabilitySetVersion,omitempty"`
	CapabilitySetHash       string                     `json:"capabilitySetHash,omitempty"`
	NodeTypes               []string                   `json:"nodeTypes,omitempty"`
	ContextCompactionPolicy *AgentContextCompactionAPI `json:"contextCompaction,omitempty"`
}

type AgentContextCompactionAPI struct {
	Enabled                  bool   `json:"enabled"`
	Strategy                 string `json:"strategy"`
	ThresholdBytes           int    `json:"thresholdBytes"`
	ThresholdHistoryMessages int    `json:"thresholdHistoryMessages"`
	RetainedRecentPairs      int    `json:"retainedRecentPairs"`
}

// AgentCapabilities resolves UI capabilities from the same registered
// definition and object authorizer used for admission. It never broadens the
// run's frozen tool directory.
func (s *Service) AgentCapabilities(userID string, scope AgentBusinessScope, definitionID string) (*AgentCapabilitiesView, error) {
	if userID == "" {
		return nil, kernel.Unauthorized("请先登录")
	}
	canonical, err := NormalizeAgentBusinessScope(userID, scope)
	if err != nil {
		return nil, err
	}
	definition, err := s.ResolveAgentDefinition(canonical.Type, definitionID)
	if err != nil {
		return nil, err
	}
	authRequest := CloudAgentRequest{Scope: &canonical, AgentDefinitionID: definition.ID, PermissionMode: "read_only"}
	if canonical.Type == "canvas" {
		authRequest.CanvasID = canonical.ID
		authRequest.ContextScope = []string{"canvas"}
	}
	if _, err := s.authorizeAgentDefinition(definition, userID, authRequest); err != nil {
		return nil, err
	}
	compileRequest := authRequest
	if !definition.ReadOnly {
		compileRequest.PermissionMode = "auto"
	}
	capabilities, tools, err := compileAgentRunCapabilityDirectory(definition, compileRequest, false)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if name, _ := function["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	defaultSkills := AgentSkillDefaultsSummary{Skills: []AgentSkillDefaultsSummaryItem{}}
	if len(definition.DefaultSkillScopes)+len(definition.SkillIDs) > 0 {
		summary, err := s.AgentSkillDefaultsForBusiness(userID, canonical.Type, definition.ID)
		if err != nil {
			return nil, err
		}
		defaultSkills = *summary
	}
	availability, err := s.AgentAvailability()
	if err != nil {
		return nil, err
	}
	permissionModes := []string{"read_only", "request_approval", "auto"}
	if definition.ReadOnly {
		permissionModes = []string{"read_only"}
	}
	view := &AgentCapabilitiesView{
		Version: 3, Scope: PublicAgentBusinessScope(canonical),
		AgentDefinitionID: definition.ID, AgentDefinitionVersion: definition.Version,
		PermissionModes: permissionModes, ContextScopes: []string{canonical.Type},
		Skills:          definition.AllowUserSkills || len(definition.DefaultSkillScopes) > 0 || len(definition.SkillIDs) > 0,
		AllowUserSkills: definition.AllowUserSkills,
		WriteTools:      len(capabilities.WriteTools) > 0, BusinessTools: slices.Clone(definition.BusinessTools),
		Tools: names, DefaultSkills: defaultSkills,
		ModelRequirements: AgentModelRequirements{Text: true, ToolCalling: true, StreamOutput: true},
		InteractionPolicy: definition.InteractionPolicy, MemoryPolicy: definition.MemoryPolicy,
		Availability: availability,
	}
	if canonical.Type == "canvas" {
		set := CloudAgentCapabilitySetInfo()
		view.CapabilitySetVersion, view.CapabilitySetHash, view.NodeTypes = set.Version, set.Hash, set.Nodes
		view.ModelRequirements.Vision = true
	}
	return view, nil
}
