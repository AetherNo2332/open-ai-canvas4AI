package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) requestCloudAgentMediaBatchApproval(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	args, err := decodeCloudAgentMediaBatchArgs(call.Function.Arguments)
	if err != nil {
		return s.cloudAgentMediaError(run, state, "admission", false, false, err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
	}
	limit, concurrency, maxRetries := cloudAgentBatchConcurrency(policy, args)
	if active, countErr := s.repo.ActiveTaskCountForUser(run.UserID); countErr == nil && active >= int64(limit) {
		return s.cloudAgentMediaError(run, state, "admission", false, false, &cloudAgentMediaAdmissionError{
			error: BadAuthRequest(fmt.Sprintf("当前已有 %d 个任务在排队或运行，达到同时任务上限；请等它们完成后再申请批次", active)), Reason: "active_task_limit",
		})
	}
	canvas, err := s.repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	if err != nil {
		return err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return err
	}
	prepared, total, videoSeconds, err := s.prepareCloudAgentBatchItems(run, state, call, args, doc)
	if err != nil {
		return s.cloudAgentMediaError(run, state, "admission", false, false, err)
	}
	budget := state.Request.Budget
	// 预算不足是"拆小一点就能继续"的问题：按参数错误交回模型，由它拆批或告诉用户调高预算。
	if budget.MaxGenerationTasks > 0 && state.Generations+len(prepared) > budget.MaxGenerationTasks {
		return s.cloudAgentMediaError(run, state, "admission", false, false, cloudAgentFieldError("items", "item_count", fmt.Sprintf("本轮最多还能生成 %d 条，本批有 %d 条；请拆批，或请用户调高本轮生成次数预算", budget.MaxGenerationTasks-state.Generations, len(prepared))))
	}
	if args.Mode == "video" && budget.MaxVideoSeconds > 0 && state.VideoSeconds+videoSeconds > budget.MaxVideoSeconds {
		return s.cloudAgentMediaError(run, state, "admission", false, false, cloudAgentFieldError("items", "item_count", fmt.Sprintf("本批视频共 %d 秒，超过本轮剩余视频时长预算 %d 秒；请拆批，或请用户调高预算", videoSeconds, budget.MaxVideoSeconds-state.VideoSeconds)))
	}
	orders, err := s.repo.BillingOrdersByTaskIDs(run.UserID, state.TaskIDs)
	if err != nil {
		return err
	}
	remaining := int64(math.Floor(budget.MaxCredits * float64(CreditScale)))
	for _, order := range orders {
		remaining -= order.AmountMicrocredits
	}
	if total > remaining {
		return s.cloudAgentMediaError(run, state, "admission", false, false, cloudAgentFieldError("items", "item_count", fmt.Sprintf("本批预计 %s，超过本轮剩余积分预算 %s；请拆批，或请用户调高本轮积分预算", cloudAgentCreditsLabel(total), cloudAgentCreditsLabel(remaining))))
	}
	approvalID := fmt.Sprintf("%s-%d-%d", run.ID, state.Step, state.CallIndex)
	batch := &cloudAgentMediaBatch{
		ID: approvalID, CallID: call.ID, Mode: args.Mode, ModelName: prepared[0].model, Phase: cloudAgentMediaBatchPhaseApproval,
		Concurrency: concurrency, Canary: args.Canary && len(prepared) > 1, MaxRetries: maxRetries,
		CapMicrocredits: total * int64(1+maxRetries), Items: make([]cloudAgentMediaBatchItem, 0, len(prepared)),
	}
	for _, entry := range prepared {
		batch.Items = append(batch.Items, entry.item)
	}
	if raw, err := json.Marshal(batch); err != nil {
		return err
	} else if len(raw) > cloudAgentMediaBatchStateLimit {
		return s.cloudAgentMediaError(run, state, "admission", false, false, cloudAgentFieldError("items", "item_count", "本批条目的提示词合计过长，请拆成两批"))
	}
	preview := cloudAgentMediaBatchApprovalPreview(batch, prepared)
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		resourceIDs := []string{}
		seen := map[string]bool{}
		for _, entry := range prepared {
			latest, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
			if err != nil {
				return err
			}
			latestDoc, err := creationDocument(latest.PayloadJSON)
			if err != nil {
				return err
			}
			// 前面条目的草稿写入会推进画布；每条都基于事务内最新内容写自己的草稿。
			entry.plan.Args.SnapshotHash = cloudAgentMediaContentHash(latestDoc)
			if err := createCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, entry.plan, nil, policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
				return err
			}
			for _, id := range entry.item.ResourceIDs {
				if !seen[id] {
					seen[id] = true
					resourceIDs = append(resourceIDs, id)
				}
			}
		}
		if len(resourceIDs) > 0 {
			if err := repo.UpsertCloudAgentResourceLeases(run.UserID, run.ID, approvalID, resourceIDs, time.Now().UTC().Add(cloudAgentMediaBatchLeaseLifetime)); err != nil {
				return err
			}
		}
		state.MediaBatch = batch
		state.Approval = &cloudAgentApproval{ID: approvalID, Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview, ModelName: batch.ModelName}
		current.Status = "waiting_approval"
		state.event(run.ID, "approval_requested", map[string]any{
			"approvalId": approvalID, "toolName": call.Function.Name, "modelName": batch.ModelName,
			"arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "text": preview.Description,
			"batch": cloudAgentMediaBatchSummary(batch, false),
		})
		return cloudAgentSave(current, state)
	})
}

