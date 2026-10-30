package service

import "yingce/backend/internal/app"

// 云端 Agent 子系统（fork 维护）的稳定导入面：handler 与 cmd 只经由本包引用 app。

type (
	AgentSchedulerUpdate           = app.AgentSchedulerUpdate
	AgentSkillDefaultItem          = app.AgentSkillDefaultItem
	AgentSubagentPolicyRequest     = app.AgentSubagentPolicyRequest
	AgentSubagentPolicyView        = app.AgentSubagentPolicyView
	AgentSubagentView              = app.AgentSubagentView
	AgentSystemPromptRequest       = app.AgentSystemPromptRequest
	AgentWebSearchSettingRequest   = app.AgentWebSearchSettingRequest
	AgentWorkspaceView             = app.AgentWorkspaceView
	CharacterListPage              = app.CharacterListPage
	CloudAgentPermissionResult     = app.CloudAgentPermissionResult
	PiAgentSnapshot                = app.PiAgentSnapshot
	PiContextCompactionCommit      = app.PiContextCompactionCommit
	PiContextCompactionStart       = app.PiContextCompactionStart
	PiContextCompactionView        = app.PiContextCompactionView
	PiMessageCheckpoint            = app.PiMessageCheckpoint
	PiModelStepRequest             = app.PiModelStepRequest
	PiModelStepView                = app.PiModelStepView
	PiNativeCompactionComplete     = app.PiNativeCompactionComplete
	PiNativeCompactionModelRequest = app.PiNativeCompactionModelRequest
	PiRuntimePhaseRequest          = app.PiRuntimePhaseRequest
	PiSkillFileRequest             = app.PiSkillFileRequest
	PiSkillReadPage                = app.PiSkillReadPage
	PiSkillSnapshot                = app.PiSkillSnapshot
	PiToolBatchRequest             = app.PiToolBatchRequest
	PiToolReceipt                  = app.PiToolReceipt
	PiTurnDecision                 = app.PiTurnDecision
	PiWorkerRecoveryRequest        = app.PiWorkerRecoveryRequest
)

const (
	FeatureAgentSubagents = app.FeatureAgentSubagents
)
