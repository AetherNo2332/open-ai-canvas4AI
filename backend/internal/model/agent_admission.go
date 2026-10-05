package model

import (
	"fmt"
	"time"
)

type AgentSchedulerSetting struct {
	ID                   int       `gorm:"primaryKey" json:"-"`
	DispatchConcurrency  int       `json:"dispatchConcurrency"`
	MaxResidentSessions  int       `json:"maxResidentSessions"`
	MaxResidentPerCanvas int       `json:"maxResidentPerCanvas"`
	Revision             int64     `json:"revision"`
	UpdatedBy            string    `json:"updatedBy,omitempty"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

func DefaultAgentSchedulerSetting() AgentSchedulerSetting {
	return AgentSchedulerSetting{ID: 1, DispatchConcurrency: 4, MaxResidentSessions: 64, MaxResidentPerCanvas: 16}
}

func ValidateAgentSchedulerSetting(p AgentSchedulerSetting) error {
	if p.DispatchConcurrency < 1 || p.DispatchConcurrency > 16 || p.MaxResidentSessions < p.DispatchConcurrency || p.MaxResidentSessions > 64 || p.MaxResidentPerCanvas < 1 || p.MaxResidentPerCanvas > p.MaxResidentSessions {
		return fmt.Errorf("Agent 配置要求：调度槽 1–16，驻留上限不小于调度槽且不超过 64，画布上限 1–驻留上限")
	}
	return nil
}

// This row serializes successful admissions across executors, independently of the event counter.
type AgentAdmissionCounter struct {
	ID    int `gorm:"primaryKey"`
	Value int64
}
type AgentCanvasAdmission struct {
	Key                   string `gorm:"primaryKey;size:200"`
	LastAdmissionSequence int64  `gorm:"index"`
}
