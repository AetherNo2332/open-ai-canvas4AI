package repository

import (
	"strings"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// insertPlatformSubmittedEvent 在提交事务里写“已提交”事件。PostgreSQL 的游标由提交时触发器分配；
// SQLite 写事务串行，这里直接从计数器取下一个游标。
func insertPlatformSubmittedEvent(tx *gorm.DB, submission *model.PlatformSubmission, taskID string) error {
	var task model.Task
	if err := tx.Select("id", "status", "billing_order_id").First(&task, "id = ?", taskID).Error; err != nil {
		return err
	}
	billing := ""
	if task.BillingOrderID != "" {
		var order model.BillingOrder
		if err := tx.Select("id", "status").First(&order, "id = ?", task.BillingOrderID).Error; err != nil {
			return err
		}
		billing = string(order.Status)
	}
	event := model.PlatformTaskEvent{
		ID: newRepositoryID(), TaskID: taskID, UserID: submission.UserID, Application: submission.Application, ProjectRef: submission.ProjectRef,
		Type: "task.submitted", TaskStatus: string(task.Status), BillingStatus: billing, OccurredAtMs: time.Now().UnixMilli(),
	}
	if tx.Dialector.Name() != "postgres" {
		position, err := nextPlatformEventPosition(tx)
		if err != nil {
			return err
		}
		event.Position = &position
	}
	return tx.Create(&event).Error
}

func nextPlatformEventPosition(tx *gorm.DB) (int64, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.PlatformEventCounter{ID: 1}).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(&model.PlatformEventCounter{}).Where("id = 1").Update("value", gorm.Expr("value + 1")).Error; err != nil {
		return 0, err
	}
	var counter model.PlatformEventCounter
	err := tx.First(&counter, "id = 1").Error
	return counter.Value, err
}

// PlatformEventQuery 读取调用者可见的平台事件：按任务或按 application（可选 projectRef）。
type PlatformEventQuery struct {
	UserID, TaskID, Application, ProjectRef string
	After                                   int64
	Limit                                   int
}

// PlatformEvents 只返回已分配游标（已提交）的事件，按游标升序。
func (r *Repository) PlatformEvents(query PlatformEventQuery) ([]model.PlatformTaskEvent, error) {
	db := r.db.Where("user_id = ? AND position IS NOT NULL AND position > ?", query.UserID, query.After)
	if query.TaskID != "" {
		db = db.Where("task_id = ?", query.TaskID)
	}
	if query.Application != "" {
		db = db.Where("application = ?", query.Application)
	}
	if strings.TrimSpace(query.ProjectRef) != "" {
		db = db.Where("project_ref = ?", query.ProjectRef)
	}
	var events []model.PlatformTaskEvent
	err := db.Order("position").Limit(query.Limit).Find(&events).Error
	return events, err
}

// PlatformEventWatermarks 返回当前最大游标与已清理水位（游标小于水位的历史可能缺失）。
func (r *Repository) PlatformEventWatermarks() (current int64, prunedBefore int64, err error) {
	var counter model.PlatformEventCounter
	err = r.db.Where("id = 1").Limit(1).Find(&counter).Error
	return counter.Value, counter.PrunedBefore, err
}

// PlatformTaskRevision 是任务最新事件的游标；快照用它作为修订号。
func (r *Repository) PlatformTaskRevision(taskID string) (int64, error) {
	var revision *int64
	err := r.db.Model(&model.PlatformTaskEvent{}).Where("task_id = ? AND position IS NOT NULL", taskID).Select("MAX(position)").Scan(&revision).Error
	if revision == nil {
		return 0, err
	}
	return *revision, err
}

// PrunePlatformTaskEvents 删除早于 before 的事件，但保留每个任务的最新一条，使修订号不倒退；
// 删除的最大游标写入水位，过期游标据此要求重新读取快照。
func (r *Repository) PrunePlatformTaskEvents(before time.Time) (int64, error) {
	var deleted int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		latest := tx.Model(&model.PlatformTaskEvent{}).Select("MAX(position)").Where("position IS NOT NULL").Group("task_id")
		stale := tx.Model(&model.PlatformTaskEvent{}).Where("position IS NOT NULL AND occurred_at_ms < ? AND position NOT IN (?)", before.UnixMilli(), latest)
		var watermark *int64
		if err := stale.Session(&gorm.Session{}).Select("MAX(position)").Scan(&watermark).Error; err != nil || watermark == nil {
			return err
		}
		result := stale.Session(&gorm.Session{}).Delete(&model.PlatformTaskEvent{})
		if result.Error != nil {
			return result.Error
		}
		deleted = result.RowsAffected
		return tx.Model(&model.PlatformEventCounter{}).Where("id = 1 AND pruned_before < ?", *watermark+1).Update("pruned_before", *watermark+1).Error
	})
	return deleted, err
}
