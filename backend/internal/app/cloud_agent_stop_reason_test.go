package app

// 自研：上游终止原因（stop reason）的可观测与截断处置。
// 背景与设计见 cloud_agent_stop_reason.go；解析点见 provider_text.go。
import (
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// setStepResult 把某一步模型任务的回执写成指定的上游结果（含终止原因）。
func setStepResult(t *testing.T, db *gorm.DB, taskID string, result map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": string(encoded),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// 各协议的原始终止原因必须收敛成同一套内部词表；认不出来的一律 unknown，不能默认成"正常结束"。
func TestCloudAgentStopReasonNormalization(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "OpenAI 正常结束", raw: "stop", want: cloudAgentStopKindStop},
		{name: "Claude 正常结束", raw: "end_turn", want: cloudAgentStopKindStop},
		{name: "停止序列", raw: "stop_sequence", want: cloudAgentStopKindStop},
		{name: "OpenAI 请求工具", raw: "tool_calls", want: cloudAgentStopKindToolCalls},
		{name: "OpenAI 旧版函数调用", raw: "function_call", want: cloudAgentStopKindToolCalls},
		{name: "Claude 请求工具", raw: "tool_use", want: cloudAgentStopKindToolCalls},
		{name: "OpenAI 输出截断", raw: "length", want: cloudAgentStopKindLength},
		{name: "Claude 输出截断", raw: "max_tokens", want: cloudAgentStopKindLength},
		{name: "Responses 输出到顶", raw: "max_output_tokens", want: cloudAgentStopKindLength},
		{name: "输入撞上下文窗口", raw: "model_context_window_exceeded", want: cloudAgentStopKindContextLimit},
		{name: "服务端工具挂起", raw: "pause_turn", want: cloudAgentStopKindPause},
		{name: "拒答", raw: "refusal", want: cloudAgentStopKindRefusal},
		{name: "内容过滤单列", raw: "content_filter", want: cloudAgentStopKindContentFilter},
		{name: "未完成但没给原因", raw: "incomplete", want: cloudAgentStopKindIncompleteUnknown},
		{name: "大小写与空格", raw: "  MAX_TOKENS ", want: cloudAgentStopKindLength},
		{name: "缺字段", raw: "", want: cloudAgentStopKindUnknown},
		{name: "未识别取值", raw: "something_new", want: cloudAgentStopKindUnknown},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := normalizeCloudAgentStopReason(item.raw); got != item.want {
				t.Fatalf("normalize(%q) = %q, want %q", item.raw, got, item.want)
			}
		})
	}
	// 只有"输出到顶"算截断：上下文窗口到顶不是输出截断，不能被关思考重试掩盖。
	for _, kind := range []string{cloudAgentStopKindStop, cloudAgentStopKindToolCalls, cloudAgentStopKindContextLimit, cloudAgentStopKindPause, cloudAgentStopKindRefusal, cloudAgentStopKindUnknown} {
		if cloudAgentStopReasonTruncated(kind) {
			t.Fatalf("%q 不应被判为截断", kind)
		}
	}
	if !cloudAgentStopReasonTruncated(cloudAgentStopKindLength) {
		t.Fatal("length 必须判为截断")
	}
}

// Responses 协议只有 status=incomplete 时才需要看 incomplete_details.reason。
func TestCloudAgentResponsesStopReasonReadsIncompleteDetails(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		details  map[string]interface{}
		wantKind string
	}{
		{name: "完成", status: "completed", wantKind: cloudAgentStopKindStop},
		{name: "输出到顶", status: "incomplete", details: map[string]interface{}{"reason": "max_output_tokens"}, wantKind: cloudAgentStopKindLength},
		{name: "内容过滤", status: "incomplete", details: map[string]interface{}{"reason": "content_filter"}, wantKind: cloudAgentStopKindContentFilter},
		// 原因未知时既不猜成"输出到顶"去重试，也不发布半截答复（评审要求单列一类）。
		{name: "没有 reason 的 incomplete", status: "incomplete", wantKind: cloudAgentStopKindIncompleteUnknown},
		{name: "失败", status: "failed", wantKind: cloudAgentStopKindUnknown},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			response := map[string]interface{}{"status": item.status}
			if item.details != nil {
				response["incomplete_details"] = item.details
			}
			raw := cloudAgentResponsesStopReason("", response)
			if got := normalizeCloudAgentStopReason(raw); got != item.wantKind {
				t.Fatalf("status=%s reason=%q → %q, want %q", item.status, raw, got, item.wantKind)
			}
		})
	}
}

