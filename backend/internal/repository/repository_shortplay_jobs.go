package repository

import (
	"errors"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// 短剧 job 队列：入队去重靠 idx_shortplay_jobs_dedupe_active（只覆盖未结束行），
// 领取用条件更新防止两个 worker 拿到同一条，收口写状态、错误与游标。

// ActiveShortplayJob 返回该作品某类仍在途（queued/running）的 job；没有则 (nil,nil)。
func (r *Repository) ActiveShortplayJob(shortplayID, kind string) (*model.ShortplayJob, error) {
	var row model.ShortplayJob
	err := r.db.Where("shortplay_id = ? AND kind = ? AND status IN ?", shortplayID, kind, shortplay.UnfinishedJobStatuses()).
		Order("created_at asc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// LatestShortplayJob 返回该作品某类、某状态的最近一条 job（按创建时间倒序）；没有则 (nil,nil)。
func (r *Repository) LatestShortplayJob(shortplayID, kind, status string) (*model.ShortplayJob, error) {
	var row model.ShortplayJob
	err := r.db.Where("shortplay_id = ? AND kind = ? AND status = ?", shortplayID, kind, status).
		Order("created_at desc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateShortplayJob 入队一条 job。未结束行的 (dedupe_key) 唯一索引冲突说明已有同键在途，
// 调用方应改为复用 ActiveShortplayJob 的那一条。
func (r *Repository) CreateShortplayJob(row *model.ShortplayJob) error {
	return r.db.Create(row).Error
}

// ClaimShortplayJob 领取一条指定 kind 的最早 queued job：先选后条件更新，
// 只有把 status 从 queued 改成 running 成功的那一方拿到它。没有可领取的返回 (nil,nil)。
func (r *Repository) ClaimShortplayJob(kind string) (*model.ShortplayJob, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var candidate model.ShortplayJob
		err := r.db.Where("kind = ? AND status = ?", kind, shortplay.JobQueued).Order("created_at asc").First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		now := time.Now()
		result := r.db.Model(&model.ShortplayJob{}).
			Where("id = ? AND status = ?", candidate.ID, shortplay.JobQueued).
			Updates(map[string]any{
				"status":     shortplay.JobRunning,
				"attempt":    gorm.Expr("attempt + 1"),
				"started_at": now,
				"updated_at": now,
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 1 {
			candidate.Status = shortplay.JobRunning
			candidate.Attempt++
			candidate.StartedAt = &now
			return &candidate, nil
		}
	}
	return nil, nil
}

// FinishShortplayJob 收口一条 job。status 只接受 succeeded/failed/canceled；
// lastError 截断到 500 字（列宽），payloadJSON 非空时覆盖游标等结果负载。
func (r *Repository) FinishShortplayJob(id, status, lastError, payloadJSON string) error {
	now := time.Now()
	updates := map[string]any{
		"status":      status,
		"last_error":  truncateShortplayError(lastError),
		"finished_at": now,
		"updated_at":  now,
	}
	if payloadJSON != "" {
		updates["payload_json"] = payloadJSON
	}
	return r.db.Model(&model.ShortplayJob{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateShortplayEpisodeAssetIDs 回写某一集命中的资产序号列表（assetIdsJson）。
func (r *Repository) UpdateShortplayEpisodeAssetIDs(episodeID, assetIDsJSON string) error {
	return r.db.Model(&model.ShortplayEpisode{}).Where("id = ?", episodeID).
		Updates(map[string]any{"asset_ids_json": assetIDsJSON, "updated_at": time.Now()}).Error
}

func truncateShortplayError(message string) string {
	runes := []rune(message)
	if len(runes) <= 500 {
		return message
	}
	return string(runes[:500])
}
