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
	// cloudAgentStopKindPause 是"服务端工具挂起"（Anthropic `pause_turn`）。
	// 正确续轮要把暂停的 assistant 消息以**原始内容块**追加后重发（官方配方），而当前结果
	// 契约只有 text/reasoning/toolCalls，无法证明原始块能无损往返，所以本轮判失败、不发布。
	cloudAgentStopKindPause = "pause"
	// cloudAgentStopKindRefusal 是明确拒答（Claude `refusal`）。
	cloudAgentStopKindRefusal = "refusal"
	// cloudAgentStopKindContentFilter 是内容过滤（OpenAI `content_filter`）。它与拒答分开记：
	// 过滤可能意味着**内容被省略**，不能凭"正文非空"就当完整答复，也不该走输出预算重试。
	cloudAgentStopKindContentFilter = "content_filter"
	// cloudAgentStopKindIncompleteUnknown 是 Responses 报了 incomplete 但没给 `incomplete_details.reason`。
	// 原因未知时既不猜成输出截断去重试，也不发布半截答复：如实失败。
	cloudAgentStopKindIncompleteUnknown = "incomplete_unknown"
	// cloudAgentStopKindUnknown 是拿不到终止原因（老上游、纯文本流、字段缺失）。
	// 显式记 unknown，避免把"不知道"当成"正常结束"；它**不等于**"收到了终态事件"，
	// 后者由 parser 的 `terminalEventSeen` 单独记录（见 cloudAgentStepStopFacts）。
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
	case "length", "max_tokens", "max_output_tokens":
		return cloudAgentStopKindLength
	case "model_context_window_exceeded":
		return cloudAgentStopKindContextLimit
	case "pause_turn":
		return cloudAgentStopKindPause
	case "refusal":
		return cloudAgentStopKindRefusal
	case "content_filter":
		return cloudAgentStopKindContentFilter
	// Responses 只报 incomplete 而没给 reason：原因未知，单列一类（不猜成输出截断）。
	case "incomplete":
		return cloudAgentStopKindIncompleteUnknown
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
		return "上游拒绝回答这次请求"
	case cloudAgentStopKindContentFilter:
		return "上游按内容策略过滤/省略了这次回答"
	case cloudAgentStopKindIncompleteUnknown:
		return "上游报未完成但没说明原因"
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

// cloudAgentApplyStopFacts 把"上游给没给终结信号"的三个解析事实写进结果契约。
// 它们与 stopReasonKind 分开：kind 是语义归一化，这三个是解析侧的原始事实。
func cloudAgentApplyStopFacts(result map[string]interface{}, terminalEventSeen, stopReasonPresent, streamDoneSeen bool) {
	if result == nil {
		return
	}
	result["terminalEventSeen"] = terminalEventSeen
	result["stopReasonPresent"] = stopReasonPresent
	result["streamDoneSeen"] = streamDoneSeen
}

// cloudAgentResponsesStatusFailed 判断 Responses 的终态是不是"这次调用本身没成功"。
// failed/cancelled 不属于"内容不完整"，而是上游调用失败：按 parser error 处理，
// 走既有的 model_failure 路径，不能由 unknown→accept 穿过去当成正常结束。
func cloudAgentResponsesStatusFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "cancelled", "canceled":
		return true
	}
	return false
}

// cloudAgentMaxTruncatedStepEscalations 是"输出被截断"允许的升级重试次数。
// 与空输出、单步超时一样只给一次：每一次都关掉思考并放大输出预算，成功与否下一步就知道。
const cloudAgentMaxTruncatedStepEscalations = 1

// 步骤处置：截断这一步先定处置，再决定要不要产生任何副作用。
const (
	// cloudAgentStepDispositionAccept 表示这一步的结果可以照常使用。
	cloudAgentStepDispositionAccept = "accept"
	// cloudAgentStepDispositionRetry 表示这一步的结果整步作废，重发同一步。
	cloudAgentStepDispositionRetry = "retry"
	// cloudAgentStepDispositionFail 表示重试额度已用尽，如实终止本轮。
	cloudAgentStepDispositionFail = "fail"
)

