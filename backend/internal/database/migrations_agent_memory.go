package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// migrateAgentMemoryDocumentColumns 补齐记忆文件的条目序号与修订的工具调用 ID。
// 早期构建执行 v76 时这两列还不存在，v76 已记为完成不会重跑；新库由 v76 建全，这里是空操作。
func migrateAgentMemoryDocumentColumns(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&model.AgentMemoryDocument{}, &model.AgentMemoryDocumentRevision{}); err != nil {
		return err
	}
	// 新增列对旧行是 NULL；写成零值，与新建行一致。
	if err := tx.Model(&model.AgentMemoryDocument{}).Where("next_entry_seq IS NULL").Update("next_entry_seq", 0).Error; err != nil {
		return err
	}
	return tx.Model(&model.AgentMemoryDocumentRevision{}).Where("call_id IS NULL").Update("call_id", "").Error
}

// migrateAgentMemoryDocumentSkillDiscovery 新建用户记忆文件及其修订表，并给用户技能状态补
// Agent 自动发现模式与读取统计列。旧状态 agent_mode 为空，按 auto 处理，行为与“已安装即可发现”一致。
func migrateAgentMemoryDocumentSkillDiscovery(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&model.AgentMemoryDocument{}, &model.AgentMemoryDocumentRevision{}); err != nil {
		return err
	}
	for _, field := range []string{"AgentMode", "AgentUseCount", "AgentLastUsedAt"} {
		if tx.Migrator().HasColumn(&model.UserSkillState{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&model.UserSkillState{}, field); err != nil {
			return err
		}
	}
	return nil
}
