package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"time"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// UpdateDramaProduction 写入用户侧控制字段，并递增版本。
func (r *Repository) UpdateDramaProduction(projectID string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	fields["revision"] = gorm.Expr("revision + 1")
	return r.db.Model(&model.DramaProduction{}).Where("project_id = ?", projectID).Updates(fields).Error
}

// SaveDramaProductionProgress 写入调度循环负责的进度字段；状态只在仍处于 expectedStatus 时改变。
func (r *Repository) SaveDramaProductionProgress(production *model.DramaProduction, expectedStatus string, statusChanged bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&model.DramaProduction{}).Where("project_id = ?", production.ProjectID).Updates(map[string]any{
			"stage": production.Stage, "bible_json": production.BibleJSON, "spent_microcredits": production.SpentMicrocredits,
			"plan_done": production.PlanDone, "last_tick_at": production.LastTickAt, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if !statusChanged {
			return nil
		}
		return tx.Model(&model.DramaProduction{}).Where("project_id = ? AND status = ?", production.ProjectID, expectedStatus).
			Updates(map[string]any{"status": production.Status, "pause_reason": production.PauseReason}).Error
	})
}

// UpdateDramaEpisodeIfRevision 只在集未被用户改动（版本未变）时写入调度结果。
func (r *Repository) UpdateDramaEpisodeIfRevision(id string, revision int64, fields map[string]any) (bool, error) {
	fields["updated_at"] = time.Now()
	result := r.db.Model(&model.DramaEpisode{}).Where("id = ? AND revision = ?", id, revision).Updates(fields)
	return result.RowsAffected == 1, result.Error
}

// UpdateDramaEpisodeUser 写入用户修改并递增版本，使调度循环基于旧版本的写入失效。
func (r *Repository) UpdateDramaEpisodeUser(id string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	fields["revision"] = gorm.Expr("revision + 1")
	return r.db.Model(&model.DramaEpisode{}).Where("id = ?", id).Updates(fields).Error
}

// UpdateDramaShotIf 只在槽位状态仍为 allowed 之一时写入，防止旧任务结果覆盖用户刚发起的重做。

func (r *Repository) UpdateDramaCastIf(id string, allowed []string, fields map[string]any) (bool, error) {
	fields["updated_at"] = time.Now()
	result := r.db.Model(&model.DramaCast{}).Where("id = ? AND image_status IN ?", id, allowed).Updates(fields)
	return result.RowsAffected == 1, result.Error
}

