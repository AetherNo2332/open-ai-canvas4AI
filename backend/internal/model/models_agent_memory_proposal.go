package model

import "time"

// AgentMemoryProposal and AgentMemoryRevision only keep upstream schema v58
// reproducible. The fork Agent applies memory through its own lesson approval
// flow and never reads or writes these tables.
type AgentMemoryProposal struct {
	ID          string    `gorm:"primaryKey;size:36"`
	UserID      string    `gorm:"size:36;not null;index:idx_memory_proposal_owner_created,priority:1"`
	TaskID      string    `gorm:"size:36;index"`
	Status      string    `gorm:"size:16;not null;index"`
	Version     int64     `gorm:"not null"`
	InputJSON   string    `gorm:"type:text;not null"`
	ChangesJSON string    `gorm:"type:text;not null"`
	SummaryJSON string    `gorm:"type:text;not null"`
	CreatedAt   time.Time `gorm:"index:idx_memory_proposal_owner_created,priority:2"`
	UpdatedAt   time.Time
	DecidedAt   *time.Time
}

type AgentMemoryRevision struct {
	ID         string    `gorm:"primaryKey;size:36"`
	UserID     string    `gorm:"size:36;not null;index:idx_memory_revision_owner_created,priority:1"`
	ProposalID string    `gorm:"size:36;not null;uniqueIndex"`
	Version    int64     `gorm:"not null"`
	BeforeJSON string    `gorm:"type:text;not null"`
	AfterJSON  string    `gorm:"type:text;not null"`
	CreatedAt  time.Time `gorm:"index:idx_memory_revision_owner_created,priority:2"`
	RestoredAt *time.Time
}
