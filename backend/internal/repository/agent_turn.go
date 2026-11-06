package repository

import (
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// ClaimRunnableAgentTurns conditionally leases the oldest due turn of each
// Session. A live lease cannot be stolen by another backend instance; after it
// expires, another instance can resume the idempotent turn.
func (r *Repository) ClaimRunnableAgentTurns(owner string, now, until time.Time, limit int) ([]model.AgentTurn, error) {
	candidates := []model.AgentTurn{}
	err := r.db.Where("status IN ?", []string{"pending", "running"}).
		Where("NOT EXISTS (SELECT 1 FROM agent_turns e WHERE e.session_id = agent_turns.session_id AND e.status IN ? AND e.message_seq < agent_turns.message_seq)", []string{"pending", "running"}).
		Where("next_at IS NULL OR next_at <= ?", now).
		Where("claim_owner = ? OR claim_expires_at IS NULL OR claim_expires_at <= ?", owner, now).
		Order("created_at ASC, message_seq ASC").Limit(limit).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	claimed := make([]model.AgentTurn, 0, len(candidates))
	for _, candidate := range candidates {
		updated := r.db.Model(&model.AgentTurn{}).
			Where("id = ? AND status IN ?", candidate.ID, []string{"pending", "running"}).
			Where("next_at IS NULL OR next_at <= ?", now).
			Where("claim_owner = ? OR claim_expires_at IS NULL OR claim_expires_at <= ?", owner, now).
			Updates(map[string]any{"claim_owner": owner, "claim_expires_at": until, "updated_at": now})
		if updated.Error != nil {
			return nil, updated.Error
		}
		if updated.RowsAffected == 0 {
			continue
		}
		candidate.ClaimOwner, candidate.ClaimExpiresAt, candidate.UpdatedAt = owner, &until, now
		claimed = append(claimed, candidate)
	}
	return claimed, nil
}

// UpdateClaimedAgentTurn updates a turn only while this backend still owns its
// claim. A stale worker that resumes after takeover cannot overwrite the winner.
func (r *Repository) UpdateClaimedAgentTurn(id, owner string, fields map[string]any) (bool, error) {
	fields["updated_at"] = time.Now()
	updated := r.db.Model(&model.AgentTurn{}).Where("id = ? AND claim_owner = ?", id, owner).Updates(fields)
	return updated.RowsAffected == 1, updated.Error
}

// FailClaimedAgentTurn records a terminal turn and its user-visible notice in
// one transaction. If the notice cannot be appended, the turn remains runnable.
func (r *Repository) FailClaimedAgentTurn(turn model.AgentTurn, reason string, notice model.AgentSessionMessage, now time.Time) (bool, error) {
	updated := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.AgentTurn{}).Where("id = ? AND claim_owner = ?", turn.ID, turn.ClaimOwner).Updates(map[string]any{
			"status":           "failed",
			"error":            reason,
			"next_at":          nil,
			"claim_owner":      "",
			"claim_expires_at": nil,
			"updated_at":       now,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		if err := appendInboxMessage(tx, turn.OwnerID, turn.SessionID, notice, now); err != nil {
			return err
		}
		updated = true
		return nil
	})
	return updated, err
}

// OwnersWithPendingOutbox lists owners that have undelivered items.
func (r *Repository) OwnersWithPendingOutbox(limit int) ([]string, error) {
	owners := []string{}
	err := r.db.Model(&model.AgentOutbox{}).Where("status = ?", "pending").Distinct("owner_id").Limit(limit).Pluck("owner_id", &owners).Error
	return owners, err
}
