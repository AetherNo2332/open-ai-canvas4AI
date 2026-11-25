package model

import "time"

const (
	AgentGenerationBatchCollecting = "collecting"
	AgentGenerationBatchArmed      = "armed"
	AgentGenerationBatchReady      = "ready"
	AgentGenerationBatchQueued     = "queued"
	AgentGenerationBatchConsumed   = "consumed"
	AgentGenerationBatchDetached   = "detached"

	AgentGenerationBatchTaskPending  = "pending"
	AgentGenerationBatchTaskTerminal = "terminal"

	AgentGenerationContinuationAllTerminal = "all_generations_terminal"
)

// AgentGenerationBatch is the durable barrier for generation tasks submitted by
// one source run. Its expected count always comes from registered task rows.
type AgentGenerationBatch struct {
	ID                string    `json:"id" gorm:"primaryKey;size:80"`
	UserID            string    `json:"-" gorm:"size:36;index"`
	CanvasID          string    `json:"canvasId" gorm:"size:80"`
	ConversationID    string    `json:"conversationId" gorm:"size:80;index"`
	SourceRunID       string    `json:"sourceRunId" gorm:"size:80;uniqueIndex:idx_agent_generation_batch_source"`
	Status            string    `json:"status" gorm:"size:24;index"`
	TriggerWhen       string    `json:"triggerWhen" gorm:"size:40"`
	Instruction       string    `json:"instruction" gorm:"type:text"`
	RequestJSON       string    `json:"-" gorm:"type:text"`
	ExpectedTasks     int       `json:"expectedTasks" gorm:"not null;default:0"`
	TerminalTasks     int       `json:"terminalTasks" gorm:"not null;default:0"`
	ResultsJSON       string    `json:"-" gorm:"type:text"`
	QueuedMessageID   string    `json:"queuedMessageId,omitempty" gorm:"size:80"`
	ContinuationRunID string    `json:"continuationRunId,omitempty" gorm:"size:80"`
	Revision          int64     `json:"revision" gorm:"not null;default:1"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// AgentGenerationBatchTask records membership and the authoritative terminal
// outcome of one actually submitted generation task.
type AgentGenerationBatchTask struct {
	BatchID     string    `json:"batchId" gorm:"primaryKey;size:80"`
	TaskID      string    `json:"taskId" gorm:"primaryKey;size:80"`
	UserID      string    `json:"-" gorm:"size:36;index"`
	NodeID      string    `json:"nodeId" gorm:"size:80"`
	Mode        string    `json:"mode,omitempty" gorm:"size:24"`
	Title       string    `json:"title,omitempty" gorm:"size:240"`
	Status      string    `json:"status" gorm:"size:24;index"`
	OutcomeJSON string    `json:"-" gorm:"type:text"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
