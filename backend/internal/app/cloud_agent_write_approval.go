package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// cloudAgentMediaAdmission 是 generate_media 在审批或 auto 提交前完成的准入结果。
// preparation 和 task 只在本次做了不扣费的试准入时非空；复用检查点里已准备好的报价时为 nil。
type cloudAgentMediaAdmission struct {
	plan        *cloudAgentMediaPlan
	request     CreateTaskRequest
	modelName   string
	task        *model.Task
	preparation *creationTaskPreparation
}

// requestCloudAgentWriteApproval 处理需要审批的写入和媒体生成：媒体先做一次不扣费的准入，
// auto 模式直接准备草稿并提交任务，其余情况生成审批预览并挂起运行。
func (s *Service) requestCloudAgentWriteApproval(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	policy, err := s.RuntimePolicy()
	if err != nil {
		return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
	}
	var media *cloudAgentMediaAdmission
	if call.Function.Name == "generate_media" {
		media, err = s.admitCloudAgentMedia(run, state, call)
		if err != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
	}
	if media != nil && state.Request.PermissionMode == "auto" {
		return s.submitCloudAgentAutoMedia(run, state, call, policy, media)
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		return checkpointCloudAgentWriteApproval(current, repo, run, state, call, policy, media)
	})
}

// admitCloudAgentMedia 解析媒体调用并做准入。失败时调用方统一按 admission 阶段的媒体错误回执。
func (s *Service) admitCloudAgentMedia(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (*cloudAgentMediaAdmission, error) {
	req, prepared, err := s.prepareCloudAgentMedia(run, state, call)
	if err != nil {
		return nil, err
	}
	admission := &cloudAgentMediaAdmission{plan: prepared, request: req}
	// A durable auto checkpoint already contains the exact admitted input and
	// quote. Reusing it avoids a second dry admission after a worker restart.
	if prepared.Prepared == nil {
		// Dry admission validates the selected model and prompt limits without a task or charge.
		admission.preparation = &creationTaskPreparation{}
		req.creationPrepare = admission.preparation
		admission.task, err = s.CreateTask(run.UserID, req)
		if err != nil {
			return nil, err
		}
		// Resolve model-owned defaults once during the dry admission. Both the
		// eventual task and the canvas draft must use this exact resolved input;
		// otherwise auto mode reports a false tool failure for omitted size or
		// duration and makes the model spend another turn repairing its own call.
		if err := applyCloudAgentResolvedMediaDefaults(&req, prepared, admission.task); err != nil {
			return nil, err
		}
		admission.request = req
	}
	admission.modelName, err = s.cloudAgentMediaModelName(prepared.Args)
	if err != nil {
		return nil, err
	}
	return admission, nil
}

// submitCloudAgentAutoMedia 是 auto 模式的媒体生成：服务端已准入，写入草稿和报价后直接提交任务，不经审批。
func (s *Service) submitCloudAgentAutoMedia(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, policy RuntimePolicySetting, media *cloudAgentMediaAdmission) error {
	plan := media.plan
	if plan.Prepared != nil {
		// The draft and quote were already checkpointed. Reuse them after a
		// worker restart instead of dry-admitting and mutating the canvas again.
		return s.enqueueLatestCloudAgentTask(run, media)
	}
	// Auto media is a direct, server-admitted write. Prepare the draft and
	// its immutable quote in one checkpoint transaction, then release the
	// lock before enqueueCloudAgentTask performs the billed submission.
	s.storageMu.Lock()
	err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		doc, err := writeCloudAgentMediaDraft(repo, run, state, &call, plan, policy)
		if err != nil {
			return err
		}
		preparedMedia, err := prepareCloudAgentMediaApproval(repo, run.UserID, doc, plan, media.request, media.task, media.preparation.Order)
		if err != nil {
			return err
		}
		plan.Prepared = preparedMedia
		state.AutoPreparedMedia = preparedMedia
		state.AutoPreparedCallHash = cloudAgentApprovalCallHash(call)
		return cloudAgentSave(current, state)
	})
	s.storageMu.Unlock()
	if err != nil {
		latest, readErr := s.repo.CloudAgent(run.UserID, run.ID)
		if readErr != nil || latest.Revision != run.Revision {
			return err
		}
		fresh, decodeErr := cloudAgentDecode(latest)
		if decodeErr != nil {
			return decodeErr
		}
		return s.cloudAgentMediaError(latest, &fresh, "admission", false, false, err)
	}
	return s.enqueueLatestCloudAgentTask(run, media)
}

// enqueueLatestCloudAgentTask 重新读取运行的最新检查点，再提交计费的生成任务。
func (s *Service) enqueueLatestCloudAgentTask(run *model.CloudAgentExecution, media *cloudAgentMediaAdmission) error {
	latest, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		return err
	}
	fresh, err := cloudAgentDecode(latest)
	if err != nil {
		return err
	}
	return s.enqueueCloudAgentTask(latest, &fresh, media.request, media.plan)
}

// writeCloudAgentMediaDraft 在检查点事务里创建媒体草稿节点，按写入后的画布刷新快照哈希并改写调用参数，
// 让审批、报价和最终提交校验同一份调用。返回写入后的画布文档。
func writeCloudAgentMediaDraft(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call *cloudAgentCall, plan *cloudAgentMediaPlan, policy RuntimePolicySetting) (map[string]any, error) {
	if err := createCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, plan, nil, policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
		return nil, err
	}
	canvas, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	plan.Args.SnapshotHash = cloudAgentMediaContentHash(doc)
	raw, err := cloudAgentMediaCallArguments(plan.Args)
	if err != nil {
		return nil, err
	}
	call.Function.Arguments = string(raw)
	state.Calls[state.CallIndex] = *call
	return doc, nil
}

