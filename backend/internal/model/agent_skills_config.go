package model

import "time"

// AgentSkillDefault 是管理员配置的全局默认技能行。管理员后续修改只影响新会话；
// 已创建会话的技能集合冻结在 agent_conversation_skills。
type AgentSkillDefault struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	Scope          string    `json:"scope" gorm:"size:16;not null;uniqueIndex:idx_agent_skill_defaults_scope_skill,priority:1"`
	SkillID        string    `json:"skillId" gorm:"size:36;not null;uniqueIndex:idx_agent_skill_defaults_scope_skill,priority:2;index"`
	SkillVersionID string    `json:"skillVersionId" gorm:"size:36;index"`
	Position       int       `json:"position" gorm:"index"`
	Enabled        bool      `json:"enabled" gorm:"index"`
	Revision       int64     `json:"revision" gorm:"not null;default:0"`
	UpdatedBy      string    `json:"updatedBy" gorm:"size:36"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// AgentConversationSkill 冻结一个会话装配过的技能集合：Source 区分 global/user，
// ContentHash 记录装配时的技能内容指纹，保证后续运行的可复现性。
// 主键必须是 (conversation_id, skill_id) 复合键：一个会话装配多行技能，
// 单列主键会让 SQLite 隐式唯一索引挡住同一会话的第二行技能。
type AgentConversationSkill struct {
	ConversationID string    `json:"conversationId" gorm:"primaryKey;size:80;uniqueIndex:idx_agent_conversation_skills_conversation_skill,priority:1"`
	SkillID        string    `json:"skillId" gorm:"primaryKey;size:36;not null;uniqueIndex:idx_agent_conversation_skills_conversation_skill,priority:2;index"`
	SkillVersionID string    `json:"skillVersionId" gorm:"size:36;index"`
	ContentHash    string    `json:"contentHash" gorm:"size:64"`
	Source         string    `json:"source" gorm:"size:16;index"`
	Position       int       `json:"position" gorm:"index"`
	CreatedAt      time.Time `json:"createdAt"`
}
