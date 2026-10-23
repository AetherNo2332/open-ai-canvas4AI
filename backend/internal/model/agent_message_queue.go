package model

import "time"

// AgentQueuedMessage is a durable submission receipt, independent of a run.
// Fingerprint always describes the original POST; edits only change RequestJSON.
type AgentQueuedMessage struct {
	ID             string `json:"id" gorm:"primaryKey;size:80"`
	UserID         string `json:"-" gorm:"size:36;uniqueIndex:idx_agent_message_key,priority:1;index:idx_agent_message_queue,priority:1"`
	ConversationID string `json:"conversationId" gorm:"size:80;index:idx_agent_message_queue,priority:2"`
	CanvasID       string `json:"canvasId" gorm:"size:80"`
	IdempotencyKey string `json:"-" gorm:"size:128;uniqueIndex:idx_agent_message_key,priority:2"`
	Fingerprint    string `json:"-" gorm:"size:64;not null"`
	Mode           string `json:"mode" gorm:"size:16;not null"`
	// An explicit send may start one recovery turn while ordinary work stays
	// paused. Stop/pause and a definitive admission failure revoke that intent.
	DispatchRequested bool       `json:"-" gorm:"not null;default:false"`
	Sequence          int64      `json:"sequence" gorm:"index:idx_agent_message_queue,priority:4"`
	Revision          int64      `json:"revision" gorm:"not null"`
	Prompt            string     `json:"prompt" gorm:"type:text"`
	RequestJSON       string     `json:"-" gorm:"type:text"`
	Status            string     `json:"status" gorm:"size:24;index:idx_agent_message_queue,priority:3"`
	RunID             string     `json:"runId,omitempty" gorm:"size:80"`
	ReplyToQuestionID string     `json:"-" gorm:"size:160"`
	DispatchToken     string     `json:"-" gorm:"size:80"`
	DispatchParentID  string     `json:"-" gorm:"size:80"`
	LeaseUntil        *time.Time `json:"-" gorm:"index"`
	Attempts          int        `json:"-"`
	NextAttemptAt     *time.Time `json:"-" gorm:"index"`
	LastError         string     `json:"error,omitempty" gorm:"size:1000"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type AgentConversationQueue struct {
	UserID             string    `json:"-" gorm:"primaryKey;size:36"`
	ConversationID     string    `json:"conversationId" gorm:"primaryKey;size:80"`
	CanvasID           string    `json:"canvasId" gorm:"size:80"`
	Revision           int64     `json:"revision" gorm:"not null"`
	NextSequence       int64     `json:"-"`
	Paused             bool      `json:"paused" gorm:"not null;default:false"`
	PauseReason        string    `json:"pauseReason,omitempty" gorm:"size:1000"`
	AwaitingQuestionID string    `json:"awaitingQuestionId,omitempty" gorm:"size:160"`
	QuestionRunID      string    `json:"questionRunId,omitempty" gorm:"size:80"`
	LastSettledRunID   string    `json:"-" gorm:"size:80"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type AgentUISetting struct {
	UserID          string    `json:"-" gorm:"primaryKey;size:36"`
	DefaultSendMode string    `json:"defaultSendMode" gorm:"size:16;not null"`
	Revision        int64     `json:"revision" gorm:"not null"`
	UpdatedAt       time.Time `json:"-"`
}
