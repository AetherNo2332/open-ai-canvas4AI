package model

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// PiSessionStorageID makes the internal Pi session key owner-scoped while the
// public conversation ID remains unchanged. Clients may reuse a conversation
// ID across accounts; the durable primary key must not turn that into a global
// uniqueness constraint or let one account collide with another account's tree.
func PiSessionStorageID(userID, conversationID string) string {
	identity := userID + "\x00" + conversationID
	digest := sha256.Sum256([]byte(identity))
	return "pi_" + hex.EncodeToString(digest[:])
}

// CloudAgentPiSession is the durable Pi v3 session shared by every run in a conversation.
// Its owner and canvas are checked on every repository access; the worker token is not an
// authorization boundary.
type CloudAgentPiSession struct {
	ID             string     `gorm:"primaryKey;size:80"`
	UserID         string     `gorm:"not null;uniqueIndex:idx_cloud_agent_pi_sessions_owner_conversation,priority:1;size:36"`
	ConversationID string     `gorm:"not null;uniqueIndex:idx_cloud_agent_pi_sessions_owner_conversation,priority:2;size:80"`
	CanvasID       string     `gorm:"not null;index;size:80"`
	FormatVersion  int        `gorm:"not null;default:3"`
	HeaderJSON     string     `gorm:"type:text;not null"`
	ActiveLeafID   string     `gorm:"size:160"`
	Revision       int64      `gorm:"not null;default:1"`
	ActiveRunID    string     `gorm:"index;size:80"`
	LeaseOwner     string     `gorm:"size:80"`
	LeaseEpoch     int64      `gorm:"not null;default:0"`
	LeaseExpiresAt *time.Time `gorm:"index"`
	Title          string     `gorm:"size:240"`
	ArchivedAt     *time.Time `gorm:"index"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CloudAgentPiEntry stores the append-only entry tree from Pi SessionManager.
// Sequence provides stable retrieval order; EntryID and ParentID preserve the Pi tree.
type CloudAgentPiEntry struct {
	SessionID string `gorm:"primaryKey;size:80;uniqueIndex:idx_cloud_agent_pi_entries_entry,priority:1;index:idx_cloud_agent_pi_entries_parent,priority:1"`
	Sequence  int    `gorm:"primaryKey;autoIncrement:false"`
	EntryID   string `gorm:"not null;uniqueIndex:idx_cloud_agent_pi_entries_entry,priority:2;size:160"`
	UserID    string `gorm:"not null;index;size:36"`
	RunID     string `gorm:"not null;index;size:80"`
	ParentID  string `gorm:"index:idx_cloud_agent_pi_entries_parent,priority:2;size:160"`
	EntryJSON string `gorm:"type:text;not null"`
	CreatedAt time.Time
}
