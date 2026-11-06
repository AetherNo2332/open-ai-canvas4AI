package model

import "time"

// AgentSession is the business-neutral Session root. Owner, definition and
// scope (application + space) are written once at creation and never updated;
// canvas conversations map onto it through ConversationID without moving the
// existing Pi history. Lease columns hold the single live writer.
type AgentSession struct {
	ID                string     `gorm:"primaryKey;size:80"`
	OwnerID           string     `gorm:"not null;size:36;uniqueIndex:idx_agent_sessions_owner_key,priority:1;index:idx_agent_sessions_owner_scope,priority:1;index:idx_agent_sessions_owner_conversation,priority:1;index:idx_agent_sessions_owner_created,priority:1"`
	IdempotencyKey    string     `gorm:"not null;size:200;uniqueIndex:idx_agent_sessions_owner_key,priority:2"`
	Fingerprint       string     `gorm:"not null;size:64"`
	DefinitionID      string     `gorm:"not null;size:120"`
	DefinitionVersion string     `gorm:"not null;size:40"`
	Role              string     `gorm:"not null;size:24"`
	Application       string     `gorm:"not null;size:40;index:idx_agent_sessions_owner_scope,priority:2"`
	SpaceType         string     `gorm:"not null;size:40;index:idx_agent_sessions_owner_scope,priority:3"`
	SpaceID           string     `gorm:"not null;size:120;index:idx_agent_sessions_owner_scope,priority:4"`
	WorkItemID        string     `gorm:"size:80;index"`
	ConversationID    string     `gorm:"size:80;index:idx_agent_sessions_owner_conversation,priority:2"`
	Title             string     `gorm:"size:240"`
	Status            string     `gorm:"not null;size:24;default:active"`
	Revision          int64      `gorm:"not null;default:1"`
	ActiveRunID       string     `gorm:"size:80"`
	LeaseOwner        string     `gorm:"size:80"`
	LeaseEpoch        int64      `gorm:"not null;default:0"`
	LeaseExpiresAt    *time.Time `gorm:"index"`
	ArchivedAt        *time.Time
	CreatedAt         time.Time `gorm:"index:idx_agent_sessions_owner_created,priority:2"`
	UpdatedAt         time.Time
}

// AgentSessionMessage is one message of a non-canvas Session. Canvas Sessions
// keep their history in the Pi tables; nothing is copied here. Sequence is
// per Session and only the live writer appends.
type AgentSessionMessage struct {
	ID        string `gorm:"primaryKey;size:80"`
	OwnerID   string `gorm:"not null;size:36;uniqueIndex:idx_agent_session_messages_key,priority:1"`
	SessionID string `gorm:"not null;size:80;uniqueIndex:idx_agent_session_messages_key,priority:2;uniqueIndex:idx_agent_session_messages_seq,priority:1"`
	MessageID string `gorm:"not null;size:120;uniqueIndex:idx_agent_session_messages_key,priority:3"`
	Sequence  int64  `gorm:"not null;uniqueIndex:idx_agent_session_messages_seq,priority:2"`
	Kind      string `gorm:"not null;size:24"`
	Actor     string `gorm:"not null;size:24"`
	Role      string `gorm:"not null;size:24"`
	Body      string `gorm:"type:text;not null"`
	RunID     string `gorm:"size:80"`
	CreatedAt time.Time
}

// AgentMemoryNote is a scoped note. Session notes carry SessionID; space
// documents carry the full space scope and the note they were published from;
// user preferences carry neither. The legacy per-user document is not migrated
// here and is never injected under the new contract.
type AgentMemoryNote struct {
	ID          string `gorm:"primaryKey;size:80"`
	OwnerID     string `gorm:"not null;size:36;index:idx_agent_memory_notes_session,priority:1;index:idx_agent_memory_notes_space,priority:1;uniqueIndex:idx_agent_memory_notes_published,priority:1"`
	Kind        string `gorm:"not null;size:24;index:idx_agent_memory_notes_space,priority:2"`
	SessionID   string `gorm:"size:80;index:idx_agent_memory_notes_session,priority:2"`
	Application string `gorm:"size:40;index:idx_agent_memory_notes_space,priority:3"`
	SpaceType   string `gorm:"size:40;index:idx_agent_memory_notes_space,priority:4"`
	SpaceID     string `gorm:"size:120;index:idx_agent_memory_notes_space,priority:5"`
	// PublishedFrom is set on space documents only, so publishing one note twice
	// returns the same document.
	PublishedFrom *string `gorm:"size:80;uniqueIndex:idx_agent_memory_notes_published,priority:2"`
	Body          string  `gorm:"type:text;not null"`
	Revision      int64   `gorm:"not null;default:1"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AgentTurn is one queued turn of a business Session: a user message (or a
// delivered work input) waiting for the Agent's reply. The background runner
// advances it until it is done or failed; the model choice is frozen on the
// first pass so a resumed turn keeps the same model.
type AgentTurn struct {
	ID             string     `gorm:"primaryKey;size:36"`
	OwnerID        string     `gorm:"not null;size:36;uniqueIndex:idx_agent_turn_message,priority:1"`
	SessionID      string     `gorm:"not null;size:80;uniqueIndex:idx_agent_turn_message,priority:2;index:idx_agent_turn_session,priority:1"`
	MessageID      string     `gorm:"not null;size:160;uniqueIndex:idx_agent_turn_message,priority:3"`
	MessageSeq     int64      `gorm:"not null;index:idx_agent_turn_session,priority:2"`
	Text           string     `gorm:"type:text;not null"`
	Status         string     `gorm:"not null;size:16;index"`
	ModelJSON      string     `gorm:"type:text"`
	BudgetGrantID  string     `gorm:"not null;size:36;default:''"`
	Attempts       int        `gorm:"not null;default:0"`
	Error          string     `gorm:"size:500"`
	NextAt         *time.Time `gorm:"index"`
	ClaimOwner     string     `gorm:"size:80;index"`
	ClaimExpiresAt *time.Time `gorm:"index"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
