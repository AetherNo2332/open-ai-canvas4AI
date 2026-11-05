package main

import "yingce/backend/internal/model"

// creationPlatformTableMigrations 是创作平台的表，依赖 tasks 与 billing_orders，放在迁移清单末尾。
func creationPlatformTableMigrations() []tableMigration {
	return []tableMigration{
		migrateTable[model.PlatformSubmission]("platform_submissions"),
		migrateTable[model.PlatformTaskEvent]("platform_task_events"),
		migrateTable[model.PlatformEventCounter]("platform_event_counters"),
	}
}