// 流式：终止块里的原因必须在解析结果里留下痕迹，否则运行时只能靠错误字符串猜。
func TestStreamingAgentParserCapturesStopReason(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		frames   []string
		wantRaw  string
		wantKind string
	}{
		{
			name: "OpenAI 输出截断", protocol: "chat-completions",
			frames: []string{
				`data: {"choices":[{"delta":{"content":"半句话的结"},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
				`data: [DONE]`,
			},
			wantRaw: "length", wantKind: cloudAgentStopKindLength,
		},
		{
			name: "OpenAI 请求工具", protocol: "chat-completions",
			frames: []string{
				`data: {"choices":[{"delta":{"content":"先看一下"}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"canvas_get_state","arguments":"{}"}}]},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: [DONE]`,
			},
			wantRaw: "tool_calls", wantKind: cloudAgentStopKindToolCalls,
		},
		{
			name: "Claude 输出截断", protocol: "claude-api",
			frames: []string{
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"半句话的结"}}`,
				`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`,
			},
			wantRaw: "max_tokens", wantKind: cloudAgentStopKindLength,
		},
		{
			name: "Responses 输出到顶", protocol: "responses",
			frames: []string{
				`data: {"type":"response.output_text.delta","delta":"半句话的结"}`,
				`data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
			},
			wantRaw: "max_output_tokens", wantKind: cloudAgentStopKindLength,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			parser := newStreamingAgentParser(item.protocol, func(string) {})
			for _, frame := range item.frames {
				parser.consume("text/event-stream", []byte(frame+"\n\n"))
			}
			parser.flush()
			result, err := parser.result()
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := result["stopReason"]; got != item.wantRaw {
				t.Fatalf("stopReason = %v, want %q", got, item.wantRaw)
			}
			if got := result["stopReasonKind"]; got != item.wantKind {
				t.Fatalf("stopReasonKind = %v, want %q", got, item.wantKind)
			}
			// 解析事实必须如实：收到终态事件 + 原因原文非空。
			if got := result["terminalEventSeen"]; got != true {
				t.Fatalf("terminalEventSeen = %v, 期望 true", got)
			}
			if got := result["stopReasonPresent"]; got != true {
				t.Fatalf("stopReasonPresent = %v, 期望 true", got)
			}
		})
	}
}

// 非流式三条分支同样要带出终止原因（有些渠道不开流）。
func TestParseAgentToolPayloadCapturesStopReason(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		payload  map[string]interface{}
		wantRaw  string
		wantKind string
	}{
		{
			name: "OpenAI 输出截断", protocol: "chat-completions",
			payload: map[string]interface{}{"choices": []interface{}{map[string]interface{}{
				"message": map[string]interface{}{"content": "半句话"}, "finish_reason": "length"}}},
			wantRaw: "length", wantKind: cloudAgentStopKindLength,
		},
		{
			name: "Claude 正常结束", protocol: "claude-api",
			payload: map[string]interface{}{
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": "说完了"}},
				"stop_reason": "end_turn"},
			wantRaw: "end_turn", wantKind: cloudAgentStopKindStop,
		},
		{
			name: "Responses 输出到顶", protocol: "responses",
			payload: map[string]interface{}{
				"output_text": "半句话", "status": "incomplete",
				"incomplete_details": map[string]interface{}{"reason": "max_output_tokens"}},
			wantRaw: "max_output_tokens", wantKind: cloudAgentStopKindLength,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result, err := parseAgentToolPayload(item.payload, item.protocol)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := result["stopReason"]; got != item.wantRaw {
				t.Fatalf("stopReason = %v, want %q", got, item.wantRaw)
			}
			if got := result["stopReasonKind"]; got != item.wantKind {
				t.Fatalf("stopReasonKind = %v, want %q", got, item.wantKind)
			}
		})
	}
}

// 被截断的正文不是完整答复：收尾闸门必须加阻塞，且重复判定要幂等、不误伤正常结束。
func TestCloudAgentTruncatedCompletionBlocker(t *testing.T) {
	blocked := cloudAgentBlockTruncatedCompletion(cloudAgentCompletionBlock{Final: true}, cloudAgentStopKindLength)
	if blocked.Final {
		t.Fatal("截断的正文不应通过收尾闸门")
	}
	if len(blocked.Blockers) != 1 || blocked.Blockers[0].Kind != cloudAgentCompletionTruncatedKind {
		t.Fatalf("阻塞原因 = %+v", blocked.Blockers)
	}
	if blocked.Fingerprint == "" || blocked.MaxAttempts != cloudAgentCompletionNudgeLimit {
		t.Fatalf("阻塞记账不完整: %+v", blocked)
	}
	if again := cloudAgentBlockTruncatedCompletion(blocked, cloudAgentStopKindLength); len(again.Blockers) != 1 {
		t.Fatalf("重复追加了阻塞原因: %+v", again.Blockers)
	}
	if plain := cloudAgentBlockTruncatedCompletion(cloudAgentCompletionBlock{Final: true}, cloudAgentStopKindStop); !plain.Final || len(plain.Blockers) != 0 {
		t.Fatalf("正常结束被误伤: %+v", plain)
	}
	// 终止文案要说清是截断，不能串成"待办未对账"。
	message := cloudAgentCompletionExhaustedMessage(cloudAgentCompletionBlock{Blockers: blocked.Blockers, Attempt: 2})
	if !strings.Contains(message, "截断") || strings.Contains(message, "对账") {
		t.Fatalf("截断的终止文案不对: %s", message)
	}
}

// 端到端：输出被截断 → 记事件 + 关思考放大预算重试一次 → 再截断也不冒充最终答复。
func TestCloudAgentTruncatedOutputEscalatesThenNeverPublishes(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	setStepResult(t, db, root.ID, map[string]any{
		"text": "半句话的结论", "stopReason": "length", "stopReasonKind": cloudAgentStopKindLength,
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	if run.Status != "running" {
		t.Fatalf("截断不能直接收尾：status=%s（%s）", run.Status, run.FailureMessage)
	}
	if state.TruncatedStepEscalated != 1 || !state.ForceThinkingOff || !state.BoostStepOutputBudget {
		t.Fatalf("截断后应关思考并放大输出预算重试: escalated=%d forceThinkingOff=%v boosted=%v",
			state.TruncatedStepEscalated, state.ForceThinkingOff, state.BoostStepOutputBudget)
	}
	if !agentHasEventWithReason(state, "model_failure_recovered", "truncated_output_escalated") {
		t.Fatal("缺少可读的截断重试事件")
	}
	// 这两条读数正是过去从事件流里读不出来的东西。
	if value, ok := agentEventValue(state, "model_step_stop", "stopReasonKind"); !ok || value != cloudAgentStopKindLength {
		t.Fatalf("事件里的终止原因 = %v（ok=%v）", value, ok)
	}
	if value, ok := agentEventValue(state, "model_step_stop", "truncated"); !ok || value != true {
		t.Fatalf("事件里的截断标记 = %v（ok=%v）", value, ok)
	}
	if value, ok := agentEventValue(state, "model_step_stop", "disposition"); !ok || value != cloudAgentStepDispositionRetry {
		t.Fatalf("事件里的处置 = %v（ok=%v）", value, ok)
	}
	// 截断步整步作废：正文不得进入历史，也不得发布任何最终答复。
	if value, ok := agentEventValue(state, "assistant_message", "final"); ok && value == true {
		t.Fatalf("截断正文被当成了最终答复: %v", value)
	}

	// 重试那一步：必须真的关掉思考。
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	_, retried := agentInterjectionState(t, s, root.ID)
	if retried.ActiveTaskID == "" {
		t.Fatal("截断重试没有重新发起模型调用")
	}
	task, err := s.repo.TaskForUser("user", retried.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if options := stepTaskTextOptions(t, s, task); options["thinking"] != false {
		t.Fatalf("重试请求没有关思考: %+v", options)
	}

	// 再截断一次：重试额度用尽 → 如实终止，绝不把半截正文当答复发布。
	setStepResult(t, db, retried.ActiveTaskID, map[string]any{
		"text": "又是半句话", "stopReason": "length", "stopReasonKind": cloudAgentStopKindLength,
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	run, state = agentInterjectionState(t, s, root.ID)
	if run.Status != "failed" {
		t.Fatalf("重试仍截断应如实终止：status=%s（%s）", run.Status, run.FailureMessage)
	}
	if !strings.Contains(run.FailureMessage, "截断") || strings.Contains(run.FailureMessage, "对账") {
		t.Fatalf("终止说明不对: %s", run.FailureMessage)
	}
	if state.TruncatedStepEscalated != 1 {
		t.Fatalf("截断重试必须只有一次，实际 %d", state.TruncatedStepEscalated)
	}
	if value, ok := agentEventValue(state, "model_step_stop", "disposition"); !ok || value != cloudAgentStepDispositionFail {
		t.Fatalf("事件里的处置 = %v（ok=%v）", value, ok)
	}
}

// 截断步即使解析出了工具调用也不执行：半截动作不许落到画布上。
func TestCloudAgentTruncatedStepDoesNotExecuteToolCalls(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	setStepResult(t, db, root.ID, map[string]any{
		"text": "先看一下", "stopReason": "length", "stopReasonKind": cloudAgentStopKindLength,
		"toolCalls": []map[string]any{{
			"id": "call-1", "type": "function",
			"function": map[string]any{"name": "canvas_get_state", "arguments": "{}"},
		}},
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	if run.Status != "running" {
		t.Fatalf("截断步应先重试：status=%s（%s）", run.Status, run.FailureMessage)
	}
	if value, ok := agentEventValue(state, "model_step_stop", "disposition"); !ok || value != cloudAgentStepDispositionRetry {
		t.Fatalf("带工具调用的截断步处置 = %v（ok=%v）", value, ok)
	}
	if value, ok := agentEventValue(state, "model_step_stop", "toolCalls"); !ok || value != float64(1) {
		t.Fatalf("事件里的工具调用数 = %v（ok=%v）", value, ok)
	}
	if state.CallIndex != 0 || len(state.Calls) != 0 {
		t.Fatalf("截断步的工具调用不得进入执行队列: calls=%d index=%d", len(state.Calls), state.CallIndex)
	}
	for _, event := range state.Events {
		if event.Type == "tool_completed" {
			t.Fatalf("截断步执行了工具: %+v", event.Payload)
		}
	}
}

// 正常结束不受新闸门影响：该完成的还是要完成，且 final 正文照常发布。
func TestCloudAgentNormalStopStillCompletes(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	setStepResult(t, db, root.ID, map[string]any{
		"text": "本轮已完成", "stopReason": "stop", "stopReasonKind": cloudAgentStopKindStop,
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	if run.Status != "completed" {
		t.Fatalf("正常结束应完成：status=%s（%s）", run.Status, run.FailureMessage)
	}
	if value, ok := agentEventValue(state, "model_step_stop", "truncated"); !ok || value != false {
		t.Fatalf("正常结束的截断标记 = %v（ok=%v）", value, ok)
	}
	if value, ok := agentEventValue(state, "assistant_message", "final"); !ok || value != true {
		t.Fatalf("最终答复应被发布: final=%v（ok=%v）", value, ok)
	}
}

// 流结束标记与"收到终止原因"是两件事：OpenAI 的 [DONE]、Claude 的 message_stop 都要单独记。
func TestStreamingAgentParserRecordsStreamDoneWithoutStopReason(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		frames   []string
	}{
		{name: "OpenAI 只有 DONE", protocol: "chat-completions", frames: []string{
			`data: {"choices":[{"delta":{"content":"话说到一半"}}]}`,
			`data: [DONE]`,
		}},
		{name: "Claude 只有 message_stop", protocol: "claude-api", frames: []string{
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"话说到一半"}}`,
			`data: {"type":"message_stop"}`,
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			parser := newStreamingAgentParser(item.protocol, func(string) {})
			for _, frame := range item.frames {
				parser.consume("text/event-stream", []byte(frame+"\n\n"))
			}
			parser.flush()
			result, err := parser.result()
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := result["streamDoneSeen"]; got != true {
				t.Fatalf("streamDoneSeen = %v, 期望 true", got)
			}
			if got := result["terminalEventSeen"]; got != false {
				t.Fatalf("terminalEventSeen = %v, 期望 false（没有终止块）", got)
			}
			if got := result["stopReasonKind"]; got != cloudAgentStopKindUnknown {
				t.Fatalf("stopReasonKind = %v, 期望 unknown", got)
			}
		})
	}
}

