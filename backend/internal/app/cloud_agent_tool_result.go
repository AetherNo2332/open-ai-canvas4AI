package app

import (
	"encoding/json"
	"errors"
	"strings"

	"yingce/backend/internal/repository"
)

// cloudAgentToolResult records one tool receipt: the event, the tool message in
// the canonical transcript and the advanced call cursor. It reports whether the
// tool's repair budget is exhausted.
func cloudAgentToolResult(runID string, state *cloudAgentRuntime, call cloudAgentCall, result any, err error) bool {
	payload := map[string]any{"toolName": call.Function.Name, "callId": call.ID, "arguments": call.Function.Arguments}
	if state.PiToolBatchTaskID != "" {
		payload["taskId"] = state.PiToolBatchTaskID
	}
	kind := "tool_completed"
	if err != nil {
		// The upgraded argument error is also what tool repair tracking sees below.
		err = cloudAgentUpgradeMissingArgumentError(state, call, err)
		result = cloudAgentToolFailureDetail(state, call, result, err, payload)
		kind = "tool_failed"
	} else {
		payload["text"] = "工具执行成功"
	}
	if cloudAgentReadCall(call) {
		result = state.withReadBudget(result)
	}
	if inspection, ok := result.(cloudAgentImageInspection); ok && err == nil {
		cloudAgentRecordImageInspectionResult(runID, state, call, inspection, kind, payload)
		return false
	}
	exhausted := cloudAgentTrackToolRepair(runID, state, call, result, err, payload)
	raw, _ := json.Marshal(cloudAgentModelToolResult(call.Function.Name, result))
	payload["result"] = result
	state.event(runID, kind, payload)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(raw)})
	state.CallIndex++
	if state.CallIndex >= len(state.Calls) {
		cloudAgentFlushPendingImages(state)
	}
	state.Approval = nil
	return exhausted
}

// cloudAgentToolFailureDetail builds the failed-tool receipt detail and sets
// payload text and errorClass. err is already upgraded by the caller. Annotation
// order matches the original single function: preflight, lease/version/media,
// argument feedback, then the stable error class.
func cloudAgentToolFailureDetail(state *cloudAgentRuntime, call cloudAgentCall, result any, err error, payload map[string]any) map[string]any {
	detail, ok := result.(map[string]any)
	if !ok || detail == nil {
		detail = map[string]any{}
	}
	message := cloudAgentSafeToolError(err)
	detail["error"] = message
	admissionSkip := cloudAgentAnnotatePreflight(detail, payload, err)
	cloudAgentAnnotateMediaFailure(detail, call, err)
	cloudAgentAnnotateArgumentFailure(state, call, detail, err)
	// 稳定归类 + 可行动字段（handoff 工作项 B 第一步）：只加标注，不改任何放行/拒绝判定。
	// allowed 与执行器用的是同一份判定（cloudAgentToolAllowed 是纯函数，结果一致），
	// 只在失败路径上算一次，成功路径不付这份开销。call_skipped 已经在上面定过性，
	// 不要再被通用归类覆盖成"参数错误"。
	if class, retryable, requiredAction := cloudAgentToolErrorClass(state.Request, call, err, cloudAgentToolAllowed(state.Request, call.Function.Name)); class != "" && !admissionSkip {
		detail["errorClass"], detail["errorClassLabel"] = class, cloudAgentToolErrorLabel(class)
		detail["retryable"] = retryable
		if requiredAction != "" {
			detail["requiredAction"] = requiredAction
		}
		payload["errorClass"] = class
		if class == cloudAgentToolErrorStateConflict && requiredAction == "reread_canvas" {
			cloudAgentSnapshotFeedback(state, call, detail)
		}
	}
	payload["text"] = message
	return detail
}

// 模型漏了必填字段（或参数不是对象）时，即便业务校验抛的是普通 AppError，也按
// "参数契约错误"处理：给出本轮字段修正信息，归类也随之变成
// schema_error（handoff 工作项 B：缺必填字段必须能自纠，且不能只算普通工具失败）。
// 只在"业务侧确实按参数问题拒绝了"（400/422）时才这么归类：上游 5xx 或网络错误
// 即便模型同时漏了字段，也该算上游故障，别把锅扣到参数上。
func cloudAgentUpgradeMissingArgumentError(state *cloudAgentRuntime, call cloudAgentCall, err error) error {
	var appErr *AppError
	var existingArgumentErr *cloudAgentArgumentError
	if missing := cloudAgentMissingRequiredArguments(cloudAgentAdvertisedTools(state), call); len(missing) > 0 &&
		!errors.As(err, &existingArgumentErr) &&
		errors.As(err, &appErr) && appErr != nil && (appErr.Status == 400 || appErr.Status == 422) {
		return &cloudAgentFieldArgumentError{
			error: &cloudAgentArgumentError{err},
			Field: strings.Join(missing, ","), Issue: "required",
		}
	}
	return err
}

// 预检结论：机器可读地说明"为什么这次没执行"。批次策略类结论不是模型的参数错，
// 因此单独给 reason=call_skipped 与可重发的 requiredAction，并跳过通用归类。
func cloudAgentAnnotatePreflight(detail, payload map[string]any, err error) bool {
	var preflightErr *cloudAgentCallAdmissionError
	if !errors.As(err, &preflightErr) {
		return false
	}
	detail["admission"] = preflightErr.Admission
	if preflightErr.Field != "" {
		detail["field"] = preflightErr.Field
	}
	if !cloudAgentAdmissionIsSkip(preflightErr.Admission) {
		return false
	}
	detail["reason"] = "call_skipped"
	detail["retryable"] = true
	detail["requiredAction"] = "resubmit_next_step"
	detail["errorClass"], detail["errorClassLabel"] = "call_skipped", cloudAgentToolErrorLabel("call_skipped")
	payload["errorClass"] = "call_skipped"
	return true
}

