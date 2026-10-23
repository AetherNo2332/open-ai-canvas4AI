package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

var ErrAgentMessageQueueFull = errors.New("待发消息已达到上限")
var ErrAgentMessageQuestionPending = errors.New("请先回答当前问题")
var ErrAgentMessageStorageLimit = errors.New("账号任务历史容量或数量已达到上限")

func (r *Repository) HasAgentMessageQueue() bool { return r.agentMessageQueueEnabled }

func (r *Repository) requireAgentMessageAdmission(run *model.CloudAgentExecution) error {
	if !r.agentMessageQueueEnabled || run.ParentID == "" || run.ConversationID == run.ID {
		return nil
	}
	queue, err := r.AgentConversationQueue(run.UserID, run.ConversationID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = r.WithAgentConversation(run.UserID, run.ConversationID, func(_ *Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
			queue = q
			return nil
		})
	}
	if err != nil {
		return err
	}
	if queue.AwaitingQuestionID != "" {
		return ErrAgentMessageQuestionPending
	}
	var claimed int64
	if err = r.db.Model(&model.AgentQueuedMessage{}).Where("user_id = ? AND conversation_id = ? AND status = ?", run.UserID, run.ConversationID, "dispatching").Count(&claimed).Error; err != nil {
		return err
	}
	if claimed > 0 {
		return ErrCreationConflict
	}
	return nil
}

func (r *Repository) observeAgentMessageQueue(run *model.CloudAgentExecution, previousEvents int) error {
	if !r.agentMessageQueueEnabled {
		return nil
	}
	identity := run.ConversationID
	if identity == "" {
		identity = run.ID
	}
	queue, err := r.AgentConversationQueue(run.UserID, identity)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	changed := false
	for _, event := range run.Journal {
		if event.Sequence > previousEvents {
			before := queue.AwaitingQuestionID
			r.observeAgentQueueEvent(queue, run, event.EventJSON)
			changed = changed || before != queue.AwaitingQuestionID
		}
	}
	if isTerminalCloudAgentRunStatus(run.Status) && run.Status != "completed" && queue.LastSettledRunID != run.ID {
		queue.Paused = true
		queue.PauseReason = "上一轮未完成"
		queue.LastSettledRunID = run.ID
		changed = true
	}
	if changed {
		return r.SaveAgentConversationQueue(queue)
	}
	return nil
}

// The caller has prepared an ordinary admission without writing it. Bind the
// original message and its billed run in the same locked transaction.
func (r *Repository) CommitAgentMessageAdmission(message model.AgentQueuedMessage, admission CloudAgentAdmission, activeLimit int, maxTasks, maxBytes int64) error {
	return r.WithAgentConversation(message.UserID, message.ConversationID, func(tx *Repository, q *model.AgentConversationQueue, latest *model.CloudAgentExecution) error {
		row, err := tx.AgentQueuedMessage(message.UserID, message.ConversationID, message.ID)
		if err != nil {
			return err
		}
		if row.Status != "dispatching" || row.DispatchToken != message.DispatchToken || row.Revision != message.Revision || latest.ID != row.DispatchParentID || (q.Paused && row.Mode != "interject") {
			return ErrCreationConflict
		}
		if q.AwaitingQuestionID != "" && (row.ReplyToQuestionID != q.AwaitingQuestionID || q.QuestionRunID != latest.ID) {
			return ErrAgentMessageQuestionPending
		}
		if row.ReplyToQuestionID != "" && row.ReplyToQuestionID != q.AwaitingQuestionID {
			return ErrCreationConflict
		}
		if admission.Execution.ParentID != latest.ID || admission.Execution.UserID != row.UserID || admission.Execution.CanvasID != row.CanvasID || admission.Execution.ConversationID != row.ConversationID {
			return ErrCreationConflict
		}
		usage, err := tx.UserStorageUsage(row.UserID)
		if err != nil {
			return err
		}
		incoming := int64(len(admission.Task.Prompt) + len(admission.Task.InputJSON) + len(admission.Task.Error) - len(row.RequestJSON) - len(row.Prompt) - len(row.LastError))
		if usage.TaskCount >= maxTasks || usage.TaskBytes+incoming > maxBytes {
			return ErrAgentMessageStorageLimit
		}
		if err = createCloudAgentHoldingTaskGuarded(tx.db, admission.Task, admission.Order, admission.Execution, activeLimit, true, admission.Skills); err != nil {
			return err
		}
		if err = tx.FinishAgentQueuedMessage(row, "started", admission.Execution.ID); err != nil {
			return err
		}
		if row.ReplyToQuestionID != "" {
			q.AwaitingQuestionID = ""
			q.QuestionRunID = ""
		}
		return tx.SaveAgentConversationQueue(q)
	})
}

