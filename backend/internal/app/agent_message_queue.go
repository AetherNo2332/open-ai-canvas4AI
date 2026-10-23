package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type AgentConversationMessageRequest struct {
	Mode              string            `json:"mode"`
	ExpectedRunID     string            `json:"expectedRunId"`
	ReplyToQuestionID string            `json:"replyToQuestionId,omitempty"`
	Request           CloudAgentRequest `json:"request"`
}
type AgentMessageReceipt struct {
	MessageID      string `json:"messageId"`
	ConversationID string `json:"conversationId"`
	Delivery       string `json:"delivery"`
	RunID          string `json:"runId,omitempty"`
	QueueRevision  int64  `json:"queueRevision"`
}
type AgentMessageQueueView struct {
	model.AgentConversationQueue
	LatestRunID string                     `json:"latestRunId"`
	Items       []model.AgentQueuedMessage `json:"items"`
}
type AgentQueuedMessageUpdate struct {
	Revision int64   `json:"revision"`
	Prompt   *string `json:"prompt,omitempty"`
	Action   string  `json:"action,omitempty"`
}
type AgentMessageQueueUpdate struct {
	Revision int64  `json:"revision"`
	Action   string `json:"action"`
}
type AgentUISetting = model.AgentUISetting

var errAgentQuestionAnswerPending = errors.New("当前问题已收到回答，请等待处理")
var errAgentQuestionStale = errors.New("问题已更新，请刷新后回答")

