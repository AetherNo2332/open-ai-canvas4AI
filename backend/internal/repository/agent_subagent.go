package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SubagentStatusQueued    = "queued"
	SubagentStatusRunning   = "running"
	SubagentStatusCompleted = "completed"
	SubagentStatusFailed    = "failed"
	SubagentStatusCancelled = "cancelled"
)

// Terminal children and abandoned families form a durable reconciliation queue.
func (r *Repository) UnsettledAgentSubagentLinks(userID, parentRunID string) ([]model.AgentSubagentLink, error) {
	rows := []model.AgentSubagentLink{}
	query := r.db.Table("agent_subagent_links AS links").Select("links.*").
		Joins("JOIN cloud_agent_executions AS child ON child.id = links.child_run_id AND child.user_id = links.user_id").
		Joins("JOIN cloud_agent_executions AS parent ON parent.id = links.parent_run_id AND parent.user_id = links.user_id").
		Where("links.status IN ? AND (child.status IN ? OR parent.status IN ?)", []string{"queued", "running"}, []string{"completed", "failed", "cancelled", "rejected"}, []string{"completed", "failed", "cancelled", "rejected"})
	if userID != "" {
		query = query.Where("links.user_id = ? AND links.parent_run_id = ?", userID, parentRunID)
	}
	err := query.Order("links.created_at, links.id").Limit(100).Find(&rows).Error
	return rows, err
}

func (r *Repository) CreateAgentSubagentLink(link *model.AgentSubagentLink) error {
	if link == nil || link.UserID == "" || link.ParentRunID == "" || link.ChildRunID == "" || link.IdempotencyKey == "" {
		return gorm.ErrInvalidData
	}
	return r.db.Create(link).Error
}

func (r *Repository) AgentSubagentLinkForUser(userID, id string) (*model.AgentSubagentLink, error) {
	var link model.AgentSubagentLink
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&link).Error
	return &link, err
}

func (r *Repository) AgentSubagentLinkByIdempotency(userID, key string) (*model.AgentSubagentLink, error) {
	var link model.AgentSubagentLink
	err := r.db.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&link).Error
	return &link, err
}

func (r *Repository) AgentSubagentLinkByParent(userID, parentRunID string, statuses []string) ([]model.AgentSubagentLink, error) {
	var links []model.AgentSubagentLink
	query := r.db.Where("user_id = ? AND parent_run_id = ?", userID, parentRunID)
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	err := query.Order("created_at asc, id asc").Find(&links).Error
	return links, err
}

func (r *Repository) AppendAgentSubagentMessage(message *model.AgentSubagentMessage) error {
	if message == nil || message.LinkID == "" || message.UserID == "" || message.IdempotencyKey == "" {
		return gorm.ErrInvalidData
	}
	if !json.Valid([]byte(message.PayloadJSON)) {
		return fmt.Errorf("subagent message payload must be JSON")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Serialize both directions on the Link before allocating a sequence.
		locked := tx.Model(&model.AgentSubagentLink{}).Where("id = ? AND user_id = ? AND parent_run_id = ? AND child_run_id = ?", message.LinkID, message.UserID, message.ParentRunID, message.ChildRunID).UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var existing model.AgentSubagentMessage
		if err := tx.Where("idempotency_key = ? AND user_id = ?", message.IdempotencyKey, message.UserID).First(&existing).Error; err == nil {
			if existing.LinkID != message.LinkID || existing.Direction != message.Direction || existing.Kind != message.Kind || !sameJSONDocument(existing.PayloadJSON, message.PayloadJSON) {
				return ErrCreationConflict
			}
			*message = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var sequence int64
		if err := tx.Model(&model.AgentSubagentMessage{}).Where("link_id = ?", message.LinkID).Select("COALESCE(MAX(sequence), 0)").Scan(&sequence).Error; err != nil {
			return err
		}
		message.Sequence = sequence + 1
		return tx.Create(message).Error
	})
}

func (r *Repository) AcknowledgeAgentSubagentMessage(userID, messageID string) error {
	return r.db.Model(&model.AgentSubagentMessage{}).Where("id = ? AND user_id = ? AND acknowledged_at IS NULL", messageID, userID).Update("acknowledged_at", time.Now()).Error
}

func (r *Repository) AgentSubagentMessages(userID, parentRunID string) ([]model.AgentSubagentMessage, error) {
	rows := []model.AgentSubagentMessage{}
	err := r.db.Where("user_id = ? AND parent_run_id = ?", userID, parentRunID).Order("created_at, link_id, sequence").Find(&rows).Error
	return rows, err
}

