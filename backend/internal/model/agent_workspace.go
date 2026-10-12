package model

import "time"

// AgentWorkspace is the one-to-one Agent extension of a CanvasProject.
// Its document and skill rows are versioned together for future runs.
type AgentWorkspace struct {
	ID           string    `json:"id" gorm:"primaryKey;size:80"`
	UserID       string    `json:"userId" gorm:"index;size:36;not null"`
	CanvasID     string    `json:"canvasId" gorm:"uniqueIndex;size:80;not null"`
	AgentsMD     string    `json:"agentsMd" gorm:"type:text"`
	AgentsMDHash string    `json:"agentsMdHash" gorm:"size:64"`
	Revision     int64     `json:"revision" gorm:"not null;default:0"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type AgentWorkspaceSkill struct {
	WorkspaceID    string `json:"workspaceId" gorm:"primaryKey;size:80"`
	SkillID        string `json:"skillId" gorm:"primaryKey;size:36"`
	SkillVersionID string `json:"skillVersionId" gorm:"size:36;index"`
	Position       int    `json:"position" gorm:"index"`
	Enabled        bool   `json:"enabled" gorm:"index"`
	CreatedAt      time.Time
}

type AgentWorkspaceSnapshot struct {
	ID           string
	UserID       string
	CanvasID     string
	AgentsMD     string
	AgentsMDHash string
	Revision     int64
	Skills       []AgentWorkspaceSkill
}