func (r *Repository) LatestAgentConversationRun(userID, conversationID string) (*model.CloudAgentExecution, error) {
	var row model.CloudAgentExecution
	err := r.db.Where("user_id = ? AND "+cloudAgentConversationIdentity+" = ?", userID, conversationID).Order("created_at DESC,id DESC").First(&row).Error
	return &row, err
}

// The account write reservation is shared with run admission, checkpoint saves
// and cancellation, and is acquired before reading any queue or run snapshot.
func (r *Repository) WithAgentConversation(userID, conversationID string, fn func(*Repository, *model.AgentConversationQueue, *model.CloudAgentExecution) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		writer := r.transactionRepository(tx)
		if err := writer.LockCanvasWrite(userID, ""); err != nil {
			return err
		}
		latest, err := writer.LatestAgentConversationRun(userID, conversationID)
		if err != nil {
			return err
		}
		queue, err := writer.AgentConversationQueue(userID, conversationID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			queue = &model.AgentConversationQueue{UserID: userID, ConversationID: conversationID, CanvasID: latest.CanvasID, Revision: 1}
			// Old ask_user events may lie outside the hydrated journal window.
			var records []model.CloudAgentEventRecord
			if err = tx.Where("user_id = ? AND run_id = ?", userID, latest.ID).Order("sequence").Find(&records).Error; err != nil {
				return err
			}
			for _, record := range records {
				writer.observeAgentQueueEvent(queue, latest, record.EventJSON)
			}
			if len(records) == 0 {
				var old struct {
					Events []json.RawMessage `json:"events"`
				}
				if json.Unmarshal([]byte(latest.StateJSON), &old) == nil {
					for _, body := range old.Events {
						writer.observeAgentQueueEvent(queue, latest, string(body))
					}
				}
			}
			if err = tx.Create(queue).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if latest.Status != "completed" && isTerminalCloudAgentRunStatus(latest.Status) && queue.LastSettledRunID != latest.ID {
			queue.Paused = true
			queue.PauseReason = "上一轮未完成"
			queue.LastSettledRunID = latest.ID
			if err = writer.SaveAgentConversationQueue(queue); err != nil {
				return err
			}
		}
		return fn(writer, queue, latest)
	})
}

func (r *Repository) AgentConversationQueue(userID, conversationID string) (*model.AgentConversationQueue, error) {
	var row model.AgentConversationQueue
	err := r.db.Where("user_id = ? AND conversation_id = ?", userID, conversationID).First(&row).Error
	return &row, err
}

