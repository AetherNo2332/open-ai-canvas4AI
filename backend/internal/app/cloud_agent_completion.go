package app

// 完成闸门检查用户插话、子代理结果和正文截断。
// 待办清单由模型维护，不参与收尾判定，也不触发催办或失败。

import (
	"fmt"
	"sort"
	"strings"

	"yingce/backend/internal/model"
)

const (
	// cloudAgentCompletionNudgeLimit 是**同一个阻塞原因**允许的收尾催办次数：
	// 到达上限仍不通过就停止自动重试。每一次重试都是真实计费的模型调用，所以不能只靠
	// "催办"这一个动作兜底。
	cloudAgentCompletionNudgeLimit = 2
	// cloudAgentCompletionNudgeCeiling 限制阻塞原因变化时的累计重试。
	cloudAgentCompletionNudgeCeiling = 6
	// cloudAgentCompletionBlockedReason 是终止时的 run_failed.reason。
	// 复用 failed 状态而不是新增 blocked 终态：不动运行状态机与前端状态映射，原因放在 reason。
	cloudAgentCompletionBlockedReason = "completion_blocked"
	// cloudAgentCompletionTruncatedKind 是"候选正文被上游输出上限截断"这条阻塞的 kind。
	// 这一步的正文确实不完整，不能作为最终答复发布。
	cloudAgentCompletionTruncatedKind = "truncated_output"
)

// cloudAgentCompletionBlocker 是一条机器可读的阻塞原因（kind）+ 给人看的细节（detail）。
type cloudAgentCompletionBlocker struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// cloudAgentCompletionBlock 是一次"候选收尾"的闸门结论。
type cloudAgentCompletionBlock struct {
	// Final 表示闸门通过，这段正文可以当本轮最终答复发布。
	Final bool `json:"final"`
	// Blockers 是未通过时的阻塞原因（为空表示通过，或是"用户刚插话"这类软阻塞）。
	Blockers []cloudAgentCompletionBlocker `json:"blockers,omitempty"`
	// Fingerprint 是本次阻塞原因的指纹：原因不变才算"重复同一个错误"。
	Fingerprint string `json:"fingerprint,omitempty"`
	// Attempt / MaxAttempts 是同一指纹下第几次被拦、上限多少。
	Attempt     int `json:"attempt,omitempty"`
	MaxAttempts int `json:"maxAttempts,omitempty"`
	// Interjected 表示只是"用户刚插话"，下一步它就会进上下文：不需要催办，也不计额度。
	Interjected bool `json:"interjected,omitempty"`
}

// cloudAgentCompletionBlockers 是闸门的判据本身：只列服务端能确证的事实。
//
// 不在这里检查的东西与理由：
//   - 待批审批：审批态本身把运行留在 waiting_approval，收尾分支到不了；
//   - 在跑的生成任务：advanceCloudAgentMedia 在媒体任务未结束时直接返回，本轮会一直等它，
//     收尾分支同样到不了；
//   - 工具失败：纠错阶梯（cloud_agent_tool_repair.go）已经管了这件事，与"收尾"是两回事。
func cloudAgentCompletionBlockers(state *cloudAgentRuntime) []cloudAgentCompletionBlocker {
	if state == nil {
		return nil
	}
	blockers := make([]cloudAgentCompletionBlocker, 0, 2)
	if state.Subagent != nil {
		blockers = append(blockers, cloudAgentCompletionBlocker{Kind: "structured_subagent_result", Detail: "使用 finish_subagent 向父 Agent 提交最终结果"})
	}
	if state.ActiveSubagents > 0 {
		blockers = append(blockers, cloudAgentCompletionBlocker{Kind: "pending_subagents", Detail: "使用 wait_subagents 等待并收集子代理结果"})
	}
	// 权限通知不阻止收尾：它只是事实同步，不该为它多开一步模型调用。
	for _, item := range state.PendingInterjections {
		if item.Source != cloudAgentPermissionSource {
			blockers = append(blockers, cloudAgentCompletionBlocker{Kind: "pending_interjection"})
			break
		}
	}
	return blockers
}

