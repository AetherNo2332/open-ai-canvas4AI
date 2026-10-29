package repository

import (
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// v77 迁移在 database 包内改写画布，不能 import 本包；这里注册投影，让迁移在同一事务里
// 写出 Agent 命令事件与实体版本。直接构造只绑定迁移事务的 Repository：不开新事务、
// 不安装 Agent 事件回调，也没有画布变更通知器。
func init() {
	database.RegisterCanvasWriteProjector(func(tx *gorm.DB, before, after *model.CanvasProject) error {
		repo := &Repository{db: tx, mediaVersionsEnabled: tx.Migrator().HasTable(&model.CanvasMediaVersion{})}
		return repo.syncLegacyCanvasWrite(before, after, false)
	})
}
