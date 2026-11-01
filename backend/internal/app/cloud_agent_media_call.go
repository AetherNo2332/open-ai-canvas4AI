package app

import (
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) executeCloudAgentMediaCall(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	if cloudAgentLegacyBlockingMedia(state) {
		return s.completeCloudAgentLegacyMediaCall(run, state, call)
	}
	req, plan, err := s.prepareCloudAgentMedia(run, state, call)
	if err != nil {
		return s.cloudAgentMediaError(run, state, "admission", false, false, err)
	}
	return s.enqueueCloudAgentTask(run, state, req, plan)
}

// completeCloudAgentLegacyMediaCall settles a pre-upgrade run that still blocks
// on its submitted generation: once the task is terminal, write the result back
// to the canvas and record the tool receipt in one checkpoint transaction.
func (s *Service) completeCloudAgentLegacyMediaCall(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	task, err := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.cloudAgentMediaError(run, state, "completion", true, true, BadAuthRequest("媒体任务不存在或已失去归属，结果未回写画布"))
	}
	if err != nil {
		return err
	}
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
		return nil
	}
	if err := s.materializeCloudAgentStoryboardTask(task); err != nil {
		return err
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return err
	}
	targetNodeID, err := s.cloudAgentLegacyMediaTarget(run, state, call, task)
	if err != nil || targetNodeID == "" {
		return err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		return commitCloudAgentLegacyMediaCompletion(repo, current, run, state, call, task, targetNodeID, policy)
	})
}

