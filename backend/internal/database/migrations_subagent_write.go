package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// subagentWritePermissionsMigrationVersion 为子代理写入权限补齐两列：
// agent_subagent_policies.write_enabled（画布级写授权开关）与
// agent_subagent_links.write_granted（spawn 时定型的子代理写标记）。
// v82 及更早建的库没有这两列；不迁移会让 reconcile 在每次 Agent 工具推进时
// 因查询缺失列而失败，进而拖垮全部 Agent 工具调用。
const subagentWritePermissionsMigrationVersion int64 = 83

const subagentWritePermissionsChecksum = "sha256:fork-subagent-write-permissions-v83-20261103"

func migrateSubagentWritePermissions(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn(&model.AgentSubagentPolicy{}, "WriteEnabled") {
		if err := tx.Migrator().AddColumn(&model.AgentSubagentPolicy{}, "WriteEnabled"); err != nil {
			return err
		}
		// AddColumn 不带默认值：旧行回填 false（只读），保持升级前语义。
		if err := tx.Exec(`UPDATE agent_subagent_policies SET write_enabled = false WHERE write_enabled IS NULL`).Error; err != nil {
			return err
		}
	}
	if !tx.Migrator().HasColumn(&model.AgentSubagentLink{}, "WriteGranted") {
		if err := tx.Migrator().AddColumn(&model.AgentSubagentLink{}, "WriteGranted"); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE agent_subagent_links SET write_granted = false WHERE write_granted IS NULL`).Error; err != nil {
			return err
		}
	}
	return nil
}
