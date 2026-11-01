package app

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Only expose known failure categories; raw provider errors can contain URLs and credentials.
func cloudAgentModelFailure(task *model.Task) (string, string) {
	detail, reason := "模型任务未成功", "model_task_failed"
	if diagnostic := taskExecutionDiagnostic(task); diagnostic != nil && diagnostic.Code == string(ReasonUpstreamDNSFailed) {
		return "模型服务域名解析失败，请检查渠道域名和后端 DNS 配置；本轮已停止。请在任务中心检查模型任务 " + task.ID, string(ReasonUpstreamDNSFailed)
	}
	raw := strings.ToLower(task.Error)
	switch {
	case strings.Contains(task.Error, "没有返回内容"):
		// 思考模型的典型失败：整个输出预算被推理吃掉，正文与工具调用皆空。
		detail, reason = "上游连续返回空内容（通常是思考占满输出预算）；已自动关思考并放大预算重试仍失败，建议换用非思考模型或调小上下文", "model_empty_output"
	case strings.Contains(task.Error, cloudAgentStepTimeoutError):
		detail, reason = "Agent 超时失败：单步模型调用超过执行时限", "model_step_timeout"
	case cloudAgentStreamStalled(task):
		// 中转在流内回报的空闲断流，不是本服务的超时；已自动重试过一次。
		detail, reason = "模型中转在生成中途断开（长时间没有输出，超过中转的空闲时限）；已自动重试一次仍失败，可调大中转空闲超时，或让 Agent 把大写入拆小", "model_stream_stalled"
	case strings.Contains(raw, "connection reset by peer"):
		detail, reason = "模型连接被对端或中间网络设备重置", "model_connection_reset"
	case strings.Contains(raw, "timeout"), strings.Contains(raw, "deadline exceeded"):
		detail, reason = "Agent 超时失败：模型请求超时", "model_request_timeout"
	case strings.Contains(raw, "connection refused"):
		detail, reason = "无法连接模型服务（连接被拒绝）", "model_connection_refused"
	}
	return detail + "；本轮已停止。请在任务中心检查模型任务 " + task.ID, reason
}

func cloudAgentSafeToolError(err error) string {
	if err == nil {
		return ""
	}
	var lease *repository.CanvasEditLeaseError
	if errors.As(err, &lease) {
		if lease.Reason == "node_locked" {
			return "用户正在编辑该节点（nodeId=" + lease.NodeID + "）；本次写入未生效，请等待用户编辑结束后重读画布再试，或先处理其他节点"
		}
		return "节点编辑租约冲突；请保留当前工作，等待编辑结束或重新获取有效证明后再操作"
	}
	var readLoopErr *cloudAgentReadLoopError
	if errors.As(err, &readLoopErr) {
		return readLoopErr.Error()
	}
	var appErr *AppError
	if errors.As(err, &appErr) && appErr != nil {
		message := strings.TrimSpace(appErr.Message)
		if cloudAgentSafeUserMessage(message) {
			return message
		}
	}
	return "工具执行失败，请检查输入或稍后重试"
}