// ApplyDramaScript 写入一集的新镜头；revision 不为 nil 时要求集版本未变。
func (r *Repository) ApplyDramaScript(episodeID string, revision *int64, fields map[string]any, shots []model.DramaShot) (bool, error) {
	applied := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		fields["updated_at"] = time.Now()
		query := tx.Model(&model.DramaEpisode{}).Where("id = ?", episodeID)
		if revision != nil {
			query = query.Where("revision = ?", *revision)
		} else {
			fields["revision"] = gorm.Expr("revision + 1")
		}
		result := query.Updates(fields)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Where("episode_id = ?", episodeID).Delete(&model.DramaShot{}).Error; err != nil {
			return err
		}
		// 剧情段随镜头重新切分，旧段的提示词不再对得上，重建为待撰写。
		if err := tx.Where("episode_id = ?", episodeID).Delete(&model.DramaSegment{}).Error; err != nil {
			return err
		}
		if len(shots) > 0 {
			if err := tx.CreateInBatches(&shots, 100).Error; err != nil {
				return err
			}
			segments := []model.DramaSegment{}
			seen := map[int]bool{}
			now := time.Now()
			for _, shot := range shots {
				if !seen[shot.SegmentIndex] {
					seen[shot.SegmentIndex] = true
					segments = append(segments, model.DramaSegment{ID: kernel.NewID(), ProjectID: shot.ProjectID, EpisodeID: episodeID, SegmentIndex: shot.SegmentIndex, CreatedAt: now, UpdatedAt: now})
				}
			}
			if err := tx.Create(&segments).Error; err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	return applied, err
}

func (r *Repository) UpdateDramaCastFields(id string, fields map[string]any) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		fields["updated_at"] = time.Now()
		fields["revision"] = gorm.Expr("revision + 1")
		if err := tx.Model(&model.DramaCast{}).Where("id = ?", id).Updates(fields).Error; err != nil {
			return err
		}
		// Textual cast changes invalidate prompt inputs. Lock-only edits do not change media.
		changed := false
		for _, key := range []string{"name", "appearance", "description", "voice_instructions", "aliases_json"} {
			if _, ok := fields[key]; ok {
				changed = true
			}
		}
		if !changed {
			return nil
		}
		var cast model.DramaCast
		if err := tx.First(&cast, "id = ?", id).Error; err != nil {
			return err
		}
		var shots []model.DramaShot
		if err := tx.Where("project_id = ? AND (scene_cast_id = ? OR cast_ids_json LIKE ?)", cast.ProjectID, id, fmt.Sprintf("%%%q%%", id)).Find(&shots).Error; err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, shot := range shots {
			key := shot.EpisodeID + ":" + fmt.Sprint(shot.SegmentIndex)
			if seen[key] {
				continue
			}
			seen[key] = true
			var seg model.DramaSegment
			if err := tx.Where("episode_id = ? AND segment_index = ?", shot.EpisodeID, shot.SegmentIndex).First(&seg).Error; err != nil {
				return err
			}
			if err := invalidateDramaSegment(tx, seg, map[string]any{"prompt_status": promptStatusAfterInputChange(seg.PromptStatus), "video_prompt_override": ""}, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// promptStatusAfterInputChange 决定撰写素材变化后的提示词状态：写好的才标记过期、等用户确认重写；
// 待撰写或撰写中的退回待撰写，由调度按新素材自动写；失败保持失败，等用户重试。
func promptStatusAfterInputChange(current string) string {
	switch current {
	case model.DramaMediaReady, model.DramaMediaStale:
		return model.DramaMediaStale
	case model.DramaMediaFailed:
		return model.DramaMediaFailed
	}
	return ""
}
func (r *Repository) UpdateDramaCastRevision(id string, revision int64, fields map[string]any) (bool, error) {
	fields["updated_at"] = time.Now()
	result := r.db.Model(&model.DramaCast{}).Where("id = ? AND revision = ?", id, revision).Updates(fields)
	return result.RowsAffected > 0, result.Error
}

// UpdateDramaEpisodeMeta 修改不影响已生成内容的集字段（标题、素材类型），不递增版本，避免打断调度写入。
func (r *Repository) UpdateDramaEpisodeMeta(episodeID string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.Model(&model.DramaEpisode{}).Where("id = ?", episodeID).Updates(fields).Error
}

func (r *Repository) DramaSegments(episodeID string) ([]model.DramaSegment, error) {
	var segments []model.DramaSegment
	err := r.db.Where("episode_id = ?", episodeID).Order("segment_index asc").Find(&segments).Error
	return segments, err
}

// SaveDramaSegmentOverride 写入或清除段级提示词改写；行不存在时创建（旧剧本没有段记录）。
func (r *Repository) SaveDramaSegmentOverride(segment model.DramaSegment) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current model.DramaSegment
		if err := tx.Where("episode_id = ? AND segment_index = ?", segment.EpisodeID, segment.SegmentIndex).First(&current).Error; err != nil {
			return err
		}
		return invalidateDramaSegment(tx, current, map[string]any{"video_prompt_override": segment.VideoPromptOverride}, true)
	})
}

