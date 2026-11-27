package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// QueueReadyGenerationContinuations turns every ready generation batch into at
// most one internal queued message. It reuses the existing conversation queue,
// dispatch, admission and lease machinery; it does not create a second run
// admission path.
func (s *Service) QueueReadyGenerationContinuations(limit int) (int, error) {
	// 失败、取消、停止路径在 source run 终态时可能留下仍在 collecting 的批次；
	// 这里统一收口，重启后第一轮派发就会修复。
	if _, err := s.repo.DetachOrphanAgentGenerationBatches(); err != nil {
		return 0, err
	}
	batches, err := s.repo.ReadyAgentGenerationBatches(limit)
	if err != nil || len(batches) == 0 {
		return 0, err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return 0, err
	}
	queued := 0
	var firstErr error
	for _, batch := range batches {
		ok, err := s.queueGenerationContinuation(batch, policy)
		if errors.Is(err, repository.ErrCreationConflict) {
			continue
		}
		if err != nil {
			// 一个批次的配额/存储拒绝必须只影响它自己：保留 ready 让下一周期
			// 重试，同时不让其它账号的续接与普通消息被跳过。
			if firstErr == nil {
				firstErr = err
			}
			// 公平扫描：失败批次让出扫描窗口，否则前 limit 条全失败会把后面的
			// 正常账号永久挡在窗口外。
			if touchErr := s.repo.TouchReadyAgentGenerationBatch(batch.UserID, batch.ID); touchErr != nil {
				log.Printf("[agent] rotate deferred generation batch %s: %v", batch.ID, touchErr)
			}
			log.Printf("[agent] generation continuation for batch %s deferred: %v", batch.ID, err)
			continue
		}
		if ok {
			queued++
		}
	}
	if queued > 0 {
		s.wakeAgentMessages()
	}
	return queued, firstErr
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

const (
	// 续接提示词的固定开销和每个任务的身份行预算；与运行提示词的字符上限共用
	// 同一预算，避免内部续接在 source run 已完成后才被 400 拒绝。
	generationContinuationHeaderRunes = 160
	generationContinuationFooterRunes = 90
	// 每个任务至少要能放下序号、taskId、nodeId、mode 和状态。taskId 与 nodeId
	// 都是 80 字符上限，按最坏情况留 240 字，保证身份行永远不被省略。
	generationContinuationTaskIdentityRunes   = 240
	generationContinuationTaskTitleRunes      = 48
	generationContinuationTaskDetailRunes     = 160
	generationContinuationMinInstructionRunes = 200
)

// generationContinuationInstructionLimit 返回本批次允许登记的指令字符数。
// 批次大小来自实际登记行，指令加上头部、结尾和每个任务的身份行后一定装得进
// 运行提示词的同一上限，不需要放宽任何安全限制。
func generationContinuationInstructionLimit(expectedTasks int) int {
	if expectedTasks < 0 {
		expectedTasks = 0
	}
	limit := cloudAgentPromptMaxRunes - generationContinuationHeaderRunes - generationContinuationFooterRunes - expectedTasks*generationContinuationTaskIdentityRunes
	if limit < generationContinuationMinInstructionRunes {
		return generationContinuationMinInstructionRunes
	}
	return limit
}

func cloudAgentGenerationContinuationPrompt(batch model.AgentGenerationBatch, tasks []model.AgentGenerationBatchTask) string {
	results := map[string]json.RawMessage{}
	if batch.ResultsJSON != "" {
		_ = json.Unmarshal([]byte(batch.ResultsJSON), &results)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })
	header := fmt.Sprintf("[系统生成结果] 这是你在上一轮提交的生成任务结果，不是用户新消息。\n批次：%s；任务数：%d；已终态：%d。\n", batch.ID, batch.ExpectedTasks, batch.TerminalTasks)
	footer := "请按以下指令继续：\n" + batch.Instruction + "\n失败、取消或缺失的任务只报告原因，不要自动重新提交付费生成。"
	// 指令在 finish_run 已按同一预算校验；这里再兜一次底，防止历史批次或人工
	// 改库把永远无法准入的提示词写进队列。
	if over := runeCount(header) + runeCount(footer) - cloudAgentPromptMaxRunes; over > 0 {
		footer = truncateRunes(footer, max(0, runeCount(footer)-over))
	}
	budget := cloudAgentPromptMaxRunes - runeCount(header) - runeCount(footer)
	// 逐级降级，但每一级都保留全部任务身份：先丢结果明细，再缩短标题，最后只留
	// taskId/nodeId/status。任何一级都不会整行省略任务，否则模型无法按 taskId 取回。
	for _, mode := range []struct{ title, detail int }{
		{generationContinuationTaskTitleRunes, generationContinuationTaskDetailRunes},
		{generationContinuationTaskTitleRunes, 0},
		{0, 0},
	} {
		if lines := continuationTaskLines(tasks, results, mode.title, mode.detail); totalRunes(lines) <= budget {
			return header + strings.Join(lines, "") + footer
		}
	}
	// 任务数量超过身份行预算时（正常预算远达不到），列出全部 taskId 供逐条取回，
	// 再不够才截断并以批次 ID 明示剩余范围。
	lines := continuationTaskIdLines(tasks)
	if totalRunes(lines) > budget {
		lines = append(truncateContinuationTaskIds(tasks, budget), fmt.Sprintf("其余 taskId 见批次 %s 的任务明细。\n", batch.ID))
	}
	return header + strings.Join(lines, "") + footer
}

