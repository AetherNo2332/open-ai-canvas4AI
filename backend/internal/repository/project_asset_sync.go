package repository

import (
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

var ErrProjectAssetSyncConflict = errors.New("project asset sync revision conflict")

// ProjectAssetSyncTransaction keeps the revision, relationship and summary in
// one snapshot. Writes reserve the revision before any relationship mutation;
// callback errors roll the reservation and all nested writes back together.
func (r *Repository) ProjectAssetSyncTransaction(userID, projectID, assetID string, expected *int64, fn func(*Repository, *model.Project, *model.Asset) error) error {
	options := &sql.TxOptions{}
	if expected == nil && r.Dialect() == "postgres" {
		options.Isolation = sql.LevelRepeatableRead
		options.ReadOnly = true
	}
	return r.db.Transaction(func(db *gorm.DB) error {
		tx := r.transactionRepository(db)
		if expected != nil {
			if err := tx.LockCanvasWrite(userID, ""); err != nil {
				return err
			}
		}
		project, err := tx.ProjectForUser(userID, projectID)
		if err != nil {
			return err
		}
		asset, err := tx.AssetForUser(userID, assetID)
		if err != nil {
			return err
		}
		if expected != nil {
			result := db.Model(&model.Project{}).Where("id = ? AND user_id = ? AND revision = ?", projectID, userID, *expected).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrProjectAssetSyncConflict
			}
		}
		return fn(tx, project, asset)
	}, options)
}

func (r *Repository) SetProjectAssetCategory(userID, projectID, assetID string, category model.AssetCategory, updatedAt time.Time) error {
	return r.db.Transaction(func(db *gorm.DB) error {
		tx := r.transactionRepository(db)
		if err := tx.LockCanvasWrite(userID, ""); err != nil {
			return err
		}
		if _, err := tx.ProjectForUser(userID, projectID); err != nil {
			return err
		}
		if err := tx.LockProjectAssetWrite(projectID); err != nil {
			return err
		}
		if _, err := tx.ProjectAssetLink(projectID, assetID); err != nil {
			return err
		}
		result := db.Model(&model.Asset{}).Where("id = ? AND user_id = ?", assetID, userID).Updates(map[string]any{"category": category, "updated_at": updatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.BumpProjectRevision(projectID)
	})
}
