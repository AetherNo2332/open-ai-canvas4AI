package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// creationPlatformMigrationVersion 建立创作平台提交记录表（幂等键唯一约束 + 任务唯一绑定）。
const creationPlatformMigrationVersion int64 = 84

const creationPlatformChecksum = "sha256:creation-platform-submissions-v84-20261105"

// creationPlatformEventsMigrationVersion 建立平台任务事件表、游标计数器，以及在 tasks / billing_orders
// 状态变化的同一事务里写事件的触发器。
const creationPlatformEventsMigrationVersion int64 = 85

const creationPlatformEventsChecksum = "sha256:creation-platform-task-events-v85-20261106"

// creationPlatformMigrations 追加在 schemaMigrations 末尾；版本号沿用迁移序列，不挪用既有编号。
func init() { schemaMigrations = append(schemaMigrations, creationPlatformMigrations...) }

var creationPlatformMigrations = []migration{
	{version: creationPlatformMigrationVersion, name: "creation_platform_submissions", checksum: creationPlatformChecksum, apply: migrateCreationPlatform},
	{version: creationPlatformEventsMigrationVersion, name: "creation_platform_task_events", checksum: creationPlatformEventsChecksum, apply: migrateCreationPlatformEvents},
}

func migrateCreationPlatform(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.PlatformSubmission{})
}

func migrateCreationPlatformEvents(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&model.PlatformTaskEvent{}, &model.PlatformEventCounter{}); err != nil {
		return err
	}
	return ensurePlatformEventTriggers(tx)
}
