package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// agentSessionsMigrationVersion 建立通用 Agent Session 根记录（业务范围不可变、单写者租约）。
const agentSessionsMigrationVersion int64 = 86

const agentSessionsChecksum = "sha256:agent-service-sessions-v86-20261106"

// agentContextMigrationVersion 建立非画布会话的消息与按范围划分的记忆笔记。
const agentContextMigrationVersion int64 = 87

const agentContextChecksum = "sha256:agent-service-context-v87-20261106"

// platformBudgetMigrationVersion 在基础计费层建立层级预算授权与订单归属；版本接在 Agent 迁移之后。
const platformBudgetMigrationVersion int64 = 88

const platformBudgetChecksum = "sha256:creation-platform-budget-grants-v88-20261106"

// agentWorkMigrationVersion 建立总控工作单、派工、输入、报告与待投递记录。
const agentWorkMigrationVersion int64 = 89

const agentWorkChecksum = "sha256:agent-service-work-v89-20261106"

// agentApprovalMigrationVersion 建立总控与业务视图共用的审批记录。
const agentApprovalMigrationVersion int64 = 90

const agentApprovalChecksum = "sha256:agent-service-approvals-v90-20261106"

// agentTurnMigrationVersion 建立业务会话的待回复轮次队列。
const agentTurnMigrationVersion int64 = 91

const agentTurnChecksum = "sha256:agent-service-turns-v91-20261106"

// agentServiceMigrations 由 migrations_creation_platform.go 的 init 接在平台迁移之后登记。
var agentServiceMigrations = []migration{
	{version: agentSessionsMigrationVersion, name: "agent_service_sessions", checksum: agentSessionsChecksum, apply: migrateAgentSessions},
	{version: agentContextMigrationVersion, name: "agent_service_context", checksum: agentContextChecksum, apply: migrateAgentContext},
	{version: platformBudgetMigrationVersion, name: "creation_platform_budget_grants", checksum: platformBudgetChecksum, apply: migratePlatformBudget},
	{version: agentWorkMigrationVersion, name: "agent_service_work", checksum: agentWorkChecksum, apply: migrateAgentWork},
	{version: agentApprovalMigrationVersion, name: "agent_service_approvals", checksum: agentApprovalChecksum, apply: migrateAgentApprovals},
	{version: agentTurnMigrationVersion, name: "agent_service_turns", checksum: agentTurnChecksum, apply: migrateAgentTurns},
}

// AgentServiceModels 是 Agent 服务的持久结构，顺序即迁移与复制顺序。
func AgentServiceModels() []any {
	return []any{&model.AgentSession{}, &model.AgentSessionMessage{}, &model.AgentMemoryNote{}, &model.AgentWorkItem{}, &model.AgentAssignment{}, &model.AgentWorkReport{}, &model.AgentWorkInput{}, &model.AgentOutbox{}, &model.AgentApproval{}, &model.AgentTurn{}}
}

func migrateAgentSessions(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.AgentSession{})
}

func migrateAgentContext(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.AgentSessionMessage{}, &model.AgentMemoryNote{})
}

func migratePlatformBudget(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.PlatformSubmission{}, &model.PlatformBudgetGrant{}, &model.PlatformBudgetCharge{})
}

// migrateAgentWork also adds the partial unique index that allows at most one
// live assignment per work item; SQLite and PostgreSQL both support it.
func migrateAgentWork(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&model.AgentWorkItem{}, &model.AgentAssignment{}, &model.AgentWorkReport{}, &model.AgentWorkInput{}, &model.AgentOutbox{}); err != nil {
		return err
	}
	return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_assignment_live ON agent_assignments (work_item_id) WHERE status <> 'ended'").Error
}

func migrateAgentApprovals(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.AgentApproval{})
}

func migrateAgentTurns(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.AgentTurn{})
}
