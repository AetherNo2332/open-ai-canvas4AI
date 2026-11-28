package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// ErrShortplayJobNotSubmitting 表示任务创建事务准备绑定时，job 已被其他 writer 收口或已经绑定。
// 调用方必须回滚任务与计费，不能把另一次提交的结果覆盖掉。
var ErrShortplayJobNotSubmitting = errors.New("短剧 job 已不再接受本次任务绑定")

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

// RunningShortplayJob 取一条「已提交任务、等待结果」的在途 job（按更新时间升序轮转）；
// 没有则 (nil,nil)。任务型 job 提交后状态是 running，不能只靠 queued 领取来推进。
func (r *Repository) RunningShortplayJob(kind string) (*model.ShortplayJob, error) {
	var row model.ShortplayJob
	err := r.db.Where("kind = ? AND status = ? AND task_id <> ''", kind, shortplay.JobRunning).
		Order("updated_at asc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// OrphanedShortplayJob 取超期且未绑定任务的 running job。静默期不证明提交者已死亡；
// 活跃慢提交仍可能被恢复先收口，之后的绑定必须拒绝并回滚任务事务。
func (r *Repository) OrphanedShortplayJob(kind string, staleBefore time.Time) (*model.ShortplayJob, error) {
	var row model.ShortplayJob
	err := r.db.Where("kind = ? AND status = ? AND COALESCE(task_id, '') = '' AND updated_at < ?",
		kind, shortplay.JobRunning, staleBefore).Order("created_at asc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ShortplayJobTasks 按结构化 metadata.shortplayJobId 反查「提交后没记下 taskId」的 job 对应
// 的任务行：只在该用户、该项目、该任务类型里找，输入 JSON 必须能解析且 metadata.shortplayJobId
// 完全相等。缩进、键序这类格式差异不影响匹配；字段挂在别处、值不同、非 JSON 都不算。
// 返回全部候选，由调用方处理 0/1/多条——多条不得挑一条当结果。
func (r *Repository) ShortplayJobTasks(shortplayJobID, userID, projectID, taskType string) ([]model.Task, error) {
	var rows []model.Task
	if err := r.db.Where("user_id = ? AND project_id = ? AND type = ?", userID, projectID, taskType).
		Order("created_at asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	matched := make([]model.Task, 0, 1)
	for _, row := range rows {
		if shortplayTaskJobID(row.InputJSON) == shortplayJobID {
			matched = append(matched, row)
		}
	}
	return matched, nil
}

// shortplayTaskJobID 只认任务输入里 metadata.shortplayJobId 这一处字段。
func shortplayTaskJobID(inputJSON string) string {
	var input struct {
		Metadata struct {
			ShortplayJobID string `json:"shortplayJobId"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return ""
	}
	return strings.TrimSpace(input.Metadata.ShortplayJobID)
}

// LinkShortplayJobTask 把找回的 taskId 写回孤儿 job。条件更新保证只动静默超期、仍是 running
// 且没有 taskId 的行：活跃提交先写回时这里 RowsAffected=0，不会被覆盖。
func (r *Repository) LinkShortplayJobTask(id, taskID string, staleBefore time.Time) (bool, error) {
	result := r.db.Model(&model.ShortplayJob{}).
		Where("id = ? AND status = ? AND COALESCE(task_id, '') = '' AND updated_at < ?",
			id, shortplay.JobRunning, staleBefore).
		Updates(map[string]any{"task_id": taskID, "updated_at": time.Now()})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// RestoreShortplayJobMediaRunning 补齐旧版孤儿任务在绑定前丢失的媒体状态。
// 已收口的 ready/failed 状态保留，避免重放结果时退回 running 或新增样本。
func (r *Repository) RestoreShortplayJobMediaRunning(job *model.ShortplayJob) error {
	var field string
	var assetID int
	switch job.Kind {
	case shortplay.JobAssetImage:
		field, assetID = "image_status", shortplay.ParseAssetImageJob(job.PayloadJSON).AssetID
	case shortplay.JobAssetAudio:
		field, assetID = "audio_status", shortplay.ParseAssetAudioJob(job.PayloadJSON).AssetID
	default:
		return nil
	}
	return r.db.Model(&model.ShortplayAsset{}).
		Where("shortplay_id = ? AND id = ?", job.ShortplayID, assetID).
		Where(field+" IN ?", []string{"", shortplay.ImageQueued, shortplay.ImageRunning}).
		Updates(map[string]any{field: shortplay.ImageRunning, "updated_at": time.Now()}).Error
}

// LinkShortplayJobTaskIfEmpty 在任务创建事务内绑定 taskId 与调度负载。
// 不再用 updated_at 静默期判断，而是依赖同一行上的条件更新：
// 绑定 UPDATE 取得 job 行锁后，恢复 writer 等待提交并重新检查条件；
// 若恢复先收口，这里 RowsAffected=0，任务创建事务必须整体回滚。
func (r *Repository) LinkShortplayJobTaskIfEmpty(id, taskID, payloadJSON string) (bool, error) {
	updates := map[string]any{"task_id": taskID, "updated_at": time.Now()}
	if payloadJSON != "" {
		updates["payload_json"] = payloadJSON
	}
	result := r.db.Model(&model.ShortplayJob{}).
		Where("id = ? AND status = ? AND COALESCE(task_id, '') = ''", id, shortplay.JobRunning).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// FailOrphanedShortplayJob 把找不到可信任务关联的孤儿 job 明确收口为 failed。与 Link 同条件：
// 只动静默超期、仍是 running 且没有 taskId 的行，避免把已经推进或正在提交的行覆盖成终态。
func (r *Repository) FailOrphanedShortplayJob(id, lastError, payloadJSON string, staleBefore time.Time) (bool, error) {
	return r.finishUnboundShortplayJob(id, shortplay.JobFailed, lastError, payloadJSON, &staleBefore)
}

// FinishUnboundShortplayJob 仅允许尚未绑定任务的 running job 收口。
// 迟到的提交者不得覆盖恢复结果；调用方在同一事务内投影资产状态。
func (r *Repository) FinishUnboundShortplayJob(id, status, lastError, payloadJSON string) (bool, error) {
	return r.finishUnboundShortplayJob(id, status, lastError, payloadJSON, nil)
}

func (r *Repository) finishUnboundShortplayJob(id, status, lastError, payloadJSON string, staleBefore *time.Time) (bool, error) {
	updates := map[string]any{
		"status":      status,
		"last_error":  truncateShortplayError(lastError),
		"finished_at": time.Now(),
		"updated_at":  time.Now(),
	}
	if payloadJSON != "" {
		updates["payload_json"] = payloadJSON
	}
	query := r.db.Model(&model.ShortplayJob{}).
		Where("id = ? AND status = ? AND COALESCE(task_id, '') = ''", id, shortplay.JobRunning)
	if staleBefore != nil {
		query = query.Where("updated_at < ?", *staleBefore)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
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

// ReleaseShortplayJob 把已领取但本轮不能派发的 job 退回 queued（例如衍生形象在等父资产）。
// 去重键仍被这条 queued 行占着，不会因此多排一条；下一 tick 重新领取。
func (r *Repository) ReleaseShortplayJob(id string) error {
	now := time.Now()
	return r.db.Model(&model.ShortplayJob{}).
		Where("id = ? AND status = ? AND task_id = ''", id, shortplay.JobRunning).
		Updates(map[string]any{"status": shortplay.JobQueued, "updated_at": now}).Error
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
