package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 媒体生成是异步的：generate_media 提交任务后立即返回回执，Agent 可以继续其他工作；
// 任务结束时由 settleCloudAgentPendingGenerations 回写画布，并在下一步边界以
// 「[系统通知]」告知 Agent。需要结果才能继续时，Agent 调用 wait_generations 挂起，
// 运行让出 Worker，最后一个生成结束后重新进入调度。

const (
	cloudAgentGenerationSource          = "generation"
	cloudAgentWaitGenerationsTool       = "wait_generations"
	cloudAgentGenerationsWaitKind       = "generations"
	cloudAgentMaxPendingGenerations     = 16
	cloudAgentGenerationNoticeMaxEvents = 8
)

type cloudAgentPendingGeneration struct {
	TaskID      string    `json:"taskId"`
	NodeID      string    `json:"nodeId"`
	CallID      string    `json:"callId"`
	Mode        string    `json:"mode,omitempty"`
	Title       string    `json:"title,omitempty"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// cloudAgentGenerationOutcome 是一次生成结束后的回写结果，供通知文本与 wait_generations 回执使用。
type cloudAgentGenerationOutcome struct {
	TaskID      string `json:"taskId"`
	NodeID      string `json:"nodeId"`
	Mode        string `json:"mode,omitempty"`
	Title       string `json:"title,omitempty"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	Error       string `json:"error,omitempty"`
	SplitResult any    `json:"splitResult,omitempty"`
	// VersionID/Adopted 只在原位生成新版本时出现：新版本 ID 与是否已自动采用。
	VersionID string `json:"versionId,omitempty"`
	Adopted   *bool  `json:"adopted,omitempty"`
}

func cloudAgentGenerationTerminal(status model.TaskStatus) bool {
	return status != model.TaskStatusQueued && status != model.TaskStatusRunning
}

func cloudAgentPendingGenerationIndex(state *cloudAgentRuntime, taskID string) int {
	for index, item := range state.PendingGenerations {
		if item.TaskID == taskID {
			return index
		}
	}
	return -1
}

// cloudAgentGenerationSubmittedReceipt 是 generate_media 提交成功后的即时回执。
func cloudAgentGenerationSubmittedReceipt(item cloudAgentPendingGeneration, pending int) map[string]any {
	return map[string]any{
		"status": "submitted", "taskSubmitted": true, "taskId": item.TaskID, "nodeId": item.NodeID, "mode": item.Mode,
		"pendingGenerations": pending,
		"next":               "生成在后台进行，完成后会以系统通知告诉你结果；期间可以继续其他工作。需要结果才能继续时调用 wait_generations，不要用 task_get 轮询",
	}
}

func cloudAgentGenerationNoticeText(outcomes []cloudAgentGenerationOutcome, remaining int) string {
	var b strings.Builder
	b.WriteString("[系统通知] 生成结果：")
	for index, outcome := range outcomes {
		if index > 0 {
			b.WriteString("；")
		}
		label := outcome.Title
		if label == "" {
			label = outcome.Mode
		}
		fmt.Fprintf(&b, "%s（nodeId %s）%s", label, outcome.NodeID, outcome.Summary)
		if outcome.SplitResult != nil {
			raw, _ := json.Marshal(outcome.SplitResult)
			b.WriteString("；行对应关系：" + string(raw))
		}
		if outcome.Error != "" {
			b.WriteString("：" + outcome.Error)
		}
	}
	if remaining > 0 {
		fmt.Fprintf(&b, "。还有 %d 个生成在进行。", remaining)
	} else {
		b.WriteString("。")
	}
	return b.String()
}

// cloudAgentQueueGenerationNotice 把本次结束的生成合并进一条待送通知；
// 同一步内多次结束只保留一条，内容按任务累加。
func cloudAgentQueueGenerationNotice(state *cloudAgentRuntime, outcomes []cloudAgentGenerationOutcome) {
	if len(outcomes) == 0 {
		return
	}
	merged := outcomes
	kept := state.PendingInterjections[:0]
	for _, item := range state.PendingInterjections {
		if item.Source == cloudAgentGenerationSource {
			var previous []cloudAgentGenerationOutcome
			if json.Unmarshal([]byte(item.PermissionFrom), &previous) == nil {
				merged = append(previous, outcomes...)
			}
			continue
		}
		kept = append(kept, item)
	}
	if len(merged) > cloudAgentGenerationNoticeMaxEvents {
		merged = merged[len(merged)-cloudAgentGenerationNoticeMaxEvents:]
	}
	encoded, _ := json.Marshal(merged)
	state.PendingInterjections = append(kept, cloudAgentInterjection{
		ID: "generation:" + merged[len(merged)-1].TaskID, Text: cloudAgentGenerationNoticeText(merged, len(state.PendingGenerations)),
		Source: cloudAgentGenerationSource, PermissionFrom: string(encoded), CreatedAt: time.Now(),
	})
}

