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
	if message == nil || message.LinkID == "" || message.IdempotencyKey == "" || message.Sequence < 1 {
		return gorm.ErrInvalidData
	}
	if !json.Valid([]byte(message.PayloadJSON)) {
		return fmt.Errorf("subagent message payload must be JSON")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.AgentSubagentMessage
		if err := tx.Where("idempotency_key = ? AND user_id = ?", message.IdempotencyKey, message.UserID).First(&existing).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(message).Error
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
