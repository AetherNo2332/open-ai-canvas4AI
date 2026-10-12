package repository

import "infinite-canvas/backend/internal/model"

// CloudAgentAdmission carries the objects prepared by application validation
// into the single repository transaction that creates a run and its holding
// task. It is independent of any orchestration strategy.
type CloudAgentAdmission struct {
	Execution *model.CloudAgentExecution
	Task      *model.Task
	Order     *model.BillingOrder
	Skills    []model.AgentConversationSkill
}
