package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// The counter row is updated inside the producer transaction. Unlike a database
// sequence, its lock survives until commit, so SSE cannot skip a late commit.
type AgentEventCounter struct {
	ID    int   `gorm:"primaryKey;autoIncrement:false"`
	Value int64 `gorm:"not null"`
}

type AgentWakeEvent struct {
	Sequence  int64     `gorm:"primaryKey;autoIncrement:false" json:"sequence"`
	RunID     string    `gorm:"size:80;index" json:"runId,omitempty"`
	UserID    string    `gorm:"size:36;index" json:"-"`
	TaskID    string    `gorm:"size:80;index" json:"taskId,omitempty"`
	Kind      string    `gorm:"size:40" json:"kind"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
}

func AppendAgentWake(tx *gorm.DB, event AgentWakeEvent) error {
	counter := AgentEventCounter{ID: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
		return err
	}
	if err := tx.Model(&AgentEventCounter{}).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 1")).Error; err != nil {
		return err
	}
	if err := tx.First(&counter, 1).Error; err != nil {
		return err
	}
	event.Sequence = counter.Value
	event.CreatedAt = time.Now()
	return tx.Create(&event).Error
}

// A prepared tool operation is durable before any asynchronous execution starts.
// Completion is recovered from the existing business receipt, never by replaying
// a completed side effect merely because the Agent missed a notification.
type AgentToolOperation struct {
	ID             string     `gorm:"primaryKey;size:240" json:"operationId"`
	UserID         string     `gorm:"size:36;index" json:"-"`
	RunID          string     `gorm:"size:80;index" json:"runId"`
	TaskID         string     `gorm:"size:80" json:"taskId"`
	CallID         string     `gorm:"size:160" json:"callId"`
	Status         string     `gorm:"size:32;index" json:"status"`
	LeaseOwner     string     `gorm:"size:80" json:"-"`
	LeaseExpiresAt *time.Time `json:"-"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AgentRuntimeInstance struct {
	ID                    string    `gorm:"primaryKey;size:80" json:"instanceId"`
	Active                int       `json:"resident"`
	Capacity              int       `json:"capacity"`
	ClaimReservations     int       `json:"claimReservations"`
	DispatchActive        int       `json:"dispatchActive"`
	ReadyQueued           int       `json:"readyQueued"`
	Draining              bool      `json:"draining"`
	AppliedConfigRevision int64     `json:"appliedConfigRevision"`
	UpdatedAt             time.Time `gorm:"index" json:"lastHeartbeatAt"`
}

type AgentCapacityReport struct {
	Active                int   `json:"active"`
	Capacity              int   `json:"capacity"`
	ClaimReservations     int   `json:"claimReservations"`
	DispatchActive        int   `json:"dispatchActive"`
	ReadyQueued           int   `json:"readyQueued"`
	Draining              bool  `json:"draining"`
	AppliedConfigRevision int64 `json:"appliedConfigRevision"`
}

type AgentRuntimeStatus struct {
	AgentRuntimeInstance
	Online bool `json:"online"`
}
