package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

var ErrAssetAlreadyExists = errors.New("asset already exists")

// CreateAssetIfAbsent never writes an existing globally unique asset ID.
// Ownership/folder checks, quota and insert share the durable account lock.
func (r *Repository) CreateAssetIfAbsent(asset *model.Asset) error {
	return r.db.Transaction(func(db *gorm.DB) error {
		tx := r.transactionRepository(db)
		if err := tx.LockCanvasWrite(asset.UserID, ""); err != nil {
			return err
		}
		var count int64
		if err := db.Model(&model.Asset{}).Where("id = ?", asset.ID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrAssetAlreadyExists
		}
		if asset.FolderID != "" {
			if _, err := tx.AssetFolderForUser(asset.UserID, asset.FolderID); err != nil {
				return err
			}
		}
		if err := tx.checkAssetWriteQuota(nil, asset); err != nil {
			return err
		}
		result := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(asset)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAssetAlreadyExists
		}
		return nil
	})
}
