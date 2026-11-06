package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

const (
	agentRunnerInterval  = 2 * time.Second
	agentRunnerBatch     = 20
	agentTurnMaxAttempts = 20
	agentTurnRetryDelay  = 3 * time.Second
)

var agentRunnerWake = make(chan struct{}, 1)

func (s *Service) wakeAgentRunner() {
	select {
	case agentRunnerWake <- struct{}{}:
	default:
	}
}

// startAgentRunner drives queued business turns and pending deliveries in the
// background, on a short interval or when a message arrives. Backend instances
// first claim an AgentTurn, then use the Session writer lease for each model or
// tool step; platform idempotency makes a resumed submission safe to replay.
func (s *Service) startAgentRunner() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(agentRunnerInterval)
		defer ticker.Stop()
		for {
			if !s.IsDraining() {
				if _, err := s.runPendingAgentTurns(agentRunnerBatch); err != nil {
					slog.Warn("agent runner pass failed", "error", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-agentRunnerWake:
			}
		}
	})
}

// runPendingAgentTurns delivers pending outbox items, then advances each due
// turn once. It returns how many turns finished in this pass.
func (s *Service) runPendingAgentTurns(limit int) (int, error) {
	owners, err := s.repo.OwnersWithPendingOutbox(limit)
	if err != nil {
		return 0, err
	}
	for _, owner := range owners {
		if _, err := s.deliverAgentOutbox(owner, limit); err != nil {
			slog.Warn("agent outbox delivery failed", "owner", owner, "error", err)
		}
	}
	now := time.Now()
	turns, err := s.repo.ClaimRunnableAgentTurns(s.agentRuntimeOwner(), now, now.Add(agentSessionLeaseTTL), limit)
	if err != nil {
		return 0, err
	}
	finished := 0
	for _, turn := range turns {
		done, err := s.advanceQueuedTurn(turn)
		if err != nil {
			return finished, err
		}
		if done {
			finished++
		}
	}
	return finished, nil
}

// advanceQueuedTurn moves one turn forward and records the outcome. A failure
// is shown to the user as a system message rather than left hanging.
func (s *Service) advanceQueuedTurn(turn model.AgentTurn) (bool, error) {
	session, err := s.agentSessionForOwner(turn.OwnerID, turn.SessionID)
	if err != nil || session.Status != as.SessionActive {
		return s.failQueuedTurn(turn, "会话不可用")
	}
	selection, err := s.agentTurnModel(turn, session)
	if err != nil {
		return s.retryOrFailQueuedTurn(turn, err)
	}
	assignment := AssignmentView{ExecutorSessionID: session.ID, BudgetGrantID: turn.BudgetGrantID}
	_, done, err := s.advanceBusinessTurn(turn.OwnerID, businessTurn{Assignment: assignment, MessageID: turn.MessageID, Text: turn.Text, Model: selection})
	switch {
	case err == nil && done:
		return s.updateQueuedTurn(turn, map[string]any{"status": "done", "error": "", "next_at": nil, "claim_owner": "", "claim_expires_at": nil})
	case err == nil:
		// Waiting for the worker to finish a model step or a generation. The
		// runner's own interval is the backoff; keeping this due lets an explicit
		// wake or a test-driven worker completion resume immediately.
		_, updateErr := s.updateQueuedTurn(turn, map[string]any{"status": "running", "next_at": nil})
		return false, updateErr
	case as.IsCode(err, as.CodeSessionConflict) || as.IsCode(err, as.CodeStaleLease):
		// Another writer holds the Session; try again shortly.
		next := time.Now().Add(agentTurnRetryDelay)
		_, updateErr := s.updateQueuedTurn(turn, map[string]any{"next_at": next})
		return false, updateErr
	}
	return s.retryOrFailQueuedTurn(turn, err)
}

func (s *Service) retryOrFailQueuedTurn(turn model.AgentTurn, turnErr error) (bool, error) {
	if !agentTurnRetryable(turnErr) || turn.Attempts+1 >= agentTurnMaxAttempts {
		return s.failQueuedTurn(turn, turnErr.Error())
	}
	next := time.Now().Add(agentTurnRetryDelay)
	_, err := s.updateQueuedTurn(turn, map[string]any{"attempts": turn.Attempts + 1, "next_at": next, "error": truncateRunes(turnErr.Error(), 500)})
	return false, err
}

func (s *Service) updateQueuedTurn(turn model.AgentTurn, fields map[string]any) (bool, error) {
	return s.repo.UpdateClaimedAgentTurn(turn.ID, turn.ClaimOwner, fields)
}

func (s *Service) failQueuedTurn(turn model.AgentTurn, reason string) (bool, error) {
	notice := model.AgentSessionMessage{ID: newID(), MessageID: turn.MessageID + ":failed", Kind: as.MessageKindMessage, Actor: as.ActorSystem, Role: "user", Body: "Agent 没能回复：" + reason}
	return s.repo.FailClaimedAgentTurn(turn, truncateRunes(reason, 500), notice, time.Now())
}

func agentTurnRetryable(err error) bool {
	if agentErr, ok := as.AsError(err); ok {
		return agentErr.Retryable
	}
	if platformErr, ok := cp.AsError(err); ok {
		return platformErr.Retryable
	}
	return true
}

// agentTurnModel returns the turn's model, choosing it on the first pass and
// freezing it on the turn. The choice is the space's own text model when the
// business has one (the drama project's text model), else the first text model
// available to the user, the same list the drama model picker offers.
func (s *Service) agentTurnModel(turn model.AgentTurn, session *model.AgentSession) (cp.ModelSelection, error) {
	var frozen cp.ModelSelection
	if turn.ModelJSON != "" && json.Unmarshal([]byte(turn.ModelJSON), &frozen) == nil {
		return frozen, nil
	}
	choice := DramaModelChoice{}
	if session.Application == as.ApplicationDrama {
		if production, err := s.repo.DramaProduction(session.SpaceID); err == nil && production.UserID == turn.OwnerID {
			choice = parseDramaSettings(production.SettingsJSON).Models.Text
		}
	}
	if choice.Empty() {
		options, _, err := s.dramaModelOptions(turn.OwnerID, "text")
		if err != nil {
			return cp.ModelSelection{}, err
		}
		if len(options) == 0 {
			return cp.ModelSelection{}, as.NewError(as.CodeInvalidRequest, "还没有可用的文本模型，请在模型设置中启用一个文本模型")
		}
		choice = options[0].Choice
	}
	selection := cp.ModelSelection{LogicalModelID: choice.LogicalModelID}
	if selection.LogicalModelID == "" {
		selection.ChannelID, selection.ModelKey = choice.ChannelID, choice.ModelKey
	}
	raw, _ := json.Marshal(selection)
	updated, err := s.updateQueuedTurn(turn, map[string]any{"model_json": string(raw)})
	if err != nil {
		return cp.ModelSelection{}, err
	}
	if !updated {
		return cp.ModelSelection{}, as.NewError(as.CodeSessionConflict, "轮次已由其他运行器接管")
	}
	return selection, nil
}