// cloudAgentLegacyMediaTarget resolves the node the legacy generation targets.
// An invalid target is recorded as a terminal media error; the empty node ID it
// returns then tells the caller to stop with that recording's result, which is
// nil when the receipt was saved.
func (s *Service) cloudAgentLegacyMediaTarget(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, task *model.Task) (string, error) {
	var target struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &target); err != nil {
		return "", s.cloudAgentMediaError(run, state, "completion", true, true, BadAuthRequest("已提交媒体任务的目标记录无效"))
	}
	if target.NodeID == "" {
		var submitted struct {
			Metadata struct {
				NodeID string `json:"nodeId"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(task.InputJSON), &submitted); err != nil {
			return "", err
		}
		target.NodeID = submitted.Metadata.NodeID
	}
	if validateCloudAgentID(target.NodeID, "生成节点ID", 80) != nil {
		return "", s.cloudAgentMediaError(run, state, "completion", true, true, BadAuthRequest("已提交媒体任务的目标节点记录无效，未回写画布"))
	}
	return target.NodeID, nil
}

func commitCloudAgentLegacyMediaCompletion(repo *repository.Repository, current *model.CloudAgentExecution, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, task *model.Task, targetNodeID string, policy RuntimePolicySetting) error {
	before, readErr := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	nodeID, writeErr := completeCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, targetNodeID, task, policy)
	if writeErr != nil {
		var appErr *AppError
		if !errors.Is(writeErr, gorm.ErrRecordNotFound) && !(errors.As(writeErr, &appErr) && (appErr.Status == 400 || appErr.Status == 409)) {
			return writeErr // Retry persistence, never resubmit the billed generation.
		}
	}
	// Replay a committed result independently of later canvas edits or
	// deletion. Legacy tasks still use the readable before document.
	if nodeID != "" {
		beforeJSON := ""
		if readErr == nil {
			beforeJSON = before.PayloadJSON
		}
		if err := emitCloudAgentMediaCompletion(repo, run, state, task.ID, beforeJSON); err != nil {
			return err
		}
	}
	result := map[string]any{"phase": "completion", "taskSubmitted": true, "taskId": task.ID, "nodeId": nodeID, "targetNodeId": targetNodeID, "status": task.Status}
	for key, value := range cloudAgentTaskDiagnostic(repo, task) {
		result[key] = value
	}
	toolErr, generationMessage, writebackReason := cloudAgentLegacyMediaOutcome(state, task, targetNodeID, writeErr, result)
	if writeErr != nil {
		recordTaskWritebackDiagnostic(task, targetNodeID, "failed", writebackReason, writeErr)
	} else {
		recordTaskWritebackDiagnostic(task, targetNodeID, "succeeded", "", nil)
	}
	if err := repo.UpdateTaskExecutionDiagnostic(run.UserID, task.ID, task.ExecutionDiagnosticJSON); err != nil {
		return err
	}
	// A billed generation failure is a tool result, not a dead run: the
	// model must still be able to tell the user what happened. Only a
	// canvas write that cannot land is terminal for the whole turn.
	if writeErr != nil {
		if current.Status != "cancelled" {
			current.Status = "failed"
		}
		current.FailureMessage = cloudAgentSafeToolError(toolErr)
		state.event(run.ID, "run_failed", map[string]any{
			"text": current.FailureMessage, "taskId": task.ID, "nodeId": targetNodeID,
			"reason": result["writebackReason"], "generationStatus": task.Status,
			"generationError": generationMessage, "writebackError": result["writebackError"],
		})
	}
	cloudAgentToolResult(run.ID, state, call, result, toolErr)
	state.MediaTaskID = ""
	return cloudAgentSave(current, state)
}

// cloudAgentLegacyMediaOutcome fills the result summary and returns the tool
// error for a failed generation or a failed canvas write-back.
func cloudAgentLegacyMediaOutcome(state *cloudAgentRuntime, task *model.Task, targetNodeID string, writeErr error, result map[string]any) (error, string, string) {
	var toolErr error
	generationMessage := ""
	writebackMessage, writebackReason := "", ""
	if task.Status != model.TaskStatusSucceeded {
		generationMessage = truncateRunes(cloudAgentSafeMediaTaskError(task), 90)
		if !cloudAgentSafeUserMessage(generationMessage) {
			generationMessage = "媒体任务未成功"
		}
		result["generationError"] = generationMessage
		toolErr = BadAuthRequest("媒体任务未成功：" + generationMessage + "；请在任务中心查看任务详情，不会自动重试收费生成")
		result["summary"] = "媒体任务未成功；任务记录保留在任务中心"
		if task.Status == model.TaskStatusSubmissionUncertain {
			// 上游可能已接单并计费：回执归类为不可重试，要求先核对原提交，不能重新生成。
			toolErr = &cloudAgentMediaAdmissionError{error: BadAuthRequest("上游接单状态尚未确认，请核对原任务；不会自动重试收费生成"), Reason: "submission_uncertain", NodeID: targetNodeID, TaskID: task.ID}
			result["summary"] = "上游接单状态待核对；任务记录保留在任务中心"
		}
	}
	if writeErr != nil {
		writebackMessage = truncateRunes(cloudAgentSafeToolError(writeErr), 60)
		writebackReason = "canvas_writeback_failed"
		var writeback *cloudAgentMediaWritebackError
		if errors.As(writeErr, &writeback) {
			writebackReason = writeback.reason
		}
		result["writebackError"], result["writebackReason"] = writebackMessage, writebackReason
		message := "媒体任务已成功，但画布回写未完成：" + writebackMessage
		if generationMessage != "" {
			message = "媒体任务未成功：" + generationMessage + "；任务状态也未回写画布：" + writebackMessage
		}
		toolErr = BadAuthRequest(message + "。请在任务中心查看详情，不会自动重试收费生成")
		result["summary"] = "画布回写未完成；任务记录保留在任务中心"
	}
	if task.Status == model.TaskStatusSucceeded && writeErr == nil {
		// Completion writes the generated asset into the canvas. Invalidate
		// the same-run snapshot cache just like ordinary canvas writes.
		cloudAgentInvalidateReadCache(state)
		result["summary"] = "生成结果已回写画布节点"
		if result["adopted"] == false {
			result["summary"] = "新版本已加入节点版本历史，保留手动选择的当前版本"
		}
	}
	return toolErr, generationMessage, writebackReason
}

func (s *Service) cloudAgentMediaError(run *model.CloudAgentExecution, state *cloudAgentRuntime, phase string, submitted, terminal bool, err error) error {
	var lease *repository.CanvasEditLeaseError
	if errors.As(err, &lease) {
		return err
	} // outer executor records only after rollback
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
			current.Status = "failed"
			state.event(run.ID, "run_failed", map[string]any{"text": "Agent 媒体调用状态无效，本轮已停止"})
			return cloudAgentSave(current, state)
		}
		cloudAgentRecordToolResult(current, state, state.Calls[state.CallIndex], map[string]any{"phase": phase, "taskSubmitted": submitted}, err)
		// Any admission failure advances the call into the repair path. Do not let
		// a prepared quote from the failed attempt leak into the corrected call.
		state.AutoPreparedMedia = nil
		state.AutoPreparedCallHash = ""
		if submitted {
			state.MediaTaskID = ""
		}
		if terminal {
			current.Status = "failed"
			state.event(run.ID, "run_failed", map[string]any{"text": "媒体任务已提交，但结果处理失败；任务不会自动重试"})
		}
		return cloudAgentSave(current, state)
	})
}

// cloudAgentLegacyBlockingMedia：升级前保存、仍停在 generate_media 上等待结果的运行
// （有 MediaTaskID、没有异步待回写记录、当前调用就是那次生成）。新运行提交即返回，不走这里。
func cloudAgentLegacyBlockingMedia(state *cloudAgentRuntime) bool {
	return state.MediaTaskID != "" && len(state.PendingGenerations) == 0 && cloudAgentPendingGenerationIndex(state, state.MediaTaskID) < 0 &&
		state.CallIndex >= 0 && state.CallIndex < len(state.Calls) && state.Calls[state.CallIndex].Function.Name == "generate_media" &&
		!cloudAgentCallHasResult(state, state.Calls[state.CallIndex].ID)
}
