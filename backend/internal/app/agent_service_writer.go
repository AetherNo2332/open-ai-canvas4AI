package app

import (
	"errors"
	"time"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// acquireAgentSessionWriter grants the single live writer of a Session. The
// swap is conditional on the epoch read here, so racing callers cannot both
// win; losers get session_conflict.
func (s *Service) acquireAgentSessionWriter(ownerID, sessionID, owner, runID string) (as.Lease, error) {
	session, err := s.agentSessionForOwner(ownerID, sessionID)
	if err != nil {
		return as.Lease{}, err
	}
	if session.Status != as.SessionActive {
		return as.Lease{}, as.NewError(as.CodeSessionConflict, "会话已归档")
	}
	now := time.Now()
	next, err := agentSessionLease(*session).Acquire(owner, runID, now, agentSessionLeaseTTL)
	if err != nil {
		return as.Lease{}, err
	}
	if err := s.repo.SwapAgentSessionLease(ownerID, session.ID, session.LeaseEpoch, next.Owner, next.RunID, next.Epoch, next.ExpiresAt); err != nil {
		if errors.Is(err, repository.ErrAgentSessionChanged) {
			return as.Lease{}, as.NewError(as.CodeSessionConflict, "会话正在处理上一条请求，请稍后再试")
		}
		return as.Lease{}, err
	}
	return next, nil
}

// commitAgentSessionWrite advances the Session revision for the live writer
// only; an older epoch, an expired lease or a stale revision is rejected.
func (s *Service) commitAgentSessionWrite(ownerID, sessionID string, lease as.Lease, expectedRevision int64) (int64, error) {
	session, err := s.agentSessionForOwner(ownerID, sessionID)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	if err := agentSessionLease(*session).Check(lease.Owner, lease.Epoch, now); err != nil {
		return 0, err
	}
	if err := s.repo.CommitAgentSessionRevision(ownerID, session.ID, lease.Owner, lease.Epoch, expectedRevision, now); err != nil {
		if errors.Is(err, repository.ErrAgentSessionChanged) {
			return 0, as.NewError(as.CodeSessionConflict, "会话已有更新，请同步后再写入")
		}
		return 0, err
	}
	return expectedRevision + 1, nil
}

func (s *Service) releaseAgentSessionWriter(ownerID, sessionID string, lease as.Lease) error {
	err := s.repo.ReleaseAgentSessionLease(ownerID, sessionID, lease.Owner, lease.Epoch)
	if errors.Is(err, repository.ErrAgentSessionChanged) {
		return as.NewError(as.CodeStaleLease, "会话写入者已更换")
	}
	return err
}

func agentSessionLease(row model.AgentSession) as.Lease {
	lease := as.Lease{Owner: row.LeaseOwner, RunID: row.ActiveRunID, Epoch: row.LeaseEpoch}
	if row.LeaseExpiresAt != nil {
		lease.ExpiresAt = *row.LeaseExpiresAt
	}
	return lease
}

// ensureCanvasAgentSessions maps every canvas conversation of the owner onto
// one Session root. The key is the conversation identity, so concurrent or
// repeated runs insert nothing new; Pi storage and history stay where they are.
func (s *Service) ensureCanvasAgentSessions(ownerID string) error {
	def, ok := s.agentRegistry().Definition(canvasAgentDefinitionID)
	if !ok {
		return as.NewError(as.CodeInternal, "画布 Agent 定义未注册")
	}
	for {
		roots, err := s.repo.CanvasAgentConversationRoots(ownerID, agentSessionMappingBatch)
		if err != nil {
			return err
		}
		for _, root := range roots {
			key := "canvas-conversation:" + root.ConversationID
			row := &model.AgentSession{
				ID: agentSessionID(ownerID, key), OwnerID: ownerID, IdempotencyKey: key, Fingerprint: key,
				DefinitionID: def.ID, DefinitionVersion: def.Version, Role: def.Role,
				Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: root.CanvasID,
				ConversationID: root.ConversationID, Title: firstNonEmpty(root.Title, def.Title), Status: as.SessionActive, Revision: 1,
				CreatedAt: root.CreatedAt,
			}
			if root.Archived {
				archivedAt := time.Now()
				row.Status, row.ArchivedAt = as.SessionArchived, &archivedAt
			}
			if _, _, err := s.repo.CreateAgentSession(row); err != nil {
				return err
			}
		}
		if len(roots) < agentSessionMappingBatch {
			return nil
		}
	}
}
