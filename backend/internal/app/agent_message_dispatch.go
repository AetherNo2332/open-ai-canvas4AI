package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func (s *Service) wakeAgentMessages() {
	if s.agentMessageWake == nil {
		return
	}
	select {
	case s.agentMessageWake <- struct{}{}:
	default:
	}
}
func (s *Service) startAgentMessages() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if !s.IsDraining() {
				if _, err := s.DispatchAgentMessages(64); err != nil {
					log.Printf("agent message dispatch: %v", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.agentMessageWake:
			}
		}
	})
}
func (s *Service) DispatchAgentMessages(limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 64
	}
	queues, err := s.repo.PendingAgentMessageConversations(limit)
	if err != nil {
		return 0, err
	}
	dispatched := 0
	for _, q := range queues {
		ok, err := s.dispatchAgentConversation(q.UserID, q.ConversationID, "")
		if err != nil {
			return dispatched, err
		}
		if ok {
			dispatched++
		}
	}
	return dispatched, nil
}

func (s *Service) dispatchAgentConversation(userID, conversationID, preferredID string) (bool, error) {
	if s.IsDraining() {
		return false, nil
	}
	var message *model.AgentQueuedMessage
	err := s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		if !cloudAgentRunTerminal(latest.Status) || latest.CleanupPending || (q.Paused && preferredID == "") {
			return nil
		}
		rows, err := tx.AgentQueuedMessages(userID, conversationID)
		if err != nil || len(rows) == 0 {
			return err
		}
		candidate := rows[0]
		if q.AwaitingQuestionID != "" && preferredID == "" {
			found := false
			for _, row := range rows {
				if row.ReplyToQuestionID == q.AwaitingQuestionID {
					candidate = row
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
		if preferredID != "" {
			found := false
			for _, row := range rows {
				if row.ID == preferredID {
					candidate = row
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
		if q.AwaitingQuestionID != "" && (candidate.ReplyToQuestionID != q.AwaitingQuestionID || q.QuestionRunID != latest.ID) {
			return nil
		}
		if candidate.ReplyToQuestionID != "" && candidate.ReplyToQuestionID != q.AwaitingQuestionID {
			q.Paused = true
			q.PauseReason = "问题已更新，请核对待发回答"
			return tx.SaveAgentConversationQueue(q)
		}
		if candidate.NextAttemptAt != nil && candidate.NextAttemptAt.After(time.Now()) {
			return nil
		}
		if candidate.Status == "dispatching" && candidate.LeaseUntil != nil && candidate.LeaseUntil.After(time.Now()) {
			return nil
		}
		if err = tx.ClaimAgentQueuedMessage(&candidate, newID(), latest.ID, time.Now().UTC().Add(time.Minute)); err != nil {
			if errors.Is(err, repository.ErrCreationConflict) {
				return nil
			}
			return err
		}
		if err = tx.SaveAgentConversationQueue(q); err != nil {
			return err
		}
		message = &candidate
		return nil
	})
	if err != nil {
		return false, agentMessageError(err)
	}
	if message == nil {
		return false, nil
	}
	var request CloudAgentRequest
	err = json.Unmarshal([]byte(message.RequestJSON), &request)
	var admission repository.CloudAgentAdmission
	if err == nil {
		_, err = s.createCloudAgentRunScoped(userID, request, message.DispatchParentID, &cloudAgentRunScope{Prepared: &admission})
	}
	if err == nil && admission.Execution == nil {
		err = creationConflict("消息身份已被其他请求使用，请核对原运行")
	}
	if err == nil {
		policy, policyErr := s.RuntimePolicy()
		err = policyErr
		if err == nil {
			err = s.repo.CommitAgentMessageAdmission(*message, admission, policy.Task.ActiveTaskLimit, policy.Resource.TaskCount, gigabytes(policy.Resource.TaskDataGB))
		}
	}
	if err == nil {
		return true, nil
	}
	return false, s.recoverAgentMessageClaim(*message, err)
}

func (s *Service) recoverAgentMessageClaim(message model.AgentQueuedMessage, cause error) error {
	transient := errors.Is(cause, repository.ErrCreationConflict) || errors.Is(cause, repository.ErrCloudAgentStaleParent) || errors.Is(cause, repository.ErrActiveTaskLimit)
	var appErr *kernel.AppError
	if errors.As(cause, &appErr) {
		transient = appErr.Retryable || appErr.Status == 409 || appErr.Status == 429 || appErr.Status >= 500
	}
	if !errors.As(cause, &appErr) && !errors.Is(cause, repository.ErrInsufficientCredits) && !errors.Is(cause, repository.ErrLogicalModelUnavailable) && !errors.Is(cause, repository.ErrAgentMessageStorageLimit) {
		// Infrastructure failures have no admission receipt. Keep the original
		// identity and let the bounded retry schedule reconcile it.
		transient = true
	}
	if message.Attempts >= 6 {
		transient = false
	}
	err := s.repo.WithAgentConversation(message.UserID, message.ConversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage(message.UserID, message.ConversationID, message.ID)
		if err != nil {
			return err
		}
		if row.Status != "dispatching" || row.DispatchToken != message.DispatchToken || row.Revision != message.Revision {
			return nil
		}
		var next *time.Time
		if transient {
			delay := time.Duration(1<<min(row.Attempts, 6)) * time.Second
			at := time.Now().UTC().Add(delay)
			next = &at
		} else {
			q.Paused = true
			q.PauseReason = agentMessageFailureText(cause)
		}
		if err = tx.ReleaseAgentMessageClaim(row, agentMessageFailureText(cause), next); err != nil {
			return err
		}
		return tx.SaveAgentConversationQueue(q)
	})
	return agentMessageError(err)
}

func agentMessageFailureText(cause error) string {
	switch {
	case errors.Is(cause, repository.ErrInsufficientCredits):
		return "积分不足，请补充额度"
	case errors.Is(cause, repository.ErrLogicalModelUnavailable):
		return "所选模型不可用，请选择模型后重新发送"
	case errors.Is(cause, repository.ErrAgentMessageStorageLimit):
		return "任务历史容量已满，请清理后重试"
	}
	var appErr *kernel.AppError
	if errors.As(cause, &appErr) && appErr.Status >= 400 && appErr.Status < 500 {
		return truncateRunes(appErr.Message, 300)
	}
	return "暂时无法发送，请稍后重试"
}

// Pause before choosing the cancellation target. A dispatch which committed
// first becomes the latest target; one still preparing loses its claim token.
func (s *Service) pauseAgentMessagesForStop(userID string, run *model.CloudAgentExecution) (*model.CloudAgentExecution, error) {
	if !s.repo.HasAgentMessageQueue() {
		return run, nil
	}
	conversationID := run.ConversationID
	if conversationID == "" {
		conversationID = run.ID
	}
	var target *model.CloudAgentExecution
	err := s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		q.Paused = true
		q.PauseReason = "用户停止"
		q.LastSettledRunID = latest.ID
		if err := tx.InvalidateAgentMessageClaims(userID, conversationID); err != nil {
			return err
		}
		if err := tx.SaveAgentConversationQueue(q); err != nil {
			return err
		}
		var err error
		target, err = tx.CloudAgent(userID, latest.ID)
		return err
	})
	return target, agentMessageError(err)
}
