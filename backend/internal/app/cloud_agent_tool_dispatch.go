package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/observability"
	"yingce/backend/internal/repository"
)

// executeCloudAgentToolCall applies one preflighted business tool call. Pi owns
// the model loop; this function performs the Go-side operation and records its
// result in the same run transaction.
func (s *Service) executeCloudAgentToolCallAttempt(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil || (run.Status != "running" && run.Status != "queued") {
		return nil
	}
	if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
		return s.failCloudAgent(run, state, "Agent 工具调用状态无效，本轮已停止")
	}
	call := state.Calls[state.CallIndex]
	// director preview 挂起在浏览器视口上：回传由 SubmitCloudAgentDirectorPreview 处理，
	// 这里只负责超时回执，避免页面未打开时卡死运行。
	if cloudAgentDirectorPreviewPending(state, call) {
		return s.advanceCloudAgentDirectorPreview(run, state, call)
	}
	// 同一批内的后续写入要基于本批自己产出的版本，否则第二个写入必然被"画布已变化"拒绝；
	// 之后再按本步读取基线把仍然匹配的哈希接到当前画布上（跨轮或模型自己换过的哈希不接）。
	call = cloudAgentRebaseWriteSnapshot(state, call)
	call = s.cloudAgentRefreshStepSnapshotHash(run, state, call)
	state.Calls[state.CallIndex] = call
	if state.Approval != nil && state.Approval.Decision == "" {
		return nil
	}
	if state.Approval != nil && state.Approval.Decision != "" && state.Approval.CallHash != "" && state.Approval.CallHash != cloudAgentApprovalCallHash(call) {
		return s.terminateCloudAgent(run, "审批内容与待执行操作不一致，本轮已停止")
	}
	// 预检没过就不进任何业务分支：不申请审批、不读画布、不提交任务，只回一条结构化错误。
	// 这一步必须在写工具的审批分支之前——否则一个参数就不合法的写入会先弹出审批卡。
	if admission, ok := cloudAgentAdmissionFor(state, state.CallIndex); ok && !admission.Allowed {
		return s.recordCloudAgentToolReceipt(run, state, call, nil, cloudAgentAdmissionError(admission))
	}
	allowedRequest := state.Request
	allowedRequest.subagent = state.Subagent
	allowed := cloudAgentToolAllowed(allowedRequest, call.Function.Name)
	if allowed {
		normalized, err := cloudAgentResolveWorkflowIDs(run.UserID, run.ID, state, call)
		if err != nil {
			return s.recordCloudAgentToolReceipt(run, state, call, nil, err)
		}
		call = normalized
		state.Calls[state.CallIndex] = call
	}
	return s.routeCloudAgentToolCall(run, state, call, allowed)
}

// routeCloudAgentToolCall 依次检查只读拒绝、退役工具、分镜执行器和两类审批闸门；都不命中时交给已准入调用的执行路径。
func (s *Service) routeCloudAgentToolCall(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, allowed bool) error {
	// read_only 只放行 director read；写 action 即使绕过了预检 schema 也在执行前拒绝。
	if allowed && cloudAgentReadOnlyDirectorWrite(state, call) {
		return s.recordCloudAgentToolReceipt(run, state, call, nil, kernel.Forbidden("只读模式下 director 只允许 action=read"))
	}
	if cloudAgentLegacyStoryboardTool(call.Function.Name) {
		return s.recordStoryboardResult(run, state, call, nil, cloudAgentFieldError("tool", "retired", "旧分镜工具已退役；请使用 storyboard 的 read/split/resplit/update 等 action。新分镜先用 canvas_apply_ops 创建 script 并写入剧情。"))
	}
	if allowed && call.Function.Name == "storyboard" {
		return s.executeCloudAgentStoryboard(run, state, call)
	}
	mediaTool := call.Function.Name == "generate_media"
	if allowed && cloudAgentWriteCall(call) && (state.Request.PermissionMode == "request_approval" || mediaTool) && state.Approval == nil {
		return s.requestCloudAgentWriteApproval(run, state, call)
	}
	if allowed && call.Function.Name == "plan_update" && cloudAgentPlanRequiresFirstApproval(state, call) {
		if preview, ok := cloudAgentPlanApprovalPreview(call); ok {
			return s.requestCloudAgentPlanApproval(run, state, call, preview)
		}
	}
	return s.executeCloudAgentAdmittedToolCall(run, state, call, allowed)
}