// checkpointCloudAgentWriteApproval 在检查点事务里准备审批预览（媒体先写草稿和报价），记录审批并挂起运行。
func checkpointCloudAgentWriteApproval(current *model.CloudAgentExecution, repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, policy RuntimePolicySetting, media *cloudAgentMediaAdmission) error {
	var preview cloudAgentApprovalPreview
	var preparedMedia *cloudAgentPreparedMedia
	modelName := ""
	if media != nil {
		modelName = media.modelName
		doc, err := writeCloudAgentMediaDraft(repo, run, state, &call, media.plan, policy)
		if err != nil {
			return err
		}
		preview = cloudAgentMediaApprovalPreview(media.plan, modelName)
		preparedMedia, err = prepareCloudAgentMediaApproval(repo, run.UserID, doc, media.plan, media.request, media.task, media.preparation.Order)
		if err != nil {
			return err
		}
	} else {
		var mutationErr error
		preview, mutationErr = cloudAgentWriteApprovalPreview(repo, run, state, call)
		if mutationErr != nil {
			return failCloudAgentApprovalPreview(current, run, state, call, mutationErr)
		}
	}
	// 审批与校验使用同一份已解析参考素材、默认规格和读取哈希的最终调用。
	state.Calls[state.CallIndex] = call
	state.Approval = &cloudAgentApproval{ID: fmt.Sprintf("%s-%d-%d", run.ID, state.Step, state.CallIndex), Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview, ModelName: modelName, Prepared: preparedMedia}
	if preparedMedia != nil {
		if err := pinCloudAgentPreparedMedia(repo, run.UserID, run.ID, state.Approval.ID, preparedMedia); err != nil {
			return err
		}
	}
	current.Status = "waiting_approval"
	state.event(run.ID, "approval_requested", map[string]any{"approvalId": state.Approval.ID, "toolName": call.Function.Name, "modelName": modelName, "arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "prepared": preparedMedia.publicView(), "text": preview.Description})
	return cloudAgentSave(current, state)
}

// cloudAgentWriteApprovalPreview 按工具准备写入审批的预览，只读取画布、不写入。
func cloudAgentWriteApprovalPreview(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) (cloudAgentApprovalPreview, error) {
	canvasID := state.Request.CanvasID
	switch call.Function.Name {
	case "canvas_bind_asset":
		plan, err := prepareCloudAgentAssetBinding(repo, run.UserID, canvasID, call)
		if err != nil {
			return cloudAgentApprovalPreview{}, err
		}
		return plan.Preview, nil
	case "canvas_undo", "canvas_redo":
		plan, err := prepareCloudAgentHistory(repo, run.UserID, canvasID, run.ID, call)
		if err != nil {
			return cloudAgentApprovalPreview{}, err
		}
		return plan.Preview, nil
	case "canvas_edit_batch_table":
		batchPlan, err := prepareCloudAgentBatchTableEdit(repo, run.UserID, canvasID, call)
		if err != nil {
			return cloudAgentApprovalPreview{}, err
		}
		return batchPlan.Preview, nil
	case "canvas_create_character":
		characterPlan, err := prepareCloudAgentCharacterCreate(repo, run.UserID, canvasID, call)
		if err != nil {
			return cloudAgentApprovalPreview{}, err
		}
		return characterPlan.Preview, nil
	case "director":
		return cloudAgentDirectorApproval(repo, run.UserID, canvasID, run.ID, call)
	case "canvas_arrange_nodes":
		arrangePlan, err := prepareCloudAgentArrangeNodes(repo, run.UserID, canvasID, call)
		if err != nil {
			return cloudAgentApprovalPreview{}, err
		}
		return arrangePlan.Preview, nil
	}
	canvasPlan, err := prepareCloudAgentCanvasMutation(repo, run.UserID, canvasID, call)
	if err != nil {
		return cloudAgentApprovalPreview{}, err
	}
	return canvasPlan.Preview, nil
}

// failCloudAgentApprovalPreview 处理审批预览失败：参数错误和过期快照作为工具回执交回模型，
// 其他业务拒绝按准入失败终止运行，基础设施错误原样返回以回滚事务。
func failCloudAgentApprovalPreview(current *model.CloudAgentExecution, run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, mutationErr error) error {
	var argumentErr *cloudAgentArgumentError
	if errors.As(mutationErr, &argumentErr) {
		cloudAgentRecordToolResult(current, state, call, nil, mutationErr)
		return cloudAgentSave(current, state)
	}
	// 过期快照是并发编辑的正常结果，不是准入失败：把它作为工具结果交回模型，
	// 让它在本轮内重新读取并重试，而不是丢掉整轮。真正的准入失败仍然终止运行。
	if cloudAgentSnapshotConflict(mutationErr) {
		cloudAgentToolResult(run.ID, state, call, nil, mutationErr)
		return cloudAgentSave(current, state)
	}
	var appErr *AppError
	if errors.As(mutationErr, &appErr) && appErr != nil {
		return failCloudAgentAdmission(current, state, run.ID, mutationErr)
	}
	return mutationErr
}
