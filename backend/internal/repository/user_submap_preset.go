package repository

import (
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

func (r *Repository) UserSubmapPresets(userID string) ([]model.UserSubmapPreset, error) {
	presets := []model.UserSubmapPreset{}
	err := r.db.Where("user_id = ?", userID).Order("updated_at desc").Find(&presets).Error
	return presets, err
}

func (r *Repository) UserSubmapPresetCount(userID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.UserSubmapPreset{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

func (r *Repository) UserSubmapPresetForUser(userID, id string) (*model.UserSubmapPreset, error) {
	var preset model.UserSubmapPreset
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&preset).Error; err != nil {
		return nil, err
	}
	return &preset, nil
}

func (r *Repository) CreateUserSubmapPreset(preset *model.UserSubmapPreset) error {
	return r.db.Create(preset).Error
}

func (r *Repository) UpdateUserSubmapPreset(preset *model.UserSubmapPreset) error {
	result := r.db.Model(&model.UserSubmapPreset{}).
		Where("id = ? AND user_id = ?", preset.ID, preset.UserID).
		Updates(map[string]any{
			"name":          preset.Name,
			"manifest_json": preset.ManifestJSON,
			"revision":      preset.Revision,
			"updated_at":    preset.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repository) DeleteUserSubmapPreset(userID, id string) error {
	result := r.db.Delete(&model.UserSubmapPreset{}, "id = ? AND user_id = ?", id, userID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
