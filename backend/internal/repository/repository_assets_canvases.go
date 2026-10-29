// 素材与画布项目的基础读写。
//
// 素材的彻底删除（含引用、Outbox）在 resource_reference.go 的 DeleteAssetsAndResources。

package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

func (r *Repository) Assets(userID string) ([]model.Asset, error) {
	var assets []model.Asset
	err := r.db.Order("updated_at desc").Find(&assets, "user_id = ?", userID).Error
	return assets, err
}

func (r *Repository) AssetSummaries(userID string) ([]model.Asset, error) {
	var assets []model.Asset
	err := r.db.Select("id", "folder_id", "kind", "category", "status", "primary_version_id", "title", "created_at", "updated_at").Order("updated_at desc").Find(&assets, "user_id = ?", userID).Error
	return assets, err
}

func (r *Repository) AssetForUser(userID string, id string) (*model.Asset, error) {
	var asset model.Asset
	if err := r.db.First(&asset, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *Repository) AssetsForUserIDs(userID string, ids []string) ([]model.Asset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var assets []model.Asset
	err := r.db.Find(&assets, "user_id = ? AND id IN ?", userID, ids).Error
	return assets, err
}

func (r *Repository) UpsertAsset(asset *model.Asset) error {
	result := r.db.Model(&model.Asset{}).
		Where("id = ? AND user_id = ?", asset.ID, asset.UserID).
		Updates(map[string]any{"folder_id": asset.FolderID, "kind": asset.Kind, "category": asset.Category, "status": asset.Status, "primary_version_id": asset.PrimaryVersionID, "title": asset.Title, "payload_json": asset.PayloadJSON, "updated_at": asset.UpdatedAt})
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	return r.db.Create(asset).Error
}

func (r *Repository) DeleteAsset(userID string, id string) error {
	return r.DeleteAssetAndResources(userID, id, nil, nil, false)
}

func (r *Repository) FindExpiredArchivedAssets(cutoff time.Time, limit int) ([]model.Asset, error) {
	var assets []model.Asset
	if limit <= 0 {
		limit = 100
	}
	err := r.db.Where("status = ? AND updated_at <= ?", model.AssetVersionStatusArchived, cutoff).
		Order("updated_at asc, id asc").
		Limit(limit).
		Find(&assets).Error
	return assets, err
}

func (r *Repository) ReplaceAssets(userID string, assets []model.Asset) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.Asset{}, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if len(assets) == 0 {
			return nil
		}
		return tx.Create(&assets).Error
	})
}

func (r *Repository) CanvasProjects(userID string) ([]model.CanvasProject, error) {
	var projects []model.CanvasProject
	err := r.db.Order("updated_at desc").Find(&projects, "user_id = ?", userID).Error
	return projects, err
}

func (r *Repository) CanvasProjectSummaries(userID string) ([]model.CanvasProject, error) {
	var projects []model.CanvasProject
	err := r.db.Select("id", "title", "revision", "created_at", "updated_at").Order("updated_at desc").Find(&projects, "user_id = ?", userID).Error
	return projects, err
}

func (r *Repository) CanvasProjectForUser(userID string, id string) (*model.CanvasProject, error) {
	var project model.CanvasProject
	if err := r.db.First(&project, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

func (r *Repository) UpsertCanvasProject(project *model.CanvasProject) error {
	return r.upsertCanvasProjectProjected(project, false)
}

// upsertCanvasProjectProjected 在同一事务里完成 revision CAS 与 Agent 命令事件投影；
// reset 为 true（历史恢复）时开启新的文档纪元。
func (r *Repository) upsertCanvasProjectProjected(project *model.CanvasProject, reset bool) error {
	return r.Transaction(func(tx *Repository) error {
		var before *model.CanvasProject
		if project.Revision > 0 {
			if err := tx.LockCanvasMutation(project.UserID, project.ID); err != nil {
				return err
			}
			current, err := tx.CanvasProjectForUser(project.UserID, project.ID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCanvasRevisionConflict
			}
			if err != nil {
				return err
			}
			if err := tx.checkCanvasEditLeasesFor(current, project, browserLeaseHolder); err != nil {
				return err
			}
			before = current
		}
		if err := tx.upsertCanvasProject(project); err != nil {
			return err
		}
		return tx.syncLegacyCanvasWrite(before, project, reset)
	})
}

func (r *Repository) upsertCanvasProject(project *model.CanvasProject) error {
	expected := project.Revision
	if expected < 0 {
		return ErrCanvasRevisionConflict
	}
	if expected == 0 {
		created := *project
		created.Revision = 1
		result := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&created)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCanvasRevisionConflict
		}
		project.Revision = 1
		r.canvasRevisionChanged(project.UserID, project.ID, project.Revision, CanvasChangeActorBrowser)
		return nil
	}
	// The revision predicate and increment must be in the same SQL statement.
	// A missing row is a conflict, never an invitation to recreate a deleted canvas.
	result := r.db.Model(&model.CanvasProject{}).
		Where("id = ? AND user_id = ? AND revision = ?", project.ID, project.UserID, expected).
		Updates(map[string]any{"project_id": project.ProjectID, "title": project.Title, "payload_json": project.PayloadJSON, "updated_at": project.UpdatedAt, "revision": expected + 1})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCanvasRevisionConflict
	}
	project.Revision = expected + 1
	r.canvasRevisionChanged(project.UserID, project.ID, project.Revision, CanvasChangeActorBrowser)
	return nil
}

func (r *Repository) DeleteCanvasProject(userID string, id string) error {
	return r.Transaction(func(repo *Repository) error {
		tx := repo.db
		// Serialize deletion with saves before reading the history IDs to remove.
		if err := tx.Model(&model.CanvasProject{}).Where("user_id = ? AND id = ?", userID, id).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
			return err
		}
		var snapshotIDs []string
		before, err := New(tx).CanvasProjectForUser(userID, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := New(tx).checkCanvasEditLeasesFor(before, nil, browserLeaseHolder); err != nil {
			return err
		}
		// 删除也投影为 Agent 命令流里的墓碑事件，让在途命令看到画布已删除。
		var document map[string]any
		if err := json.Unmarshal([]byte(before.PayloadJSON), &document); err != nil {
			return err
		}
		if document == nil {
			return fmt.Errorf("canvas document is null")
		}
		document["_deleted"] = true
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		tombstone := *before
		tombstone.Revision++
		tombstone.PayloadJSON = string(encoded)
		if err := repo.syncLegacyCanvasWrite(before, &tombstone, false); err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND canvas_id = ?", userID, id).Delete(&model.CanvasEditLease{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.CanvasSnapshot{}).Where("user_id = ? AND canvas_id = ?", userID, id).Pluck("id", &snapshotIDs).Error; err != nil {
			return err
		}
		if err := deleteCanvasSnapshots(tx, snapshotIDs); err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND project_id = ?", userID, id).Delete(&model.CanvasShare{}).Error; err != nil {
			return err
		}
		if err := tx.Where("canvas_id = ?", id).Delete(&model.CanvasUnitLink{}).Error; err != nil {
			return err
		}
		// 任务和会话是审计记录，不随独立画布实体保留归属 ID，避免删除后继续挂住画布上下文。
		if err := tx.Model(&model.Task{}).Where("user_id = ? AND project_id = ?", userID, id).Update("project_id", "").Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.CanvasProject{}, "id = ? AND user_id = ?", id, userID).Error; err != nil {
			return err
		}
		repo.canvasChanged(userID, id)
		return nil
	})
}
