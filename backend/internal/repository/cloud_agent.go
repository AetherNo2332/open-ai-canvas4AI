package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

// CloudAgentJournalWindow 是 hydrate 时载入的事件窗口条数：事件全量在表里，
// 内存只保留最近这一窗供摘要、记忆提取与卡死判定使用。
const CloudAgentJournalWindow = 40

func (r *Repository) EnsureCloudAgent(run *model.CloudAgentExecution) error {
	err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(run).Error
	if err != nil {
		return err
	}
	// 创建行时还不知道有多少消息/事件：先把它们写进表，再把计数回填到刚建的行上，
	// 否则"行上计数为 0、表里已有事件"会让下次解码判定记录不完整。
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := writeCloudAgentPending(tx, run); err != nil {
			return err
		}
		return tx.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{
			"checkpoint_version": run.CheckpointVersion,
			"message_count":      run.MessageCount,
			"event_count":        run.EventCount,
		}).Error
	})
}
func (r *Repository) CloudAgent(userID, id string) (*model.CloudAgentExecution, error) {
	var run model.CloudAgentExecution
	err := r.db.First(&run, "id = ? AND user_id = ?", id, userID).Error
	if err == nil {
		err = r.hydrateCloudAgent(&run)
	}
	return &run, err
}

// hydrateCloudAgent 把检查点之外的消息与事件窗口读进来。
//
// 检查点从 v2 起只放控制面：消息在 cloud_agent_messages（全量，压缩时整体重写），
// 事件在 cloud_agent_run_events（这里只取最近一窗，供摘要、记忆与卡死判定使用；
// 运行详情仍按 seq 分页读表）。旧运行（CheckpointVersion<2）消息与事件都还在 StateJSON 里，
// 不需要 hydrate。
func (r *Repository) hydrateCloudAgent(run *model.CloudAgentExecution) error {
	if run == nil || run.CheckpointVersion < model.CloudAgentCheckpointVersion {
		return nil
	}
	messages, err := r.CloudAgentMessages(run.UserID, run.ID)
	if err != nil {
		return err
	}
	events, err := r.CloudAgentRunEventWindow(run.UserID, run.ID, CloudAgentJournalWindow)
	if err != nil {
		return err
	}
	run.Transcript, run.Journal = messages, events
	return nil
}

// CloudAgentMessages 按 kind、sequence 升序取该运行的全部消息。
func (r *Repository) CloudAgentMessages(userID, runID string) ([]model.CloudAgentMessageRecord, error) {
	records := []model.CloudAgentMessageRecord{}
	err := r.db.Where("user_id = ? AND run_id = ?", userID, runID).Order("kind ASC, sequence ASC").Find(&records).Error
	return records, err
}

// CloudAgentRunEventWindow 取最近 limit 条事件（升序返回）。
func (r *Repository) CloudAgentRunEventWindow(userID, runID string, limit int) ([]model.CloudAgentRunEvent, error) {
	if limit <= 0 {
		limit = CloudAgentJournalWindow
	}
	events := []model.CloudAgentRunEvent{}
	err := r.db.Where("user_id = ? AND run_id = ?", userID, runID).Order("seq DESC").Limit(limit).Find(&events).Error
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

// writeCloudAgentPending 把本次转移攒下的消息与事件写进同一个事务：
// 消息与检查点必须一起成功，否则崩溃会丢消息；事件另有唯一索引 (run_id, seq) 兜底幂等。
func writeCloudAgentPending(tx *gorm.DB, run *model.CloudAgentExecution) error {
	if run == nil {
		return nil
	}
	if run.MessagesReplaced {
		if err := tx.Where("run_id = ?", run.ID).Delete(&model.CloudAgentMessageRecord{}).Error; err != nil {
			return err
		}
		run.MessageCount = 0
	}
	if len(run.PendingMessages) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "run_id"}, {Name: "kind"}, {Name: "sequence"}},
			DoNothing: true,
		}).CreateInBatches(run.PendingMessages, 100).Error; err != nil {
			return err
		}
		run.MessageCount += len(run.PendingMessages)
	}
	if len(run.PendingEvents) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "run_id"}, {Name: "seq"}},
			DoNothing: true,
		}).CreateInBatches(run.PendingEvents, 100).Error; err != nil {
			return err
		}
		run.EventCount = run.PendingEvents[len(run.PendingEvents)-1].Seq
	}
	// 版本与计数由持久化层决定：只有消息/事件真的写进去了，行上的计数才成立。
	run.CheckpointVersion = model.CloudAgentCheckpointVersion
	return nil
}

