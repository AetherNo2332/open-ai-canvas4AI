package model

import "time"

type AgentCrew struct {
	ID                  string    `json:"id" gorm:"primaryKey;size:80"`
	UserID              string    `json:"userId" gorm:"index;size:36;not null"`
	WorkspaceID         string    `json:"workspaceId" gorm:"index;size:80;not null"`
	Name                string    `json:"name" gorm:"size:120"`
	Description         string    `json:"description" gorm:"type:text"`
	Status              string    `json:"status" gorm:"size:24;index"`
	CoordinatorMemberID string    `json:"coordinatorMemberId" gorm:"size:80"`
	Revision            int64     `json:"revision" gorm:"not null;default:0"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type AgentCrewMember struct {
	ID               string               `json:"id" gorm:"primaryKey;size:80"`
	CrewID           string               `json:"crewId" gorm:"index;uniqueIndex:idx_crew_member_position,priority:1;size:80;not null"`
	Role             CrewMemberRole       `json:"role" gorm:"size:24"`
	Name             string               `json:"name" gorm:"size:120"`
	ModelConfigJSON  string               `json:"-" gorm:"type:text"`
	PermissionMode   CrewMemberPermission `json:"permissionMode" gorm:"size:24"`
	FocusNodeIDsJSON string               `json:"-" gorm:"type:text"`
	BudgetJSON       string               `json:"-" gorm:"type:text"`
	Enabled          bool                 `json:"enabled"`
	Position         int                  `json:"position" gorm:"uniqueIndex:idx_crew_member_position,priority:2"`
	CreatedAt        time.Time            `json:"createdAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
}

type AgentCrewMemberSkill struct {
	MemberID       string `json:"memberId" gorm:"primaryKey;size:80"`
	SkillID        string `json:"skillId" gorm:"primaryKey;size:36"`
	SkillVersionID string `json:"skillVersionId" gorm:"index;size:36"`
	Position       int    `json:"position"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      time.Time
}

type AgentCrewSnapshot struct {
	Crew    AgentCrew
	Members []AgentCrewMember
	Skills  []AgentCrewMemberSkill
}
