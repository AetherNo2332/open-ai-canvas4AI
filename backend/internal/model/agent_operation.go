package model

import "time"

// AgentOperation is the durable receipt of one tool execution attempt.
// The operation key is the Session turn, step and provider call ID, so a
// provider that reuses call IDs across turns or steps cannot alias two
// executions. A succeeded row is the only replayable outcome: re-running the
// same operation returns its stored result instead of touching the business
// again. pending marks an attempt whose outcome is not known yet; refused and
// uncertain rows are audit records that a later attempt re-evaluates.
type AgentOperation struct {
	ID              string `gorm:"primaryKey;size:36"`
	OwnerID         string `gorm:"not null;size:36;uniqueIndex:idx_agent_tool_operation,priority:1"`
	OperationKey    string `gorm:"not null;size:400;uniqueIndex:idx_agent_tool_operation,priority:2"`
	SessionID       string `gorm:"not null;size:80;index"`
	WorkItemID      string `gorm:"not null;size:36;default:''"`
	AssignmentID    string `gorm:"size:36;default:''"`
	AssignmentEpoch int64  `gorm:"not null;default:0"`
	TurnMessageID   string `gorm:"not null;size:160;default:''"`
	Step            int    `gorm:"not null;default:0"`
	CallID          string `gorm:"not null;size:160"`
	Name            string `gorm:"not null;size:120"`
	ArgumentsHash   string `gorm:"not null;size:64"`
	Status          string `gorm:"not null;size:16;index"`
	// ApprovalID 记录等待审批的挂起操作；批准后轮次被重置重放，同一操作键提交任务。
	ApprovalID      string `gorm:"size:36;default:'';index"`
	ResultJSON      string `gorm:"type:text"`
	OperationRef    string `gorm:"size:160;default:''"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AgentTurnContract freezes the agent contract of one turn at its first model
// step: the definition version and the tool schemas the model was offered. A
// resumed turn of the same key keeps them across instances and redeploys; only
// a new turn freezes the contract that is current then.
type AgentTurnContract struct {
	ID                string `gorm:"primaryKey;size:36"`
	OwnerID           string `gorm:"not null;size:36;uniqueIndex:idx_agent_turn_contract,priority:1"`
	TurnKey           string `gorm:"not null;size:240;uniqueIndex:idx_agent_turn_contract,priority:2"`
	SessionID         string `gorm:"not null;size:80;index"`
	DefinitionID      string `gorm:"not null;size:120"`
	DefinitionVersion string `gorm:"not null;size:40"`
	ToolsJSON         string `gorm:"type:text"`
	CreatedAt         time.Time
}

// AgentEcommerceBatch is the durable batch record of an ecommerce space:
// product material, the chosen preset and the tasks submitted under the batch,
// so the page and the Agent recover the same batch across restarts.
type AgentEcommerceBatch struct {
	ID          string `gorm:"primaryKey;size:36"`
	OwnerID     string `gorm:"not null;size:36;uniqueIndex:idx_agent_ecommerce_batch,priority:1"`
	BatchID     string `gorm:"not null;size:120;uniqueIndex:idx_agent_ecommerce_batch,priority:2"`
	ProductJSON string `gorm:"type:text"`
	PresetJSON  string `gorm:"type:text"`
	TaskRefsJSON string `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
