package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// creationPlatformMigrationVersion 建立创作平台提交记录表（幂等键唯一约束 + 任务唯一绑定）。
const creationPlatformMigrationVersion int64 = 84

const creationPlatformChecksum = "sha256:creation-platform-submissions-v84-20261105"

func migrateCreationPlatform(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.PlatformSubmission{})
}
