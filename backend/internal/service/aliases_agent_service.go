package service

import (
	as "yingce/backend/internal/agentservice"
)

// AgentSessionsFor 返回 svc 的通用 Agent Session 入口；合同由 internal/agentservice 定义。
func AgentSessionsFor(svc *Service) as.Sessions { return svc.AgentSessions() }
