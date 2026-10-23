package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

type CloudAgentHistoryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

const cloudAgentConversationIdentity = "COALESCE(NULLIF(conversation_id, ''), id)"

// Only control columns are needed for directory polling; never hydrate journals.
const cloudAgentHistoryColumns = "id, user_id, conversation_id, parent_id, title, canvas_id, status, revision, cleanup_pending, created_at, updated_at"

func (r *Repository) CloudAgentConversations(userID, canvasID string, before *CloudAgentHistoryCursor, limit int) ([]model.CloudAgentExecution, error) {
	rows := []model.CloudAgentExecution{}
	query := r.db.Table("cloud_agent_executions e").Select("e.id, e.user_id, e.conversation_id, e.parent_id, e.title, e.canvas_id, e.status, e.revision, e.cleanup_pending, e.created_at, e.updated_at").
		Where("e.user_id = ?", userID).
		Where("NOT EXISTS (SELECT 1 FROM agent_subagent_links l WHERE l.user_id = e.user_id AND l.child_run_id = COALESCE(NULLIF(e.conversation_id, ''), e.id))").
		Where(`EXISTS (SELECT 1 FROM cloud_agent_executions c WHERE c.user_id = e.user_id AND c.canvas_id = ?
			AND COALESCE(NULLIF(c.conversation_id, ''), c.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id))`, canvasID).
		Where(`NOT EXISTS (SELECT 1 FROM cloud_agent_executions n WHERE n.user_id = e.user_id
			AND COALESCE(NULLIF(n.conversation_id, ''), n.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id)
			AND (n.created_at > e.created_at OR (n.created_at = e.created_at AND n.id > e.id)))`)
	if before != nil {
		query = query.Where("e.created_at < ? OR (e.created_at = ? AND e.id < ?)", before.CreatedAt, before.CreatedAt, before.ID)
	}
	err := query.Order("e.created_at DESC, e.id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *Repository) CloudAgentConversationRuns(userID, conversationID string, after *CloudAgentHistoryCursor, limit int) ([]model.CloudAgentExecution, error) {
	rows := []model.CloudAgentExecution{}
	query := r.db.Model(&model.CloudAgentExecution{}).Select(cloudAgentHistoryColumns).
		Where("user_id = ? AND "+cloudAgentConversationIdentity+" = ?", userID, conversationID)
	if after != nil {
		query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", after.CreatedAt, after.CreatedAt, after.ID)
	}
	err := query.Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *Repository) CloudAgentConversationExists(userID, conversationID string) (bool, error) {
	var row model.CloudAgentExecution
	err := r.db.Select("id").Where("user_id = ? AND "+cloudAgentConversationIdentity+" = ?", userID, conversationID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

var ErrCloudAgentStaleParent = errors.New("对话已有更新，请同步最新一轮后再发送")

// The holding task, execution and credit reservation commit together. Lock the
// parent before reading its current tip, so two instances cannot branch a turn.
func (r *Repository) requireCurrentCloudAgentParent(run *model.CloudAgentExecution) error {
	if run.ParentID == "" {
		return nil
	}
	// Dynamic subagents deliberately start an independent conversation from an
	// active parent; they retain their existing admission and concurrency limits.
	if run.ConversationID == run.ID {
		return nil
	}
	// A raw no-op write acquires SQLite's writer lock before any SELECT.
	// This must precede repository initialization/schema probes and callbacks
	// that might otherwise pin a read snapshot before obtaining the writer lock.
	locked := r.db.Exec("UPDATE cloud_agent_executions SET revision = revision WHERE id = ? AND user_id = ?", run.ParentID, run.UserID)
	if locked.Error != nil {
		return locked.Error
	}
	if locked.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	var parent model.CloudAgentExecution
	if err := r.db.Select(cloudAgentHistoryColumns).First(&parent, "id = ? AND user_id = ?", run.ParentID, run.UserID).Error; err != nil {
		return err
	}
	identity := parent.ConversationID
	if identity == "" {
		identity = parent.ID
	}
	if identity != run.ConversationID {
		return ErrCloudAgentStaleParent
	}
	var latest model.CloudAgentExecution
	if err := r.db.Select("id").Where("user_id = ? AND "+cloudAgentConversationIdentity+" = ?", run.UserID, identity).Order("created_at DESC, id DESC").First(&latest).Error; err != nil {
		return err
	}
	if latest.ID != parent.ID || !cloudAgentParentTerminal(parent.Status) || parent.CleanupPending {
		return ErrCloudAgentStaleParent
	}
	return nil
}
func cloudAgentParentTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "rejected"
}