// Always lock the parent before the child, including parent-to-child messages,
// so opposite-direction deliveries cannot deadlock on PostgreSQL.
func (r *Repository) WithAgentSubagentFamily(userID, parentRunID, childRunID string, fn func(*Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, id := range []string{parentRunID, childRunID} {
			q := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND user_id = ?", id, userID).UpdateColumn("revision", gorm.Expr("revision"))
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		return fn(New(tx))
	})
}

// The Link, independent session, holding task and credit reservation either all
// commit or all roll back. A child is never visible to workers without its Link.
func (r *Repository) CreateDynamicSubagentAdmission(admission CloudAgentAdmission, link *model.AgentSubagentLink, activeTaskLimit, maxChildren, maxConcurrent int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		locked := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND user_id = ? AND status IN ?", link.ParentRunID, link.UserID, []string{"queued", "running"}).UpdateColumn("revision", gorm.Expr("revision"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return ErrCreationConflict
		}
		var total, active int64
		if err := tx.Model(&model.AgentSubagentLink{}).Where("user_id = ? AND parent_run_id = ?", link.UserID, link.ParentRunID).Count(&total).Error; err != nil {
			return err
		}
		if err := tx.Table("agent_subagent_links AS links").Joins("JOIN cloud_agent_executions AS runs ON runs.id = links.child_run_id AND runs.user_id = links.user_id").Where("links.user_id = ? AND links.parent_run_id = ? AND runs.status IN ?", link.UserID, link.ParentRunID, []string{"queued", "running", "waiting_approval"}).Count(&active).Error; err != nil {
			return err
		}
		if total >= int64(maxChildren) || active >= int64(maxConcurrent) {
			return ErrTaskStateConflict
		}
		if err := createCloudAgentHoldingTask(tx, admission.Task, admission.Order, admission.Execution, activeTaskLimit, admission.Skills); err != nil {
			return err
		}
		return tx.Create(link).Error
	})
}

func (r *Repository) NextAgentSubagentMessageSequence(linkID string) (int64, error) {
	var max int64
	err := r.db.Model(&model.AgentSubagentMessage{}).Where("link_id = ?", linkID).Select("COALESCE(MAX(sequence), 0)").Scan(&max).Error
	return max + 1, err
}

func (r *Repository) UpdateAgentSubagentLinkStatus(userID, linkID, status string) error {
	return r.db.Model(&model.AgentSubagentLink{}).Where("id = ? AND user_id = ?", linkID, userID).Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error
}

func (r *Repository) AgentSubagentPolicy(userID, canvasID string) (*model.AgentSubagentPolicy, error) {
	var policy model.AgentSubagentPolicy
	err := r.db.Where("user_id = ? AND canvas_id = ?", userID, canvasID).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		policy = model.AgentSubagentPolicy{ID: kernel.NewID(), UserID: userID, CanvasID: canvasID}
		if err = r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&policy).Error; err != nil {
			return nil, err
		}
		err = r.db.Where("user_id = ? AND canvas_id = ?", userID, canvasID).First(&policy).Error
	}
	return &policy, err
}

// SaveAgentSubagentPolicy updates the persistent switch with optimistic locking.
func (r *Repository) SaveAgentSubagentPolicy(userID, canvasID string, enabled bool, expectedRevision int64) (*model.AgentSubagentPolicy, error) {
	var out model.AgentSubagentPolicy
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current model.AgentSubagentPolicy
		err := tx.Where("user_id = ? AND canvas_id = ?", userID, canvasID).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if expectedRevision != 0 {
				return ErrTaskStateConflict
			}
			current = model.AgentSubagentPolicy{ID: kernel.NewID(), UserID: userID, CanvasID: canvasID, Enabled: enabled, Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := tx.Create(&current).Error; err != nil {
				return err
			}
			out = current
			return nil
		}
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrTaskStateConflict
		}
		result := tx.Model(&model.AgentSubagentPolicy{}).Where("id = ? AND revision = ?", current.ID, expectedRevision).Updates(map[string]any{"enabled": enabled, "revision": expectedRevision + 1, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTaskStateConflict
		}
		current.Enabled, current.Revision = enabled, expectedRevision+1
		current.UpdatedAt = time.Now()
		out = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
