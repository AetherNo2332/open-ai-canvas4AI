package repository

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
)

type MediaVersionScope struct {
	OwnerID, CanvasID, Epoch, NodeID string
	Generation                       int64
}

func (r *Repository) mediaVersionScope(scope MediaVersionScope) *gorm.DB {
	return r.db.Model(&model.CanvasMediaVersion{}).Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND node_id = ? AND generation = ?", scope.OwnerID, scope.CanvasID, scope.Epoch, scope.NodeID, scope.Generation)
}
func (r *Repository) CanvasMediaVersion(scope MediaVersionScope, id string) (*model.CanvasMediaVersion, error) {
	var row model.CanvasMediaVersion
	err := r.mediaVersionScope(scope).Where("id = ?", id).First(&row).Error
	return &row, err
}
func (r *Repository) CanvasMediaVersionForTask(owner, taskID string) (*model.CanvasMediaVersion, error) {
	var row model.CanvasMediaVersion
	err := r.db.Where("owner_id = ? AND task_id = ?", owner, taskID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}
func (r *Repository) CanvasMediaVersions(scope MediaVersionScope, before int64, limit int) ([]model.CanvasMediaVersion, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	query := r.mediaVersionScope(scope)
	if before > 0 {
		query = query.Where("sequence < ?", before)
	}
	rows := []model.CanvasMediaVersion{}
	err := query.Order("sequence DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
func (r *Repository) CanvasMediaVersionSequence(scope MediaVersionScope) (int64, error) {
	var sequence int64
	err := r.mediaVersionScope(scope).Select("COALESCE(MAX(sequence),0)").Scan(&sequence).Error
	return sequence, err
}
func (r *Repository) PendingCanvasMediaVersion(scope MediaVersionScope) (*model.CanvasMediaVersion, error) {
	var row model.CanvasMediaVersion
	err := r.mediaVersionScope(scope).Where("status IN ?", []string{"queued", "running", "submission_uncertain", "materializing"}).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}
func mediaVersionBytes(v *model.CanvasMediaVersion) int64 {
	return int64(len(v.RequestJSON) + len(v.MediaJSON) + len(v.OutputsJSON) + len(v.Error))
}
func (r *Repository) InsertCanvasMediaVersion(row *model.CanvasMediaVersion) error {
	if r.structuredWriteQuota != nil {
		if err := r.structuredWriteQuota(r, row.OwnerID, "task", 0, mediaVersionBytes(row)); err != nil {
			return err
		}
	}
	return r.db.Create(row).Error
}
func (r *Repository) UpdateCanvasMediaVersion(row *model.CanvasMediaVersion) error {
	var before model.CanvasMediaVersion
	if err := r.db.Where("id = ? AND owner_id = ?", row.ID, row.OwnerID).First(&before).Error; err != nil {
		return err
	}
	if r.structuredWriteQuota != nil {
		if err := r.structuredWriteQuota(r, row.OwnerID, "task", 0, mediaVersionBytes(row)-mediaVersionBytes(&before)); err != nil {
			return err
		}
	}
	row.UpdatedAt = time.Now().UTC()
	return r.db.Model(&model.CanvasMediaVersion{}).Where("id = ? AND owner_id = ?", row.ID, row.OwnerID).
		Select("status", "media_json", "outputs_json", "resource_id", "adoption", "error", "updated_at").Updates(row).Error
}

type mediaVersionRoot struct {
	CanvasID, Epoch, NodeID string
	Generation              int64
}

// The ledger itself is not a permanent root: a live node or retained document
// must still expose its family. This protects every historical version of a
// recoverable family while letting ordinary resource cleanup reclaim orphans.
func (r *Repository) MediaVersionResourceReferences(owner string, ids []string) ([]ResourceDirectReference, error) {
	refs := []ResourceDirectReference{}
	if !r.mediaVersionsEnabled || len(ids) == 0 {
		return refs, nil
	}
	var rows []model.CanvasMediaVersion
	if err := r.db.Where("owner_id = ?", owner).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return refs, nil
	}
	roots := map[mediaVersionRoot]bool{}
	collect := func(canvas, raw string) error {
		if raw == "" {
			return nil
		}
		var doc struct {
			Nodes []struct {
				ID       string `json:"id"`
				Metadata struct {
					Version struct {
						Epoch      string `json:"epoch"`
						Generation int64  `json:"generation"`
					} `json:"mediaVersion"`
				} `json:"metadata"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return err
		}
		for _, node := range doc.Nodes {
			if node.Metadata.Version.Epoch != "" {
				roots[mediaVersionRoot{canvas, node.Metadata.Version.Epoch, node.ID, node.Metadata.Version.Generation}] = true
			}
		}
		return nil
	}
	var canvases []model.CanvasProject
	if err := r.db.Select("id", "payload_json").Where("user_id = ?", owner).Find(&canvases).Error; err != nil {
		return nil, err
	}
	for _, canvas := range canvases {
		if err := collect(canvas.ID, canvas.PayloadJSON); err != nil {
			return nil, err
		}
	}
	if r.db.Migrator().HasTable(&model.CanvasSnapshot{}) {
		var snapshots []model.CanvasSnapshot
		if err := r.db.Select("canvas_id", "payload_json").Where("user_id = ?", owner).Find(&snapshots).Error; err != nil {
			return nil, err
		}
		for _, snapshot := range snapshots {
			if err := collect(snapshot.CanvasID, snapshot.PayloadJSON); err != nil {
				return nil, err
			}
		}
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	for _, row := range rows {
		if !roots[mediaVersionRoot{row.CanvasID, row.Epoch, row.NodeID, row.Generation}] {
			continue
		}
		found := map[string]bool{}
		if row.ResourceID != "" {
			found[row.ResourceID] = true
		}
		for _, raw := range []string{row.RequestJSON, row.MediaJSON, row.OutputsJSON} {
			if raw == "" {
				continue
			}
			values, err := assets.CollectDocumentResourceReferences(raw)
			if err != nil {
				return nil, err
			}
			for _, value := range values {
				found[value.ResourceID] = true
			}
		}
		for id := range found {
			if wanted[id] {
				refs = append(refs, ResourceDirectReference{Kind: "画布媒体版本", ID: row.ID, Title: "保留的媒体版本", ResourceID: id})
			}
		}
	}
	return refs, nil
}
