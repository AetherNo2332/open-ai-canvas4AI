package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
	"yingce/backend/internal/canvas/commands"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// The async task, canvas binding and Agent checkpoint commit together. A worker
// restart observes either all three or none, just like media generation.
func (s *Service) recordStoryboardResult(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, result any, toolErr error) error {
	var commandErr *commands.Error
	if errors.As(toolErr, &commandErr) {
		toolErr = cloudAgentFieldError(commandErr.Field, commandErr.Reason, commandErr.Message)
	}
	if toolErr != nil && !cloudAgentToolErrorIsBusiness(toolErr) {
		return toolErr
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		cloudAgentRecordToolResult(current, state, call, result, toolErr)
		return cloudAgentSave(current, state)
	})
}

func storyboardArguments(call cloudAgentCall) (map[string]any, string, error) {
	var fields map[string]any
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &fields); err != nil {
		return nil, "", cloudAgentJSONArgumentError(err)
	}
	action := stringValue(fields["action"])
	if stringValue(fields["nodeId"]) == "" {
		return nil, action, cloudAgentFieldError("nodeId", "required", "分镜工具需要 nodeId")
	}
	return fields, action, nil
}
func storyboardCallFields(call cloudAgentCall, fields map[string]any, keys ...string) cloudAgentCall {
	subset := map[string]any{}
	for _, key := range keys {
		if value, exists := fields[key]; exists {
			subset[key] = value
		}
	}
	raw, _ := json.Marshal(subset)
	call.Function.Arguments = string(raw)
	return call
}