// cloudAgentEvaluateCompletion 是收尾分支的唯一入口：算出这次候选收尾能不能通过。
func cloudAgentEvaluateCompletion(state *cloudAgentRuntime) cloudAgentCompletionBlock {
	blockers := cloudAgentCompletionBlockers(state)
	if len(blockers) == 0 {
		return cloudAgentCompletionBlock{Final: true}
	}
	block := cloudAgentCompletionBlock{Blockers: blockers, Fingerprint: cloudAgentCompletionFingerprint(blockers), MaxAttempts: cloudAgentCompletionNudgeLimit}
	if len(blockers) == 1 && blockers[0].Kind == "pending_interjection" {
		block.Interjected = true
	}
	return block
}

// cloudAgentBlockTruncatedCompletion 给候选收尾追加"正文被输出上限截断"这条阻塞。
// 只在这一步确实被截断时生效；正文完整的常规收尾不受影响。
func cloudAgentBlockTruncatedCompletion(block cloudAgentCompletionBlock, stopKind string) cloudAgentCompletionBlock {
	if !cloudAgentStopReasonTruncated(stopKind) {
		return block
	}
	for _, existing := range block.Blockers {
		if existing.Kind == cloudAgentCompletionTruncatedKind {
			return block
		}
	}
	block.Blockers = append(block.Blockers, cloudAgentCompletionBlocker{Kind: cloudAgentCompletionTruncatedKind})
	block.Final = false
	// 截断与"用户刚插话"无关：这条阻塞要走催办计数，不能被当成软阻塞放行。
	block.Interjected = false
	block.Fingerprint = cloudAgentCompletionFingerprint(block.Blockers)
	block.MaxAttempts = cloudAgentCompletionNudgeLimit
	return block
}

// cloudAgentCompletionHasBlocker 判断本次阻塞里是否含指定 kind。
func cloudAgentCompletionHasBlocker(block cloudAgentCompletionBlock, kind string) bool {
	for _, blocker := range block.Blockers {
		if blocker.Kind == kind {
			return true
		}
	}
	return false
}

// cloudAgentCompletionFingerprint 把阻塞类型和事实细节压成稳定指纹。
func cloudAgentCompletionFingerprint(blockers []cloudAgentCompletionBlocker) string {
	if len(blockers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		parts = append(parts, blocker.Kind+"\n"+blocker.Detail)
	}
	sort.Strings(parts)
	return creationHash(strings.Join(parts, "\n"))
}

// cloudAgentNoteCompletionBlocked 给"这次被拦下"记账，返回同一指纹下的催办进度。
// 指纹变化即重置，同一协议问题才会累计重复次数。
func cloudAgentNoteCompletionBlocked(state *cloudAgentRuntime, fingerprint string) (attempt int, exhausted bool) {
	if state.CompletionNudgeFingerprint != fingerprint {
		state.CompletionNudgeFingerprint = fingerprint
		state.CompletionNudgeAttempt = 0
	}
	state.CompletionNudgeAttempt++
	state.CompletionNudges++
	exhausted = state.CompletionNudgeAttempt > cloudAgentCompletionNudgeLimit || state.CompletionNudges > cloudAgentCompletionNudgeCeiling
	return state.CompletionNudgeAttempt, exhausted
}

// cloudAgentCompletionPayload 是 completion_blocked 控制事件的载荷。它是**给用户看的原因**，
// 模型侧通过 completion_blocked runtime 帧接收同一阻塞事实。
func cloudAgentCompletionPayload(block cloudAgentCompletionBlock, text string) map[string]any {
	return map[string]any{
		"blockers":    block.Blockers,
		"attempt":     block.Attempt,
		"maxAttempts": block.MaxAttempts,
		"fingerprint": block.Fingerprint,
		"text":        text,
	}
}

// cloudAgentCompletionBlockerText 是控制事件与终止说明共用的一句人话。
func cloudAgentCompletionBlockerText(block cloudAgentCompletionBlock) string {
	parts := make([]string, 0, len(block.Blockers))
	for _, blocker := range block.Blockers {
		switch blocker.Kind {
		case "pending_interjection":
			parts = append(parts, "用户刚发来插话，还没送进上下文")
		case "pending_subagents", "structured_subagent_result":
			parts = append(parts, blocker.Detail)
		case cloudAgentCompletionTruncatedKind:
			parts = append(parts, "上一步正文被输出上限截断，不是完整答复")
		default:
			parts = append(parts, blocker.Kind)
		}
	}
	return strings.Join(parts, "；")
}

