package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"math"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) markCloudAgentMediaBatchItemFailed(run *model.CloudAgentExecution, state *cloudAgentRuntime, index int, message string) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		item := &state.MediaBatch.Items[index]
		item.Status, item.Error = cloudAgentMediaBatchItemFailed, truncateRunes(message, 200)
		s.emitCloudAgentMediaBatchProgress(run.ID, state, index)
		return cloudAgentSave(current, state)
	})
}

func (s *Service) submitCloudAgentMediaBatchItem(run *model.CloudAgentExecution, state *cloudAgentRuntime, index int, policy RuntimePolicySetting) error {
	batch := state.MediaBatch
	item := batch.Items[index]
	budget := state.Request.Budget
	if budget.MaxGenerationTasks > 0 && state.Generations >= budget.MaxGenerationTasks {
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, "已达到本轮媒体生成次数上限，未提交")
	}
	if batch.Mode == "video" && budget.MaxVideoSeconds > 0 && state.VideoSeconds+item.Duration > budget.MaxVideoSeconds {
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, "已超过本轮视频时长预算，未提交")
	}
	allowed, err := s.cloudAgentBatchItemWithinBudget(run, state, item.Amount)
	if err != nil {
		return err
	}
	if !allowed {
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, "本轮积分预算或批次总额上限不足，未提交")
	}
	maxCharge := item.Amount
	var a cloudAgentMediaArgs
	if err := json.Unmarshal([]byte(item.Arguments), &a); err != nil {
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, "批次条目参数无效，未提交")
	}
	call, err := cloudAgentMediaBatchItemCall(batch.CallID, index, a)
	if err != nil {
		return err
	}
	probe := cloudAgentMediaBatchProbeState(state)
	// Stored item arguments already contain the user-approved model and specs.
	// Do not inherit the pre-approval draft configuration again at submission.
	// The empty approval disables inheritance without reusing a single-item quote.
	probe.Approval = &cloudAgentApproval{}
	req, plan, err := s.prepareCloudAgentMedia(run, probe, call)
	if err != nil {
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, "提交前校验未通过："+cloudAgentSafeToolError(err))
	}
	preparation := &creationTaskPreparation{}
	req.creationPrepare = preparation
	req.admission = &taskAdmission{
		ID:        cloudAgentID(run.UserID, fmt.Sprintf("%s:batch:%s:%d:%d", run.ID, batch.ID, index, item.Attempts)),
		MaxCharge: maxCharge, AgentRunID: run.ID, GenerationID: newID(), ApprovalID: batch.ID,
	}
	task, err := s.CreateTask(run.UserID, req)
	if err != nil {
		if strings.Contains(err.Error(), "同时排队或运行的任务") {
			return errCloudAgentMediaBatchWait
		}
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, "提交前校验未通过："+cloudAgentSafeToolError(err))
	}
	if err := validateCloudAgentBatchItemQuote(item, task); err != nil {
		return s.markCloudAgentMediaBatchItemFailed(run, state, index, err.Error())
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return err
	}
	if err := s.protectTaskSecrets(input); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	task.InputJSON = string(raw)
	if preparation.Order != nil {
		task.BillingOrderID = preparation.Order.ID
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		canvas, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
		if err != nil {
			return err
		}
		doc, err := creationDocument(canvas.PayloadJSON)
		if err != nil {
			return err
		}
		plan.Args.SnapshotHash = cloudAgentMediaContentHash(doc)
		if err := createCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, plan, task, policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
			return err
		}
		if err := createTaskWithStorageQuotaRepository(repo, task, preparation.Order, policy); err != nil {
			return err
		}
		if len(item.ResourceIDs) > 0 {
			if err := repo.UpsertCloudAgentResourceLeases(run.UserID, run.ID, "task:"+task.ID, item.ResourceIDs, time.Now().UTC().Add(cloudAgentMediaBatchTaskLease)); err != nil {
				return err
			}
		}
		state.TaskIDs = append(state.TaskIDs, task.ID)
		state.Generations++
		state.VideoSeconds += plan.Args.Duration
		entry := &state.MediaBatch.Items[index]
		entry.Status, entry.Error = cloudAgentMediaBatchItemSubmitted, ""
		entry.TaskIDs = append(entry.TaskIDs, task.ID)
		s.emitCloudAgentMediaBatchProgress(run.ID, state, index)
		return cloudAgentSave(current, state)
	})
}