func (s *Service) executeCloudAgentStoryboard(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	fields, action, err := storyboardArguments(call)
	if err != nil {
		return s.recordStoryboardResult(run, state, call, nil, err)
	}
	if action == "read" {
		result, err := cloudAgentReadToolCached(s.repo, run.UserID, state, call, s)
		if resultMap, ok := result.(map[string]any); ok && fields["view"] == nil && fields["fields"] == nil {
			resultMap["markdown"] = cloudAgentStoryboardMarkdown(resultMap)
		}
		return s.recordStoryboardResult(run, state, call, result, err)
	}
	if state.Request.PermissionMode == "read_only" || (state.Subagent != nil && !state.Subagent.WriteGranted) {
		return s.recordStoryboardResult(run, state, call, nil, kernel.Forbidden("当前权限只允许读取分镜"))
	}
	if action == "split" || action == "resplit" {
		return s.executeCloudAgentStoryboardSplit(run, state, call, fields, action)
	}
	internal, replayErr := normalizedStoryboardWriteCall(call, fields, action)
	if replayErr != nil {
		return s.recordStoryboardResult(run, state, call, nil, replayErr)
	}
	if result, found, err := cloudAgentStoryboardReplay(s.repo, run.UserID, state.Request.CanvasID, internal); found || err != nil {
		return s.recordStoryboardResult(run, state, call, result, err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		internal, err := prepareStoryboardWriteCall(repo, run.UserID, state.Request.CanvasID, call, fields, action)
		if err != nil {
			if !cloudAgentToolErrorIsBusiness(err) {
				return err
			}
			cloudAgentRecordToolResult(current, state, call, nil, err)
			return cloudAgentSave(current, state)
		}
		if state.Request.PermissionMode == "request_approval" && state.Approval == nil {
			plan, err := prepareCloudAgentStoryboardEdit(repo, run.UserID, state.Request.CanvasID, internal)
			if err != nil {
				if !cloudAgentToolErrorIsBusiness(err) {
					return err
				}
				cloudAgentRecordToolResult(current, state, call, nil, err)
				return cloudAgentSave(current, state)
			}
			requestStoryboardApproval(current, state, call, plan.Preview, "")
			return cloudAgentSave(current, state)
		}
		result, err := applyCloudAgentStoryboardMutation(repo, run.UserID, state.Request.CanvasID, internal, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		if err != nil && !cloudAgentToolErrorIsBusiness(err) {
			return err
		}
		cloudAgentRecordToolResult(current, state, call, result, err)
		return cloudAgentSave(current, state)
	})
}

func prepareStoryboardWriteCall(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, fields map[string]any, action string) (cloudAgentCall, error) {
	switch action {
	case "update", "lock", "unlock", "set_assets", "add_assets", "remove_assets", "upsert_assets", "set_references", "add_references", "remove_references", "bind_output", "unbind_output":
	default:
		return call, cloudAgentFieldError("action", "invalid_value", "未知分镜 action；生成分镜请使用 split 或 resplit")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return call, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return call, err
	}
	_, _, rows, err := storyboardNodeFromDocument(doc, stringValue(fields["nodeId"]))
	if err != nil {
		return call, err
	}
	ids := cloudAgentStrings(fields["rowIds"])
	if id := stringValue(fields["rowId"]); id != "" {
		ids = append(ids, id)
	}
	for _, update := range creationMaps(fields["updates"]) {
		ids = append(ids, stringValue(update["rowId"]))
	}
	if cloudAgentStoryboardSharedAction(action) {
		ids = nil
		for _, row := range rows {
			ids = append(ids, stringValue(row["id"]))
		}
	} else if len(ids) == 0 {
		return call, cloudAgentFieldError("rowIds", "required", "请指定要修改的真实镜头 ID")
	}
	if !cloudAgentStoryboardSharedAction(action) {
		if err := validateCloudAgentStoryboardRowIDs(rows, ids, "rowIds"); err != nil {
			return call, err
		}
	}
	for _, row := range rows {
		if row["locked"] == true && action != "unlock" && cloudAgentContainsString(ids, stringValue(row["id"])) {
			return call, cloudAgentFieldError("rowIds", "locked", fmt.Sprintf("镜头 %v 已锁定，请先请用户解锁或跳过", row["shotNumber"]))
		}
	}
	return normalizedStoryboardWriteCall(call, fields, action)
}

func normalizedStoryboardWriteCall(call cloudAgentCall, fields map[string]any, action string) (cloudAgentCall, error) {
	internal := storyboardCallFields(call, fields, "operationId", "storyboardRevision", "snapshotHash", "nodeId", "rowId", "rowIds", "patch", "updates", "assets", "references", "referenceNodeIds", "outputMode", "outputNodeId")
	var args map[string]any
	_ = json.Unmarshal([]byte(internal.Function.Arguments), &args)
	if action == "lock" || action == "unlock" {
		args["action"] = "update"
		args["patch"] = map[string]any{"locked": action == "lock"}
	} else if action == "update" && fields["updates"] != nil {
		args["action"] = "batch_update"
	} else {
		args["action"] = action
	}
	if action == "update" {
		patches := []map[string]any{}
		if patch, ok := fields["patch"].(map[string]any); ok {
			patches = append(patches, patch)
		}
		for _, update := range creationMaps(fields["updates"]) {
			if patch, ok := update["patch"].(map[string]any); ok {
				patches = append(patches, patch)
			}
		}
		for _, patch := range patches {
			for key := range patch {
				if key == "locked" || key == "mediaReview" {
					return call, cloudAgentFieldError("patch."+key, "managed_field", "锁定请用 lock/unlock；媒体复核标记由画布管理")
				}
			}
		}
	}
	raw, _ := json.Marshal(args)
	internal.Function.Arguments = string(raw)
	return internal, nil
}
func requestStoryboardApproval(current *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, preview cloudAgentApprovalPreview, quoteHash string) {
	state.Approval = &cloudAgentApproval{ID: fmt.Sprintf("%s-%d-%d", current.ID, state.Step, state.CallIndex), Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview, QuoteHash: quoteHash}
	current.Status = "waiting_approval"
	state.event(current.ID, "approval_requested", map[string]any{"approvalId": state.Approval.ID, "toolName": "storyboard", "arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "text": preview.Description})
}

func (s *Service) executeCloudAgentStoryboardSplit(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, fields map[string]any, action string) error {
	rowIDs := cloudAgentStrings(fields["rowIds"])
	if err := cloudAgentSplitRowAndBudgetGate(state, action, rowIDs); err != nil {
		return s.recordStoryboardResult(run, state, call, nil, err)
	}
	orders, err := s.repo.BillingOrdersByTaskIDs(run.UserID, state.TaskIDs)
	if err != nil {
		return err
	}
	remaining := int64(math.Floor(state.Request.Budget.MaxCredits * float64(CreditScale)))
	for _, order := range orders {
		remaining -= order.AmountMicrocredits
	}
	if remaining < 0 {
		return s.recordStoryboardResult(run, state, call, nil, BadAuthRequest("Agent 累计预算已耗尽"))
	}
	logical, channel, key, fallback, err := s.cloudAgentResolveSplitModelSelection(run, state, fields)
	if err != nil {
		return s.recordStoryboardResult(run, state, call, nil, err)
	}
	if logical != "" && (channel != "" || key != "") {
		return s.recordStoryboardResult(run, state, call, nil, cloudAgentFieldError("logicalModelId", "mutually_exclusive", "模型选择不能并用"))
	}
	req := CanvasStoryboardSplitRequest{NodeID: stringValue(fields["nodeId"]), RowIDs: rowIDs, Request: stringValue(fields["request"]), ShotCount: int(numberValue(fields["shotCount"], 0)), ShotDurationSeconds: int(numberValue(fields["shotDurationSeconds"], 0)), LogicalModelID: logical, Config: map[string]any{"channelId": channel, "channelModelKey": key, "model": key}, ClientID: "cloud-agent", OperationID: cloudAgentID(run.UserID, fmt.Sprintf("storyboard:%s:%d:%s", run.ID, state.Step, call.ID))}
	approvalID := ""
	if state.Approval != nil {
		approvalID = state.Approval.ID
	}
	admission := &taskAdmission{ID: cloudAgentID(run.UserID, fmt.Sprintf("%s:task:%d", run.ID, len(state.TaskIDs))), MaxCharge: remaining, AgentRunID: run.ID, ApprovalID: approvalID}
	prepareStart := time.Now()
	plan, err := s.prepareCanvasStoryboardSplit(run.UserID, state.Request.CanvasID, req, admission)
	log.Printf("agent storyboard split prepare: run=%s call=%s action=%s fallback=%t duration=%s err=%v", run.ID, call.ID, action, fallback, time.Since(prepareStart), err)
	if err != nil {
		return s.recordStoryboardResult(run, state, call, nil, err)
	}
	if state.Approval != nil {
		if err := s.verifyStoryboardQuote(state.Approval.QuoteHash, plan.quoteKey); err != nil {
			return s.recordStoryboardResult(run, state, call, nil, err)
		}
	}
	s.storageMu.Lock()
	commitStart := time.Now()
	err = s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, s.cloudAgentCommitStoryboardSplitMutation(run, state, call, action, req, plan, fallback))
	s.storageMu.Unlock()
	log.Printf("agent storyboard split commit: run=%s call=%s action=%s duration=%s err=%v", run.ID, call.ID, action, time.Since(commitStart), err)
	if err != nil {
		return s.recordStoryboardResult(run, state, call, nil, err)
	}
	s.wakeTaskDispatcher()
	return nil
}

// cloudAgentSplitRowAndBudgetGate 校验拆镜入参形态与生成容量：resplit 必须带连续
// rowIds、split 不接受 rowIds，且本步待回执生成数与轮内生成预算都不能超限。
func cloudAgentSplitRowAndBudgetGate(state *cloudAgentRuntime, action string, rowIDs []string) error {
	if (action == "resplit" && len(rowIDs) == 0) || (action == "split" && len(rowIDs) > 0) {
		return cloudAgentFieldError("rowIds", "invalid_value", "resplit 必须指定连续镜头；split 不接受 rowIds")
	}
	if len(state.PendingGenerations) >= cloudAgentMaxPendingGenerations || (state.Request.Budget.MaxGenerationTasks > 0 && state.Generations >= state.Request.Budget.MaxGenerationTasks) {
		return BadAuthRequest("本轮生成数量已达到上限，请等待已有任务完成或调整预算")
	}
	return nil
}

// cloudAgentResolveSplitModelSelection 解析拆镜的模型选择：selectionId 与显式
// logicalModelId/channel 三元组互斥，selectionId 展开成三元组。三种凭证完全
// 省略时回退本轮运行的系统渠道模型对——同一个模型对就是模型步骤用的写作
// 模型（cloud_agent_pi_model_step 的 config），必然存在且是文本模型；没有它的
// 运行连模型步都走不了，所以不给第三层兜底，缺渠道对时明确报错。
func (s *Service) cloudAgentResolveSplitModelSelection(run *model.CloudAgentExecution, state *cloudAgentRuntime, fields map[string]any) (string, string, string, bool, error) {
	logical, channel, key := stringValue(fields["logicalModelId"]), stringValue(fields["channelId"]), stringValue(fields["channelModelKey"])
	fallback := false
	if token := stringValue(fields["selectionId"]); token != "" {
		if logical != "" || channel != "" || key != "" {
			return "", "", "", fallback, cloudAgentFieldError("selectionId", "mutually_exclusive", "selectionId 与其他模型选择不能并用")
		}
		selected, err := s.resolveCloudAgentSelectionID(run.UserID, token)
		if err != nil {
			return "", "", "", fallback, err
		}
		logical, channel, key = selected.LogicalModelID, selected.ChannelID, selected.ChannelModelKey
	} else if logical == "" && channel == "" && key == "" {
		fallback = true
		channel, key = state.Request.ChannelID, state.Request.ChannelModelKey
		if channel == "" || key == "" {
			return "", "", "", fallback, InvalidModelSelection("本轮运行缺少系统渠道模型，无法回退拆镜；请重新发起运行或显式提供文本模型凭证")
		}
	}
	if err := s.cloudAgentAssertSplitModelCapability(channel, key, fallback); err != nil {
		return "", "", "", fallback, err
	}
	return logical, channel, key, fallback, nil
}

// cloudAgentAssertSplitModelCapability 把能力不匹配拦在任务准入之前：拆镜任务
// 固定 mode=text，图像/音频模型凭证即使过了 schema 也必然在 CreateTask 被拒，
// 提前报错才能让模型在下一步自纠（省略选择即回退本轮模型）。
func (s *Service) cloudAgentAssertSplitModelCapability(channelID, modelKey string, fallback bool) error {
	if channelID == "" || modelKey == "" {
		return nil
	}
	models, err := s.repo.ChannelModels(channelID, false)
	if err != nil {
		return err
	}
	for i := range models {
		item := &models[i]
		if item.ModelKey != modelKey {
			continue
		}
		if normalizeCapability(item.Capability) == "text" {
			return nil
		}
		if fallback {
			return InvalidModelSelection(fmt.Sprintf("本轮运行的渠道模型 %s 能力是 %s，不支持文本拆镜；请确认写作模型，或显式提供文本模型凭证", modelKey, item.Capability))
		}
		return cloudAgentFieldError("selectionId", "capability_mismatch",
			fmt.Sprintf("拆镜使用文本模型：渠道模型 %s 的能力是 %s；请省略模型选择由服务端使用本轮文本模型，或改用文本模型凭证", modelKey, item.Capability))
	}
	return InvalidModelSelection(fmt.Sprintf("渠道模型 %s 不存在或已停用，无法用于拆镜；请省略模型选择由服务端使用本轮文本模型", modelKey))
}

// cloudAgentCommitStoryboardSplitMutation 在 Agent 存储事务里落地拆镜：首次执行
// 请求审批，已批准则提交计费任务并登记待回执生成。
func (s *Service) cloudAgentCommitStoryboardSplitMutation(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, action string, req CanvasStoryboardSplitRequest, plan *canvasStoryboardSplitPlan, fallback bool) func(current *model.CloudAgentExecution, repo *repository.Repository) error {
	return func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		if state.Request.PermissionMode == "request_approval" && state.Approval == nil {
			preview := cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "确认分镜拆镜", Description: fmt.Sprintf("从画布剧情和风格执行%s，预计费用 %.4f 积分；结果由服务端写回。", action, float64(plan.Result.Quote.AmountMicrocredits)/float64(CreditScale))}
			requestStoryboardApproval(current, state, call, preview, plan.Result.Quote.QuoteHash)
			return cloudAgentSave(current, state)
		}
		handle, err := plan.submit(repo, s)
		if err != nil {
			return err
		}
		plan.Result.TaskID = handle.TaskID
		state.TaskIDs = append(state.TaskIDs, handle.TaskID)
		state.Generations++
		pending := cloudAgentPendingGeneration{TaskID: handle.TaskID, NodeID: req.NodeID, CallID: call.ID, Mode: "storyboard", Title: "分镜拆镜", SubmittedAt: time.Now().UTC()}
		state.PendingGenerations = append(state.PendingGenerations, pending)
		state.MediaTaskID = handle.TaskID
		state.event(run.ID, "generation_task_created", map[string]any{"toolName": "storyboard", "taskId": handle.TaskID, "nodeId": req.NodeID, "mode": "storyboard", "canvasId": state.Request.CanvasID})
		cloudAgentRecordToolResult(current, state, call, cloudAgentStoryboardSubmittedReceipt(&plan.Result, action, req.NodeID, len(state.PendingGenerations), fallback), nil)
		return cloudAgentSave(current, state)
	}
}