func continuationTaskLines(tasks []model.AgentGenerationBatchTask, results map[string]json.RawMessage, titleRunes, detailRunes int) []string {
	lines := make([]string, 0, len(tasks))
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
		if titleRunes > 0 {
			label = truncateRunes(label, titleRunes)
		} else {
			label = ""
		}
		var b strings.Builder
		if label != "" {
			fmt.Fprintf(&b, "%d. %s（taskId %s，nodeId %s，mode %s）：%s", index+1, label, task.TaskID, task.NodeID, task.Mode, firstNonEmpty(outcome.Status, "terminal"))
		} else {
			fmt.Fprintf(&b, "%d. taskId %s，nodeId %s，mode %s：%s", index+1, task.TaskID, task.NodeID, task.Mode, firstNonEmpty(outcome.Status, "terminal"))
		}
		if detailRunes > 0 {
			if outcome.Summary != "" {
				b.WriteString("；" + truncateRunes(outcome.Summary, detailRunes))
			}
			if outcome.Error != "" {
				b.WriteString("；错误：" + truncateRunes(outcome.Error, detailRunes))
			}
		}
		b.WriteString("\n")
		lines = append(lines, b.String())
	}
	return lines
}

// continuationTaskIdLines 是最后的降级形态：只保留 taskId 与状态，确保每个任务
// 都能被逐条取回。
func continuationTaskIdLines(tasks []model.AgentGenerationBatchTask) []string {
	lines := make([]string, 0, len(tasks))
	for index, task := range tasks {
		lines = append(lines, fmt.Sprintf("%d. taskId %s：%s\n", index+1, task.TaskID, task.Status))
	}
	return lines
}

// truncateContinuationTaskIds 只用于任务数量本身超出提示词预算的极端情况：尽量
// 多列 taskId，剩余部分由批次 ID 指向任务明细。
func truncateContinuationTaskIds(tasks []model.AgentGenerationBatchTask, budget int) []string {
	kept := []string{}
	used := 0
	for _, line := range continuationTaskIdLines(tasks) {
		if used+runeCount(line) > budget {
			break
		}
		kept = append(kept, line)
		used += runeCount(line)
	}
	return kept
}

// detachTerminatedGenerationBatch closes a still-collecting batch for a run that
// terminated without registering a continuation. It is idempotent and keeps the
// batch rows, task results and billing untouched.
func (s *Service) detachTerminatedGenerationBatch(repo *repository.Repository, userID, runID string) error {
	if repo == nil {
		return nil
	}
	return repo.DetachAgentGenerationBatchBySourceRun(userID, runID)
}

func runeCount(value string) int { return utf8.RuneCountInString(value) }

func totalRunes(values []string) int {
	total := 0
	for _, value := range values {
		total += runeCount(value)
	}
	return total
}
