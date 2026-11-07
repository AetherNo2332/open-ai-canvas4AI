package model

import "time"

// AgentWorkItem is durable supervised work. Its lifecycle is independent of
// any run: a supervisor turn ending never ends the work. Children point at
// their parent; a root work owns a supervisor Session and a budget root.
type AgentWorkItem struct {
	ID                  string `gorm:"primaryKey;size:36"`
	OwnerID             string `gorm:"not null;size:36;uniqueIndex:idx_agent_work_key,priority:1;index:idx_agent_work_parent,priority:1"`
	IdempotencyKey      string `gorm:"not null;size:200;uniqueIndex:idx_agent_work_key,priority:2"`
	ParentWorkID        string `gorm:"not null;size:36;default:'';index:idx_agent_work_parent,priority:2"`
	RootWorkID          string `gorm:"not null;size:36;index"`
	Goal                string `gorm:"type:text;not null"`
	Status              string `gorm:"not null;size:24"`
	Version             int64  `gorm:"not null;default:1"`
	SourceSessionID     string `gorm:"not null;size:80;default:''"`
	SourceMessageID     string `gorm:"not null;size:120;default:''"`
	SupervisorSessionID string `gorm:"not null;size:80;default:''"`
	Application         string `gorm:"not null;size:40;default:''"`
	SpaceType           string `gorm:"not null;size:40;default:''"`
	SpaceID             string `gorm:"not null;size:120;default:''"`
	BudgetGrantID       string `gorm:"not null;size:36;default:''"`
	ActiveAssignmentID  string `gorm:"not null;size:36;default:''"`
	// AuthorizationJSON is the work tree's authorization snapshot (scopes,
	// tool allow-list, approval threshold); children carry a copy taken at
	// delegation time. AuthorizationVersion expires approvals when it moves.
	AuthorizationJSON    string `gorm:"type:text"`
	AuthorizationVersion int64  `gorm:"not null;default:1"`
	ResultSummary        string `gorm:"type:text"`
	StoppedReason        string `gorm:"size:500"`
	StoppedAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// AgentAssignment is one execution attempt of a work item by a business
// Session. At most one assignment per work is not ended; the epoch grows on
// every reassignment so reports from an older attempt are recognised.
type AgentAssignment struct {
	ID                string `gorm:"primaryKey;size:36"`
	OwnerID           string `gorm:"not null;size:36;index"`
	WorkItemID        string `gorm:"not null;size:36;uniqueIndex:idx_agent_assignment_epoch,priority:1"`
	Epoch             int64  `gorm:"not null;uniqueIndex:idx_agent_assignment_epoch,priority:2"`
	ExecutorSessionID string `gorm:"not null;size:80"`
	DefinitionID      string `gorm:"not null;size:120"`
	Handoff           string `gorm:"type:text"`
	Status            string `gorm:"not null;size:16"`
	EndReason         string `gorm:"size:40"`
	EndedAt           *time.Time
	CreatedAt         time.Time
}

// AgentWorkReport is an executor report. (assignment, report ID) is unique, so
// a redelivered report is stored once. Stale reports are kept with Applied
// false for audit.
type AgentWorkReport struct {
	ID           string    `gorm:"primaryKey;size:36"`
	OwnerID      string    `gorm:"not null;size:36;index"`
	WorkItemID   string    `gorm:"not null;size:36;index"`
	AssignmentID string    `gorm:"not null;size:36;uniqueIndex:idx_agent_report_key,priority:1"`
	ReportID     string    `gorm:"not null;size:120;uniqueIndex:idx_agent_report_key,priority:2"`
	LeaseEpoch   int64     `gorm:"not null"`
	WorkVersion  int64     `gorm:"not null"`
	Status       string    `gorm:"not null;size:24"`
	Summary      string    `gorm:"type:text"`
	Applied      bool      `gorm:"not null;default:false"`
	CreatedAt    time.Time `gorm:"index"`
}

// AgentWorkInput is an input for a work item from the user or the supervisor;
// (work, idempotency key) is unique.
type AgentWorkInput struct {
	ID              string    `gorm:"primaryKey;size:36"`
	OwnerID         string    `gorm:"not null;size:36;index"`
	WorkItemID      string    `gorm:"not null;size:36;uniqueIndex:idx_agent_input_key,priority:1"`
	IdempotencyKey  string    `gorm:"not null;size:200;uniqueIndex:idx_agent_input_key,priority:2"`
	Actor           string    `gorm:"not null;size:24"`
	SourceMessageID string    `gorm:"not null;size:120;default:''"`
	Text            string    `gorm:"type:text;not null"`
	WorkVersion     int64     `gorm:"not null"`
	CreatedAt       time.Time `gorm:"index"`
}

// AgentOutbox is a durable pending delivery written in the same transaction
// as the state change it announces. Delivery is at least once; receivers
// deduplicate on ID. Seq is the per-owner write order: delivery follows it, so
// items written in one clock tick still arrive in the order they were written.
type AgentOutbox struct {
	ID              string `gorm:"primaryKey;size:36"`
	OwnerID         string `gorm:"not null;size:36;index:idx_agent_outbox_pending,priority:1;uniqueIndex:idx_agent_outbox_seq,priority:1"`
	Seq             int64  `gorm:"not null;default:0;uniqueIndex:idx_agent_outbox_seq,priority:2"`
	WorkItemID      string `gorm:"not null;size:36;index"`
	TargetSessionID string `gorm:"not null;size:80"`
	Kind            string `gorm:"not null;size:40"`
	Payload         string `gorm:"type:text"`
	Status          string `gorm:"not null;size:16;index:idx_agent_outbox_pending,priority:2"`
	CreatedAt       time.Time
	DeliveredAt     *time.Time
}

// AgentApproval is one approval request. The same ID is shown in the
// supervisor and business views; it binds the work authorization version,
// the quote and the target object version, and can be consumed once.
type AgentApproval struct {
	ID                string `gorm:"primaryKey;size:36"`
	OwnerID           string `gorm:"not null;size:36;uniqueIndex:idx_agent_approval_call,priority:1;index:idx_agent_approval_root,priority:1"`
	WorkItemID        string `gorm:"not null;size:36;index"`
	RootWorkID        string `gorm:"not null;size:36;index:idx_agent_approval_root,priority:2"`
	SessionID         string `gorm:"not null;size:80;uniqueIndex:idx_agent_approval_call,priority:2"`
	CallID            string `gorm:"not null;size:160;uniqueIndex:idx_agent_approval_call,priority:3"`
	Summary           string `gorm:"size:500"`
	QuoteMicrocredits int64  `gorm:"not null"`
	TargetVersion     string `gorm:"not null;size:160;default:''"`
	// ArgumentsHash pins the arguments the approval was requested for; a
	// changed payload invalidates the approval even with the same call identity.
	ArgumentsHash        string `gorm:"size:64;default:''"`
	AuthorizationVersion int64  `gorm:"not null"`
	Status               string `gorm:"not null;size:16"`
	DecidedAt            *time.Time
	ConsumedAt           *time.Time
	CreatedAt            time.Time
}
