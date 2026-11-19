package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"yingce/backend/internal/kernel"
)

const (
	cloudAgentToolSchemaTotalBudget   = 64000
	cloudAgentToolSchemaPerToolBudget = 12000
	agentCapabilityDirectoryVersion   = 1
)

func cloudAgentToolDirectoryCost(tools []map[string]any, runID string) (int, error) {
	raw, err := json.Marshal(tools)
	if err != nil {
		return 0, agentCapabilityConfigurationError("工具目录无法序列化", "")
	}
	for _, tool := range tools {
		encoded, _ := json.Marshal(tool)
		if len(encoded) > cloudAgentToolSchemaPerToolBudget {
			name, _ := cloudAgentToolSchemaIdentity(tool)
			return 0, agentCapabilityBudgetError(runID, "tool_bytes", name, len(encoded), cloudAgentToolSchemaPerToolBudget, 0)
		}
	}
	if len(raw) > cloudAgentToolSchemaTotalBudget {
		return 0, agentCapabilityBudgetError(runID, "directory_bytes", "", len(raw), cloudAgentToolSchemaTotalBudget, 0)
	}
	return cloudAgentEstimatedTokens(raw), nil
}

func agentCapabilityBudgetError(runID, budget, tool string, actual, limit, toolTokens int) error {
	reason, unit := "agent_tool_schema_budget_exceeded", "字节"
	if budget == "context_tokens" {
		reason, unit = "agent_context_budget_exceeded", "Token（估算）"
	}
	target := "Agent 工具目录"
	if tool != "" {
		target = "Agent 工具 " + tool
	}
	if budget == "context_tokens" {
		target = "Agent 初始输入"
	}
	err := kernel.NewAppError(422, fmt.Sprintf("%s需要 %d %s，上限 %d（诊断 %s）", target, actual, unit, limit, runID))
	err.Reason = kernel.ErrorReason(reason)
	err.Details = map[string]any{"diagnosticId": runID, "budget": budget, "actual": actual, "limit": limit, "toolTokens": toolTokens}
	if tool != "" {
		err.Details["tool"] = tool
	}
	return err
}

func (s *Service) checkAgentModelToolSupport(req CloudAgentRequest, runID string) error {
	if !s.cloudAgentModelSupportsTools(req) {
		err := kernel.NewAppError(422, "当前模型或可用回退线路声明不支持调用工具（诊断 "+runID+"）")
		err.Reason = "agent_model_tools_unsupported"
		err.Details = map[string]any{"diagnosticId": runID}
		return err
	}
	return nil
}

func (s *Service) cloudAgentModelSupportsTools(req CloudAgentRequest) bool {
	if text := s.cloudAgentChannelTextCapability(req); text != nil && text.ToolCallingSupported != nil && !*text.ToolCallingSupported {
		return false
	}
	logicalID := strings.TrimSpace(req.LogicalModelID)
	if logicalID == "" {
		return true
	}
	catalog, err := s.routeCatalogSnapshot()
	if err != nil || catalog == nil {
		return true
	}
	for _, route := range catalog.Models[logicalID].Routes {
		channelModel := route.ChannelModel
		config, err := normalizedChannelModelCapability(&channelModel)
		if err == nil && config != nil && config.Text != nil &&
			config.Text.ToolCallingSupported != nil && !*config.Text.ToolCallingSupported {
			return false
		}
	}
	return true
}

func agentCapabilityDirectoryHash(snapshot AgentCapabilitySnapshot, tools []map[string]any) (string, error) {
	snapshot.DirectoryHash = ""
	raw, err := json.Marshal(struct {
		Capabilities AgentCapabilitySnapshot `json:"capabilities"`
		Tools        []map[string]any        `json:"tools"`
	}{snapshot, tools})
	if err != nil {
		return "", err
	}
	return cloudAgentTextDigest(string(raw)), nil
}
