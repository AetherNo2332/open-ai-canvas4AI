package app

import as "yingce/backend/internal/agentservice"

// cloudAgentExecutionState is the business-neutral identity frozen on a Pi run
// created from an AgentSession. A canvas run leaves Execution nil and keeps its
// canvas contract; a generic run reads everything it needs from here instead
// of the canvas canonical/policy snapshot.
type cloudAgentExecutionState struct {
	ProtocolVersion   string               `json:"protocolVersion"`
	SessionID         string               `json:"sessionId"`
	DefinitionID      string               `json:"definitionId"`
	DefinitionVersion string               `json:"definitionVersion"`
	Role              string               `json:"role"`
	Scope             as.Scope             `json:"scope"`
	TriggerMessageID  string               `json:"triggerMessageId"`
	Binding           *as.ExecutionBinding `json:"binding,omitempty"`
	Tools             []as.ToolSpec        `json:"tools"`
}
