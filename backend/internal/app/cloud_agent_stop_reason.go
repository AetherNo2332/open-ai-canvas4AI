package app

import (
	"strings"
)

// 上游终止原因（stop reason）的归一化与处置判据。
//
// 背景：在这之前，整个后端没有一处读取上游的终止原因（OpenAI `finish_reason` / Claude
// `stop_reason` / Responses 的 `status`+`incomplete_details`），恢复阶梯只能靠匹配上游错误
// 字符串（"工具参数不是完整 JSON"、"没有返回内容"）来倒推。于是"任务成功但输出被输出上限
// 截断"与"模型正常说完"在服务端完全同形：截断稿既不会被重试，也可能被当成最终答复发布。
//
// 上游文档把这件事写成了硬要求（Anthropic《Handling stop reasons》）：按 stop_reason 分支，
// `tool_use` 执行工具、`max_tokens` 走截断处理、`end_turn` 收尾，不要按 content 猜。
// 这里把各协议的说法收敛成一套内部词表，供运行时与事件流共用。

const (
	// cloudAgentStopKindStop 是"模型正常结束"：OpenAI `stop`、Claude `end_turn` / `stop_sequence`。
	cloudAgentStopKindStop = "stop"
	// cloudAgentStopKindToolCalls 是"模型要求调用工具"：OpenAI `tool_calls` / `function_call`、Claude `tool_use`。
	cloudAgentStopKindToolCalls = "tool_calls"
	// cloudAgentStopKindLength 是"输出被输出上限截断"：OpenAI `length`、Claude `max_tokens`。
	// 只有这一类才允许被当作"内容不完整"处置。
	cloudAgentStopKindLength = "length"
	// cloudAgentStopKindContextLimit 是"输入先撞上模型上下文窗口"（Anthropic `model_context_window_exceeded`）。
	// 它与输出截断不是一回事：属于预算/压缩问题，不能靠关思考或放大输出预算解决。
	cloudAgentStopKindContextLimit = "context_limit"
	// cloudAgentStopKindPause 是"服务端工具挂起"（Anthropic `pause_turn`），重发同一轮即可继续。
	cloudAgentStopKindPause = "pause"
	// cloudAgentStopKindRefusal 是拒答或内容过滤（Claude `refusal`、OpenAI `content_filter`）。
	cloudAgentStopKindRefusal = "refusal"
	// cloudAgentStopKindUnknown 是拿不到终止原因（老上游、纯文本流、字段缺失）。
	// 显式记 unknown，避免把"不知道"当成"正常结束"。
	cloudAgentStopKindUnknown = "unknown"
)

// normalizeCloudAgentStopReason 把各协议的原始终止原因收敛成内部词表。
// 各协议的取值基本不重叠，因此不需要按协议分支；未识别的取值一律落到 unknown。
func normalizeCloudAgentStopReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return cloudAgentStopKindUnknown
	case "stop", "end_turn", "stop_sequence", "completed":
		return cloudAgentStopKindStop
	case "tool_calls", "function_call", "tool_use":
		return cloudAgentStopKindToolCalls
	case "length", "max_tokens", "max_output_tokens", "incomplete":
		return cloudAgentStopKindLength
	case "model_context_window_exceeded":
		return cloudAgentStopKindContextLimit
	case "pause_turn":
		return cloudAgentStopKindPause
	case "refusal", "content_filter":
		return cloudAgentStopKindRefusal
	default:
		return cloudAgentStopKindUnknown
	}
}

// cloudAgentResponsesStopReason 取 Responses 协议的终止原因。
// 非流式取 `status` + `incomplete_details.reason`；流式拿不到 response 时由调用方传事件名兜底。
func cloudAgentResponsesStopReason(status string, response map[string]interface{}) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(stringField(response, "status")))
	}
	switch status {
	case "incomplete":
		// `incomplete_details.reason` 才是截断与内容过滤的区分点（如 max_output_tokens / content_filter）。
		if details, ok := response["incomplete_details"].(map[string]interface{}); ok {
			if reason := strings.TrimSpace(stringField(details, "reason")); reason != "" {
				return reason
			}
		}
		return "incomplete"
	case "completed":
		return "completed"
	case "failed", "cancelled", "canceled", "queued", "in_progress":
		// 这些不是"说完了"：返回值刻意不等于 completed，交给归一化落到 unknown。
		return status
	default:
		return status
	}
}

