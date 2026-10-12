package model

import "time"

type CanvasDocumentEpoch struct {
	CanvasID  string `gorm:"primaryKey;size:80"`
	OwnerID   string `gorm:"index;size:36"`
	Epoch     string `gorm:"size:80"`
	EventSeq  int64  `gorm:"not null"`
	Deleted   bool   `gorm:"not null;default:false"`
	UpdatedAt time.Time
}
type CanvasEntityVersion struct {
	CanvasID  string `gorm:"primaryKey;size:80"`
	Epoch     string `gorm:"primaryKey;size:80"`
	EntityKey string `gorm:"primaryKey;size:200"`
	Version   int64  `gorm:"not null"`
	Deleted   bool   `gorm:"not null;default:false"`
}
type CanvasCommandReceipt struct {
	OwnerID       string `gorm:"primaryKey;size:36"`
	CanvasID      string `gorm:"primaryKey;size:80"`
	Epoch         string `gorm:"primaryKey;size:80"`
	ActorID       string `gorm:"primaryKey;size:80"`
	ClientID      string `gorm:"primaryKey;size:128"`
	OperationID   string `gorm:"primaryKey;size:128"`
	CanonicalJSON string `gorm:"type:text;not null"`
	ReceiptJSON   string `gorm:"type:text;not null"`
	CreatedAt     time.Time
}
type CanvasCommandEvent struct {
	CanvasID    string `gorm:"primaryKey;size:80"`
	Sequence    int64  `gorm:"primaryKey"`
	OwnerID     string `gorm:"index;size:36"`
	Epoch       string `gorm:"index;size:80"`
	OperationID string `gorm:"size:128"`
	EventJSON   string `gorm:"type:text;not null"`
	CreatedAt   time.Time
}
type CanvasActorHistory struct {
	ID                   string `gorm:"primaryKey;size:80"`
	CanvasID             string `gorm:"index:idx_canvas_actor_history,priority:1;size:80"`
	Epoch                string `gorm:"index:idx_canvas_actor_history,priority:2;size:80"`
	OwnerID              string `gorm:"size:36"`
	ActorID              string `gorm:"index:idx_canvas_actor_history,priority:3;size:80"`
	ClientID             string `gorm:"index:idx_canvas_actor_history,priority:4;size:128"`
	OperationID          string `gorm:"size:128"`
	Sequence             int64  `gorm:"index:idx_canvas_actor_history,priority:5"`
	State                string `gorm:"size:20;not null"`
	ForwardJSON          string `gorm:"type:text;not null"`
	InverseJSON          string `gorm:"type:text;not null"`
	ExpectedVersionsJSON string `gorm:"type:text;not null"`
	CreatedAt            time.Time
}

// Protect current resources and those needed to undo retained command history.
type CanvasCommandResource struct {
	OwnerID     string `gorm:"index;size:36"`
	CanvasID    string `gorm:"primaryKey;size:80"`
	Epoch       string `gorm:"primaryKey;size:80"`
	OperationID string `gorm:"primaryKey;size:128"`
	Kind        string `gorm:"primaryKey;size:20"`
	ResourceID  string `gorm:"primaryKey;index;size:80"`
}
type CanvasCreateReceipt struct {
	OwnerID       string `gorm:"primaryKey;size:36"`
	ActorID       string `gorm:"primaryKey;size:80"`
	ClientID      string `gorm:"primaryKey;size:128"`
	OperationID   string `gorm:"primaryKey;size:128"`
	CanonicalJSON string `gorm:"type:text;not null"`
	CanvasID      string `gorm:"size:80"`
	SnapshotJSON  string `gorm:"type:text;not null"`
	CreatedAt     time.Time
}