func (s *Service) cloudAgentBatchItemWithinBudget(run *model.CloudAgentExecution, state *cloudAgentRuntime, maxCharge int64) (bool, error) {
	budget := state.Request.Budget
	batch := state.MediaBatch
	orders, err := s.repo.BillingOrdersByTaskIDs(run.UserID, state.TaskIDs)
	if err != nil {
		return false, err
	}
	remaining := int64(math.Floor(budget.MaxCredits * float64(CreditScale)))
	batchTasks := map[string]bool{}
	for _, entry := range batch.Items {
		for _, id := range entry.TaskIDs {
			batchTasks[id] = true
		}
	}
	var batchSpent int64
	for _, order := range orders {
		remaining -= order.AmountMicrocredits
		if batchTasks[order.TaskID] {
			batchSpent += order.AmountMicrocredits
		}
	}
	return remaining >= maxCharge && batch.CapMicrocredits-batchSpent >= maxCharge, nil
}

func validateCloudAgentBatchItemQuote(item cloudAgentMediaBatchItem, task *model.Task) error {
	if item.PromptChanged {
		if hash, hashErr := cloudAgentResolvedMediaSpecHash(task); hashErr != nil || hash != item.SpecHash {
			return BadAuthRequest("模型或规格与批准时不同，未提交；需要重新申请批次")
		}
	} else if hash, hashErr := cloudAgentResolvedMediaHash(task); hashErr != nil || hash != item.ResolvedHash {
		return BadAuthRequest("节点内容、参考或规格与批准时不同，未提交；需要重新申请批次")
	}
	return nil
}

func (s *Service) settleCloudAgentMediaBatchItem(run *model.CloudAgentExecution, state *cloudAgentRuntime, index int, task *model.Task) error {
	item := state.MediaBatch.Items[index]
	policy, err := s.RuntimePolicy()
	if err != nil {
		return err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		entry := &state.MediaBatch.Items[index]
		if task == nil {
			entry.Status, entry.Error = cloudAgentMediaBatchItemFailed, "任务记录不存在或已失去归属，结果未回写画布"
			s.emitCloudAgentMediaBatchProgress(run.ID, state, index)
			return cloudAgentSave(current, state)
		}
		before, readErr := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
		nodeID, writeErr := completeCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, item.NodeID, task, policy)
		if writeErr != nil {
			var appErr *AppError
			if !errors.Is(writeErr, gorm.ErrRecordNotFound) && !(errors.As(writeErr, &appErr) && (appErr.Status == 400 || appErr.Status == 409)) {
				return writeErr
			}
		}
		if nodeID != "" {
			if readErr != nil {
				return readErr
			}
			if err := emitCloudAgentCanvasChange(repo, run.ID, state, cloudAgentMutationInput{UserID: run.UserID, CanvasID: state.Request.CanvasID, BeforeJSON: before.PayloadJSON, Operation: "generate_media_complete"}); err != nil {
				return err
			}
		}
		switch {
		case task.Status == model.TaskStatusSucceeded && writeErr == nil:
			entry.Status, entry.Error = cloudAgentMediaBatchItemSucceeded, ""
			cloudAgentInvalidateReadCache(state)
			recordTaskWritebackDiagnostic(task, item.NodeID, "succeeded", "", nil)
		case writeErr != nil:
			entry.Status = cloudAgentMediaBatchItemFailed
			entry.Error = "画布回写未完成：" + truncateRunes(cloudAgentSafeToolError(writeErr), 80)
			recordTaskWritebackDiagnostic(task, item.NodeID, "failed", "canvas_writeback_failed", writeErr)
		default:
			message := truncateRunes(cloudAgentSafeMediaTaskError(task), 90)
			if !cloudAgentSafeUserMessage(message) {
				message = "媒体任务未成功"
			}
			entry.Status, entry.Error = cloudAgentMediaBatchItemFailed, message
			recordTaskWritebackDiagnostic(task, item.NodeID, "succeeded", "", nil)
		}
		if err := repo.UpdateTaskExecutionDiagnostic(run.UserID, task.ID, task.ExecutionDiagnosticJSON); err != nil {
			return err
		}
		s.emitCloudAgentMediaBatchProgress(run.ID, state, index)
		return cloudAgentSave(current, state)
	})
}
