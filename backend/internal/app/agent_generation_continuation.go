package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// QueueReadyGenerationContinuations turns every ready generation batch into at
// most one internal queued message. It reuses the existing conversation queue,
// dispatch, admission and lease machinery; it does not create a second run
// admission path.
func (s *Service) QueueReadyGenerationContinuations(limit int) (int, error) {
	batches, err := s.repo.ReadyAgentGenerationBatches(limit)
	if err != nil || len(batches) == 0 {
		return 0, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, batch := range batches {
		ok, err := s.queueGenerationContinuation(batch, policy)
		if errors.Is(err, repository.ErrCreationConflict) {
			continue
		}
		if err != nil {
			return queued, err
		}
		if ok {
			queued++
		}
	}
	if queued > 0 {
		s.wakeAgentMessages()
	}
	return queued, nil
}

func (s *Service) queueGenerationContinuation(batch model.AgentGenerationBatch, policy RuntimePolicySetting) (bool, error) {
	request, err := s.generationContinuationRequest(batch)
	if err != nil {
		return false, err
	}
	tasks, err := s.repo.AgentGenerationBatchTasks(batch.UserID, batch.ID)
	if err != nil {
		return false, err
	}
	prompt := cloudAgentGenerationContinuationPrompt(batch, tasks)
	request.Prompt = prompt
	request.IdempotencyKey = "generation-batch:" + batch.ID
	raw, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	fingerprint := creationHash(string(raw))
	row := &model.AgentQueuedMessage{
		ID:     "am" + strings.TrimPrefix(cloudAgentID(batch.UserID, request.IdempotencyKey), "ag"),
		UserID: batch.UserID, CanvasID: batch.CanvasID, ConversationID: batch.ConversationID,
		IdempotencyKey: request.IdempotencyKey, Fingerprint: fingerprint,
		Mode: "queue", Kind: model.AgentQueuedMessageGenerationContinuation,
		RequestJSON: string(raw), Prompt: prompt,
	}
	queued := false
	err = s.repo.WithAgentConversation(batch.UserID, batch.ConversationID, func(tx *repository.Repository, q *model.AgentConversationQueue, _ *model.CloudAgentExecution) error {
		current, err := tx.AgentGenerationBatch(batch.UserID, batch.ID)
		if err != nil {
			return err
		}
		if current.Status != model.AgentGenerationBatchReady {
			return nil
		}
		usage, err := tx.UserStorageUsage(batch.UserID)
		if err != nil {
			return err
		}
		if err := validateTaskDataGrowthQuotaWithPolicy(usage, int64(len(raw)+len(prompt)+len(fingerprint)), policy.Resource); err != nil {
			return err
		}
		// Internal continuations bypass ordinary pending-count limits but still
		// pass storage quota and every normal run admission check.
		if err := tx.InsertAgentQueuedMessage(row, q, math.MaxInt, math.MaxInt); err != nil {
			return err
		}
		if row.Status != "queued" && row.Status != "dispatching" {
			return nil
		}
		updated, err := tx.QueueReadyAgentGenerationBatch(batch.UserID, batch.ID, row.ID)
		if err != nil {
			return err
		}
		if updated.Status != model.AgentGenerationBatchQueued || updated.QueuedMessageID != row.ID {
			return nil
		}
		queued = true
		return nil
	})
	return queued, agentMessageError(err)
}

func (s *Service) generationContinuationRequest(batch model.AgentGenerationBatch) (CloudAgentRequest, error) {
	var request CloudAgentRequest
	if strings.TrimSpace(batch.RequestJSON) != "" {
		if err := json.Unmarshal([]byte(batch.RequestJSON), &request); err != nil {
			return request, err
		}
		return request, nil
	}
	run, err := s.repo.CloudAgent(batch.UserID, batch.SourceRunID)
	if err != nil {
		return request, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return request, err
	}
	return state.Request, nil
}

func cloudAgentGenerationContinuationPrompt(batch model.AgentGenerationBatch, tasks []model.AgentGenerationBatchTask) string {
	results := map[string]json.RawMessage{}
	if batch.ResultsJSON != "" {
		_ = json.Unmarshal([]byte(batch.ResultsJSON), &results)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })
	var b strings.Builder
	fmt.Fprintf(&b, "[系统生成结果] 这是你在上一轮提交的生成任务结果，不是用户新消息。\n批次：%s；任务数：%d；已终态：%d。\n", batch.ID, batch.ExpectedTasks, batch.TerminalTasks)
	for index, task := range tasks {
		var outcome struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(results[task.TaskID], &outcome)
		label := strings.TrimSpace(task.Title)
		if label == "" {
			label = task.Mode
		}
		fmt.Fprintf(&b, "%d. %s（taskId %s，nodeId %s，mode %s）：%s", index+1, label, task.TaskID, task.NodeID, task.Mode, firstNonEmpty(outcome.Status, "terminal"))
		if outcome.Summary != "" {
			b.WriteString("；" + outcome.Summary)
		}
		if outcome.Error != "" {
			b.WriteString("；错误：" + outcome.Error)
		}
		b.WriteString("\n")
	}
	b.WriteString("请按以下指令继续：\n")
	b.WriteString(batch.Instruction)
	b.WriteString("\n失败、取消或缺失的任务只报告原因，不要自动重新提交付费生成。")
	return b.String()
}