// cloudAgentCompletionExhaustedMessage 是停止时的失败说明：要能直接照着做。
// 区分协议恢复次数耗尽与没有下一次模型调用预算。
func cloudAgentCompletionExhaustedMessage(block cloudAgentCompletionBlock) string {
	if block.Attempt == 0 {
		return fmt.Sprintf("本轮已停止：%s。本轮已经没有下一次模型调用的预算，先把结果交回给你；可以继续对话让它接着做。",
			cloudAgentCompletionBlockerText(block))
	}
	if cloudAgentCompletionHasBlocker(block, cloudAgentCompletionTruncatedKind) {
		return fmt.Sprintf("本轮已停止：%s。已重试 %d 次仍被截断；可以继续对话让它接着写，或在管理端调高单步输出上限。",
			cloudAgentCompletionBlockerText(block), block.Attempt)
	}
	return fmt.Sprintf("本轮已停止：%s。已连续 %d 次尝试处理收尾阻塞，仍未通过；可以继续对话或重新发起。",
		cloudAgentCompletionBlockerText(block), block.Attempt)
}

// cloudAgentCompletionBlockedNudge 记录界面控制事件和模型侧的协议阻塞事实。
func cloudAgentCompletionBlockedNudge(runID string, state *cloudAgentRuntime, block cloudAgentCompletionBlock) {
	text := cloudAgentCompletionBlockerText(block) + "；已将阻塞原因返回 Agent。"
	state.event(runID, "completion_blocked", cloudAgentCompletionPayload(block, text))
	state.Canonical.Messages = append(state.Canonical.Messages, cloudAgentRuntimeMessage(cloudAgentRuntimeContext{
		Kind: cloudAgentContextCompletionBlocked, Detail: cloudAgentCompletionBlockerText(block),
	}))
}

// cloudAgentFinishRunAlreadyRequested 判断同一批里前面是否已经申请过一次收尾。
// 模型一次返回多个 finish_run 时只处理第一个：否则一个模型步骤就能烧掉整轮催办额度。
func cloudAgentFinishRunAlreadyRequested(state *cloudAgentRuntime) bool {
	if state == nil {
		return false
	}
	limit := min(state.CallIndex, len(state.Calls))
	for _, call := range state.Calls[:max(0, limit)] {
		if call.Function.Name == "finish_run" {
			return true
		}
	}
	return false
}

// --- finish_run：显式完成动作 ---