// settleCloudAgentPendingGenerations 回写所有已结束的生成。返回是否改动了运行状态。
// 它在 Worker 读取快照、工具推进与看门狗巡检时调用；任务状态变化已经会唤醒所属运行。
func (s *Service) settleCloudAgentPendingGenerations(run *model.CloudAgentExecution) (bool, error) {
	state, err := cloudAgentDecode(run)
	if err != nil || len(state.PendingGenerations) == 0 {
		return false, err
	}
	finished := map[string]*model.Task{}
	for _, item := range state.PendingGenerations {
		task, err := s.repo.TaskForUser(run.UserID, item.TaskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			finished[item.TaskID] = nil
			continue
		}
		if err != nil {
			return false, err
		}
		if cloudAgentGenerationTerminal(task.Status) {
			if err := s.materializeCloudAgentStoryboardTask(task); err != nil {
				return false, err
			}
			if item.Mode == "storyboard" {
				job, err := s.repo.CanvasMaterialization(task.ID)
				if err != nil {
					return false, err
				}
				if job.State != "done" {
					continue
				}
			}
			finished[item.TaskID] = task
		}
	}
	if len(finished) == 0 {
		return false, nil
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return false, err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	err = s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		outcomes := []cloudAgentGenerationOutcome{}
		remaining := state.PendingGenerations[:0]
		for _, item := range state.PendingGenerations {
			task, done := finished[item.TaskID]
			if !done {
				remaining = append(remaining, item)
				continue
			}
			outcome, err := settleCloudAgentGeneration(repo, run, &state, item, task, policy)
			if err != nil {
				return err
			}
			outcomes = append(outcomes, outcome)
		}
		state.PendingGenerations = remaining
		cloudAgentQueueGenerationNotice(&state, outcomes)
		if len(remaining) == 0 && current.WaitKind == cloudAgentGenerationsWaitKind {
			s.resumeCloudAgentGenerationsWait(current, &state, outcomes)
		}
		return cloudAgentSave(current, &state)
	})
	return err == nil, err
}