// SaveDramaSegmentReferenceFields 一次保存图片与音色选择，并使在途作业按修订号失效。
func (r *Repository) SaveDramaSegmentReferenceFields(episodeID string, index int, fields map[string]any) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current model.DramaSegment
		if err := tx.Where("episode_id = ? AND segment_index = ?", episodeID, index).First(&current).Error; err != nil {
			return err
		}
		values := map[string]string{"reference_cast_ids_json": current.ReferenceCastIDsJSON, "reference_images_json": current.ReferenceImagesJSON, "audio_cast_ids_json": current.AudioCastIDsJSON}
		changed := false
		for key, value := range fields {
			if values[key] != value {
				changed = true
			}
		}
		if !changed {
			return nil
		}
		fields["prompt_status"] = promptStatusAfterInputChange(current.PromptStatus)
		return invalidateDramaSegment(tx, current, fields, true)
	})
}

// RequestDramaSegmentPrompts 把指定段标记为待撰写；正在撰写的段保持不动。返回实际排队的段数。
func (r *Repository) RequestDramaSegmentPrompts(projectID, episodeID string, indexes []int) (int, error) {
	queued := 0
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for _, index := range indexes {
			var segment model.DramaSegment
			if err := tx.Where("project_id = ? AND episode_id = ? AND segment_index = ?", projectID, episodeID, index).First(&segment).Error; err != nil {
				return err
			}
			if segment.PromptStatus == model.DramaMediaRunning {
				continue
			}
			if err := invalidateDramaSegment(tx, segment, map[string]any{"prompt_status": "", "prompt_error": ""}, true); err != nil {
				return err
			}
			queued++
		}
		return nil
	})
	return queued, err
}

// PendingDramaSegments 返回待撰写提示词的段；按集与段序排列。
func (r *Repository) PendingDramaSegments(projectID string) ([]model.DramaSegment, error) {
	var segments []model.DramaSegment
	err := r.db.Where("project_id = ? AND prompt_status = ?", projectID, "").Order("episode_id asc, segment_index asc").Limit(500).Find(&segments).Error
	return segments, err
}

func (r *Repository) DramaSegment(projectID string, id string) (*model.DramaSegment, error) {
	var segment model.DramaSegment
	if err := r.db.First(&segment, "id = ? AND project_id = ?", id, projectID).Error; err != nil {
		return nil, err
	}
	return &segment, nil
}

// MarkDramaSegmentVideoStale 在参考图重做后让该段已生成或生成中的视频过期。提示词只引用参考图编号，
// 不含图片内容，因此保持不变；尚未生成的视频提交时会读取新参考图，无需处理。返回是否有视频被标记。
func (r *Repository) MarkDramaSegmentVideoStale(episodeID string, index int) (bool, error) {
	marked := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var segment model.DramaSegment
		if err := tx.Where("episode_id = ? AND segment_index = ?", episodeID, index).First(&segment).Error; err != nil {
			return err
		}
		if segment.VideoStatus != model.DramaMediaReady && segment.VideoStatus != model.DramaMediaRunning {
			return nil
		}
		fields := map[string]any{"video_status": model.DramaMediaStale, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}
		// 版本递增会让撰写中的提示词结果作废，退回待撰写由调度重写。
		if segment.PromptStatus == model.DramaMediaRunning {
			fields["prompt_status"] = ""
		}
		if err := tx.Model(&model.DramaSegment{}).Where("id = ?", segment.ID).Updates(fields).Error; err != nil {
			return err
		}
		marked = true
		return markLaterSegmentsStale(tx, segment.EpisodeID, segment.SegmentIndex)
	})
	return marked, err
}