// cloudAgentFinishRunArguments 解析 finish_run 的参数。summary 就是本轮最终答复的全文，
// 必填：让"最终答复"有一个显式来源，而不是从最后一段正文里猜。
func cloudAgentFinishRunArguments(call cloudAgentCall) (string, error) {
	var args struct {
		Summary string `json:"summary"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return "", cloudAgentJSONArgumentError(err)
	}
	summary := strings.TrimSpace(args.Summary)
	if summary == "" {
		return "", &cloudAgentFieldArgumentError{
			error: &cloudAgentArgumentError{BadAuthRequest("finish_run 的 summary 是本轮给用户的最终答复，不能为空")},
			Field: "summary", Issue: "required",
		}
	}
	if len(summary) > cloudAgentMaxOutputBytes {
		return "", &cloudAgentFieldArgumentError{
			error: &cloudAgentArgumentError{BadAuthRequest("finish_run 的 summary 过长，请精简到 " + fmt.Sprint(cloudAgentMaxOutputBytes) + " 字节以内")},
			Field: "summary", Issue: "too_long",
		}
	}
	return summary, nil
}

// cloudAgentFinishRun 在工具调用点执行完成闸门：通过才收尾，不通过就回结构化回执。
// 这是本工具与"纯文本收尾"的唯一区别——闸门在这里、也在文本收尾路径上跑，判据完全相同。
func cloudAgentFinishRun(runID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	summary, err := cloudAgentFinishRunArguments(call)
	if err != nil {
		return nil, err
	}
	if cloudAgentFinishRunAlreadyRequested(state) {
		// 同一批里的重复申请：不重复计数、不重复回执原因，只如实告知本次未处理。
		return map[string]any{"phase": "completion", "summary": summary, "completionBlocked": true, "duplicate": true,
			"text": "同一批里前面已经申请过一次收尾，本次未重复处理。"}, nil
	}
	block := cloudAgentEvaluateCompletion(state)
	result := map[string]any{"phase": "completion", "summary": summary, "completionBlocked": !block.Final}
	if block.Final {
		return result, nil
	}
	if block.Interjected {
		// 用户刚插话（软阻塞）：下一步 drain 就会把它送进上下文，既不该催办也不该计额度，
		// 与"纯文本收尾"路径保持同一口径。
		result["blockers"] = block.Blockers
		result["requiredAction"] = "answer_interjection"
		result["text"] = "用户刚发来插话，还没送进上下文；下一步会先按它处理，之后再申请收尾。"
		return result, nil
	}
	attempt, exhausted := cloudAgentNoteCompletionBlocked(state, block.Fingerprint)
	block.Attempt = attempt
	state.event(runID, "completion_blocked", cloudAgentCompletionPayload(block,
		cloudAgentCompletionBlockerText(block)+"；已将阻塞原因返回 Agent。"))
	result["blockers"] = block.Blockers
	result["fingerprint"] = block.Fingerprint
	result["attempt"], result["maxAttempts"] = attempt, cloudAgentCompletionNudgeLimit
	result["requiredAction"] = "resolve_blockers"
	result["text"] = cloudAgentCompletionBlockerText(block) + "；处理后再调用 finish_run。"
	if exhausted {
		result["exhausted"] = true
	}
	return result, nil
}

// cloudAgentFinishRunAccepted 判断 finish_run 的回执是不是"通过"。
func cloudAgentFinishRunAccepted(result any) bool {
	detail, ok := result.(map[string]any)
	if !ok {
		return false
	}
	return detail["completionBlocked"] != true
}

// cloudAgentFinishRunExhausted 判断这次 finish_run 是不是已经把催办额度用尽：
// 是的话本轮要如实终止，而不是让模型继续重复"申请收尾"。
func cloudAgentFinishRunExhausted(result any) bool {
	detail, ok := result.(map[string]any)
	return ok && detail["exhausted"] == true
}

// cloudAgentFailBlockedCompletion 是"闸门不放行、又不能再继续"的统一终止：
// 状态用 failed（不新增终态、不动前端状态映射），原因放在 run_failed.reason，
// 候选正文早已以过程说明发布过，用户不会只看到一句"失败了"。
func cloudAgentFailBlockedCompletion(current *model.CloudAgentExecution, state *cloudAgentRuntime, runID string, block cloudAgentCompletionBlock) error {
	message := cloudAgentCompletionExhaustedMessage(block)
	current.Status = "failed"
	current.FailureMessage = truncateRunes(message, 1000)
	cloudAgentDropInterjections(runID, "本轮已结束："+truncateRunes(message, 120), state)
	state.event(runID, "run_failed", map[string]any{
		"text": message, "reason": cloudAgentCompletionBlockedReason,
		"blockers": block.Blockers, "attempts": block.Attempt,
	})
	return cloudAgentSave(current, state)
}

// cloudAgentCompleteByFinishRun 把一次通过的 finish_run 落成"本轮唯一一次最终答复"并收尾。
// 顺序按模型协议来：先落这次调用的回执（assistant(tool_calls) → tool），再落最终正文，
// 这样这段 canonical 即便再看一次也是自洽的（assistant 的最后一句就是最终答复）。
func cloudAgentCompleteByFinishRun(current *model.CloudAgentExecution, state *cloudAgentRuntime, runID string, call cloudAgentCall, result any) error {
	summary := cloudAgentToolCallSummary(call)
	if receipt, ok := result.(map[string]any); ok {
		receipt["runId"], receipt["finalMessageId"] = runID, call.ID
		receipt["committed"], receipt["terminalStatus"] = true, "completed"
	}
	cloudAgentRecordToolResult(current, state, call, result, nil)
	// 同批剩下的调用与"纯文本收尾"的路径一样收成回执：本轮已经结束，它们不会被执行。
	skipRemainingCloudAgentCalls(runID, state)
	if summary != "" {
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": summary})
		state.event(runID, "assistant_message", map[string]any{"messageId": call.ID, "text": summary, "final": true, "via": "finish_run"})
	}
	current.Status = "completed"
	return cloudAgentSave(current, state)
}

// cloudAgentToolCallSummary 取一次 finish_run 调用的 summary（工具已校验过，这里只做取值）。
func cloudAgentToolCallSummary(call cloudAgentCall) string {
	var args struct {
		Summary string `json:"summary"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Summary)
}
