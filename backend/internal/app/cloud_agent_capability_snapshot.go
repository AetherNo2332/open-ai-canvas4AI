package app

import (
	"context"
	"encoding/json"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

type PiAgentCapabilityDirectory struct {
	Version           int    `json:"version"`
	Hash              string `json:"hash"`
	DefinitionID      string `json:"definitionId,omitempty"`
	DefinitionVersion int    `json:"definitionVersion,omitempty"`
}

func agentCapabilityDirectoryUnavailable(runID, detail string) error {
	err := kernel.NewAppError(409, "Agent 冻结工具目录不可恢复："+detail+"（诊断 "+runID+"），请发起新一轮")
	err.Reason = "agent_capability_directory_unavailable"
	err.Details = map[string]any{"diagnosticId": runID}
	return err
}

func (s *Service) verifyAgentCapabilityDirectory(state *cloudAgentRuntime) error {
	frozen := state.Capabilities
	// 冻结目录缺失是旧执行合同（业务 read 扩展之前或之后）的形态：没有目录就无法
	// 证明工具身份与版本，可执行入口一律拒绝继续。
	if frozen == nil {
		return agentCapabilityDirectoryUnavailable(state.RuntimeRunID, "缺少冻结工具目录")
	}
	if frozen.DirectoryVersion != agentCapabilityDirectoryVersion || frozen.DirectoryHash == "" {
		return agentCapabilityDirectoryUnavailable(state.RuntimeRunID, "版本不兼容")
	}
	hash, err := agentCapabilityDirectoryHash(*frozen, state.Canonical.Tools)
	if err != nil || hash != frozen.DirectoryHash {
		return agentCapabilityDirectoryUnavailable(state.RuntimeRunID, "目录内容已变化")
	}
	registry := s.agentDefinitionRegistry()
	if _, err := s.resolveFrozenAgentDefinition(frozen); err != nil {
		return agentCapabilityDirectoryUnavailable(state.RuntimeRunID, "配置版本不可用")
	}
	for name, version := range frozen.Tools {
		tool := registry.tools[name]
		if tool.execute == nil || tool.version != version {
			return agentCapabilityDirectoryUnavailable(state.RuntimeRunID, "工具执行器版本不可用："+name)
		}
	}
	return nil
}

func piAgentCapabilityDirectory(state *cloudAgentRuntime) PiAgentCapabilityDirectory {
	if frozen := state.Capabilities; frozen != nil {
		return PiAgentCapabilityDirectory{
			Version: frozen.DirectoryVersion, Hash: frozen.DirectoryHash,
			DefinitionID: frozen.DefinitionID, DefinitionVersion: frozen.DefinitionVersion,
		}
	}
	raw, _ := json.Marshal(state.Canonical.Tools)
	return PiAgentCapabilityDirectory{Version: 0, Hash: cloudAgentTextDigest(string(raw))}
}

func (s *Service) stopIncompatibleAgentDirectory(run *model.CloudAgentExecution, state *cloudAgentRuntime, cause error) error {
	if cloudAgentAwaitingFirstStep(state) {
		if err := s.failCloudAgentFirstStepAdmission(run, state, cause); err != nil {
			return err
		}
	} else if err := s.failCloudAgent(run, state, cause.Error()); err != nil {
		return err
	}
	terminal, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		return err
	}
	if err := s.finishCloudAgentCleanup(context.Background(), terminal); err != nil {
		return err
	}
	return cause
}
