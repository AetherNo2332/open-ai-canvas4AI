package database

import (
	"fmt"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// 111 = 用户自定义 submap 预制件表。纯新表，不改历史表或历史校验和。
// 文件名排在 migrations_zzz_agent_wake.go 之后，保证 init 追加后版本仍严格递增。
const userSubmapPresetMigrationVersion int64 = 111

const userSubmapPresetChecksum = "sha256:user-submap-preset-v111-20261123"

func init() { schemaMigrations = append(schemaMigrations, userSubmapPresetMigrations...) }

var userSubmapPresetMigrations = []migration{
	{
		version:  userSubmapPresetMigrationVersion,
		name:     "user_submap_preset",
		checksum: userSubmapPresetChecksum,
		apply:    migrateUserSubmapPreset,
	},
}

func migrateUserSubmapPreset(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&model.UserSubmapPreset{}); err != nil {
		return fmt.Errorf("创建用户自定义 submap 预制件表：%w", err)
	}
	return nil
}