// settleCloudAgentGeneration 回写一次已结束的生成并给出结果摘要。
// 回写失败不再判死整轮：生成已计费，结果留在任务中心，由通知如实告诉 Agent。
func settleCloudAgentGeneration(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, item cloudAgentPendingGeneration, task *model.Task, policy RuntimePolicySetting) (cloudAgentGenerationOutcome, error) {
	if item.Mode == "storyboard" {
		return settleCloudAgentStoryboardGeneration(repo, run, state, item, task)
	}
	outcome := cloudAgentGenerationOutcome{TaskID: item.TaskID, NodeID: item.NodeID, Mode: item.Mode, Title: item.Title}
	if task == nil {
		outcome.Status, outcome.Summary = "missing", "生成任务不存在或已失去归属，结果未回写画布"
		return outcome, nil
	}
	outcome.Status = string(task.Status)
	before, readErr := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	nodeID, writeErr := completeCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, item.NodeID, task, policy)
	if writeErr != nil {
		var appErr *AppError
		if !errors.Is(writeErr, gorm.ErrRecordNotFound) && !(errors.As(writeErr, &appErr) && (appErr.Status == 400 || appErr.Status == 409)) {
			return outcome, writeErr // 持久化失败重试，不重提已计费的生成
		}
	}
	if nodeID != "" {
		outcome.NodeID = nodeID
		beforeJSON := ""
		if readErr == nil {
			beforeJSON = before.PayloadJSON
		}
		if err := emitCloudAgentMediaCompletion(repo, run, state, task.ID, beforeJSON); err != nil {
			return outcome, err
		}
	}
	switch {
	case task.Status != model.TaskStatusSucceeded:
		message := truncateRunes(cloudAgentSafeMediaTaskError(task), 90)
		if !cloudAgentSafeUserMessage(message) {
			message = "媒体任务未成功"
		}
		outcome.Summary, outcome.Error = "生成未成功，任务记录保留在任务中心，不会自动重试收费生成", message
		if task.Status == model.TaskStatusSubmissionUncertain {
			outcome.Summary = "上游接单状态待核对，请先核对原任务，不要重新生成"
		}
	case writeErr != nil:
		outcome.Summary, outcome.Error = "生成成功，但画布回写未完成，作品保留在资源库", truncateRunes(cloudAgentSafeToolError(writeErr), 60)
	default:
		cloudAgentInvalidateReadCache(state)
		outcome.Summary = "已生成并写入画布"
		facts := cloudAgentTaskDiagnostic(repo, task)
		if versionID, ok := facts["versionId"].(string); ok && versionID != "" {
			outcome.VersionID = versionID
		}
		if adopted, ok := facts["adopted"].(bool); ok {
			outcome.Adopted = &adopted
			if !adopted {
				outcome.Summary = "已生成，新版本已加入节点版本历史，保留手动选择的当前版本"
			}
		}
	}
	status := "succeeded"
	reason := ""
	if writeErr != nil {
		status, reason = "failed", "canvas_writeback_failed"
		var writeback *cloudAgentMediaWritebackError
		if errors.As(writeErr, &writeback) {
			reason = writeback.reason
		}
	}
	recordTaskWritebackDiagnostic(task, item.NodeID, status, reason, writeErr)
	if err := repo.UpdateTaskExecutionDiagnostic(run.UserID, task.ID, task.ExecutionDiagnosticJSON); err != nil {
		return outcome, err
	}
	settled := map[string]any{
		"taskId": task.ID, "nodeId": outcome.NodeID, "callId": item.CallID, "mode": item.Mode,
		"status": outcome.Status, "summary": outcome.Summary, "error": outcome.Error,
	}
	if outcome.VersionID != "" {
		settled["versionId"] = outcome.VersionID
	}
	if outcome.Adopted != nil {
		settled["adopted"] = *outcome.Adopted
	}
	state.event(run.ID, "generation_settled", settled)
	return outcome, nil
}

func settleCloudAgentStoryboardGeneration(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, item cloudAgentPendingGeneration, task *model.Task) (cloudAgentGenerationOutcome, error) {
	outcome := cloudAgentGenerationOutcome{TaskID: item.TaskID, NodeID: item.NodeID, Mode: item.Mode, Title: item.Title}
	if task == nil {
		outcome.Status, outcome.Summary = "missing", "拆镜任务不存在或已失去归属，结果未写回画布"
		return outcome, nil
	}
	outcome.Status = string(task.Status)
	job, jobErr := repo.CanvasMaterialization(task.ID)
	if jobErr != nil && !repo.IsMissing(jobErr) {
		return outcome, jobErr
	}
	switch {
	case task.Status != model.TaskStatusSucceeded:
		message := truncateRunes(cloudAgentSafeMediaTaskError(task), 120)
		if !cloudAgentSafeUserMessage(message) {
			message = "拆镜任务未成功"
		}
		outcome.Summary, outcome.Error = "拆镜未成功，任务记录保留在任务中心", message
	case job == nil || job.State != "done" || job.Outcome != "applied":
		outcome.Status = "writeback_failed"
		outcome.Summary = "拆镜任务完成，但结果未写入画布"
		outcome.Error = "请读取分镜节点状态并重新拆镜"
	default:
		cloudAgentInvalidateReadCache(state)
		outcome.Summary = "已拆镜并写入画布"
		if binding, bindingErr := repo.CanvasTaskBinding(task.ID); bindingErr == nil {
			var context commands.StoryboardTaskContext
			if json.Unmarshal([]byte(binding.ResultContextJSON), &context) == nil && context.Result != nil {
				outcome.SplitResult = context.Result
				outcome.Summary = fmt.Sprintf("已写回分镜：替换 %d 行，写入 %d 行，%d 行画面待复核", context.Result.ReplacedCount, len(context.Result.Rows), len(context.Result.ReviewRowIDs))
			}
		}
	}
	settled := map[string]any{
		"taskId": task.ID, "nodeId": item.NodeID, "callId": item.CallID, "mode": item.Mode,
		"status": outcome.Status, "summary": outcome.Summary, "error": outcome.Error,
	}
	if outcome.SplitResult != nil {
		settled["splitResult"] = outcome.SplitResult
	}
	state.event(run.ID, "generation_settled", settled)
	return outcome, nil
}

