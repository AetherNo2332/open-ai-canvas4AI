package platform

import "infinite-canvas/backend/internal/model"

type AgentSchedulerSetting = model.AgentSchedulerSetting

func DefaultAgentSchedulerSetting() AgentSchedulerSetting {
	p := model.DefaultAgentSchedulerSetting()
	p.DispatchConcurrency = envInt("CANVAS_AGENT_CONCURRENCY", p.DispatchConcurrency)
	p.MaxResidentSessions = envInt("CANVAS_AGENT_MAX_SESSIONS", p.MaxResidentSessions)
	p.MaxResidentPerCanvas = envInt("CANVAS_AGENT_MAX_SESSIONS_PER_CANVAS", min(16, p.MaxResidentSessions))
	return p
}

func ValidateAgentSchedulerSetting(p AgentSchedulerSetting) error {
	return model.ValidateAgentSchedulerSetting(p)
}
