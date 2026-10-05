package model

import "time"

type AgentCrewEvent struct {
	CrewRunID   string `gorm:"primaryKey;size:80"`
	Sequence    int64  `gorm:"primaryKey"`
	MemberRunID string `gorm:"size:80"`
	Type        string `gorm:"size:40"`
	PayloadJSON string `gorm:"type:text"`
	CreatedAt   time.Time
}
