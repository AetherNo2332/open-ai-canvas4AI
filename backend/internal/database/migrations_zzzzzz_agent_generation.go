package database

import (
	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// 生成批次续接：批次与任务明细持久化，并为会话队列增加内部消息种类。
// v112 由短剧工作台占用，本功能取 v113。
const agentGenerationContinuationVersion int64 = 113

func init() {
	schemaMigrations = append(schemaMigrations, migration{
		version:  agentGenerationContinuationVersion,
		name:     "agent_generation_continuation",
		checksum: "sha256:agent-generation-continuation-v113-20261126",
		apply: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.AgentGenerationBatch{}, &model.AgentGenerationBatchTask{}, &model.AgentQueuedMessage{})
		},
	})
}
