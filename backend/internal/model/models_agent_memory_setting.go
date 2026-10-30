package model

import "time"

const (
	AgentMemoryCompactIntervalOff     = "off"
	AgentMemoryCompactIntervalDaily   = "daily"
	AgentMemoryCompactIntervalWeekly  = "weekly"
	AgentMemoryCompactIntervalMonthly = "monthly"

	AgentMemoryCompactStatusIdle      = "idle"
	AgentMemoryCompactStatusQueued    = "queued"
	AgentMemoryCompactStatusRunning   = "running"
	AgentMemoryCompactStatusSucceeded = "succeeded"
	AgentMemoryCompactStatusFailed    = "failed"
)

// AgentMemorySetting 是已下线的经验压缩设置；表随 schema v58 保留，运行时不再读写。
type AgentMemorySetting struct {
	UserID           string     `json:"userId" gorm:"primaryKey;size:36"`
	CompactInterval  string     `json:"compactInterval" gorm:"size:16;index"`
	LogicalModelID   string     `json:"logicalModelId" gorm:"size:36"`
	ChannelID        string     `json:"channelId" gorm:"size:36"`
	ChannelModelKey  string     `json:"channelModelKey" gorm:"size:160"`
	Model            string     `json:"model" gorm:"size:240"`
	LastCompactAt    *time.Time `json:"lastCompactAt,omitempty"`
	CompactStartedAt *time.Time `json:"compactStartedAt,omitempty"`
	CompactTaskID    string     `json:"compactTaskId,omitempty" gorm:"size:36;index"`
	LastStatus       string     `json:"lastStatus" gorm:"size:16;index"`
	LastSummaryJSON  string     `json:"-" gorm:"type:text"`
	LastError        string     `json:"lastError,omitempty" gorm:"type:text"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}