func (r *Repository) CloudAgentForActiveTask(userID, taskID string) (*model.CloudAgentExecution, error) {
	var run model.CloudAgentExecution
	err := r.db.Where("user_id = ? AND active_task_id = ? AND status IN ?", userID, taskID, []string{"running", "queued"}).First(&run).Error
	if err == nil {
		err = r.hydrateCloudAgent(&run)
	}
	return &run, err
}
func (r *Repository) CloudAgentRoots() ([]model.Task, error) {
	var tasks []model.Task
	err := r.db.Where("operation = ? AND id NOT IN (SELECT id FROM cloud_agent_executions)", "cloud_agent").Order("created_at").Limit(50).Find(&tasks).Error
	return tasks, err
}

// A stable keyset makes waiting rows yield to later runs without changing business timestamps.
func (r *Repository) ActiveCloudAgentsAfter(after string, limit int) ([]model.CloudAgentExecution, error) {
	var runs []model.CloudAgentExecution
	if limit < 1 || limit > 50 {
		limit = 50
	}
	err := r.db.Where("(status IN ? OR cleanup_pending = ?) AND id > ?", []string{"running", "queued"}, true, after).Order("id").Limit(limit).Find(&runs).Error
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if err = r.hydrateCloudAgent(&runs[i]); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (r *Repository) ActiveCloudAgents() ([]model.CloudAgentExecution, error) {
	return r.ActiveCloudAgentsAfter("", 50)
}

// Do not load the transcript, tasks and bills for an unchanged SSE subscription.
func (r *Repository) CloudAgentRevision(userID, id string) (int64, error) {
	var run model.CloudAgentExecution
	err := r.db.Select("id", "revision").Where("id = ? AND user_id = ?", id, userID).First(&run).Error
	return run.Revision, err
}

// Lock before reading: checkpoints, canvas writes and task reservations commit together.
func (r *Repository) MutateCloudAgent(userID, id string, revision int64, fn func(*model.CloudAgentExecution, *Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND user_id = ? AND revision = ?", id, userID, revision).UpdateColumn("revision", gorm.Expr("revision + 1"))
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return ErrCreationConflict
		}
		run, err := New(tx).CloudAgent(userID, id)
		if err != nil {
			return err
		}
		if err = fn(run, New(tx)); err != nil {
			return err
		}
		if err = writeCloudAgentPending(tx, run); err != nil {
			return err
		}
		return tx.Save(run).Error
	})
}

func (r *Repository) CreateCloudAgentCanvasMutation(mutation *model.CloudAgentCanvasMutation) error {
	return r.db.Create(mutation).Error
}

func (r *Repository) LatestCloudAgentCanvasMutation(userID, runID string) (*model.CloudAgentCanvasMutation, error) {
	var mutation model.CloudAgentCanvasMutation
	err := r.db.Where("user_id = ? AND run_id = ?", userID, runID).
		Order("created_at DESC, id DESC").First(&mutation).Error
	return &mutation, err
}

