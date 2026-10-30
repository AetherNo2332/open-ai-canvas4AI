package database_test

import (
	"fmt"
	"testing"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestAgentRuntimeReadinessUpgradeFromOldTable(t *testing.T) {
	// 只有本仓库 Agent 迁移（v78）建立该表；旧形状的表升级时必须补就绪列并要求重新上报。
	for _, version := range []int{74} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			db, err := database.Open(database.Config{Driver: "sqlite", DSN: fmt.Sprintf("file:runtime-readiness-v%d?mode=memory&cache=shared", version)})
			if err != nil {
				t.Fatal(err)
			}
			if err := database.MigrateSchema(db); err != nil {
				t.Fatal(err)
			}
			// Recreate this one table from the persisted v59/v61 shape. AutoMigrate
			// against today's model would hide the missing forward migration.
			if err := db.Migrator().DropTable(&model.AgentRuntimeInstance{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`CREATE TABLE agent_runtime_instances (
				id text PRIMARY KEY, active integer, capacity integer,
				claim_reservations integer, dispatch_active integer, ready_queued integer,
				draining numeric, applied_config_revision integer, updated_at datetime
			)`).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("CREATE INDEX idx_agent_runtime_instances_updated_at ON agent_runtime_instances(updated_at)").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`INSERT INTO agent_runtime_instances
				(id, active, capacity, claim_reservations, dispatch_active, ready_queued, draining, applied_config_revision, updated_at)
				VALUES ('old-worker', 0, 2, 0, 0, 0, 0, 0, '2000-01-01 00:00:00')`).Error; err != nil {
				t.Fatal(err)
			}
			if db.Migrator().HasColumn(&model.AgentRuntimeInstance{}, "Readiness") || db.Migrator().HasColumn(&model.AgentRuntimeInstance{}, "ReadinessReason") {
				t.Fatal("fixture must have neither readiness column before upgrade")
			}
			if err := db.Exec("DELETE FROM schema_migrations WHERE version > ?", version).Error; err != nil {
				t.Fatal(err)
			}
			if err := database.MigrateSchema(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn(&model.AgentRuntimeInstance{}, "Readiness") || !db.Migrator().HasColumn(&model.AgentRuntimeInstance{}, "ReadinessReason") {
				t.Fatal("fork Agent runtime migration did not add both readiness columns")
			}
			var old model.AgentRuntimeInstance
			if err := db.First(&old, "id = ?", "old-worker").Error; err != nil {
				t.Fatal(err)
			}
			if old.Readiness != "starting" || old.ReadinessReason != "" || old.UpdatedAt.Year() != 2000 {
				t.Fatalf("old worker became ready without a report: %+v", old)
			}
			if err := repository.New(db).AgentCapacityReport("new-worker", model.AgentCapacityReport{
				Capacity: 2, Readiness: "ready", ReadinessReason: "capacity available",
			}); err != nil {
				t.Fatalf("capacity report after upgrade: %v", err)
			}
			var fresh model.AgentRuntimeInstance
			if err := db.First(&fresh, "id = ?", "new-worker").Error; err != nil {
				t.Fatal(err)
			}
			if fresh.Readiness != "ready" || fresh.ReadinessReason != "capacity available" {
				t.Fatalf("new readiness report not persisted: %+v", fresh)
			}
		})
	}
}
