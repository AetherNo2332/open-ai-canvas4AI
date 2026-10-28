package repository

import (
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// LockProjectAssetWrite requires a transaction. The first statement acquires
// the account lock (and SQLite write reservation) without a preceding read;
// project/asset/link/folder locks must only be taken afterward.
func (r *Repository) LockProjectAssetWrite(projectID string) error {
	owner := r.db.Model(&model.Project{}).Select("user_id").Where("id = ?", projectID)
	if err := r.db.Model(&model.User{}).Where("id = (?)", owner).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
		return err
	}
	result := r.db.Model(&model.Project{}).Where("id = ?", projectID).UpdateColumn("revision", gorm.Expr("revision"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
