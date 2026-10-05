package model

import "time"

// AgentSubagentPolicy is the durable per-canvas consent switch. Enabled stays
// effective for later turns until the user explicitly disables it.
type AgentSubagentPolicy struct {
	ID        string    `json:"id" gorm:"primaryKey;size:80"`
	UserID    string    `json:"userId" gorm:"index:idx_agent_subagent_policy_owner_canvas,priority:1;size:36;not null"`
	CanvasID  string    `json:"canvasId" gorm:"index:idx_agent_subagent_policy_owner_canvas,priority:2;size:80;not null"`
	Enabled   bool      `json:"enabled"`
	Revision  int64     `json:"revision" gorm:"not null;default:0"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AgentSubagentLink is the explicit business hierarchy between parent and child runs.
type AgentSubagentLink struct {
	ID                  string    `json:"id" gorm:"primaryKey;size:80"`
	UserID              string    `json:"userId" gorm:"index;size:36;not null"`
	CanvasID            string    `json:"canvasId" gorm:"index;size:80;not null"`
	ParentRunID         string    `json:"parentRunId" gorm:"index;size:80;not null"`
	ChildRunID          string    `json:"childRunId" gorm:"uniqueIndex;size:80;not null"`
	TaskID              string    `json:"taskId" gorm:"size:80"`
	IdempotencyKey      string    `json:"-" gorm:"uniqueIndex;size:160;not null"`
	DisplayName         string    `json:"displayName" gorm:"size:120;not null"`
	RoleLabel           string    `json:"roleLabel" gorm:"size:120;not null"`
	Objective           string    `json:"objective" gorm:"type:text;not null"`
	Instructions        string    `json:"instructions" gorm:"type:text"`
	Depth               int       `json:"depth" gorm:"not null;default:1"`
	Status              string    `json:"status" gorm:"index;size:24;not null"`
	BudgetJSON          string    `json:"-" gorm:"type:text"`
	ContextSnapshotJSON string    `json:"-" gorm:"type:text"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type AgentSubagentMessage struct {
	ID             string     `json:"id" gorm:"primaryKey;size:80"`
	LinkID         string     `json:"linkId" gorm:"index;size:80;not null"`
	UserID         string     `json:"userId" gorm:"index;size:36;not null"`
	ParentRunID    string     `json:"parentRunId" gorm:"index;size:80;not null"`
	ChildRunID     string     `json:"childRunId" gorm:"index;size:80;not null"`
	Direction      string     `json:"direction" gorm:"size:16;not null"`
	Kind           string     `json:"kind" gorm:"size:32;not null"`
	Sequence       int64      `json:"sequence" gorm:"not null"`
	PayloadJSON    string     `json:"payloadJson" gorm:"type:text;not null"`
	IdempotencyKey string     `json:"-" gorm:"uniqueIndex;size:160;not null"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}