func cloudAgentMediaBatchApprovalPreview(batch *cloudAgentMediaBatch, prepared []cloudAgentMediaBatchPrepared) cloudAgentApprovalPreview {
	descriptor, _ := cloudAgentNodeCapabilityForGenerationMode(batch.Mode)
	var total int64
	items := make([]cloudAgentApprovalPreviewItem, 0, len(prepared))
	for index, entry := range prepared {
		total += entry.item.Amount
		args := entry.plan.Args
		details := []string{fmt.Sprintf("单价：%s", cloudAgentCreditsLabel(entry.item.Amount))}
		if entry.model != batch.ModelName {
			details = append(details, "模型："+truncateRunes(entry.model, 80))
		}
		if len(args.ReferenceNodeIDs) > 0 {
			details = append(details, fmt.Sprintf("参考 %d 个", len(args.ReferenceNodeIDs)))
		}
		if args.Duration > 0 {
			details = append(details, fmt.Sprintf("时长：%d 秒", args.Duration))
		}
		if index == 0 && batch.Canary {
			details = append(details, "先试跑这一条")
		}
		var promptHint struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal([]byte(entry.item.Arguments), &promptHint)
		items = append(items, cloudAgentApprovalPreviewItem{
			Operation: "generate_media_item", NodeID: args.NodeID, NodeTitle: truncateRunes(args.Title, 120),
			NodeType: descriptor.Type, NodeTypeLabel: descriptor.Label, Details: details,
			Summary: truncateRunes(strings.Join(strings.Fields(promptHint.Prompt), " "), 90),
		})
	}
	spec := []string{}
	first := prepared[0].plan.Args
	for _, value := range []string{first.Size, first.Quality, first.ImageResolution} {
		if strings.TrimSpace(value) != "" {
			spec = append(spec, value)
		}
	}
	description := fmt.Sprintf("一次批准 %d 个%s生成：模型 %s", len(items), descriptor.Label, truncateRunes(batch.ModelName, 60))
	if len(spec) > 0 {
		description += "，规格 " + strings.Join(spec, " · ")
	}
	description += fmt.Sprintf("；预计 %s，并发 %d", cloudAgentCreditsLabel(total), batch.Concurrency)
	if batch.MaxRetries > 0 {
		description += fmt.Sprintf("；每条最多重试 %d 次，本批总额上限 %s", batch.MaxRetries, cloudAgentCreditsLabel(batch.CapMicrocredits))
	}
	if batch.Canary {
		description += "；先试跑第 1 条，结果交给 Agent 确认后再放行其余条目"
	}
	description += "。草稿节点已建好，批准后才提交收费任务，结果逐条回写画布。"
	return cloudAgentApprovalPreview{
		Kind: "media_batch", Title: fmt.Sprintf("确认批量生成 %d 个%s", len(items), descriptor.Label),
		Description: description, Items: items,
	}
}