func cloudAgentSafeUserMessage(message string) bool {
	if message == "" || !utf8.ValidString(message) || strings.ContainsAny(message, "\x00\r\n") || utf8.RuneCountInString(message) > 240 {
		return false
	}
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"http://", "https://", "ftp://", "file://", "authorization", "cookie", "secret", "token", "api_key", "apikey", "x-api-key",
		"/var/", "/tmp/", "\\", "stack trace", "traceback", " at ", "sql:", "sqlite", "postgres",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// cloudAgentSafeMediaTaskError preserves a short, user-facing task diagnostic
// while refusing provider details that commonly contain URLs, credentials, or
// internal request metadata. Task.Error is not a safe presentation field.
func cloudAgentSafeMediaTaskError(task *model.Task) string {
	if task == nil {
		return "媒体任务未成功"
	}
	detail := strings.TrimSpace(task.Error)
	if detail == "" || !utf8.ValidString(detail) || strings.ContainsAny(detail, "\r\n\x00") {
		return "媒体任务未成功"
	}
	lower := strings.ToLower(detail)
	for _, marker := range []string{
		"http://", "https://", "ftp://", "authorization", "cookie", "secret", "token", "api_key", "apikey", "x-api-key",
	} {
		if strings.Contains(lower, marker) {
			return "媒体任务未成功"
		}
	}
	runes := []rune(detail)
	if len(runes) > 240 {
		detail = string(runes[:240]) + "…"
	}
	return detail
}

// cloudAgentReadOnlyDirectorWrite：只读运行仍列出 director（仅 action=read），写 action 必须拒绝。
func cloudAgentReadOnlyDirectorWrite(state *cloudAgentRuntime, call cloudAgentCall) bool {
	return call.Function.Name == cloudAgentDirectorTool && state.Request.PermissionMode == "read_only" && cloudAgentWriteCall(call)
}

// cloudAgentCallPermitted 判断当前权限下这次调用能否执行，审批决定与执行前检查共用同一规则。
func cloudAgentCallPermitted(state *cloudAgentRuntime, call cloudAgentCall) bool {
	request := state.Request
	request.subagent = state.Subagent
	return cloudAgentToolAllowed(request, call.Function.Name) && !cloudAgentReadOnlyDirectorWrite(state, call)
}

// cloudAgentModelHiddenFields 列出只供界面、版本链与审计使用、模型不会回传给任何工具参数的字段。
// 处理函数照常返回完整结果（recordCanvasBatchHash 等在投影之前读取），这里只从模型那份里去掉。
var cloudAgentModelHiddenFields = map[string][]string{
	// 导演写入：模型下一步只需要场景 snapshotHash（作为 expectedSceneHash）。
	cloudAgentDirectorTool: {"canvasId", "canvasSnapshotHash", "beforeCanvasSnapshotHash"},
	// 撤销/重做的 operationId 是写入记录行 ID，没有工具接收它。
	"canvas_undo": {"operationId", "canvasId"},
	"canvas_redo": {"operationId", "canvasId"},
}

func cloudAgentWithoutModelHiddenFields(toolName string, fields map[string]any) map[string]any {
	hidden := cloudAgentModelHiddenFields[toolName]
	if cloudAgentCanvasWriteTool(toolName) {
		// canvasId 是本轮唯一画布，beforeSnapshotHash 只给版本链登记使用。
		hidden = append(append([]string{}, hidden...), "canvasId", "beforeSnapshotHash")
	}
	present := false
	for _, key := range hidden {
		if _, ok := fields[key]; ok {
			present = true
			break
		}
	}
	if !present {
		return fields
	}
	trimmed := make(map[string]any, len(fields))
	for key, value := range fields {
		trimmed[key] = value
	}
	for _, key := range hidden {
		delete(trimmed, key)
	}
	return trimmed
}

// cloudAgentModelToolResult 是"面向模型的那一份"工具结果。
//
// 审批卡明细是写给人看的，而且已经通过 approval_requested 事件进了 SSE/界面；
// 进入模型历史的工具结果只保留模型真正需要的东西：发生了什么、哪个节点、哪个快照。
// 原生技能读取也区分模型正文与面向用户的回执。
//
// task_get 等工具的返回里
// 不带 preview，因此原样穿过，不受影响。
func cloudAgentModelToolResult(toolName string, result any) any {
	fields, ok := result.(map[string]any)
	if !ok {
		return result
	}
	fields = cloudAgentWithoutModelHiddenFields(toolName, fields)
	preview, hasPreview := fields["preview"]
	if !hasPreview {
		return fields
	}
	receipt := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		if key == "preview" {
			continue
		}
		receipt[key] = value
	}
	// 审批卡文案是写给人看的（"准备…批准后才会写入画布"）。模型收到的这份工具结果只在写入
	// 真正发生后才会产生，所以必须给出结果口径，否则模型会以为还在等审批并反复重读重试。
	if cloudAgentCanvasWriteTool(toolName) {
		receipt["applied"] = true
		receipt["outcome"] = "本次画布操作已执行；每个模型步骤最多一个写入/生成调用，可在下一模型步骤复用本回执的新 snapshotHash 继续；仅在实际冲突或所需状态未知时重新读取目标并核对原操作"
		if fields["replayed"] == true {
			receipt["outcome"] = "这是该批次已提交的历史回执，本次没有重复写入或再次申请审批；snapshotHash/storyboardRevision 属于原提交。currentSnapshotHash 仅标记当前版本；继续新批次前按需读取目标字段和查缺，不能据历史回执断言当前内容未被人工修改"
		}
		if item := cloudAgentReceiptItemSummary(preview); item != "" {
			receipt["summary"] = item
		}
	}
	receipt["previewOmitted"] = true
	receipt["previewNote"] = "审批卡明细只发给用户；本回执保留变更条目、nodeId 与 snapshotHash"
	return receipt
}

// cloudAgentReceiptItemSummary 取审批预览里面向节点的短句（"修改分镜脚本《…》"），
// 它描述的是发生了什么，而不是"准备做什么"。
func cloudAgentReceiptItemSummary(preview any) string {
	value, ok := preview.(cloudAgentApprovalPreview)
	if !ok || len(value.Items) == 0 {
		return ""
	}
	return strings.TrimSpace(value.Items[0].Summary)
}

