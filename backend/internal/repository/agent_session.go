package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// ErrAgentSessionChanged means a conditional Session write lost its race: the
// revision, lease epoch or status the caller read is no longer current.
var ErrAgentSessionChanged = errors.New("agent session changed")

// AgentSessionCursor orders the directory by creation time, newest first.
type AgentSessionCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

// AgentSessionFilter narrows the owner's directory to one fixed scope.
type AgentSessionFilter struct {
	Application, SpaceType, SpaceID string
}

// CreateAgentSession inserts the Session or, when the owner already used the
// idempotency key (or the deterministic ID), returns the stored row unchanged.
func (r *Repository) CreateAgentSession(session *model.AgentSession) (*model.AgentSession, bool, error) {
	created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(session)
	if created.Error != nil {
		return nil, false, created.Error
	}
	if created.RowsAffected == 1 {
		return session, true, nil
	}
	var stored model.AgentSession
	err := r.db.First(&stored, "owner_id = ? AND idempotency_key = ?", session.OwnerID, session.IdempotencyKey).Error
	return &stored, false, err
}

// AgentSessionForOwner never returns another owner's Session.
func (r *Repository) AgentSessionForOwner(ownerID, id string) (*model.AgentSession, error) {
	var session model.AgentSession
	if err := r.db.First(&session, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *Repository) AgentSessions(ownerID string, filter *AgentSessionFilter, before *AgentSessionCursor, limit int) ([]model.AgentSession, error) {
	rows := []model.AgentSession{}
	query := r.db.Where("owner_id = ?", ownerID)
	if filter != nil {
		query = query.Where("application = ? AND space_type = ? AND space_id = ?", filter.Application, filter.SpaceType, filter.SpaceID)
	}
	if before != nil {
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", before.CreatedAt, before.CreatedAt, before.ID)
	}
	err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// SwapAgentSessionLease replaces the lease only if nobody acquired one since
// the caller read prevEpoch; every acquisition bumps the epoch, so at most one
// of any number of racing callers succeeds.
func (r *Repository) SwapAgentSessionLease(ownerID, id string, prevEpoch int64, owner, runID string, epoch int64, expiresAt time.Time) error {
	updated := r.db.Model(&model.AgentSession{}).
		Where("id = ? AND owner_id = ? AND status = ? AND lease_epoch = ?", id, ownerID, "active", prevEpoch).
		Updates(map[string]any{"lease_owner": owner, "active_run_id": runID, "lease_epoch": epoch, "lease_expires_at": expiresAt, "updated_at": time.Now()})
	return agentSessionSwapResult(updated)
}

// CommitAgentSessionRevision advances the revision for the current live writer.
func (r *Repository) CommitAgentSessionRevision(ownerID, id, owner string, epoch, expectedRevision int64, now time.Time) error {
	updated := r.db.Model(&model.AgentSession{}).
		Where("id = ? AND owner_id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at > ? AND revision = ?", id, ownerID, "active", owner, epoch, now, expectedRevision).
		Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now()})
	return agentSessionSwapResult(updated)
}

// ReleaseAgentSessionLease clears the writer but keeps the epoch, so the
// released writer's later writes still fail the epoch check.
func (r *Repository) ReleaseAgentSessionLease(ownerID, id, owner string, epoch int64) error {
	updated := r.db.Model(&model.AgentSession{}).
		Where("id = ? AND owner_id = ? AND lease_owner = ? AND lease_epoch = ?", id, ownerID, owner, epoch).
		Updates(map[string]any{"lease_owner": "", "active_run_id": "", "lease_expires_at": nil, "updated_at": time.Now()})
	return agentSessionSwapResult(updated)
}

// ArchiveAgentSession archives at the expected revision when no writer holds
// the lease epoch the caller read.
func (r *Repository) ArchiveAgentSession(ownerID, id string, expectedRevision, leaseEpoch int64, now time.Time) error {
	updated := r.db.Model(&model.AgentSession{}).
		Where("id = ? AND owner_id = ? AND status = ? AND revision = ? AND lease_epoch = ? AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)", id, ownerID, "active", expectedRevision, leaseEpoch, now).
		Updates(map[string]any{"status": "archived", "archived_at": now, "revision": gorm.Expr("revision + 1"), "updated_at": now})
	return agentSessionSwapResult(updated)
}

func agentSessionSwapResult(updated *gorm.DB) error {
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrAgentSessionChanged
	}
	return nil
}

// CanvasAgentConversationRoot is one canvas conversation eligible for a
// Session mapping: its identity, canvas, latest title and archive state.
type CanvasAgentConversationRoot struct {
	ConversationID string
	CanvasID       string
	Title          string
	CreatedAt      time.Time
	Archived       bool
}

// CanvasAgentConversationRoots lists the owner's canvas conversations without a
// Session mapping yet. Temporary subagent conversations are excluded, keeping
// their existing no-direct-chat rule.
func (r *Repository) CanvasAgentConversationRoots(ownerID string, limit int) ([]CanvasAgentConversationRoot, error) {
	rows := []CanvasAgentConversationRoot{}
	// The root run's creation time orders the directory; SQLite returns MIN(created_at) as text,
	// so the root is joined instead of aggregated.
	err := r.db.Table("cloud_agent_executions e").Joins("JOIN cloud_agent_executions root ON root.user_id = e.user_id AND root.id = COALESCE(NULLIF(e.conversation_id, ''), e.id)").
		Select(`COALESCE(NULLIF(e.conversation_id, ''), e.id) AS conversation_id, e.canvas_id, e.title, root.created_at AS created_at,
		EXISTS (SELECT 1 FROM cloud_agent_executions a WHERE a.user_id = e.user_id AND COALESCE(NULLIF(a.conversation_id, ''), a.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id) AND a.archived_at IS NOT NULL) AS archived`).
		Where("e.user_id = ? AND e.canvas_id <> ''", ownerID).
		Where("NOT EXISTS (SELECT 1 FROM agent_subagent_links l WHERE l.user_id = e.user_id AND l.child_run_id = COALESCE(NULLIF(e.conversation_id, ''), e.id))").
		Where(`NOT EXISTS (SELECT 1 FROM cloud_agent_executions n WHERE n.user_id = e.user_id
			AND COALESCE(NULLIF(n.conversation_id, ''), n.id) = COALESCE(NULLIF(e.conversation_id, ''), e.id)
			AND (n.created_at > e.created_at OR (n.created_at = e.created_at AND n.id > e.id)))`).
		Where("NOT EXISTS (SELECT 1 FROM agent_sessions s WHERE s.owner_id = e.user_id AND s.conversation_id = COALESCE(NULLIF(e.conversation_id, ''), e.id))").
		Order("e.created_at ASC, e.id ASC").Limit(limit).Scan(&rows).Error
	return rows, err
}
