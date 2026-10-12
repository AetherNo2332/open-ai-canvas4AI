package repository

import (
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// Atomically compare the stored document to protect concurrent key changes.
func (r *Repository) SaveAgentWebSearchSetting(next *model.SystemSetting, previous string) error {
	if previous == "" {
		result := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(next)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
	} else {
		result := r.db.Model(&model.SystemSetting{}).Where("key = ? AND value_json = ?", next.Key, previous).Updates(map[string]any{"value_json": next.ValueJSON, "updated_by": next.UpdatedBy, "updated_at": next.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
	}
	return kernel.NewAppError(409, "联网搜索配置已修改，请重新读取后保存")
}