// DeleteDramaCastLook 删除一套造型，并把引用它的镜头回退到角色本体（从 look_cast_ids_json 里移除）。
// 段级手动参考中的造型同步回退到角色本体，重新编号后提示词与视频过期。
func (r *Repository) DeleteDramaCastLook(projectID, lookID string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var look model.DramaCast
		if err := tx.First(&look, "id = ? AND project_id = ? AND parent_id <> ''", lookID, projectID).Error; err != nil {
			return err
		}
		var shots []model.DramaShot
		if err := tx.Where("project_id = ? AND look_cast_ids_json LIKE ?", projectID, "%\""+lookID+"\"%").Find(&shots).Error; err != nil {
			return err
		}
		for _, shot := range shots {
			var ids []string
			_ = json.Unmarshal([]byte(shot.LookCastIDsJSON), &ids)
			kept := make([]string, 0, len(ids))
			for _, id := range ids {
				if id != lookID {
					kept = append(kept, id)
				}
			}
			encoded, _ := json.Marshal(kept)
			if err := tx.Model(&model.DramaShot{}).Where("id = ?", shot.ID).Update("look_cast_ids_json", string(encoded)).Error; err != nil {
				return err
			}
		}
		var segments []model.DramaSegment
		if err := tx.Where("project_id = ? AND reference_cast_ids_json LIKE ?", projectID, "%\""+lookID+"\"%").Find(&segments).Error; err != nil {
			return err
		}
		for _, segment := range segments {
			var ids []string
			if err := json.Unmarshal([]byte(segment.ReferenceCastIDsJSON), &ids); err != nil {
				return err
			}
			kept := []string{}
			seen := map[string]bool{}
			for _, id := range ids {
				if id == lookID {
					id = look.ParentID
				}
				if !seen[id] {
					kept = append(kept, id)
					seen[id] = true
				}
			}
			encoded, err := json.Marshal(kept)
			if err != nil {
				return err
			}
			if err := invalidateDramaSegment(tx, segment, map[string]any{"reference_cast_ids_json": string(encoded), "prompt_status": promptStatusAfterInputChange(segment.PromptStatus)}, true); err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND project_id = ?", lookID, projectID).Delete(&model.DramaCast{}).Error
	})
}

func (r *Repository) DramaShotsReferencingCast(projectID string, castID string) ([]model.DramaShot, error) {
	var shots []model.DramaShot
	pattern := "%\"" + castID + "\"%"
	segments := r.db.Model(&model.DramaSegment{}).Select("1").Where("drama_segments.episode_id = drama_shots.episode_id AND drama_segments.segment_index = drama_shots.segment_index AND (reference_cast_ids_json LIKE ? OR audio_cast_ids_json LIKE ?)", pattern, pattern)
	err := r.db.Where("project_id = ? AND (scene_cast_id = ? OR cast_ids_json LIKE ? OR prop_ids_json LIKE ? OR look_cast_ids_json LIKE ? OR EXISTS (?))", projectID, castID, pattern, pattern, pattern, segments).Find(&shots).Error
	return shots, err
}

// DramaSegmentStat 是集列表进度使用的剧情段聚合计数；VideoGenerated 是持有视频的段（含已过期的旧视频）。
type DramaSegmentStat struct {
	EpisodeID      string
	Total          int64
	PromptReady    int64
	VideoReady     int64
	VideoGenerated int64
	Failed         int64
}