// cloudAgentAnnotateMediaFailure adds lease, media version and media admission
// reasons, including the new_version example arguments.
func cloudAgentAnnotateMediaFailure(detail map[string]any, call cloudAgentCall, err error) {
	var admissionErr *cloudAgentMediaAdmissionError
	var lease *repository.CanvasEditLeaseError
	var versionErr *cloudAgentMediaVersionError
	if errors.As(err, &lease) {
		detail["reason"], detail["canvasId"], detail["nodeId"] = lease.Reason, lease.CanvasID, lease.NodeID
		detail["guidance"] = "本次工具副作用已回滚；不要立即重复写入。等待节点编辑结束，或让编辑客户端重新获取有效证明；可继续处理其他节点或只读工作"
	}
	if errors.As(err, &versionErr) {
		detail["reason"] = versionErr.reason
	}
	if !errors.As(err, &admissionErr) {
		return
	}
	detail["reason"], detail["nodeId"] = admissionErr.Reason, admissionErr.NodeID
	if admissionErr.TaskID != "" {
		detail["taskId"] = admissionErr.TaskID
	}
	if admissionErr.Reason == "new_version_required" {
		var args cloudAgentMediaArgs
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		example := map[string]any{"mode": args.Mode, "nodeId": admissionErr.NodeID, "intent": "new_version", "snapshotHash": "<canvas_get_state.snapshotHash>", "prompt": "<用户确认的新版本提示词>", "selectionId": "<模型目录返回的 selectionId>"}
		if args.StoryboardNodeID != "" {
			delete(example, "nodeId")
			delete(example, "prompt")
			example["storyboardNodeId"], example["storyboardRowId"] = args.StoryboardNodeID, args.StoryboardRowID
		}
		detail["exampleArguments"] = example
		detail["guidance"] = "先确认用户确实需要重新付费生成，再用新版本意图提交；不要删除原任务或解绑产物"
	}
}

func cloudAgentAnnotateArgumentFailure(state *cloudAgentRuntime, call cloudAgentCall, detail map[string]any, err error) {
	var argumentErr *cloudAgentArgumentError
	if !errors.As(err, &argumentErr) {
		return
	}
	detail["reason"] = "invalid_tool_arguments"
	var fieldErr *cloudAgentFieldArgumentError
	if errors.As(err, &fieldErr) {
		detail["field"], detail["issue"] = fieldErr.Field, fieldErr.Issue
	}
	cloudAgentArgumentFeedback(state, call, detail)
	var rowErr *cloudAgentStoryboardRowIDError
	if errors.As(err, &rowErr) {
		detail["invalidRowIds"], detail["duplicateRowIds"] = rowErr.InvalidRowIDs, rowErr.DuplicateRowIDs
	}
	if cloudAgentReferenceMigrationBlocked(err) {
		detail["reason"] = detail["issue"]
		detail["guidance"] = "资产尚未关联，请用户在画布的确认素材引用入口完成规范化后再继续；停止改参数重试，不得用其他字段替代或报告关联成功"
		delete(detail, "parameters")
		delete(detail, "argumentHints")
		delete(detail, "exampleArguments")
	}
}

// cloudAgentRecordImageInspectionResult records a successful image inspection:
// the text receipt goes into the transcript now and staged images are flushed as
// one user message after the last call of the batch.
func cloudAgentRecordImageInspectionResult(runID string, state *cloudAgentRuntime, call cloudAgentCall, inspection cloudAgentImageInspection, kind string, payload map[string]any) {
	// 重复查看时只回执文字（ImageURL 为空），不再附图。
	staged := strings.TrimSpace(inspection.ImageURL) != ""
	if staged {
		cloudAgentStageImageInspection(state, inspection)
	}
	lastOfBatch := state.CallIndex+1 >= len(state.Calls)
	if lastOfBatch {
		// 本批最后一次看图：把"这一批到底附了哪几张"写进回执，模型不必再靠试探猜。
		// 必须赶在下面序列化回执之前补上，否则这条说明对模型不可见。
		cloudAgentDescribeImageBatch(state, inspection.Receipt)
	}
	// 看图成功（含按修正后的格式保存摘要）同样结束这个工具的纠错组并恢复完整工具集，
	// 否则修好一张图后，另一张图的新错误会接着旧次数熔断。
	cloudAgentTrackToolRepair(runID, state, call, inspection, nil, payload)
	receipt, _ := json.Marshal(inspection.Receipt)
	payload["result"] = inspection.Receipt
	state.event(runID, kind, payload)
	// tool 角色只接受字符串内容（四种上游图式都是纯文本），因此工具回执照常入历史，
	// 图片另起一条 user 消息携带，并显式标注为数据而非指令。
	state.Canonical.Messages = append(state.Canonical.Messages,
		map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(receipt)})
	// 一批里可能有多个调用（模型一次发起并行 tool calls），上游要求
	// assistant(tool_calls) 之后紧跟每一个 tool_call_id 的 tool 消息，所以图片
	// 不能插在 tool 结果之间。只有本批最后一个调用执行完，才把整批缓冲合并成
	// 一条 user 消息追加在全部 tool 结果之后。
	if lastOfBatch {
		cloudAgentFlushPendingImages(state)
	}
	state.CallIndex++
	state.Approval = nil
}