// cloudAgentMediaBatchSummary 是事件与工具回执共用的批次摘要；条目只带状态，不带提示词。

func (s *Service) prepareCloudAgentBatchItems(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, args cloudAgentMediaBatchArgs, doc map[string]any) ([]cloudAgentMediaBatchPrepared, int64, int, error) {
	probe := cloudAgentMediaBatchProbeState(state)
	prepared := make([]cloudAgentMediaBatchPrepared, 0, len(args.Items))
	var total int64
	videoSeconds := 0
	for index := range args.Items {
		itemArgs, err := cloudAgentMediaBatchItemArgs(doc, args, index)
		if err != nil {
			return nil, 0, 0, err
		}
		itemCall, err := cloudAgentMediaBatchItemCall(call.ID, index, itemArgs)
		if err != nil {
			return nil, 0, 0, err
		}
		req, plan, err := s.prepareCloudAgentMedia(run, probe, itemCall)
		if err != nil {
			return nil, 0, 0, cloudAgentMediaBatchItemAdmissionError(index, err)
		}
		preparation := &creationTaskPreparation{}
		req.creationPrepare = preparation
		task, err := s.CreateTask(run.UserID, req)
		if err != nil {
			return nil, 0, 0, cloudAgentMediaBatchItemAdmissionError(index, err)
		}
		if err := applyCloudAgentResolvedMediaDefaults(&req, plan, task); err != nil {
			return nil, 0, 0, cloudAgentMediaBatchItemAdmissionError(index, err)
		}
		resolvedHash, err := cloudAgentResolvedMediaHash(task)
		if err != nil {
			return nil, 0, 0, err
		}
		specHash, err := cloudAgentResolvedMediaSpecHash(task)
		if err != nil {
			return nil, 0, 0, err
		}
		modelName, err := s.cloudAgentMediaModelName(plan.Args)
		if err != nil {
			return nil, 0, 0, cloudAgentMediaBatchItemAdmissionError(index, err)
		}
		stored := plan.Args
		stored.Prompt, stored.SnapshotHash = itemArgs.Prompt, ""
		raw, err := json.Marshal(stored)
		if err != nil {
			return nil, 0, 0, err
		}
		amount := int64(0)
		if preparation.Order != nil {
			amount = preparation.Order.AmountMicrocredits
		}
		total += amount
		videoSeconds += plan.Args.Duration
		prepared = append(prepared, cloudAgentMediaBatchPrepared{plan: plan, model: modelName, item: cloudAgentMediaBatchItem{
			NodeID: plan.Args.NodeID, Title: plan.Args.Title, Arguments: string(raw), ResolvedHash: resolvedHash, SpecHash: specHash,
			Amount: amount, Duration: plan.Args.Duration, ResourceIDs: cloudAgentMediaBatchResourceIDs(req.Input), Status: cloudAgentMediaBatchItemPending,
		}})
	}
	return prepared, total, videoSeconds, nil
}

func cloudAgentBatchConcurrency(policy RuntimePolicySetting, args cloudAgentMediaBatchArgs) (int, int, int) {
	limit := policy.Task.ActiveTaskLimit
	if limit <= 0 {
		limit = 1
	}
	concurrency := args.Concurrency
	if concurrency == 0 {
		concurrency = cloudAgentMediaBatchDefaultSlots
	}
	if concurrency > limit {
		concurrency = limit
	}
	maxRetries := 1
	if args.MaxRetries != nil {
		maxRetries = *args.MaxRetries
	}

	return limit, concurrency, maxRetries
}