// resumeCloudAgentGenerationsWait 在最后一个生成结束时完成挂起的 wait_generations 调用，
// 让运行重新进入调度（WaitKind 清空后准入不再跳过它）。
func (s *Service) resumeCloudAgentGenerationsWait(current *model.CloudAgentExecution, state *cloudAgentRuntime, outcomes []cloudAgentGenerationOutcome) {
	current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
	if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].Function.Name != cloudAgentWaitGenerationsTool {
		return
	}
	// 结果已写进 wait_generations 回执，同一批结果的通知不必再送一次。
	kept := state.PendingInterjections[:0]
	for _, item := range state.PendingInterjections {
		if item.Source != cloudAgentGenerationSource {
			kept = append(kept, item)
		}
	}
	state.PendingInterjections = kept
	cloudAgentToolResult(current.ID, state, state.Calls[state.CallIndex], map[string]any{"pending": 0, "results": outcomes}, nil)
}

// cloudAgentWaitGenerations 执行 wait_generations：没有待完成生成时立即返回，
// 否则挂起运行（WaitKind=generations），由最后一个生成结束时恢复。
func cloudAgentWaitGenerations(current *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (any, bool) {
	if len(state.PendingGenerations) == 0 {
		return map[string]any{"pending": 0, "results": []cloudAgentGenerationOutcome{}, "next": "当前没有进行中的生成"}, false
	}
	current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "waiting_tool", cloudAgentGenerationsWaitKind, call.ID, "等待生成结果"
	return nil, true
}

// cloudAgentPendingGenerationsDetail 用于收尾回执：本轮仍在进行的生成不阻止结束。
func cloudAgentPendingGenerationsDetail(state *cloudAgentRuntime) []map[string]any {
	items := make([]map[string]any, 0, len(state.PendingGenerations))
	for _, item := range state.PendingGenerations {
		items = append(items, map[string]any{"taskId": item.TaskID, "nodeId": item.NodeID, "mode": item.Mode})
	}
	return items
}

// settleCloudAgentGenerationsAndReload 回写已结束的生成；有改动时返回重新读取的运行，否则返回 nil。
// 终态运行（已结束或已取消）同样回写：生成照常完成，结果写回画布。
func (s *Service) settleCloudAgentGenerationsAndReload(run *model.CloudAgentExecution) (*model.CloudAgentExecution, error) {
	changed, err := s.settleCloudAgentPendingGenerations(run)
	if errors.Is(err, repository.ErrCreationConflict) {
		// 另一方正在推进这条运行，由它（或下一次读取）完成回写。
		return nil, nil
	}
	if err != nil || !changed {
		return nil, err
	}
	return s.repo.CloudAgent(run.UserID, run.ID)
}

// SettlePendingCloudAgentGenerations 由看门狗定期调用，回写所有运行里已结束的生成。
func (s *Service) SettlePendingCloudAgentGenerations() (int, error) {
	runs, err := s.repo.CloudAgentRunsWithPendingGenerations(20)
	if err != nil {
		return 0, err
	}
	settled := 0
	for index := range runs {
		// 列表只带行头；回写需要完整的对话记录，按归属重新读取。
		run, err := s.repo.CloudAgent(runs[index].UserID, runs[index].ID)
		if err != nil {
			return settled, err
		}
		changed, err := s.settleCloudAgentPendingGenerations(run)
		if errors.Is(err, repository.ErrCreationConflict) {
			continue
		}
		if err != nil {
			return settled, err
		}
		if changed {
			settled++
		}
	}
	return settled, nil
}

// cloudAgentCallHasResult 判断某次工具调用是否已在对话历史里有工具结果。
func cloudAgentCallHasResult(state *cloudAgentRuntime, callID string) bool {
	for index := len(state.Canonical.Messages) - 1; index >= 0; index-- {
		message := state.Canonical.Messages[index]
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == callID {
			return true
		}
	}
	return false
}

// cloudAgentPendingGenerationIndexID 是写入 media_task_id 列的值：最早一个待回写生成，没有时为空。
func cloudAgentPendingGenerationIndexID(state *cloudAgentRuntime) string {
	if len(state.PendingGenerations) == 0 {
		return ""
	}
	return state.PendingGenerations[0].TaskID
}