func (r *Repository) DramaSegmentStats(projectID string) (map[string]DramaSegmentStat, error) {
	var rows []DramaSegmentStat
	err := r.db.Model(&model.DramaSegment{}).Select(`episode_id,
		COUNT(*) AS total,
		SUM(CASE WHEN (prompt_status = 'ready' OR video_prompt_override <> '') THEN 1 ELSE 0 END) AS prompt_ready,
		SUM(CASE WHEN video_status = 'ready' THEN 1 ELSE 0 END) AS video_ready,
		SUM(CASE WHEN video_resource_id <> '' THEN 1 ELSE 0 END) AS video_generated,
		SUM(CASE WHEN prompt_status = 'failed' OR video_status = 'failed' THEN 1 ELSE 0 END) AS failed`).
		Where("project_id = ?", projectID).Group("episode_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	stats := make(map[string]DramaSegmentStat, len(rows))
	for _, row := range rows {
		stats[row.EpisodeID] = row
	}
	return stats, nil
}

// FailedDramaSegments 返回提示词或视频失败的剧情段，供待处理与一键重试使用。
func (r *Repository) FailedDramaSegments(projectID string, limit int) ([]model.DramaSegment, error) {
	var segments []model.DramaSegment
	err := r.db.Where("project_id = ? AND (prompt_status = ? OR video_status = ?)", projectID, model.DramaMediaFailed, model.DramaMediaFailed).
		Order("updated_at desc").Limit(limit).Find(&segments).Error
	return segments, err
}

func (r *Repository) UpdateDramaSegmentFields(id string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.Model(&model.DramaSegment{}).Where("id = ?", id).Updates(fields).Error
}

func (r *Repository) UpdateDramaSegmentRevision(id string, revision int64, fields map[string]any) (bool, error) {
	fields["updated_at"] = time.Now()
	result := r.db.Model(&model.DramaSegment{}).Where("id = ? AND revision = ?", id, revision).Updates(fields)
	return result.RowsAffected > 0, result.Error
}

// SetDramaSegmentTail 写入后台抽好的尾帧：只在段版本未变、段视频仍是抽取的那一条且尚无尾帧时生效，
// 迟到的旧视频尾帧不会落到新视频上。
func (r *Repository) SetDramaSegmentTail(id string, revision int64, videoResourceID string, tailResourceID string) (bool, error) {
	result := r.db.Model(&model.DramaSegment{}).
		Where("id = ? AND revision = ? AND video_resource_id = ? AND tail_frame_resource_id = ''", id, revision, videoResourceID).
		Updates(map[string]any{"tail_frame_resource_id": tailResourceID, "updated_at": time.Now()})
	return result.RowsAffected > 0, result.Error
}

func markLaterSegmentsStale(tx *gorm.DB, episodeID string, index int) error {
	// Queued descendants remain requested, but receive a new revision and wait for the new tail.
	// The new revision also voids running prompt jobs, so those segments go back to pending instead of staying "running".
	return tx.Model(&model.DramaSegment{}).Where("episode_id = ? AND segment_index > ?", episodeID, index).
		Updates(map[string]any{
			"video_status":  gorm.Expr("CASE WHEN video_status IN ? THEN ? ELSE video_status END", []string{"ready", "running"}, "stale"),
			"prompt_status": gorm.Expr("CASE WHEN prompt_status = ? THEN '' ELSE prompt_status END", model.DramaMediaRunning),
			"revision":      gorm.Expr("revision + 1"),
			"updated_at":    time.Now(),
		}).Error
}

// invalidateDramaSegment makes user edits and downstream invalidation one transaction.
func invalidateDramaSegment(tx *gorm.DB, segment model.DramaSegment, fields map[string]any, prompts bool) error {
	if _, ok := fields["video_status"]; !ok {
		if segment.VideoStatus != "" {
			fields["video_status"] = model.DramaMediaStale
		}
	}
	fields["revision"] = gorm.Expr("revision + 1")
	fields["updated_at"] = time.Now()
	if err := tx.Model(&model.DramaSegment{}).Where("id = ?", segment.ID).Updates(fields).Error; err != nil {
		return err
	}
	if err := markLaterSegmentsStale(tx, segment.EpisodeID, segment.SegmentIndex); err != nil {
		return err
	}
	stage := "video"
	if prompts {
		stage = "prompts"
	}
	if err := tx.Model(&model.DramaEpisode{}).Where("id = ?", segment.EpisodeID).Updates(map[string]any{"stage": stage, "approved": false, "final_stale": true, "error": "", "revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error; err != nil {
		return err
	}
	return tx.Model(&model.DramaJob{}).Where("episode_id = ? AND status = ? AND kind IN ?", segment.EpisodeID, "queued", []string{"segment_prompt", "segment_video", "render"}).Updates(map[string]any{"status": "canceled", "updated_at": time.Now()}).Error
}

func (r *Repository) UpdateDramaShotScript(shot model.DramaShot, fields map[string]any) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		fields["updated_at"] = time.Now()
		if err := tx.Model(&model.DramaShot{}).Where("id = ? AND project_id = ?", shot.ID, shot.ProjectID).Updates(fields).Error; err != nil {
			return err
		}
		var segment model.DramaSegment
		if err := tx.Where("episode_id = ? AND segment_index = ?", shot.EpisodeID, shot.SegmentIndex).First(&segment).Error; err != nil {
			return err
		}
		if err := invalidateDramaSegment(tx, segment, map[string]any{"prompt_status": promptStatusAfterInputChange(segment.PromptStatus), "video_prompt_override": ""}, true); err != nil {
			return err
		}
		return tx.Model(&model.DramaEpisode{}).Where("id = ?", shot.EpisodeID).Update("duration_ms", tx.Model(&model.DramaShot{}).Select("COALESCE(SUM(duration_ms),0)").Where("episode_id = ?", shot.EpisodeID)).Error
	})
}