// cloudAgentStepStopDisposition 判定这一步该怎么处置。按 kind 显式分支，没有"其余一律接受"的兜底：
//   - accept：正常结束、请求工具，以及 context_limit（官方明确"响应仍然有效"，只是可能不完整，
//     按部分答复标记发布并给后续请求喂压缩信号）；
//   - retry/fail：只有输出被截断（length）走"关思考 + 放大输出预算"重发一次；
//   - fail：pause / content_filter / refusal / incomplete_unknown —— 这些不是"重发就能变好"的失败，
//     也不允许把半截正文当答复发布。
//
// `unknown` 仍按可用处理（本轮不硬拦）：拿不到终结信号与"没有证据"是两件事，先靠 parser 的
// terminalEventSeen/stopReasonPresent 统计各 provider 的覆盖率，再决定是否升级为阻断。
func cloudAgentStepStopDisposition(state *cloudAgentRuntime, kind string) string {
	switch kind {
	case cloudAgentStopKindStop, cloudAgentStopKindToolCalls,
		cloudAgentStopKindContextLimit, cloudAgentStopKindUnknown:
		return cloudAgentStepDispositionAccept
	case cloudAgentStopKindLength:
		if state != nil && state.TruncatedStepEscalated < cloudAgentMaxTruncatedStepEscalations {
			return cloudAgentStepDispositionRetry
		}
		return cloudAgentStepDispositionFail
	default:
		return cloudAgentStepDispositionFail
	}
}

// cloudAgentStepStopFacts 是"上游到底给没给终结信号"的三个**解析事实**。
// 必须由 parser 记录，不能从归一化 kind 反推：上游给了一个我们还不认识的新词时 kind 会是 unknown，
// 但"确实收到了终态事件"这件事为真，反推会把覆盖率统计做假（评审明确要求）。
type cloudAgentStepStopFacts struct {
	// TerminalEventSeen 表示解析器确实收到该协议的终态事件（OpenAI 非空 finish_reason /
	// Claude message_delta / Responses completed|incomplete|failed）。
	TerminalEventSeen bool
	// StopReasonPresent 表示终止原因原文非空。
	StopReasonPresent bool
	// StreamDoneSeen 表示该协议的流结束标记已收到（OpenAI `[DONE]`、Claude `message_stop`、
	// Responses 的终态事件）。非流式请求天然视为已结束。
	StreamDoneSeen bool
}

// cloudAgentTruncatedStepReason 是截断重试耗尽后 run_failed 的 reason（保留给既有文档与测试）。
const cloudAgentTruncatedStepReason = "truncated_output"

// cloudAgentStepStopFailureReason 是"这一步不该被采纳"时 run_failed 的 reason。
// 截断沿用 `truncated_output`（既有口径），其余按 kind 单列，便于事后按原因聚合。
func cloudAgentStepStopFailureReason(kind string) string {
	if kind == cloudAgentStopKindLength {
		return cloudAgentTruncatedStepReason
	}
	return "step_stop_" + kind
}

// cloudAgentStepStopFailureMessage 是这一步不被采纳时给用户的一句人话：要能直接照着做。
func cloudAgentStepStopFailureMessage(kind string) string {
	switch kind {
	case cloudAgentStopKindLength:
		return "本轮已停止：上一步模型输出达到输出上限被截断（已关闭思考并放大输出预算重试过一次仍未完整）。" +
			"已保留本轮已完成的工作，没有按半截结果执行工具或发布答复；可以继续对话让它接着写，或在管理端调高画布 Agent 单步的输出上限。"
	case cloudAgentStopKindPause:
		return "本轮已停止：上游把这一步挂起（pause_turn）等待续轮，当前版本还不能无损续上这个回合。" +
			"已保留本轮已完成的工作，没有把挂起的半截结果当成答复发布；可以继续对话让它接着做。"
	case cloudAgentStopKindContentFilter:
		return "本轮已停止：上游按内容策略过滤或省略了这一步的回答。已保留本轮已完成的工作，" +
			"没有把不完整的回答当成最终答复发布；可以调整措辞后重试。"
	case cloudAgentStopKindRefusal:
		return "本轮已停止：上游拒绝回答这一步的请求。已保留本轮已完成的工作；可以调整请求内容后重试。"
	case cloudAgentStopKindIncompleteUnknown:
		return "本轮已停止：上游报告这一步未完成，但没有说明原因。已保留本轮已完成的工作，" +
			"没有把不完整的回答当成答复发布；可以重试。"
	default:
		return "本轮已停止：这一步的终止原因（" + kind + "）不允许作为答复发布。已保留本轮已完成的工作。"
	}
}