// 处置按 kind 显式分支：不是"其余一律接受"。
func TestCloudAgentStepStopDispositionFollowsStopKind(t *testing.T) {
	cases := []struct {
		name string
		kind string
		want string
	}{
		{name: "正常结束", kind: cloudAgentStopKindStop, want: cloudAgentStepDispositionAccept},
		{name: "请求工具", kind: cloudAgentStopKindToolCalls, want: cloudAgentStepDispositionAccept},
		{name: "上下文窗口到顶按部分答复发布", kind: cloudAgentStopKindContextLimit, want: cloudAgentStepDispositionAccept},
		{name: "拿不到原因暂按可用处理", kind: cloudAgentStopKindUnknown, want: cloudAgentStepDispositionAccept},
		{name: "首次截断重试", kind: cloudAgentStopKindLength, want: cloudAgentStepDispositionRetry},
		{name: "服务端工具挂起不发布", kind: cloudAgentStopKindPause, want: cloudAgentStepDispositionFail},
		{name: "内容过滤不发布", kind: cloudAgentStopKindContentFilter, want: cloudAgentStepDispositionFail},
		{name: "拒答不发布", kind: cloudAgentStopKindRefusal, want: cloudAgentStepDispositionFail},
		{name: "未完成但无原因不发布", kind: cloudAgentStopKindIncompleteUnknown, want: cloudAgentStepDispositionFail},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := cloudAgentStepStopDisposition(&cloudAgentRuntime{}, item.kind); got != item.want {
				t.Fatalf("disposition(%s) = %s, want %s", item.kind, got, item.want)
			}
		})
	}
	// 截断额度用尽 → 失败（每个 run 最多一次截断升级）
	exhausted := &cloudAgentRuntime{TruncatedStepEscalated: cloudAgentMaxTruncatedStepEscalations}
	if got := cloudAgentStepStopDisposition(exhausted, cloudAgentStopKindLength); got != cloudAgentStepDispositionFail {
		t.Fatalf("额度用尽后 disposition = %s, want fail", got)
	}
	// 只有 context_limit 算"响应有效但可能不完整"
	if !cloudAgentStopReasonPartialAnswer(cloudAgentStopKindContextLimit) {
		t.Fatal("context_limit 应标记为部分答复")
	}
	for _, kind := range []string{cloudAgentStopKindStop, cloudAgentStopKindToolCalls, cloudAgentStopKindLength,
		cloudAgentStopKindPause, cloudAgentStopKindContentFilter, cloudAgentStopKindRefusal,
		cloudAgentStopKindIncompleteUnknown, cloudAgentStopKindUnknown} {
		if cloudAgentStopReasonPartialAnswer(kind) {
			t.Fatalf("%s 不该被标记为部分答复", kind)
		}
	}
}