func (r *Repository) RequestDramaSegmentVideos(projectID, episodeID string, indexes []int, modelJSON *string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, index := range indexes {
			var segment model.DramaSegment
			if err := tx.Where("project_id = ? AND episode_id = ? AND segment_index = ?", projectID, episodeID, index).First(&segment).Error; err != nil {
				return err
			}
			if segment.VideoStatus == "running" {
				return errors.New("剧情段视频正在生成")
			}
			fields := map[string]any{"video_status": "queued", "video_error": ""}
			if modelJSON != nil {
				fields["video_model_json"] = *modelJSON
			}
			if err := invalidateDramaSegment(tx, segment, fields, false); err != nil {
				return err
			}
		}
		return tx.Model(&model.DramaEpisode{}).Where("id = ?", episodeID).Updates(map[string]any{"approved": true, "media_released": true}).Error
	})
}

func (r *Repository) RetryDramaResultJobs(projectID string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// A successful provider task is re-applied, never submitted a second time.
		if err := tx.Model(&model.DramaJob{}).Where("project_id = ? AND status = ? AND task_id IN (?)", projectID, "failed", tx.Model(&model.Task{}).Select("id").Where("status = ?", model.TaskStatusSucceeded)).Updates(map[string]any{"status": "running", "error": "", "finished_at": nil}).Error; err != nil {
			return err
		}
		return nil
	})
}

// ReopenDramaSegmentVideoJob 把生成任务已成功、回写失败的段视频作业重新置为运行中，
// 调度循环会重放同一任务结果；段状态同步改回生成中。返回作业是否仍满足重放条件。
func (r *Repository) ReopenDramaSegmentVideoJob(jobID, segmentID string) (bool, error) {
	reopened := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&model.DramaJob{}).
			Where("id = ? AND shot_id = ? AND kind = ? AND status = ? AND task_id IN (?)", jobID, segmentID, model.DramaJobSegmentVideo, model.DramaJobFailed, tx.Model(&model.Task{}).Select("id").Where("status = ?", model.TaskStatusSucceeded)).
			Updates(map[string]any{"status": model.DramaJobRunning, "error": "", "finished_at": nil, "updated_at": now})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		segment := tx.Model(&model.DramaSegment{}).Where("id = ? AND video_status = ?", segmentID, model.DramaMediaFailed).Updates(map[string]any{"video_status": model.DramaMediaRunning, "video_error": "", "updated_at": now})
		if segment.Error != nil {
			return segment.Error
		}
		if segment.RowsAffected == 0 {
			// 段状态已被并发操作改变（例如用户刚点了重新生成），放弃重放。
			return errReopenRaced
		}
		reopened = true
		return nil
	})
	if errors.Is(err, errReopenRaced) {
		return false, nil
	}
	return reopened, err
}

var errReopenRaced = errors.New("drama segment changed during reopen")

func (r *Repository) RecentDramaSegmentJobs(projectID, episodeID string) ([]model.DramaJob, error) {
	var jobs []model.DramaJob
	err := r.db.Where("project_id = ? AND episode_id = ? AND kind IN ?", projectID, episodeID, []string{model.DramaJobSegmentPrompt, model.DramaJobSegmentVideo}).Order("created_at desc").Find(&jobs).Error
	return jobs, err
}
