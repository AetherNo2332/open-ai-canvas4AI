package app

import (
	"errors"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/repository"
	"time"
)

type AgentSchedulerUpdate struct {
	ExpectedRevision     int64 `json:"expectedRevision"`
	DispatchConcurrency  int   `json:"dispatchConcurrency"`
	MaxResidentSessions  int   `json:"maxResidentSessions"`
	MaxResidentPerCanvas int   `json:"maxResidentPerCanvas"`
}

func (s *Service) AgentSchedulerConfig() (model.AgentSchedulerSetting, error) {
	fallback := platform.DefaultAgentSchedulerSetting()
	p, err := s.repo.AgentSchedulerSetting(fallback)
	if err != nil {
		return p, err
	}
	return p, model.ValidateAgentSchedulerSetting(p)
}

func (s *Service) AdminAgentSchedulerSetting(actor *model.User) (*model.AgentSchedulerSetting, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	p, err := s.AgentSchedulerConfig()
	return &p, err
}

func (s *Service) UpdateAgentSchedulerSetting(actor *model.User, input AgentSchedulerUpdate) (*model.AgentSchedulerSetting, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	p := model.AgentSchedulerSetting{ID: 1, DispatchConcurrency: input.DispatchConcurrency, MaxResidentSessions: input.MaxResidentSessions, MaxResidentPerCanvas: input.MaxResidentPerCanvas, UpdatedBy: actor.ID}
	if input.ExpectedRevision < 0 {
		return nil, BadAuthRequest("Invalid config revision")
	}
	if err := model.ValidateAgentSchedulerSetting(p); err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	audit, err := newAdminAuditEvent(actor, "agent_scheduler.update", "agent_scheduler_setting", "1", "更新 Agent 调度配置", input)
	if err != nil {
		return nil, err
	}
	if err = s.repo.SaveAgentSchedulerSetting(&p, input.ExpectedRevision, audit); err != nil {
		if errors.Is(err, repository.ErrCreationConflict) {
			return nil, kernel.NewAppError(409, "Agent 配置已被更新，请刷新后重试")
		}
		return nil, err
	}
	return &p, nil
}

func (s *Service) AdminAgentSchedulerStatus(actor *model.User) ([]model.AgentRuntimeStatus, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := s.repo.AgentRuntimeInstances()
	if err != nil {
		return nil, err
	}
	result := make([]model.AgentRuntimeStatus, 0, len(rows))
	for _, row := range rows {
		result = append(result, model.AgentRuntimeStatus{AgentRuntimeInstance: row, Online: row.UpdatedAt.After(time.Now().Add(-45 * time.Second))})
	}
	return result, nil
}

func (s *Service) PiCapacityReport(owner string, p model.AgentCapacityReport) error {
	if owner == "" || len(owner) > 80 || p.Active < 0 || p.Active > 64 || p.Capacity < 1 || p.Capacity > 64 || p.ClaimReservations < 0 || p.ClaimReservations > 16 || p.DispatchActive < 0 || p.DispatchActive > 16 || p.ReadyQueued < 0 || p.AppliedConfigRevision < 0 || (p.Active > p.Capacity && !p.Draining) {
		return BadAuthRequest("Invalid Agent capacity")
	}
	return s.repo.AgentCapacityReport(owner, p)
}
