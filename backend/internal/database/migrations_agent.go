package database

import (
	"fmt"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// forkAgentRuntimeMigrationVersion 接在上游 v77 之后（v75–v77 留给上游迁移），承载 fork 云端 Agent 子系统的全部表：
// Pi 会话树、事件调度与准入、Worker 注册、技能默认与 Workspace、动态子代理、消息队列，
// 以及 Agent 画布命令/任务回写依赖的版本与绑定表。上游 v1–v77 保持原样。
const forkAgentRuntimeMigrationVersion int64 = 78

const forkAgentRuntimeChecksum = "sha256:fork-agent-runtime-v78-20261029"

// upstreamPiSessionLegacyTable 保存上游 Agent 运行时写下的 Pi 会话（run_id 主键 + JSONL 正文）。
// fork 的 Pi worker 使用按会话归属的条目树，两种格式不能共存在同一张表里；
// 旧表只改名留存，不删除数据，便于需要时人工核对或回滚。
const upstreamPiSessionLegacyTable = "cloud_agent_pi_sessions_upstream_legacy"

// AgentRuntimeModels 是 fork 云端 Agent 子系统的表清单；Models() 与 v78 迁移共用。
func AgentRuntimeModels() []any {
	return []any{
		&model.CloudAgentPiEntry{},
		&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.AgentUserAdmission{},
		&model.AgentRuntimeInstance{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{}, &model.AgentToolOperation{},
		&model.AgentWorker{},
		&model.AgentQueuedMessage{}, &model.AgentConversationQueue{}, &model.AgentUISetting{},
		&model.AgentSkillDefault{}, &model.AgentConversationSkill{},
		&model.AgentSubagentPolicy{}, &model.AgentSubagentLink{}, &model.AgentSubagentMessage{},
		&model.AgentWorkspace{}, &model.AgentWorkspaceSkill{},
		// Agent 画布命令与任务回写（canvas/commands、canvas/tasks、canvas/mediaversions）。
		&model.CanvasDocumentEpoch{}, &model.CanvasEntityVersion{}, &model.CanvasCommandReceipt{}, &model.CanvasCommandEvent{},
		&model.CanvasActorHistory{}, &model.CanvasCommandResource{}, &model.CanvasCreateReceipt{},
		&model.CanvasTaskBinding{}, &model.CanvasMaterializationJob{}, &model.CanvasTaskOutput{}, &model.CanvasTaskResource{},
		&model.CanvasMediaVersion{},
	}
}

func migrateForkAgentRuntime(tx *gorm.DB) error {
	if err := relocateUpstreamPiSessions(tx); err != nil {
		return err
	}
	models := append([]any{&model.CloudAgentExecution{}, &model.CloudAgentCanvasMutation{}, &model.CloudAgentPiSession{}}, AgentRuntimeModels()...)
	if err := tx.AutoMigrate(models...); err != nil {
		return err
	}
	// 没有就绪状态的旧心跳必须重新上报后才能领取工作。
	if err := tx.Model(&model.AgentRuntimeInstance{}).Where("readiness IS NULL OR readiness = ''").UpdateColumn("readiness", "starting").Error; err != nil {
		return err
	}
	return tx.Model(&model.AgentSchedulerSetting{}).
		Where("max_resident_per_user IS NULL OR max_resident_per_user = 0").
		Update("max_resident_per_user", model.DefaultAgentSchedulerSetting().MaxResidentPerUser).Error
}

// relocateUpstreamPiSessions 只在表仍是上游格式（含 session_jsonl 列）时改名，幂等。
func relocateUpstreamPiSessions(tx *gorm.DB) error {
	migrator := tx.Migrator()
	if !migrator.HasTable("cloud_agent_pi_sessions") || !migrator.HasColumn("cloud_agent_pi_sessions", "session_jsonl") {
		return nil
	}
	if migrator.HasTable(upstreamPiSessionLegacyTable) {
		return fmt.Errorf("上游 Pi 会话表需要改名为 %s，但该表已存在，请人工核对后再迁移", upstreamPiSessionLegacyTable)
	}
	if err := migrator.RenameTable("cloud_agent_pi_sessions", upstreamPiSessionLegacyTable); err != nil {
		return err
	}
	// 索引名随表保留；PostgreSQL 的索引名在 schema 内唯一，改名后要让出给新表。
	if tx.Dialector.Name() == "postgres" {
		return tx.Exec(`DO $$
DECLARE idx record;
BEGIN
  FOR idx IN SELECT indexname FROM pg_indexes WHERE tablename = '` + upstreamPiSessionLegacyTable + `' AND indexname LIKE 'idx_cloud_agent_pi_sessions_%' LOOP
    EXECUTE format('ALTER INDEX %I RENAME TO %I', idx.indexname, 'legacy_' || idx.indexname);
  END LOOP;
END $$`).Error
	}
	rows, err := tx.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name LIKE 'idx_cloud_agent_pi_sessions_%'", upstreamPiSessionLegacyTable).Rows()
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()
	// SQLite 不能给索引改名；旧表的索引只服务于留存数据，删掉即可让出名字。
	for _, name := range names {
		if err := tx.Exec(`DROP INDEX IF EXISTS "` + name + `"`).Error; err != nil {
			return err
		}
	}
	return nil
}

// MigrateCanvasCommands 只建立 Agent 画布命令所需的表，供只迁移局部表的测试夹具使用；
// 生产库由 v78 迁移统一建立。
func MigrateCanvasCommands(db *gorm.DB) error {
	return db.AutoMigrate(&model.CanvasDocumentEpoch{}, &model.CanvasEntityVersion{}, &model.CanvasCommandReceipt{}, &model.CanvasCommandEvent{}, &model.CanvasActorHistory{}, &model.CanvasCommandResource{}, &model.CanvasCreateReceipt{}, &model.CanvasSnapshot{}, &model.CanvasSnapshotResource{})
}

// MigrateCanvasTasks 只建立 Agent 画布任务回写所需的表，用途同 MigrateCanvasCommands。
func MigrateCanvasTasks(db *gorm.DB) error {
	return db.AutoMigrate(&model.CanvasTaskBinding{}, &model.CanvasMaterializationJob{}, &model.CanvasTaskOutput{}, &model.CanvasTaskResource{}, &model.CanvasMediaVersion{})
}