func agentMessageError(err error) error {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return kernel.NotFound("Agent 对话或待发消息不存在")
	case errors.Is(err, repository.ErrCloudAgentStaleParent):
		return &kernel.AppError{Status: 409, Code: 409, Reason: "agent_conversation_stale_parent", Message: err.Error()}
	case errors.Is(err, repository.ErrAgentMessageQuestionPending):
		return &kernel.AppError{Status: 409, Code: 409, Reason: "agent_question_pending", Message: err.Error()}
	case errors.Is(err, errAgentQuestionAnswerPending):
		return &kernel.AppError{Status: 409, Code: 409, Reason: "agent_question_answer_pending", Message: err.Error()}
	case errors.Is(err, errAgentQuestionStale):
		return &kernel.AppError{Status: 409, Code: 409, Reason: "agent_question_stale", Message: err.Error()}
	case errors.Is(err, repository.ErrCreationConflict):
		return creationConflict(err.Error())
	case errors.Is(err, repository.ErrAgentMessageQueueFull), errors.Is(err, repository.ErrAgentMessageStorageLimit):
		return QuotaExceeded(err.Error())
	default:
		return err
	}
}
func agentMessageReceipt(row *model.AgentQueuedMessage, q *model.AgentConversationQueue) *AgentMessageReceipt {
	delivery := row.Status
	if delivery == "dispatching" {
		delivery = "queued"
	}
	return &AgentMessageReceipt{MessageID: row.ID, ConversationID: row.ConversationID, Delivery: delivery, RunID: row.RunID, QueueRevision: q.Revision}
}
func (s *Service) requireAgentQueueOwner(userID, conversationID string) error {
	if userID == "" {
		return kernel.Unauthorized("请先登录")
	}
	if err := validateCloudAgentID(conversationID, "会话 ID", 80); err != nil {
		return err
	}
	owned, err := s.repo.AgentMessageConversationOwned(userID, conversationID)
	if err != nil {
		return err
	}
	if !owned {
		return kernel.NotFound("Agent 对话不存在")
	}
	return nil
}
func (s *Service) AgentMessageQueue(userID, conversationID string) (*AgentMessageQueueView, error) {
	if err := s.requireAgentQueueOwner(userID, conversationID); err != nil {
		return nil, err
	}
	var result *AgentMessageQueueView
	err := s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		rows, err := tx.AgentQueuedMessages(userID, conversationID)
		if err != nil {
			return err
		}
		result = &AgentMessageQueueView{AgentConversationQueue: *q, LatestRunID: latest.ID, Items: rows}
		return nil
	})
	return result, agentMessageError(err)
}
func (s *Service) SendAgentConversationMessage(userID, conversationID string, input AgentConversationMessageRequest) (*AgentMessageReceipt, error) {
	if err := s.requireAgentQueueOwner(userID, conversationID); err != nil {
		return nil, err
	}
	if input.Mode != "interject" && input.Mode != "queue" {
		return nil, BadAuthRequest("发送方式无效")
	}
	if err := validateCloudAgentRequest(&input.Request); err != nil {
		return nil, err
	}
	if err := validateCloudAgentID(input.ExpectedRunID, "当前轮次", 80); err != nil {
		return nil, err
	}
	if input.ReplyToQuestionID != "" && (input.Mode != "interject" || validateCloudAgentID(input.ReplyToQuestionID, "问题标识", 160) != nil) {
		return nil, BadAuthRequest("问题回答关联无效")
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	if _, err = s.repo.CanvasProjectForUser(userID, input.Request.CanvasID); err != nil {
		return nil, agentMessageError(err)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	requestJSON, err := json.Marshal(input.Request)
	if err != nil {
		return nil, err
	}
	row := &model.AgentQueuedMessage{ID: "am" + strings.TrimPrefix(cloudAgentID(userID, input.Request.IdempotencyKey), "ag"), UserID: userID, CanvasID: input.Request.CanvasID, ConversationID: conversationID, IdempotencyKey: input.Request.IdempotencyKey, Fingerprint: hex.EncodeToString(digest[:]), Mode: input.Mode, RequestJSON: string(requestJSON), Prompt: input.Request.Prompt, ReplyToQuestionID: input.ReplyToQuestionID}
	err = s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		prior, lookup := tx.AgentMessageByKey(userID, row.IdempotencyKey)
		if lookup == nil {
			if prior.Fingerprint != row.Fingerprint || prior.ConversationID != conversationID {
				return repository.ErrCreationConflict
			}
			*row = *prior
			return nil
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if latest.ID != input.ExpectedRunID {
			return repository.ErrCloudAgentStaleParent
		}
		if latest.CanvasID != input.Request.CanvasID {
			return kernel.Forbidden("不能跨画布追加 Agent 消息")
		}
		if q.AwaitingQuestionID != "" && input.Mode == "interject" && (input.ReplyToQuestionID != q.AwaitingQuestionID || q.QuestionRunID != latest.ID) {
			return repository.ErrAgentMessageQuestionPending
		}
		if input.ReplyToQuestionID != "" && input.ReplyToQuestionID != q.AwaitingQuestionID {
			return errAgentQuestionStale
		}
		if input.ReplyToQuestionID != "" {
			pending, err := tx.AgentQuestionAnswerPending(userID, conversationID, input.ReplyToQuestionID)
			if err != nil {
				return err
			}
			if pending {
				return errAgentQuestionAnswerPending
			}
		}
		if input.Mode == "interject" && cloudAgentAcceptsInterjection(latest.Status) && utf8.RuneCountInString(row.Prompt) > cloudAgentInterjectionMaxRunes {
			return BadAuthRequest("插话内容超过 4000 字，请改为排队")
		}
		usage, err := tx.UserStorageUsage(userID)
		if err != nil {
			return err
		}
		immediateInterjection := input.Mode == "interject" && cloudAgentAcceptsInterjection(latest.Status)
		incoming := len(row.RequestJSON) + len(row.Prompt) + len(row.Fingerprint)
		if immediateInterjection {
			incoming = len(row.Fingerprint)
		}
		if err = validateTaskDataGrowthQuotaWithPolicy(usage, int64(incoming), policy.Resource); err != nil {
			return err
		}
		conversationLimit, userLimit := policy.Task.AgentMaxQueuedMessagesPerConversation, policy.Task.AgentMaxQueuedMessagesPerUser
		// Immediate receipts and the single explicit answer must remain usable
		// when the ordinary task queue is full; answers are unique per question.
		if immediateInterjection || input.ReplyToQuestionID != "" {
			conversationLimit, userLimit = math.MaxInt, math.MaxInt
		}
		if err = tx.InsertAgentQueuedMessage(row, q, conversationLimit, userLimit); err != nil {
			return err
		}
		if immediateInterjection {
			if _, err = interjectCloudAgentRepository(tx, userID, latest.ID, row.ID, row.Prompt); err != nil {
				return err
			}
			if err = tx.FinishAgentQueuedMessage(row, "interjected", latest.ID); err != nil {
				return err
			}
			return tx.SaveAgentConversationQueue(q)
		}
		return nil
	})
	if err != nil {
		return nil, agentMessageError(err)
	}
	if row.Status == "queued" {
		preferred := ""
		if input.Mode == "interject" {
			preferred = row.ID
		}
		if _, err = s.dispatchAgentConversation(userID, conversationID, preferred); err != nil {
			return nil, err
		}
	}
	s.wakeAgentMessages()
	return s.agentMessageReceipt(userID, conversationID, row.ID)
}
func (s *Service) agentMessageReceipt(userID, conversationID, id string) (*AgentMessageReceipt, error) {
	row, err := s.repo.AgentQueuedMessage(userID, conversationID, id)
	if err != nil {
		return nil, agentMessageError(err)
	}
	q, err := s.repo.AgentConversationQueue(userID, conversationID)
	if err != nil {
		return nil, agentMessageError(err)
	}
	return agentMessageReceipt(row, q), nil
}
func (s *Service) UpdateAgentQueuedMessage(userID, conversationID, id string, input AgentQueuedMessageUpdate) (*AgentMessageReceipt, error) {
	if err := s.requireAgentQueueOwner(userID, conversationID); err != nil {
		return nil, err
	}
	if input.Revision < 1 || (input.Prompt == nil) == (input.Action == "") || (input.Action != "" && input.Action != "interject") {
		return nil, BadAuthRequest("请选择编辑或插队")
	}
	if input.Prompt != nil {
		if err := validateCloudAgentPrompt(*input.Prompt, 16000); err != nil {
			return nil, err
		}
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	err = s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage(userID, conversationID, id)
		if err != nil {
			return err
		}
		if row.Revision != input.Revision || row.Status != "queued" {
			return repository.ErrCreationConflict
		}
		if input.Prompt != nil {
			var request CloudAgentRequest
			if err = json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
				return err
			}
			request.Prompt = strings.TrimSpace(*input.Prompt)
			raw, err := json.Marshal(request)
			if err != nil {
				return err
			}
			usage, err := tx.UserStorageUsage(userID)
			if err != nil {
				return err
			}
			if err = validateTaskDataGrowthQuotaWithPolicy(usage, int64(len(raw)+len(request.Prompt)-len(row.RequestJSON)-len(row.Prompt)), policy.Resource); err != nil {
				return err
			}
			if err = tx.UpdateAgentQueuedMessageBody(row, string(raw), request.Prompt); err != nil {
				return err
			}
		} else {
			if q.AwaitingQuestionID != "" {
				return repository.ErrAgentMessageQuestionPending
			}
			if cloudAgentAcceptsInterjection(latest.Status) {
				if utf8.RuneCountInString(row.Prompt) > cloudAgentInterjectionMaxRunes {
					return BadAuthRequest("插话内容超过 4000 字")
				}
				if _, err = interjectCloudAgentRepository(tx, userID, latest.ID, row.ID, row.Prompt); err != nil {
					return err
				}
				if err = tx.FinishAgentQueuedMessage(row, "interjected", latest.ID); err != nil {
					return err
				}
			} else {
				if err = tx.PromoteAgentQueuedMessage(row); err != nil {
					return err
				}
			}
		}
		return tx.SaveAgentConversationQueue(q)
	})
	if err != nil {
		return nil, agentMessageError(err)
	}
	if input.Action == "interject" {
		if _, err = s.dispatchAgentConversation(userID, conversationID, id); err != nil {
			return nil, err
		}
	}
	s.wakeAgentMessages()
	return s.agentMessageReceipt(userID, conversationID, id)
}
func (s *Service) CancelAgentQueuedMessage(userID, conversationID, id string, revision int64) (*AgentMessageReceipt, error) {
	if err := s.requireAgentQueueOwner(userID, conversationID); err != nil {
		return nil, err
	}
	err := s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage(userID, conversationID, id)
		if err != nil {
			return err
		}
		if row.Revision != revision || row.Status != "queued" {
			return repository.ErrCreationConflict
		}
		if err = tx.FinishAgentQueuedMessage(row, "cancelled", ""); err != nil {
			return err
		}
		return tx.SaveAgentConversationQueue(q)
	})
	if err != nil {
		return nil, agentMessageError(err)
	}
	return s.agentMessageReceipt(userID, conversationID, id)
}
func (s *Service) UpdateAgentMessageQueue(userID, conversationID string, input AgentMessageQueueUpdate) (*AgentMessageQueueView, error) {
	if err := s.requireAgentQueueOwner(userID, conversationID); err != nil {
		return nil, err
	}
	if input.Revision < 1 || (input.Action != "pause" && input.Action != "resume") {
		return nil, BadAuthRequest("队列操作无效")
	}
	err := s.repo.WithAgentConversation(userID, conversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		if q.Revision != input.Revision {
			return repository.ErrCreationConflict
		}
		q.Paused = input.Action == "pause"
		q.PauseReason = ""
		if q.Paused {
			q.PauseReason = "用户暂停"
			if err := tx.InvalidateAgentMessageClaims(userID, conversationID); err != nil {
				return err
			}
		}
		return tx.SaveAgentConversationQueue(q)
	})
	if err != nil {
		return nil, agentMessageError(err)
	}
	s.wakeAgentMessages()
	return s.AgentMessageQueue(userID, conversationID)
}
func (s *Service) AgentUISettings(userID string) (*model.AgentUISetting, error) {
	if userID == "" {
		return nil, kernel.Unauthorized("请先登录")
	}
	return s.repo.AgentUISettings(userID)
}
func (s *Service) UpdateAgentUISettings(userID string, input model.AgentUISetting) (*model.AgentUISetting, error) {
	if userID == "" {
		return nil, kernel.Unauthorized("请先登录")
	}
	if input.Revision < 0 || (input.DefaultSendMode != "interject" && input.DefaultSendMode != "queue") {
		return nil, BadAuthRequest("默认发送方式无效")
	}
	input.UserID = userID
	err := s.repo.SaveAgentUISettings(&input)
	return &input, agentMessageError(err)
}