func (r *Repository) SaveAgentConversationQueue(row *model.AgentConversationQueue) error {
	expected := row.Revision
	row.Revision++
	row.UpdatedAt = time.Now().UTC()
	result := r.db.Model(&model.AgentConversationQueue{}).Where("user_id = ? AND conversation_id = ? AND revision = ?", row.UserID, row.ConversationID, expected).Select("canvas_id", "revision", "next_sequence", "paused", "pause_reason", "awaiting_question_id", "question_run_id", "last_settled_run_id", "updated_at").Updates(row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

func (r *Repository) AgentMessageByKey(userID, key string) (*model.AgentQueuedMessage, error) {
	var row model.AgentQueuedMessage
	err := r.db.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&row).Error
	return &row, err
}
func (r *Repository) AgentQueuedMessage(userID, conversationID, id string) (*model.AgentQueuedMessage, error) {
	var row model.AgentQueuedMessage
	err := r.db.Where("user_id = ? AND conversation_id = ? AND id = ?", userID, conversationID, id).First(&row).Error
	return &row, err
}
func (r *Repository) AgentQueuedMessages(userID, conversationID string) ([]model.AgentQueuedMessage, error) {
	rows := []model.AgentQueuedMessage{}
	err := r.db.Where("user_id = ? AND conversation_id = ? AND status IN ?", userID, conversationID, []string{"queued", "dispatching"}).Order("sequence,id").Find(&rows).Error
	return rows, err
}
func (r *Repository) InsertAgentQueuedMessage(row *model.AgentQueuedMessage, queue *model.AgentConversationQueue, perConversation, perUser int) error {
	prior, err := r.AgentMessageByKey(row.UserID, row.IdempotencyKey)
	if err == nil {
		if prior.Fingerprint != row.Fingerprint || prior.ConversationID != row.ConversationID {
			return ErrCreationConflict
		}
		*row = *prior
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var userCount, conversationCount int64
	query := r.db.Model(&model.AgentQueuedMessage{}).Where("user_id = ? AND status IN ?", row.UserID, []string{"queued", "dispatching"})
	if err = query.Count(&userCount).Error; err != nil {
		return err
	}
	if err = query.Where("conversation_id = ?", row.ConversationID).Count(&conversationCount).Error; err != nil {
		return err
	}
	if userCount >= int64(perUser) || conversationCount >= int64(perConversation) {
		return ErrAgentMessageQueueFull
	}
	queue.NextSequence++
	row.Sequence = queue.NextSequence
	row.Status = "queued"
	row.Revision = 1
	if err = r.db.Create(row).Error; err != nil {
		return err
	}
	return r.SaveAgentConversationQueue(queue)
}

func (r *Repository) UpdateAgentQueuedMessageBody(row *model.AgentQueuedMessage, requestJSON, prompt string) error {
	result := r.db.Model(&model.AgentQueuedMessage{}).Where("id = ? AND user_id = ? AND revision = ? AND status = ?", row.ID, row.UserID, row.Revision, "queued").Updates(map[string]any{"request_json": requestJSON, "prompt": prompt, "last_error": "", "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	row.RequestJSON = requestJSON
	row.Prompt = prompt
	row.Revision++
	row.LastError = ""
	return nil
}

func (r *Repository) PromoteAgentQueuedMessage(row *model.AgentQueuedMessage) error {
	result := r.db.Model(&model.AgentQueuedMessage{}).Where("id = ? AND user_id = ? AND revision = ? AND status = ?", row.ID, row.UserID, row.Revision, "queued").Updates(map[string]any{"mode": "interject", "last_error": "", "next_attempt_at": nil, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}
func (r *Repository) ClaimAgentQueuedMessage(row *model.AgentQueuedMessage, token, parentID string, until time.Time) error {
	now := time.Now().UTC()
	var active int64
	if err := r.db.Model(&model.AgentQueuedMessage{}).Where("user_id = ? AND conversation_id = ? AND status = ? AND lease_until > ?", row.UserID, row.ConversationID, "dispatching", now).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		return ErrCreationConflict
	}
	result := r.db.Model(&model.AgentQueuedMessage{}).Where("id = ? AND user_id = ? AND revision = ? AND (status = ? OR (status = ? AND lease_until <= ?))", row.ID, row.UserID, row.Revision, "queued", "dispatching", now).Updates(map[string]any{"status": "dispatching", "dispatch_token": token, "dispatch_parent_id": parentID, "lease_until": until, "attempts": gorm.Expr("attempts + 1"), "revision": gorm.Expr("revision + 1"), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	row.Status = "dispatching"
	row.DispatchToken = token
	row.DispatchParentID = parentID
	row.LeaseUntil = &until
	row.Attempts++
	row.Revision++
	return nil
}
func (r *Repository) FinishAgentQueuedMessage(row *model.AgentQueuedMessage, status, runID string) error {
	result := r.db.Model(&model.AgentQueuedMessage{}).Where("id = ? AND user_id = ? AND revision = ?", row.ID, row.UserID, row.Revision).Updates(map[string]any{"status": status, "run_id": runID, "request_json": "", "prompt": "", "dispatch_token": "", "lease_until": nil, "last_error": "", "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	row.Status = status
	row.RunID = runID
	row.RequestJSON = ""
	row.Prompt = ""
	row.Revision++
	return nil
}
func (r *Repository) ReleaseAgentMessageClaim(row *model.AgentQueuedMessage, message string, next *time.Time) error {
	result := r.db.Model(&model.AgentQueuedMessage{}).Where("id = ? AND user_id = ? AND revision = ? AND dispatch_token = ?", row.ID, row.UserID, row.Revision, row.DispatchToken).Updates(map[string]any{"status": "queued", "dispatch_token": "", "lease_until": nil, "next_attempt_at": next, "last_error": message, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}
func (r *Repository) InvalidateAgentMessageClaims(userID, conversationID string) error {
	return r.db.Model(&model.AgentQueuedMessage{}).Where("user_id = ? AND conversation_id = ? AND status = ?", userID, conversationID, "dispatching").Updates(map[string]any{"status": "queued", "dispatch_token": "", "lease_until": nil, "revision": gorm.Expr("revision + 1")}).Error
}
func (r *Repository) PendingAgentMessageConversations(limit int) ([]model.AgentConversationQueue, error) {
	rows := []model.AgentConversationQueue{}
	err := r.db.Model(&model.AgentConversationQueue{}).Where("paused = ? AND EXISTS (SELECT 1 FROM agent_queued_messages m WHERE m.user_id = agent_conversation_queues.user_id AND m.conversation_id = agent_conversation_queues.conversation_id AND m.status IN ? AND (m.next_attempt_at IS NULL OR m.next_attempt_at <= ?))", false, []string{"queued", "dispatching"}, time.Now().UTC()).Order("updated_at,user_id,conversation_id").Limit(limit).Find(&rows).Error
	return rows, err
}
func (r *Repository) AgentUISettings(userID string) (*model.AgentUISetting, error) {
	var row model.AgentUISetting
	err := r.db.Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.AgentUISetting{UserID: userID, DefaultSendMode: "interject"}, nil
	}
	return &row, err
}
func (r *Repository) SaveAgentUISettings(row *model.AgentUISetting) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		writer := r.transactionRepository(tx)
		if err := writer.LockCanvasWrite(row.UserID, ""); err != nil {
			return err
		}
		initial := model.AgentUISetting{UserID: row.UserID, DefaultSendMode: "interject"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&initial).Error; err != nil {
			return err
		}
		result := tx.Model(&model.AgentUISetting{}).Where("user_id = ? AND revision = ?", row.UserID, row.Revision).Updates(map[string]any{"default_send_mode": row.DefaultSendMode, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCreationConflict
		}
		row.Revision++
		return nil
	})
}

func (r *Repository) observeAgentQueueEvent(queue *model.AgentConversationQueue, run *model.CloudAgentExecution, body string) {
	var event struct {
		Type string `json:"type"`
		Seq  int    `json:"seq"`
	}
	if json.Unmarshal([]byte(body), &event) == nil && event.Type == "user_question" {
		queue.AwaitingQuestionID = fmt.Sprintf("%s:%d", run.ID, event.Seq)
		queue.QuestionRunID = run.ID
	}
}