// cloudAgentRebaseWriteSnapshot 把同一批内后续写入的 snapshotHash 换成该批自己产出的最新版本。
// 只在模型提交的版本属于本批已出现过的版本时才替换，替换后仍要与当前文档一致才会通过校验，
// 因此浏览器/其他端的并发改动依旧会被"画布已变化"拒绝。
func cloudAgentRebaseWriteSnapshot(state *cloudAgentRuntime, call cloudAgentCall) cloudAgentCall {
	if state == nil || len(state.CanvasBatchHashes) == 0 || !cloudAgentCanvasWriteTool(call.Function.Name) {
		return call
	}
	var args map[string]any
	if decodeCloudAgentJSONObject(call.Function.Arguments, &args) != nil {
		return call
	}
	requested, _ := args["snapshotHash"].(string)
	latest := state.CanvasBatchHashes[len(state.CanvasBatchHashes)-1]
	if requested == "" || requested == latest || latest == "" {
		return call
	}
	known := false
	for _, hash := range state.CanvasBatchHashes {
		if hash == requested {
			known = true
			break
		}
	}
	if !known {
		return call
	}
	args["snapshotHash"] = latest
	encoded, err := json.Marshal(args)
	if err != nil {
		return call
	}
	call.Function.Arguments = string(encoded)
	return call
}

// recordCanvasBatchHash 在写入成功后登记本批的版本链（写入前的版本 + 写入产出的版本）。
func (state *cloudAgentRuntime) recordCanvasBatchHash(result any, toolName string) {
	// Previs uses a scene snapshotHash for its own optimistic concurrency. It
	// must never enter the whole-canvas batch chain used to rebase canvas writes.
	// Only results from canvas write tools carry the canvas snapshot contract.
	if state == nil || !cloudAgentCanvasWriteTool(toolName) {
		return
	}
	fields, ok := result.(map[string]any)
	if !ok {
		return
	}
	if fields["replayed"] == true {
		return
	}
	produced, _ := fields["snapshotHash"].(string)
	if toolName == cloudAgentDirectorTool {
		produced, _ = fields["canvasSnapshotHash"].(string)
	}
	if produced == "" {
		return
	}
	if len(state.CanvasBatchHashes) == 0 {
		before, _ := fields["beforeSnapshotHash"].(string)
		if toolName == cloudAgentDirectorTool {
			before, _ = fields["beforeCanvasSnapshotHash"].(string)
		}
		if before != "" {
			state.CanvasBatchHashes = append(state.CanvasBatchHashes, before)
		}
	}
	if last := state.CanvasBatchHashes; len(last) == 0 || last[len(last)-1] != produced {
		state.CanvasBatchHashes = append(state.CanvasBatchHashes, produced)
	}
}

// cloudAgentReadResultInContext reports whether the full cached result is still
// present in the canonical transcript. Context compaction deliberately removes old
// tool bodies, so a replay must restore the body once instead of returning a receipt
// that refers to history the model can no longer see.
func cloudAgentReadResultInContext(state *cloudAgentRuntime, result json.RawMessage) bool {
	if state == nil || len(result) == 0 {
		return false
	}
	// Unit-level callers may exercise the cache helper without constructing a
	// transcript. The real runtime always has the first tool message here.
	if len(state.Canonical.Messages) == 0 {
		return true
	}
	want := string(result)
	for _, message := range state.Canonical.Messages {
		if stringValue(message["role"]) == "tool" && stringValue(message["content"]) == want {
			return true
		}
	}
	return false
}

// cloudAgentBatchableReadTool identifies read calls that are independent of
// each other and safe to execute from one model tool-call response. Keeping
// this list limited to the cacheable read contract is important: mutations,
// approvals, user questions, media and vision calls remain ordered state
// transitions and must each retain their existing semantics.
func cloudAgentBatchableReadTool(name string) bool {
	return cloudAgentReadToolReadOnly(name)
}

func cloudAgentInvalidateReadCache(state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	// Canvas writes invalidate only canvas projections. Skill documents and
	// the model catalog remain valid, avoiding unrelated repeated reads.
	for key := range state.ToolReadResults {
		if strings.HasPrefix(key, "canvas_get_state:") || strings.HasPrefix(key, "storyboard:") || strings.HasPrefix(key, cloudAgentDirectorTool+":") {
			delete(state.ToolReadResults, key)
			delete(state.ToolReadReplays, key)
		}
	}
}

// cloudAgentToolErrorIsBusiness 判断工具错误是否可以安全地作为"业务回执"交回模型。
// 业务拒绝（参数不合法、快照冲突、权限/准入类 AppError、读循环纠错）→ true，可入回执；
// 其余（数据库/存储/事务等基础设施错误）→ false，必须向上返回以回滚事务。
func cloudAgentToolErrorIsBusiness(err error) bool {
	if err == nil {
		return false
	}
	var lease *repository.CanvasEditLeaseError
	if errors.As(err, &lease) {
		return false
	} // even if an adapter wrapped it in AppError
	var argumentErr *cloudAgentArgumentError
	if errors.As(err, &argumentErr) {
		return true
	}
	var readLoopErr *cloudAgentReadLoopError
	if errors.As(err, &readLoopErr) {
		return true
	}
	if cloudAgentSnapshotConflict(err) {
		return true
	}
	var appErr *AppError
	if errors.As(err, &appErr) && appErr != nil {
		return true
	}
	return false
}