// pause：不发布、不执行工具、不改请求形状、如实失败（本轮不做无损续轮）。
func TestCloudAgentPausedStepFailsWithoutPublishingOrRunningTools(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	setStepResult(t, db, root.ID, map[string]any{
		"text": "先看一下", "stopReason": "pause_turn", "stopReasonKind": cloudAgentStopKindPause,
		"toolCalls": []map[string]any{{
			"id": "call-1", "type": "function",
			"function": map[string]any{"name": "canvas_get_state", "arguments": "{}"},
		}},
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	if run.Status != "failed" {
		t.Fatalf("pause 应如实失败：status=%s（%s）", run.Status, run.FailureMessage)
	}
	if !strings.Contains(run.FailureMessage, "挂起") || strings.Contains(run.FailureMessage, "输出上限") {
		t.Fatalf("pause 的终止说明不对: %s", run.FailureMessage)
	}
	if state.TruncatedStepEscalated != 0 {
		t.Fatal("pause 不该走截断重试阶梯")
	}
	if state.ForceThinkingOff || state.BoostStepOutputBudget {
		t.Fatalf("pause 不该改请求形状：thinkingOff=%v boosted=%v", state.ForceThinkingOff, state.BoostStepOutputBudget)
	}
	if len(state.Calls) != 0 || state.CallIndex != 0 {
		t.Fatalf("pause 步的工具调用不得进入执行队列: calls=%d index=%d", len(state.Calls), state.CallIndex)
	}
	for _, event := range state.Events {
		if event.Type == "tool_completed" {
			t.Fatalf("pause 步执行了工具: %+v", event.Payload)
		}
	}
	if value, ok := agentEventValue(state, "model_step_stop", "disposition"); !ok || value != cloudAgentStepDispositionFail {
		t.Fatalf("事件里的处置 = %v（ok=%v）", value, ok)
	}
}

// D：升级开关必须在重试被接受后复位，否则一次截断会让本轮余下所有步骤都关着思考。
func TestCloudAgentEscalationSwitchesRestoredAfterAcceptedRetry(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	setStepResult(t, db, root.ID, map[string]any{
		"text": "半句话的结论", "stopReason": "length", "stopReasonKind": cloudAgentStopKindLength,
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	_, escalated := agentInterjectionState(t, s, root.ID)
	if !escalated.ForceThinkingOff || !escalated.BoostStepOutputBudget {
		t.Fatal("未按截断升级（开关应为 true）")
	}
	if escalated.EscalationRestore == nil {
		t.Fatal("升级时必须保存升级前的开关值")
	}
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	_, retried := agentInterjectionState(t, s, root.ID)
	if retried.ActiveTaskID == "" {
		t.Fatal("截断重试没有重新发起模型调用")
	}
	setStepResult(t, db, retried.ActiveTaskID, map[string]any{
		"text": "本轮已完成", "stopReason": "stop", "stopReasonKind": cloudAgentStopKindStop,
	})
	if err := s.advanceCloudAgentByID("user", root.ID); err != nil {
		t.Fatal(err)
	}
	run, restored := agentInterjectionState(t, s, root.ID)
	if run.Status != "completed" {
		t.Fatalf("重试成功应完成：status=%s（%s）", run.Status, run.FailureMessage)
	}
	if restored.ForceThinkingOff || restored.BoostStepOutputBudget {
		t.Fatalf("升级开关未复位：thinkingOff=%v boosted=%v", restored.ForceThinkingOff, restored.BoostStepOutputBudget)
	}
	if restored.EscalationRestore != nil {
		t.Fatal("复位后不该残留升级快照")
	}
}