// cloudAgentStopReasonTruncated 判断这次终止是否"输出被截断"。
// 只有输出上限截断才允许走关思考 / 放大输出预算的恢复阶梯，也才禁止把正文当最终答复。
func cloudAgentStopReasonTruncated(kind string) bool {
	return kind == cloudAgentStopKindLength
}

// cloudAgentStopReasonLabel 是给用户与事件流看的一句人话。
func cloudAgentStopReasonLabel(kind, raw string) string {
	switch kind {
	case cloudAgentStopKindStop:
		return "模型正常结束"
	case cloudAgentStopKindToolCalls:
		return "模型请求调用工具"
	case cloudAgentStopKindLength:
		return "模型输出达到输出上限被截断"
	case cloudAgentStopKindContextLimit:
		return "输入已撞上模型上下文窗口"
	case cloudAgentStopKindPause:
		return "上游服务端工具挂起（pause_turn）"
	case cloudAgentStopKindRefusal:
		return "上游拒绝回答或触发内容过滤"
	default:
		if strings.TrimSpace(raw) == "" {
			return "上游没有返回终止原因"
		}
		return "上游返回了未识别的终止原因：" + raw
	}
}

// cloudAgentApplyStopReason 把原始终止原因与归一化结果写进上游结果契约。
// 两个键始终写：拿不到原因时记 unknown，避免下游把"不知道"读成"正常结束"。
func cloudAgentApplyStopReason(result map[string]interface{}, raw string) {
	if result == nil {
		return
	}
	raw = strings.TrimSpace(raw)
	result["stopReason"] = raw
	result["stopReasonKind"] = normalizeCloudAgentStopReason(raw)
}

// cloudAgentMaxTruncatedStepEscalations 是"输出被截断"允许的升级重试次数。
// 与空输出、单步超时一样只给一次：每一次都关掉思考并放大输出预算，成功与否下一步就知道。
const cloudAgentMaxTruncatedStepEscalations = 1

// cloudAgentEscalateTruncatedStep 记录一次"输出被截断"的升级重试：关思考 + 放大输出预算，重发同一步。
// 只改内存态并落事件，**不开新事务**：调用点本身已经在 MutateCloudAgent 的事务里，
// 在这里再开一次事务会在 SQLite 上自锁（实测 busy_timeout 5s 后报 database is locked）。
func cloudAgentEscalateTruncatedStep(runID string, state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	state.TruncatedStepEscalated++
	state.ForceThinkingOff = true
	state.BoostStepOutputBudget = true
	state.ActiveTaskID = ""
	state.Calls = nil
	state.CallIndex = 0
	if runID != "" {
		state.event(runID, "model_failure_recovered", map[string]any{
			"reason": "truncated_output_escalated",
			"text":   "上一步输出达到输出上限被截断；已关闭思考并放大输出预算重试同一步",
		})
	}
}

// cloudAgentStopReasonPayload 组装 step 级终止原因事件载荷。
// 只放枚举、计数与短标识：事件载荷有 128KiB 上限，且这些读数要能在事件流里直接聚合。
// 长度一律按字节（textBytes/reasoningBytes），与项目其它读数口径一致。
func cloudAgentStopReasonPayload(step int, taskID, raw, kind string, textBytes, reasoningBytes, toolCalls int) map[string]any {
	return map[string]any{
		"step":           step,
		"taskId":         taskID,
		"stopReason":     raw,
		"stopReasonKind": kind,
		"truncated":      cloudAgentStopReasonTruncated(kind),
		"textBytes":      textBytes,
		"reasoningBytes": reasoningBytes,
		"toolCalls":      toolCalls,
	}
}
