package main

import "yingce/backend/internal/model"

// creationPlatformTableMigrations 是创作平台的表，依赖 tasks 与 billing_orders，放在迁移清单末尾。
func creationPlatformTableMigrations() []tableMigration {
	return []tableMigration{
		migrateTable[model.PlatformSubmission]("platform_submissions"),
		migrateTable[model.PlatformTaskEvent]("platform_task_events"),
		migrateTable[model.PlatformEventCounter]("platform_event_counters"),
		migrateTable[model.PlatformBudgetGrant]("platform_budget_grants"),
		migrateTable[model.PlatformBudgetCharge]("platform_budget_charges"),
	}
}

// agentServiceTableMigrations 是 Agent 服务的表（schema v86 起），放在平台表之后。
func agentServiceTableMigrations() []tableMigration {
	return []tableMigration{
		migrateTable[model.AgentSession]("agent_sessions"),
		migrateTable[model.AgentSessionMessage]("agent_session_messages"),
		migrateTable[model.AgentMemoryNote]("agent_memory_notes"),
	}
}
