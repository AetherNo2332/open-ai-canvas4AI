package model

import "time"

type AgentCrewRun struct {
	ID                string        `gorm:"primaryKey;size:80"`
	UserID            string        `gorm:"index;uniqueIndex:idx_crew_run_idempotency,priority:1;size:36"`
	CrewID            string        `gorm:"index;size:80"`
	WorkspaceID       string        `gorm:"index;size:80"`
	CanvasID          string        `gorm:"index;size:80"`
	CoordinatorRunID  string        `gorm:"size:80;index"`
	IdempotencyKey    string        `gorm:"uniqueIndex:idx_crew_run_idempotency,priority:2;size:160"`
	InputHash         string        `gorm:"size:64"`
	Status            CrewRunStatus `gorm:"size:32;index"`
	Revision          int64
	WorkspaceHash     string `gorm:"size:64"`
	WorkspaceRevision int64
	SnapshotJSON      string `gorm:"type:text"`
	BudgetJSON        string `gorm:"type:text"`
	LatestSequence    int64
	ApprovalID        string `gorm:"size:80"`
	ProposalJSON      string `gorm:"type:text"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type AgentCrewMemberRun struct {
	ID             string               `gorm:"primaryKey;size:80"`
	CrewRunID      string               `gorm:"uniqueIndex:idx_crew_member_run_identity,priority:1;index;size:80"`
	MemberID       string               `gorm:"uniqueIndex:idx_crew_member_run_identity,priority:2;size:80"`
	AgentRunID     string               `gorm:"uniqueIndex;size:80"`
	Role           CrewMemberRole       `gorm:"size:24"`
	Name           string               `gorm:"size:120"`
	Permission     CrewMemberPermission `gorm:"size:24"`
	Position       int
	Status         MemberRunStatus `gorm:"size:32;index"`
	Attempt        int             `gorm:"uniqueIndex:idx_crew_member_run_identity,priority:3"`
	TaskID         string          `gorm:"size:160;index"`
	TaskHash       string          `gorm:"size:64"`
	TaskJSON       string          `gorm:"type:text"`
	ResultJSON     string          `gorm:"type:text"`
	Summary        string          `gorm:"type:text"`
	ErrorCode      string          `gorm:"size:80"`
	LeaseOwner     string          `gorm:"size:80"`
	LeaseEpoch     int64
	LeaseExpiresAt *time.Time `gorm:"index"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Only structured task/result envelopes are shared between members.
type AgentCrewMessage struct {
	ID            string `gorm:"primaryKey;size:80"`
	CrewRunID     string `gorm:"index;size:80;uniqueIndex:idx_crew_message_identity,priority:1"`
	MemberRunID   string `gorm:"size:80;uniqueIndex:idx_crew_message_identity,priority:2"`
	TaskID        string `gorm:"size:160;uniqueIndex:idx_crew_message_identity,priority:3"`
	Kind          string `gorm:"size:24;uniqueIndex:idx_crew_message_identity,priority:4"`
	PayloadJSON   string `gorm:"type:text"`
	ParameterHash string `gorm:"size:64"`
	CreatedAt     time.Time
}

type AgentCrewRunSnapshot struct {
	Run     AgentCrewRun
	Members []AgentCrewMemberRun
}