func (r *Repository) MarkCloudAgentCanvasMutationUndone(userID, runID, mutationID string, undoneAt time.Time) error {
	result := r.db.Model(&model.CloudAgentCanvasMutation{}).
		Where("id = ? AND user_id = ? AND run_id = ? AND status = ?", mutationID, userID, runID, "applied").
		Updates(map[string]any{"status": "undone", "undone_at": undoneAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

// MarkCloudAgentFailed is a terminal CAS transition that does not read or
// rewrite StateJSON. It is deliberately usable when a corrupted runtime state
// can no longer be decoded; leaving such a run in running would make the
// scheduler retry it forever.
func (r *Repository) MarkCloudAgentFailed(userID, id string, revision int64, message ...string) error {
	detail := "Agent 运行状态损坏，本轮已停止"
	if len(message) > 0 {
		detail = message[0]
	}
	result := r.db.Model(&model.CloudAgentExecution{}).
		Where("id = ? AND user_id = ? AND revision = ? AND status IN ?", id, userID, revision, []string{"queued", "running"}).
		Updates(map[string]any{"status": "failed", "cleanup_pending": true, "failure_message": detail, "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

// MarkCloudAgentCancelled is the corruption-safe cancellation transition. It
// intentionally does not touch StateJSON: cancellation must still stop the
// scheduler when the orchestration blob can no longer be decoded.
func (r *Repository) MarkCloudAgentCancelled(userID, id string, revision int64) error {
	result := r.db.Model(&model.CloudAgentExecution{}).
		Where("id = ? AND user_id = ? AND revision = ? AND status IN ?", id, userID, revision, []string{"queued", "running", "waiting_approval"}).
		Updates(map[string]any{"status": "cancelled", "cleanup_pending": true, "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

// AppendCloudAgentRunEvents 批量追加运行事件：唯一索引 (run_id, seq) + DO NOTHING，
// 因此重复写入（重试、升级期补写）天然幂等。
func (r *Repository) AppendCloudAgentRunEvents(events []model.CloudAgentRunEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "run_id"}, {Name: "seq"}}, DoNothing: true}).
		CreateInBatches(events, 100).Error
}

// CloudAgentRunEvents 按 seq 升序取事件：afterSeq 之前（含）跳过，limit 封顶。
func (r *Repository) CloudAgentRunEvents(userID, runID string, afterSeq, limit int) ([]model.CloudAgentRunEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	events := []model.CloudAgentRunEvent{}
	err := r.db.Where("user_id = ? AND run_id = ? AND seq > ?", userID, runID, afterSeq).
		Order("seq ASC").Limit(limit).Find(&events).Error
	return events, err
}

// CloudAgentRunEventsBefore 取 beforeSeq 之前最近的若干条（升序返回），用于"加载更早的记录"。
func (r *Repository) CloudAgentRunEventsBefore(userID, runID string, beforeSeq, limit int) ([]model.CloudAgentRunEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	events := []model.CloudAgentRunEvent{}
	err := r.db.Where("user_id = ? AND run_id = ? AND seq < ?", userID, runID, beforeSeq).
		Order("seq DESC").Limit(limit).Find(&events).Error
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

// CloudAgentRunEventCount 返回该运行已入库的事件条数（运行详情用它告诉客户端还有多少历史）。
func (r *Repository) CloudAgentRunEventCount(userID, runID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.CloudAgentRunEvent{}).Where("user_id = ? AND run_id = ?", userID, runID).Count(&count).Error
	return count, err
}

// DeleteCloudAgentRunEvents 清理运行事件（运行删除、画布删除、保留期到期）。
func (r *Repository) DeleteCloudAgentRunEvents(userID, runID string) error {
	return r.db.Where("user_id = ? AND run_id = ?", userID, runID).Delete(&model.CloudAgentRunEvent{}).Error
}

// ClearCloudAgentRunEventCanvasScope 在画布被删除时清掉事件上的画布归属：
// 事件是审计记录（与任务同口径），保留内容但不再挂住已删除的画布。
func (r *Repository) ClearCloudAgentRunEventCanvasScope(userID, canvasID string) error {
	return r.db.Model(&model.CloudAgentRunEvent{}).Where("user_id = ? AND canvas_id = ?", userID, canvasID).
		Update("canvas_id", "").Error
}

// PurgeExpiredCloudAgentRunEvents 删除超过保留期的事件，返回删除条数。
func (r *Repository) PurgeExpiredCloudAgentRunEvents(now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	query := r.db.Where("expires_at > ? AND expires_at < ?", time.Time{}, now).Limit(limit).Delete(&model.CloudAgentRunEvent{})
	return query.RowsAffected, query.Error
}
