package app

import (
	"errors"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/repository"
	"os"
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
	if p.Readiness == "" {
		p.Readiness = "starting"
	}
	if owner == "" || len(owner) > 80 || p.Active < 0 || p.Active > 64 || p.Capacity < 1 || p.Capacity > 64 || p.ClaimReservations < 0 || p.ClaimReservations > 16 || p.DispatchActive < 0 || p.DispatchActive > 16 || p.ReadyQueued < 0 || p.AppliedConfigRevision < 0 || (p.Active > p.Capacity && !p.Draining) || len(p.ReadinessReason) > 512 || (p.Readiness != "starting" && p.Readiness != "ready" && p.Readiness != "degraded" && p.Readiness != "draining") {
		return BadAuthRequest("Invalid Agent capacity")
	}
	return s.repo.AgentCapacityReport(owner, p)
}

type AgentAvailability struct {
	Configured   bool   `json:"configured"`
	ReadyWorkers int    `json:"readyWorkers"`
	QueueReason  string `json:"queueReason"`
	Reason       string `json:"reason"`
}

func (s *Service) AgentAvailability() (AgentAvailability, error) {
	status := AgentAvailability{Configured: os.Getenv("CANVAS_AGENT_INTERNAL_TOKEN") != ""}
	if !status.Configured {
		status.QueueReason, status.Reason = "not_configured", "Agent 未配置：请设置 CANVAS_AGENT_INTERNAL_TOKEN 并部署 Worker"
		return status, nil
	}
	rows, err := s.repo.AgentRuntimeInstances()
	if err != nil {
		return status, err
	}
	var online, free int
	for _, row := range rows {
		if !row.UpdatedAt.After(time.Now().Add(-45 * time.Second)) {
			continue
		}
		online++
		if row.Readiness == "ready" && !row.Draining {
			status.ReadyWorkers++
			if row.Active+row.ClaimReservations < row.Capacity {
				free++
			}
		}
	}
	switch {
	case status.ReadyWorkers == 0 && online == 0:
		status.QueueReason, status.Reason = "worker_offline", "Agent Worker 离线；请检查容器、令牌和后端连接，任务将在限定时间内等待"
	case status.ReadyWorkers == 0:
		status.QueueReason, status.Reason = "worker_not_ready", "Agent Worker 在线但未就绪；请检查协议、Harness 和配置"
	case free == 0:
		status.QueueReason, status.Reason = "capacity", "Agent Worker 已满载，任务正在排队"
	}
	return status, nil
}
