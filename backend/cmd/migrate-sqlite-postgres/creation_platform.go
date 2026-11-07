package main

import (
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
)

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
	byName := make(map[string]tableMigration)
	for _, migration := range agentServiceTableMigrationDefinitions() {
		byName[migration.name] = migration
	}
	contractNames := make(map[string]bool)
	for _, contract := range database.AgentServiceMigrationContract() {
		contractNames[contract.Name] = true
	}
	for name := range byName {
		if !contractNames[name] {
			panic("agent copier table is missing from migration contract: " + name)
		}
	}
	ordered := make([]tableMigration, 0, len(byName))
	for _, contract := range database.AgentServiceMigrationContract() {
		migration, ok := byName[contract.Name]
		if !ok {
			// Existing Cloud Agent/Pi tables are registered in main.go's
			// application list; this function owns only schema v86+ tables.
			continue
		}
		ordered = append(ordered, migration)
	}
	return ordered
}

func agentServiceTableMigrationDefinitions() []tableMigration {
	return []tableMigration{
		migrateTable[model.AgentSession]("agent_sessions"),
		migrateTable[model.AgentSessionMessage]("agent_session_messages"),
		migrateTable[model.AgentMemoryNote]("agent_memory_notes"),
		migrateTable[model.AgentWorkItem]("agent_work_items"),
		migrateTable[model.AgentAssignment]("agent_assignments"),
		migrateTable[model.AgentWorkReport]("agent_work_reports"),
		migrateTable[model.AgentWorkInput]("agent_work_inputs"),
		migrateTable[model.AgentOutbox]("agent_outboxes"),
		migrateTable[model.AgentApproval]("agent_approvals"),
		migrateTable[model.AgentTurn]("agent_turns"),
		migrateTable[model.AgentOperation]("agent_operations"),
		migrateTable[model.AgentTurnContract]("agent_turn_contracts"),
		migrateTable[model.AgentEcommerceBatch]("agent_ecommerce_batches"),
	}
}