// cloudAgentEscalationRestore 记录"升级之前"的两个开关值，用于重试被接受后复位。
// 用指针区分"没保存过"与"保存的就是 false"——无条件置 false 会覆盖别的恢复路径已经设过的开关。
type cloudAgentEscalationRestore struct {
	ForceThinkingOff      *bool `json:"forceThinkingOff,omitempty"`
	BoostStepOutputBudget *bool `json:"boostStepOutputBudget,omitempty"`
}

// cloudAgentRestoreEscalationSwitches 在被接受的重试之后把两个开关复位到升级前的值。
// 必须在交给其它恢复阶梯**之前**调用：否则会把空输出/单步超时阶梯刚设的开关又覆盖回去。
func cloudAgentRestoreEscalationSwitches(state *cloudAgentRuntime) {
	if state == nil || state.EscalationRestore == nil {
		return
	}
	if saved := state.EscalationRestore.ForceThinkingOff; saved != nil {
		state.ForceThinkingOff = *saved
	}
	if saved := state.EscalationRestore.BoostStepOutputBudget; saved != nil {
		state.BoostStepOutputBudget = *saved
	}
	state.EscalationRestore = nil
}

// cloudAgentEscalateTruncatedStep 记录一次"输出被截断"的升级重试：关思考 + 放大输出预算，重发同一步。
// 只改内存态并落事件，**不开新事务**：调用点本身已经在 MutateCloudAgent 的事务里，
// 在这里再开一次事务会在 SQLite 上自锁（实测 busy_timeout 5s 后报 database is locked）。
func cloudAgentEscalateTruncatedStep(runID string, state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	state.TruncatedStepEscalated++
	// 记下升级前的开关值：重试被接受后要复位（见 cloudAgentRestoreEscalationSwitches）。
	// 只在第一次升级时保存，避免二次升级把第一次的原始值覆盖成"已升级"的值。
	if state.EscalationRestore == nil {
		forceThinkingOff, boostStepOutputBudget := state.ForceThinkingOff, state.BoostStepOutputBudget
		state.EscalationRestore = &cloudAgentEscalationRestore{
			ForceThinkingOff:      &forceThinkingOff,
			BoostStepOutputBudget: &boostStepOutputBudget,
		}
	}
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
// 长度一律按字节（textBytes/reasoningBytes），与项目其它读数口径一致；
// 三个终结信号事实来自 parser（见 cloudAgentStepStopFacts），不从 kind 反推。
func cloudAgentStopReasonPayload(step int, taskID, raw, kind, disposition string, facts cloudAgentStepStopFacts, textBytes, reasoningBytes, toolCalls int) map[string]any {
	return map[string]any{
		"step":              step,
		"taskId":            taskID,
		"stopReason":        raw,
		"stopReasonKind":    kind,
		"terminalEventSeen": facts.TerminalEventSeen,
		"stopReasonPresent": facts.StopReasonPresent,
		"streamDoneSeen":    facts.StreamDoneSeen,
		"truncated":         cloudAgentStopReasonTruncated(kind),
		"partialAnswer":     cloudAgentStopReasonPartialAnswer(kind),
		"disposition":       disposition,
		"textBytes":         textBytes,
		"reasoningBytes":    reasoningBytes,
		"toolCalls":         toolCalls,
	}
}

// cloudAgentStopReasonPartialAnswer 表示"响应有效但可能不完整"：目前只有 context_limit。
// 官方对它的措辞是 "The response is still valid but was limited by context window"，
// 因此允许发布，但必须让用户和后续请求都知道这份答复可能没写完。
func cloudAgentStopReasonPartialAnswer(kind string) bool {
	return kind == cloudAgentStopKindContextLimit
}
