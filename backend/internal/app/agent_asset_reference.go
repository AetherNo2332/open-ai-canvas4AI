package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// agentCompactionThreshold is the history size in bytes above which a Session
// condenses its older turns into one charged summary step.
const agentCompactionThreshold = 4096

// maybeCompactAgentContext condenses one Session's history when it outgrows
// the byte budget: a compaction model step (charged to the same grant) writes
// one summary message, and the context builder drops everything before the
// newest summary. The compaction key is the Session plus the history length,
// so a replay returns the same summary instead of a second charge.
func (r *businessTurnRun) maybeCompactContext() error {
	rows, err := r.s.repo.AgentSessionMessages(r.ownerID, r.session.ID)
	if err != nil {
		return err
	}
	lastSeq, compactable := int64(0), 0
	compacting := []model.AgentSessionMessage{}
	for _, row := range rows {
		if row.Kind == as.MessageKindSummary {
			compactable, compacting = 0, nil
			continue
		}
		compactable += len(row.Body)
		lastSeq = row.Sequence
		compacting = append(compacting, row)
	}
	if compactable < agentCompactionThreshold {
		return nil
	}
	key := fmt.Sprintf("compact:%s:%d", r.session.ID, lastSeq)
	summaryID := "summary:" + agentShortHash(key)
	for _, row := range rows {
		if row.MessageID == summaryID {
			return nil
		}
	}
	taskID, err := r.s.structuredStepTask(r.ownerID, agentBillingApplication(r.session.Application), key)
	if err != nil {
		return err
	}
	if taskID == "" {
		req := cp.StructuredTextRequest{
			Model: r.turn.Model, System: "把下面的会话历史压缩成一段摘要：保留未完成事项、已做的决定、用户补充的要求与所有业务标识。",
			ToolChoice: cp.ToolChoiceNone, IdempotencyKey: key,
			Business: cp.BusinessRef{Application: agentBillingApplication(r.session.Application), ProjectID: r.session.SpaceID, ObjectType: "agent_session", ObjectID: r.session.ID, OperationID: key},
		}
		for _, row := range compacting {
			role := cp.RoleUser
			if row.Role == "assistant" {
				role = cp.RoleAssistant
			}
			req.Messages = append(req.Messages, cp.StructuredMessage{Role: role, Text: row.Body})
		}
		receipt, err := r.s.SubmitStructuredText(context.Background(), cp.Caller{UserID: r.ownerID}, req, r.turn.Assignment.BudgetGrantID)
		if err != nil {
			return err
		}
		taskID = receipt.Snapshot.TaskID
	}
	result, state, err := r.s.StructuredResult(context.Background(), cp.Caller{UserID: r.ownerID}, taskID)
	if err != nil {
		return err
	}
	if state.Execution != cp.ExecutionSucceeded {
		// The compaction step is still running; a later pass picks it up.
		return nil
	}
	summary := strings.TrimSpace(result.Text)
	if summary == "" {
		return nil
	}
	return r.append(as.HistoryMessage{ID: summaryID, Kind: as.MessageKindSummary, Actor: as.ActorSystem, Role: "user", Text: "会话摘要：" + summary})
}

func agentShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

// resolveAgentAssetReference checks one asset reference for the calling work:
// the resource must belong to the owner and exist; the caller can then use it
// and reports trace it back through the operation receipt.
func (s *Service) resolveAgentAssetReference(ownerID, resourceID string) (*model.Resource, error) {
	resource, err := s.repo.ResourceForUser(ownerID, strings.TrimSpace(resourceID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, as.NewError(as.CodeNotFound, "素材不存在或不属于当前账号")
		}
		return nil, err
	}
	return resource, nil
}
