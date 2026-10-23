package model

import "time"

const (
	AgentProfileScopeSystemPrompt = "system_prompt"
)

// AgentProfile stores the account system prompt. Historical user/project/canvas
// preference rows are retained but are no longer read by Agent runs.
type AgentProfile struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	UserID    string    `json:"userId" gorm:"size:36;uniqueIndex:idx_agent_profiles_scope,priority:1;index"`
	Scope     string    `json:"scope" gorm:"size:16;uniqueIndex:idx_agent_profiles_scope,priority:2"`
	ProjectID string    `json:"projectId,omitempty" gorm:"size:36;uniqueIndex:idx_agent_profiles_scope,priority:3"`
	CanvasID  string    `json:"canvasId,omitempty" gorm:"size:80;uniqueIndex:idx_agent_profiles_scope,priority:4"`
	Content   string    `json:"content" gorm:"type:text"`
	Revision  int64     `json:"revision"`
	Hash      string    `json:"hash" gorm:"size:64;index"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