// executeCloudAgentAdmittedToolCall 执行已过审批闸门的调用：已批准的媒体生成、导演台预览和子代理创建在检查点事务外执行，
// 其余工具在检查点事务里执行。
func (s *Service) executeCloudAgentAdmittedToolCall(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, allowed bool) error {
	if allowed && (call.Function.Name == "generate_media") && state.Approval != nil && state.Approval.Decision == "approve" {
		return s.executeCloudAgentMediaCall(run, state, call)
	}
	if allowed && call.Function.Name == cloudAgentDirectorTool && cloudAgentWriteCall(call) && cloudAgentDirectorPreviewAction(call) {
		return s.startCloudAgentDirectorPreview(run, state, call)
	}
	if allowed && call.Function.Name == "spawn_subagent" {
		return s.executeCloudAgentSpawnSubagent(run, state, call)
	}
	return s.checkpointCloudAgentToolCall(run, state, call, allowed)
}

// recordCloudAgentToolReceipt 把一条工具回执写入运行检查点，不执行工具本身。
func (s *Service) recordCloudAgentToolReceipt(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, result any, toolErr error) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		cloudAgentRecordToolResult(current, state, call, result, toolErr)
		return cloudAgentSave(current, state)
	})
}

// requestCloudAgentPlanApproval 挂起运行，等待用户确认首次提交的计划。
func (s *Service) requestCloudAgentPlanApproval(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, preview cloudAgentApprovalPreview) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		approvalID := fmt.Sprintf("%s-%d-%d", run.ID, state.Step, state.CallIndex)
		state.Approval = &cloudAgentApproval{ID: approvalID, Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview}
		current.Status = "waiting_approval"
		state.event(run.ID, "approval_requested", map[string]any{"approvalId": approvalID, "toolName": call.Function.Name, "arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "text": preview.Description})
		return cloudAgentSave(current, state)
	})
}

// executeCloudAgentSpawnSubagent 在检查点事务外创建子代理，再把回执和 subagent_spawned 事件写入检查点。
func (s *Service) executeCloudAgentSpawnSubagent(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	result, plan, spawnErr := s.spawnDynamicSubagent(run, state, call)
	if spawnErr != nil && !cloudAgentToolErrorIsBusiness(spawnErr) {
		return spawnErr
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		cloudAgentRecordToolResult(current, state, call, result, spawnErr)
		if spawnErr == nil {
			// carve 份额必须在回执的同一检查点事务里登记：单独事务会把父
			// revision +1，让这条回执的 CAS 保存必然冲突（v125 审查阻塞 4）。
			applySubagentBudgetCarve(state, result["linkId"].(string), *plan)
			state.event(run.ID, "subagent_spawned", result)
		}
		return cloudAgentSave(current, state)
	})
}

// cloudAgentToolOutsideReads 是检查点事务之前完成的识图准备、服务读取和长期记忆写入的结果。
type cloudAgentToolOutsideReads struct {
	inspectionResult any
	inspectionErr    error
	skillResult      any
	skillErr         error
}

// cloudAgentToolCheckpoint 是在检查点事务里执行一次工具调用所需的上下文。
type cloudAgentToolCheckpoint struct {
	run     *model.CloudAgentExecution
	state   *cloudAgentRuntime
	call    cloudAgentCall
	allowed bool
	policy  RuntimePolicySetting
	outside cloudAgentToolOutsideReads
}

// cloudAgentToolOutcome 是检查点事务内一次工具执行的产出。finished 为 true 时工具已自行保存检查点或要求回滚，
// 事务直接返回 err，不再走统一收尾。
type cloudAgentToolOutcome struct {
	result   any
	toolErr  error
	finished bool
	err      error
}

func cloudAgentToolFinished(err error) cloudAgentToolOutcome {
	return cloudAgentToolOutcome{finished: true, err: err}
}