func cloudAgentStoryboardSubmittedReceipt(result *CanvasStoryboardSplitResult, action, nodeID string, pending int, fallback bool) map[string]any {
	next := "拆镜在后台执行；写回后在同一任务的下一步通过系统通知告知结果。依赖结果时调用 wait_generations，不要轮询 task_get"
	if fallback {
		next = "未传模型选择，服务端使用本轮运行的文本模型拆镜。" + next
	}
	return map[string]any{"status": "submitted", "taskSubmitted": true, "taskId": result.TaskID, "nodeId": nodeID, "action": action, "partial": result.Partial, "rowIds": result.RowIDs, "pendingGenerations": pending, "modelFallback": fallback, "next": next}
}
func cloudAgentStoryboardMarkdown(result map[string]any) string {
	board, _ := result["storyboard"].(map[string]any)
	rows := creationMaps(board["rows"])
	lines := []string{"| 镜头 | rowId | 时长 | 景别 | 画面 | 台词 | 角色 | 锁定 | 待复核 |", "|---:|---|---:|---|---|---|---|---|---|"}
	cell := func(value any) string {
		if value == nil {
			return ""
		}
		text := fmt.Sprint(value)
		text = strings.ReplaceAll(text, "\r", "")
		text = strings.ReplaceAll(text, "\n", "<br>")
		return strings.ReplaceAll(text, "|", "\\|")
	}
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s |", cell(row["shotNumber"]), cell(row["rowId"]), cell(row["durationSeconds"]), cell(row["shotSize"]), cell(row["plotDescription"]), cell(row["dialogue"]), cell(row["characters"]), cell(row["locked"]), cell(row["mediaReview"])))
	}
	return strings.Join(lines, "\n")
}
