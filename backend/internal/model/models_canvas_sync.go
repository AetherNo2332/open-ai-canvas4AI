package model

import "time"

// CanvasNodeEditLease is serialized by the same account/canvas write lock as
// document mutations. Token is private authentication material, never state.
type CanvasNodeEditLease struct {
	CanvasID  string    `gorm:"primaryKey;size:128" json:"canvasId"`
	NodeID    string    `gorm:"primaryKey;size:128" json:"nodeId"`
	OwnerID   string    `gorm:"size:128;not null;index" json:"-"`
	SessionID string    `gorm:"size:128;not null" json:"sessionId"`
	Token     string    `gorm:"size:128;not null" json:"-"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expiresAt"`
}
