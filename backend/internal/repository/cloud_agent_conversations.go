package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

type CloudAgentHistoryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

const cloudAgentConversationIdentity = "COALESCE(NULLIF(conversation_id, ''), id)"

// Only control columns are needed for directory polling; never hydrate journals.
const cloudAgentHistoryColumns = "id, user_id, conversation_id, parent_id, title, canvas_id, status, revision, cleanup_pending, runtime_phase, wait_kind, wait_id, wait_reason, created_at, updated_at, scope_type, scope_id, agent_definition_id, agent_context_version"

func (r *Repository) CloudAgentConversations(userID, canvasID string, before *CloudAgentHistoryCursor, limit int, scope ...model.AgentConversationBinding) ([]model.CloudAgentExecution, error) {
	rows := []model.CloudAgentExecution{}
	query := r.db.Table("cloud_agent_executions e").Select(`e.id, e.user_id, e.conversation_id, e.parent_id, e.title, e.canvas_id, e.status, e.revision, e.cleanup_pending, e.runtime_phase, e.wait_kind, e.wait_id, e.wait_reason, e.created_at, e.updated_at,
		e.scope_type, e.scope_id, e.agent_definition_id, e.agent_context_version,
		EXISTS (SELECT 1 FROM cloud_agent_executions a WHERE a.user_id = e.user_id AND COALESCE(NULLIF(a.conversation_id, ''), a.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id) AND a.archived_at IS NOT NULL) AS conversation_archived`).
		Where("e.user_id = ?", userID).
		Where("NOT EXISTS (SELECT 1 FROM agent_subagent_links l WHERE l.user_id = e.user_id AND l.child_run_id = COALESCE(NULLIF(e.conversation_id, ''), e.id))").
		Where(`NOT EXISTS (SELECT 1 FROM cloud_agent_executions n WHERE n.user_id = e.user_id
			AND COALESCE(NULLIF(n.conversation_id, ''), n.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id)
			AND (n.created_at > e.created_at OR (n.created_at = e.created_at AND n.id > e.id)))`)
	if len(scope) > 0 {
		query = query.Where("e.scope_type = ? AND e.scope_id = ? AND e.agent_definition_id = ?", scope[0].ScopeType, scope[0].ScopeID, scope[0].AgentDefinitionID)
	} else {
		query = query.Where(`EXISTS (SELECT 1 FROM cloud_agent_executions c WHERE c.user_id = e.user_id AND c.canvas_id = ?
			AND COALESCE(NULLIF(c.conversation_id, ''), c.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id))`, canvasID)
	}
	if before != nil {
		query = query.Where("e.created_at < ? OR (e.created_at = ? AND e.id < ?)", before.CreatedAt, before.CreatedAt, before.ID)
	}
	err := query.Order("e.created_at DESC, e.id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// PendingDirectorPreviewRuns returns runs whose durable external wait is a
// browser viewport recording. The caller still decodes each checkpoint and
// verifies the request deadline and current business access.
func (r *Repository) PendingDirectorPreviewRuns(userID string, limit int) ([]model.CloudAgentExecution, error) {
	rows := []model.CloudAgentExecution{}
	err := r.db.Where("user_id = ? AND wait_kind = ? AND status IN ? AND cleanup_pending = ?", userID, "director_preview", []string{"running", "queued"}, false).
		Order("updated_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error
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

// Archive is independent of run checkpoints and billing. Keep a tombstone in
// the directory so devices with cached history can remove their local entry.
func (r *Repository) ArchiveCloudAgentConversation(userID, conversationID string) error {
	return r.db.Model(&model.CloudAgentExecution{}).Where("user_id = ? AND "+cloudAgentConversationIdentity+" = ?", userID, conversationID).UpdateColumn("archived_at", time.Now()).Error
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
	if run.AgentContextVersion != 0 && parent.AgentConversationBinding != run.AgentConversationBinding {
		return ErrCloudAgentStaleParent
	}
	// Tip ordering must be monotonic even on a coarse clock or after a clock
	// adjustment. PostgreSQL stores microseconds; smaller increments can round
	// back onto the parent's timestamp and let its ID win the tie again.
	if !run.CreatedAt.After(parent.CreatedAt) {
		run.CreatedAt = parent.CreatedAt.Add(time.Microsecond)
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