// checkpointCloudAgentToolCall 在检查点事务里执行其余工具。父子代理之间的消息和完成在两条运行的家族锁内提交。
func (s *Service) checkpointCloudAgentToolCall(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, allowed bool) error {
	policy, err := s.RuntimePolicy()
	if err != nil {
		return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
	}
	state.CallLimits = cloudAgentCallLimitsForPolicy(policy.Task)
	outside, budgetExhausted := s.readCloudAgentToolOutsideCheckpoint(run, state, call, allowed)
	if budgetExhausted {
		return s.failCloudAgent(run, state, cloudAgentImageInspectionBudgetMessage)
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	tc := &cloudAgentToolCheckpoint{run: run, state: state, call: call, allowed: allowed, policy: policy, outside: outside}
	checkpoint := func(dbRepo *repository.Repository) error {
		return dbRepo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
			outcome := s.runCloudAgentCheckpointTool(tc, current, repo)
			if outcome.finished {
				return outcome.err
			}
			return s.settleCloudAgentToolResult(tc, current, outcome.result, outcome.toolErr)
		})
	}
	if allowed && (call.Function.Name == "send_parent_message" || call.Function.Name == "finish_subagent") && state.Subagent != nil {
		return s.repo.WithAgentSubagentFamily(run.UserID, state.Subagent.ParentRunID, run.ID, checkpoint)
	}
	if allowed && call.Function.Name == "message_subagent" {
		var target struct {
			LinkID string `json:"linkId"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &target); err == nil {
			if link, err := s.repo.AgentSubagentLinkForUser(run.UserID, target.LinkID); err == nil && link.ParentRunID == run.ID {
				return s.repo.WithAgentSubagentFamily(run.UserID, run.ID, link.ChildRunID, checkpoint)
			}
		}
	}
	return checkpoint(s.repo)
}

// readCloudAgentToolOutsideCheckpoint 在检查点事务之前完成识图准备、服务读取和长期记忆写入。
// budgetExhausted 表示识图额度已用尽，调用方终止本轮。
func (s *Service) readCloudAgentToolOutsideCheckpoint(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, allowed bool) (reads cloudAgentToolOutsideReads, budgetExhausted bool) {
	// 看图的资源与能力校验在写事务外完成；真实图片只在模型任务执行时读取。
	if allowed && call.Function.Name == "canvas_inspect_image" && state.Request.VisionEnabled {
		reads.inspectionResult, reads.inspectionErr = s.prepareCloudAgentImageInspection(run.UserID, state.Request.CanvasID, state, call)
		if errors.Is(reads.inspectionErr, errCloudAgentImageInspectionBudget) {
			return reads, true
		}
	}
	// Service-backed reads, including outbound search, run before the checkpoint
	// transaction to avoid nested DB reads and holding a write lock during HTTP.
	if allowed && (call.Function.Name == "web_search" || call.Function.Name == "model_list") {
		reads.skillResult, reads.skillErr = cloudAgentReadToolCached(s.repo, run.UserID, state, call, s)
	}
	// 长期记忆写的是用户自己的记忆文件（独立的 CAS 与修订事务），不是画布：在检查点事务外执行，
	// 回执与 memory_updated 事件随下面的检查点一起落库。同一 callID 重放时返回原修订，不重复写入。
	if allowed && call.Function.Name == "memory_update" {
		reads.skillResult, reads.skillErr = s.cloudAgentHandleMemoryUpdate(s.repo, run.UserID, run.ID, state, call)
	}
	return reads, false
}

// runCloudAgentCheckpointTool 在检查点事务里执行一次工具调用。未下发的工具最先拒绝；其余工具名互不重叠，按类别分发。
func (s *Service) runCloudAgentCheckpointTool(tc *cloudAgentToolCheckpoint, current *model.CloudAgentExecution, repo *repository.Repository) cloudAgentToolOutcome {
	if !tc.allowed {
		_, message := cloudAgentUnadvertisedToolAdmission(tc.state, tc.call.Function.Name)
		return cloudAgentToolOutcome{toolErr: BadAuthRequest(message)}
	}
	if outcome, ok := s.runCloudAgentSubagentTool(tc, current, repo); ok {
		return outcome
	}
	if outcome, ok := runCloudAgentCanvasWriteTool(tc, repo); ok {
		return outcome
	}
	return s.runCloudAgentControlTool(tc, current, repo)
}

// runCloudAgentSubagentTool 执行父子代理之间的协作工具；ok 为 false 表示不是这类工具。
func (s *Service) runCloudAgentSubagentTool(tc *cloudAgentToolCheckpoint, current *model.CloudAgentExecution, repo *repository.Repository) (outcome cloudAgentToolOutcome, ok bool) {
	run, state, call := tc.run, tc.state, tc.call
	switch call.Function.Name {
	case "send_parent_message":
		result, err := s.deliverParentSubagentMessage(repo, current, state, call)
		return cloudAgentToolOutcome{result: result, toolErr: err}, true
	case "finish_subagent":
		result, err := s.finishDynamicSubagent(repo, current, state, call)
		if err != nil {
			return cloudAgentToolOutcome{result: result, toolErr: err}, true
		}
		cloudAgentRecordToolResult(current, state, call, result, nil)
		skipRemainingCloudAgentCalls(run.ID, state)
		current.Status = "completed"
		current.CleanupPending = true
		state.event(run.ID, "subagent_completed", result)
		return cloudAgentToolFinished(cloudAgentSave(current, state)), true
	case "message_subagent":
		result, err := s.sendSubagentInstruction(repo, current, state, call)
		return cloudAgentToolOutcome{result: result, toolErr: err}, true
	case "wait_subagents":
		return cloudAgentWaitSubagents(repo, current, state, call), true
	case "subagent_status":
		if state.Subagent != nil {
			return cloudAgentToolOutcome{toolErr: kernel.Forbidden("子代理不能读取父级子代理状态")}, true
		}
		views, err := agentSubagentViews(repo, current.UserID, current.ID)
		return cloudAgentToolOutcome{result: views, toolErr: err}, true
	}
	return cloudAgentToolOutcome{}, false
}

// cloudAgentWaitSubagents 在仍有子代理未结束时挂起本次调用（只保存检查点），全部结束后返回各子代理的结果。
func cloudAgentWaitSubagents(repo *repository.Repository, current *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) cloudAgentToolOutcome {
	if state.Subagent != nil {
		return cloudAgentToolOutcome{toolErr: kernel.Forbidden("子代理不能等待或创建下一级子代理")}
	}
	views, err := agentSubagentViews(repo, current.UserID, current.ID)
	if err != nil {
		return cloudAgentToolFinished(err)
	}
	pending := 0
	for _, view := range views {
		if !cloudAgentRunTerminal(view.Status) {
			pending++
		}
	}
	state.ActiveSubagents = pending
	if pending > 0 {
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "waiting_tool", "subagents", call.ID, "等待子代理结果"
		return cloudAgentToolFinished(cloudAgentSave(current, state))
	}
	// 子代理消息已按子代理归入 subagents[].messages（含正文）；不再附带原始消息行。
	result := map[string]any{"pending": false, "subagents": views}
	current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
	return cloudAgentToolOutcome{result: result}
}

// runCloudAgentCanvasWriteTool 执行写画布的工具，写入和画布事件与检查点同一事务提交；ok 为 false 表示不是这类工具。
func runCloudAgentCanvasWriteTool(tc *cloudAgentToolCheckpoint, repo *repository.Repository) (outcome cloudAgentToolOutcome, ok bool) {
	run, state, call, policy := tc.run, tc.state, tc.call, tc.policy
	var result any
	var toolErr error
	switch {
	case call.Function.Name == "canvas_apply_ops":
		result, toolErr = applyCloudAgentCanvas(repo, run.UserID, state.Request.CanvasID, call, policy, state.Request.StoryboardDisabled, cloudAgentCanvasEventRecorder(run.ID, state))
	case call.Function.Name == "canvas_bind_asset":
		result, toolErr = applyCloudAgentAssetBinding(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
	case call.Function.Name == "canvas_undo", call.Function.Name == "canvas_redo":
		result, toolErr = applyCloudAgentHistory(repo, run.UserID, state.Request.CanvasID, run.ID, state, call, policy)
	case call.Function.Name == "director" && cloudAgentWriteCall(call):
		var plan *cloudAgentDirectorPlan
		plan, result, toolErr = applyCloudAgentDirectorMutation(repo, run.UserID, state.Request.CanvasID, run.ID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		if toolErr == nil {
			// 写入与 director_open 同一事务提交：前端收到事件时场景已落库。
			state.event(run.ID, "director_open", cloudAgentDirectorOpenPayload(state.Request.CanvasID, plan.NodeID, plan.SceneID, plan.ShotID, plan.Args.Action, ""))
		}
	case call.Function.Name == "canvas_arrange_nodes":
		result, toolErr = applyCloudAgentArrangeNodes(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
	case call.Function.Name == "canvas_edit_batch_table":
		result, toolErr = applyCloudAgentBatchTableMutation(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
	case call.Function.Name == "canvas_create_character":
		result, toolErr = applyCloudAgentCharacterCreate(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
	default:
		return cloudAgentToolOutcome{}, false
	}
	return cloudAgentToolOutcome{result: result, toolErr: toolErr}, true
}

// runCloudAgentControlTool 执行其余工具：生成等待、工具类别切换、识图回执、显式收尾、事务外已完成的读取，
// 以及默认的只读工具。
func (s *Service) runCloudAgentControlTool(tc *cloudAgentToolCheckpoint, current *model.CloudAgentExecution, repo *repository.Repository) cloudAgentToolOutcome {
	run, state, call := tc.run, tc.state, tc.call
	switch call.Function.Name {
	case "wait_generations":
		result, suspended := cloudAgentWaitGenerations(current, state, call)
		if suspended {
			return cloudAgentToolFinished(cloudAgentSave(current, state))
		}
		return cloudAgentToolOutcome{result: result}
	case "agent_tools_control", "agent_tools_memory", "agent_tools_skills", "agent_tools_canvas_read", "agent_tools_image", "agent_tools_canvas_edit", "agent_tools_generation":
		state.SelectedToolCategory = call.Function.Name
		state.ActivatedToolCategories = cloudAgentAppendActivatedCategory(state.ActivatedToolCategories, call.Function.Name)
		return cloudAgentToolOutcome{result: map[string]any{"category": call.Function.Name, "tools": cloudAgentCategoryChildren(state.Canonical.Tools, call.Function.Name)}}
	case "canvas_inspect_image":
		return s.settleCloudAgentImageInspection(tc, repo)
	case "finish_run":
		if err := refreshActiveSubagents(repo, current, state); err != nil {
			return cloudAgentToolFinished(err)
		}
		// 显式完成：这里的闸门与"纯文本收尾"用的是同一份判据（cloud_agent_completion.go）。
		result, err := cloudAgentFinishRun(run.ID, state, call)
		return cloudAgentToolOutcome{result: result, toolErr: err}
	case "web_search", "model_list", "memory_update":
		return cloudAgentToolOutcome{result: tc.outside.skillResult, toolErr: tc.outside.skillErr}
	}
	result, err := cloudAgentReadToolCached(repo, run.UserID, state, call)
	return cloudAgentToolOutcome{result: result, toolErr: err}
}

// settleCloudAgentImageInspection 把事务外准备好的识图结果落到检查点：有摘要时写入识图缓存并记录观察，
// 否则只标记这张图已看过。
func (s *Service) settleCloudAgentImageInspection(tc *cloudAgentToolCheckpoint, repo *repository.Repository) cloudAgentToolOutcome {
	run, state := tc.run, tc.state
	result, toolErr := tc.outside.inspectionResult, tc.outside.inspectionErr
	if toolErr != nil || result == nil {
		return cloudAgentToolOutcome{result: result, toolErr: toolErr}
	}
	inspection, ok := result.(cloudAgentImageInspection)
	if !ok {
		return cloudAgentToolOutcome{result: result}
	}
	if inspection.Summary != nil {
		if writeErr := s.persistCloudAgentVisionCache(repo, run.UserID, state.Request.CanvasID, inspection, tc.policy); writeErr != nil {
			return cloudAgentToolOutcome{result: result, toolErr: writeErr}
		}
	}
	// 两种回执都算一次调用预算：attached=false 表示这次只回执文字、没有附图。
	attached := strings.TrimSpace(inspection.ImageURL) != ""
	if inspection.Summary != nil {
		state.cloudAgentRecordImageObservations(stringValue(inspection.Receipt["nodeId"])+"："+stringValue(inspection.Summary["short"]), true)
	} else {
		state.markCanvasImageInspection(stringValue(inspection.Receipt["nodeId"]), attached, stringValue(inspection.Receipt["contentSignature"]))
	}
	if inspection.CacheKey != "" {
		if state.ImageInspectionReads == nil {
			state.ImageInspectionReads = map[string]int{}
		}
		state.ImageInspectionReads[inspection.CacheKey]++
	}
	return cloudAgentToolOutcome{result: result}
}

// settleCloudAgentToolResult 记录观测事件和工具回执，并处理读循环终止、计划更新、向用户提问和显式收尾。
func (s *Service) settleCloudAgentToolResult(tc *cloudAgentToolCheckpoint, current *model.CloudAgentExecution, result any, toolErr error) error {
	run, state, call := tc.run, tc.state, tc.call
	toolStatus := observability.StatusCompleted
	toolReason := ""
	if toolErr != nil {
		toolStatus = observability.StatusFailed
		toolReason = toolErr.Error()
	}
	s.RecordObservability(observability.Event{TaskID: run.ActiveTaskID, RunID: run.ID, TraceID: run.ID, Kind: observability.KindTool, ToolType: call.Function.Name, Status: toolStatus, StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), Reason: toolReason})
	if toolErr == nil && cloudAgentWriteCall(call) {
		// A successful canvas mutation changes the read model. Do not replay a
		// pre-mutation canvas snapshot later in the same Agent run.
		cloudAgentInvalidateReadCache(state)
	}
	var readLoopErr *cloudAgentReadLoopError
	if errors.As(toolErr, &readLoopErr) {
		cloudAgentRecordToolResult(current, state, call, result, toolErr)
		current.Status = "failed"
		current.FailureMessage = truncateRunes(readLoopErr.Error(), 1000)
		cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(current.FailureMessage, 120), state)
		reason := readLoopErr.reasonCode()
		state.event(run.ID, "run_failed", map[string]any{
			"text": current.FailureMessage, "reason": reason,
			"toolName": call.Function.Name, "readCount": readLoopErr.Count,
		})
		return cloudAgentSave(current, state)
	}
	if toolErr == nil {
		state.recordCanvasBatchHash(result, call.Function.Name)
	}
	if call.Function.Name == "plan_update" && toolErr == nil {
		state.event(run.ID, "plan_updated", map[string]any{"items": state.Plan, "pendingTitles": cloudAgentPendingPlanItems(state.Plan)})
	}
	if call.Function.Name == "ask_user" && toolErr == nil {
		payload, _ := result.(map[string]any)
		state.event(run.ID, "user_question", payload)
		cloudAgentRecordToolResult(current, state, call, result, nil)
		skipRemainingCloudAgentCalls(run.ID, state)
		current.Status = "completed"
		return cloudAgentSave(current, state)
	}
	if call.Function.Name == "finish_run" && toolErr == nil {
		if handled, err := s.settleCloudAgentFinishRun(tc, current, result); handled {
			return err
		}
	}
	// 基础设施错误必须让整个事务回滚，不能降级成"工具失败回执"后照常提交：
	// 画布写入与 recorder 共用本事务，recorder 失败时若仍提交，就会出现
	// "画布已改 + 失败回执"的不一致状态（Codex 评审指出）。
	// 只有已知的业务拒绝才作为回执交回模型自纠。
	// Lease errors must also roll back here; the outer call wrapper records
	// their business receipt only after this transaction has been undone.
	if toolErr != nil && !cloudAgentToolErrorIsBusiness(toolErr) {
		return toolErr
	}
	cloudAgentRecordToolResult(current, state, call, result, toolErr)
	return cloudAgentSave(current, state)
}

// settleCloudAgentFinishRun 处理 finish_run 的闸门结果；handled 为 false 时按普通工具回执记录。
func (s *Service) settleCloudAgentFinishRun(tc *cloudAgentToolCheckpoint, current *model.CloudAgentExecution, result any) (handled bool, err error) {
	run, state, call := tc.run, tc.state, tc.call
	// 闸门通过：summary 就是本轮唯一一次最终答复，本轮就此结束。
	if cloudAgentFinishRunAccepted(result) {
		err := cloudAgentCompleteByFinishRun(current, state, run.ID, call, result)
		if err == nil {
			s.RecordObservability(observability.Event{TaskID: run.ActiveTaskID, RunID: run.ID, TraceID: run.ID, Kind: observability.KindTask, Status: observability.StatusCompleted, EndedAt: time.Now().UTC()})
		}
		return true, err
	}
	// 申请收尾用的次数也已用尽：与"纯文本收尾"走同一条终止路径。
	if cloudAgentFinishRunExhausted(result) {
		block := cloudAgentEvaluateCompletion(state)
		block.Attempt = state.CompletionNudgeAttempt
		cloudAgentRecordToolResult(current, state, call, result, nil)
		return true, cloudAgentFailBlockedCompletion(current, state, run.ID, block)
	}
	return false, nil
}
