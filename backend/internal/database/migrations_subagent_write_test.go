package database

import (
	"fmt"
	"testing"

	"yingce/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// v82 旧库升级：v83 前的两张表没有 write_enabled / write_granted 列，
// 迁移必须补齐且不破坏既有行；否则 reconcile 在每次 Agent 工具推进时
// 查询缺失列，拖垮全部 Agent 工具调用。
func TestMigrateSubagentWritePermissionsUpgradesLegacyTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	createLegacy := func(sql string) {
		t.Helper()
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 与 v78 建表时等形、但不含 v83 新列的旧表。
	createLegacy(`CREATE TABLE agent_subagent_policies (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		canvas_id TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT false,
		revision INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME
	)`)
	createLegacy(`CREATE TABLE agent_subagent_links (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		canvas_id TEXT NOT NULL,
		parent_run_id TEXT NOT NULL,
		child_run_id TEXT NOT NULL,
		task_id TEXT,
		idempotency_key TEXT NOT NULL,
		display_name TEXT NOT NULL,
		role_label TEXT NOT NULL,
		objective TEXT NOT NULL,
		instructions TEXT,
		depth INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL,
		budget_json TEXT,
		context_snapshot_json TEXT,
		created_at DATETIME,
		updated_at DATETIME
	)`)
	createLegacy(`INSERT INTO agent_subagent_policies (id, user_id, canvas_id, enabled, revision) VALUES ('p1', 'user', 'canvas-1', true, 3)`)
	createLegacy(`INSERT INTO agent_subagent_links (id, user_id, canvas_id, parent_run_id, child_run_id, idempotency_key, display_name, role_label, objective, depth, status) VALUES ('l1', 'user', 'canvas-1', 'parent', 'child', 'key-1', '写手', '写', '写分镜', 1, 'running')`)

	if err := migrateSubagentWritePermissions(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&model.AgentSubagentPolicy{}, "WriteEnabled") {
		t.Fatal("agent_subagent_policies.write_enabled missing after migration")
	}
	if !db.Migrator().HasColumn(&model.AgentSubagentLink{}, "WriteGranted") {
		t.Fatal("agent_subagent_links.write_granted missing after migration")
	}
	var policyTotal, policyBackfilled, linkTotal, linkBackfilled int64
	if err := db.Raw(`SELECT COUNT(*) FROM agent_subagent_policies`).Scan(&policyTotal).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM agent_subagent_policies WHERE write_enabled IS NOT NULL`).Scan(&policyBackfilled).Error; err != nil {
		t.Fatal(err)
	}
	if policyTotal != 1 || policyBackfilled != 1 {
		t.Fatalf("existing policy rows must survive with backfill: total=%d backfilled=%d", policyTotal, policyBackfilled)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM agent_subagent_links`).Scan(&linkTotal).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM agent_subagent_links WHERE write_granted IS NOT NULL`).Scan(&linkBackfilled).Error; err != nil {
		t.Fatal(err)
	}
	if linkTotal != 1 || linkBackfilled != 1 {
		t.Fatalf("existing link rows must survive with backfill: total=%d backfilled=%d", linkTotal, linkBackfilled)
	}
	// 幂等：重复执行不再报错。
	if err := migrateSubagentWritePermissions(db); err != nil {
		t.Fatalf("migration must be idempotent: %v", err)
	}
}
