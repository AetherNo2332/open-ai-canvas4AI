package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func runAgentToolTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	// 浏览器持久化的是协议中立请求；资源水合和模型路由完成后才展开上游协议，
	// 防止供应商请求体反向污染任务记录，也避免切换模型时复用错误协议。
	if input.AgentRequests != nil && input.AgentRequests.Canonical != nil {
		_, declarative := agentProtocolAdapterForContext(ctx, input.Config.InterfaceType)
		requests, err := expandCanonicalAgentRequest(input.AgentRequests.Canonical, input.Config, declarative)
		if err != nil {
			return nil, err
		}
		input.AgentRequests = requests
	}
	if adapter, ok := agentProtocolAdapterForContext(ctx, input.Config.InterfaceType); ok {
		return runDeclarativeAgentTask(ctx, input, adapter)
	}
	if input.AgentRequests == nil {
		return nil, errors.New("画布 Agent 工具请求缺少协议参数")
	}
	request := input.AgentRequests.ChatCompletion
	path := "/chat/completions"
	protocol := "chat-completion"
	if input.Config.InterfaceType == string(model.ChannelInterfaceOpenAIResponse) {
		request = input.AgentRequests.Responses
		path = "/responses"
		protocol = "responses"
	} else if input.Config.InterfaceType == string(model.ChannelInterfaceClaudeAPI) {
		if input.AgentRequests.Claude != nil {
			request = input.AgentRequests.Claude
		}
		path = "/messages"
		protocol = "claude-api"
	}
	if request == nil {
		return nil, errors.New("画布 Agent 工具请求缺少协议参数")
	}
	body := cloneStringAnyMap(request)
	if protocol == "claude-api" && input.AgentRequests.Claude == nil {
		body = claudeAgentBody(body)
	}
	body["model"] = input.Config.Model
	applyTextThinking(body, input, protocol)
	applyAgentOutputLimit(body, cloudAgentOutputTokens(input), protocol)
	applyAgentStrictTools(body, input.TextOptions.StrictTools)
	normalizeAgentToolChoice(body, input, protocol)
	result, err := postAgentRequest(ctx, input, path, body, protocol)
	// 上游不认 strict：去掉标记重发一次，并记住这条线路（下次不再尝试）。
	if err != nil && input.TextOptions.StrictTools && isAgentStrictToolsCompatibilityError(err) {
		if withoutStrict := cloneStringAnyMap(body); stripAgentStrictTools(withoutStrict) {
			cloudAgentRememberStrictToolsRejected(input.Config.ChannelID, input.Config.ChannelModelKey)
			result, err = postAgentRequest(ctx, input, path, withoutStrict, protocol)
		}
	}
	if protocol == "chat-completion" && isAgentToolChoiceCompatibilityError(err) {
		if !isAutoAgentToolChoice(body["tool_choice"]) {
			autoBody := cloneStringAnyMap(body)
			autoBody["tool_choice"] = "auto"
			result, err = postAgentRequest(ctx, input, path, autoBody, protocol)
		}
		if isAgentToolChoiceCompatibilityError(err) {
			withoutToolChoice := cloneStringAnyMap(body)
			delete(withoutToolChoice, "tool_choice")
			result, err = postAgentRequest(ctx, input, path, withoutToolChoice, protocol)
		}
	}
	// 少数 OpenAI 兼容上游不认 parallel_tool_calls：回退到不带该字段的单调用行为，
	// 而不是让整步失败。
	if err != nil && protocol == "chat-completion" && isAgentParallelToolCallsCompatibilityError(err) {
		withoutParallel := cloneStringAnyMap(body)
		delete(withoutParallel, "parallel_tool_calls")
		result, err = postAgentRequest(ctx, input, path, withoutParallel, protocol)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// cloudAgentOutputTokens 取本次调用的输出上限。
//
// 三条来源语义不同，必须分层而不是"谁先谁赢"：
//   - input.MaxOutputTokens / input.TextOptions.MaxOutputTokens 是**策略上限**
//     （画布 Agent 每步按运行时策略下发；0 表示这一步不限制输出）；
//   - input.CapabilityMaxOutputTokens 是**模型物理上限**（渠道模型能力里声明的 maxOutputTokens）。
//
// 因此取所有非零值中的最小值：策略设 131072、能力 16384 → 16384；策略设 0（不限制）→ 用能力值；
// 两者都为 0 → 不限制（上游按剩余上下文放行）。
func cloudAgentOutputTokens(input canvasGenerationInput) int {
	limit := 0
	for _, candidate := range []int{input.MaxOutputTokens, input.TextOptions.MaxOutputTokens, input.CapabilityMaxOutputTokens} {
		if candidate <= 0 {
			continue
		}
		if limit == 0 || candidate < limit {
			limit = candidate
		}
	}
	return limit
}

// applyAgentOutputLimit 按协议写入输出上限字段名。
func applyAgentOutputLimit(body map[string]interface{}, limit int, protocol string) {
	if limit <= 0 {
		return
	}
	field := "max_tokens"
	if protocol == "responses" {
		field = "max_output_tokens"
	}
	applyTextOutputLimit(body, limit, field)
}

// isAgentParallelToolCallsCompatibilityError 识别上游不认识 parallel_tool_calls 的报错。
func isAgentParallelToolCallsCompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	var payloadErr providerPayloadError
	if errors.As(err, &payloadErr) {
		message += " " + strings.ToLower(payloadErr.raw)
	}
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		message += " " + strings.ToLower(httpErr.Body)
	}
	return strings.Contains(message, "parallel_tool_calls") || strings.Contains(message, "parallel tool calls") || strings.Contains(message, "parallel_tool_call")
}

func postAgentRequest(ctx context.Context, input canvasGenerationInput, path string, body map[string]interface{}, protocol string) (map[string]interface{}, error) {
	if input.StreamText {
		return postStreamingAgent(ctx, input.Config, path, body, protocol, input.OnTextDelta, input.OnReasoningDelta)
	}
	delete(body, "stream")
	var payload map[string]interface{}
	if err := postJSON(ctx, input.Config, path, body, &payload); err != nil {
		return nil, err
	}
	return parseAgentToolPayload(payload, protocol)
}

func runDeclarativeAgentTask(ctx context.Context, input canvasGenerationInput, adapter protocol.AgentAdapter) (map[string]interface{}, error) {
	wire := input.Config.InterfaceType
	if wire == string(model.ChannelInterfaceOpenAIResponse) {
		wire = "responses"
	}
	knownWire := wire == "chat-completion" || wire == "responses" || wire == "claude-api"
	if input.TextOptions.Thinking && !knownWire {
		return nil, errors.New("当前声明式 Agent 渠道尚不支持思考模式，请关闭思考模式或切换内置协议渠道")
	}
	if input.AgentRequests == nil {
		return nil, errors.New("画布 Agent 工具请求缺少协议参数")
	}
	request := map[string]any{
		"chatCompletion": input.AgentRequests.ChatCompletion,
		"responses":      input.AgentRequests.Responses,
		"claude":         input.AgentRequests.Claude,
		"gemini":         input.AgentRequests.Gemini,
	}
	spec, err := adapter.BuildAgent(ctx, protocol.AgentRequestContext{BaseURL: input.Config.BaseURL, Model: input.Config.Model, Request: request})
	if err != nil {
		return nil, err
	}
	if knownWire {
		body := protocolBodyObject(spec.Body)
		if body == nil {
			return nil, errors.New("声明式 Agent 请求体必须是 JSON 对象")
		}
		applyTextThinking(body, input, wire)
		applyAgentOutputLimit(body, cloudAgentOutputTokens(input), wire)
		applyAgentStrictTools(body, input.TextOptions.StrictTools)
		normalizeAgentToolChoice(body, input, wire)
		spec.Body = body
		if input.StreamText {
			body["stream"] = true
			if wire == "chat-completion" {
				if err := ensureChatCompletionStreamUsage(body); err != nil {
					return nil, err
				}
			}
			parser := newStreamingAgentParser(wire, input.OnTextDelta)
			parser.emitReasoning = input.OnReasoningDelta
			data, mime, err := executeProtocolBinaryRequestWithConsumer(ctx, input.Config, spec, parser.consume)
			if err != nil {
				return nil, err
			}
			if strings.Contains(strings.ToLower(mime), "event-stream") {
				parser.flush()
				result, resultErr := parser.result()
				if resultErr == nil {
					attachProviderPiUsage(result, providerPiUsageFromBody(data, wire))
				}
				return result, resultErr
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, fmt.Errorf("Agent 接口返回格式无效：%w", err)
			}
			return parseAgentToolPayload(payload, wire)
		}
	}
	body, err := executeDeclarativeAgentWithGeminiCache(ctx, input, spec)
	if err != nil {
		return nil, err
	}
	// Known protocols must preserve stop reasons and provider usage for non-stream
	// requests too. Native compaction cannot accept a truncated summary as complete.
	if knownWire {
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("Agent 接口返回格式无效：%w", err)
		}
		return parseAgentToolPayload(payload, wire)
	}
	parsed, err := adapter.ParseAgent(ctx, body)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{"mode": "text", "text": parsed.Text, "toolCalls": []interface{}{}}
	if parsed.Reasoning != "" {
		result["reasoning"] = parsed.Reasoning
	}
	attachProviderPiUsage(result, providerPiUsageFromManifestUsage(parsed.Usage))
	calls := make([]interface{}, 0, len(parsed.ToolCalls))
	for _, call := range parsed.ToolCalls {
		mapped := map[string]interface{}{
			"id":       call.ID,
			"type":     "function",
			"function": map[string]interface{}{"name": call.Name, "arguments": call.Arguments},
		}
		if call.ThoughtSignature != "" {
			mapped["thoughtSignature"] = call.ThoughtSignature
		}
		calls = append(calls, mapped)
	}
	result["toolCalls"] = calls
	if strings.TrimSpace(parsed.Text) == "" && len(calls) == 0 {
		return nil, errors.New("声明式 Agent 接口没有返回内容")
	}
	return result, nil
}

// executeDeclarativeAgentWithGeminiCache keeps explicit Prompt Cache entirely
// optional: cache creation, cleanup, and a single stale-cache rebuild can never
// turn a valid uncached Agent request into a failed run.
func executeDeclarativeAgentWithGeminiCache(ctx context.Context, input canvasGenerationInput, spec protocol.RequestSpec) ([]byte, error) {
	baseSpec, err := cloneProtocolRequestSpec(spec)
	if err != nil {
		return nil, err
	}
	executeSpec := baseSpec
	cacheUsed := false
	if input.Config.InterfaceType == officialGeminiAgentInterface {
		prepared, used, prepareErr := prepareOfficialGeminiAgentCache(ctx, input, baseSpec)
		if prepareErr != nil {
			log.Printf("gemini agent prompt cache unavailable: model=%s reason=%s", safeProviderModel(input.Config.Model), safeProviderLogError(prepareErr))
		} else {
			executeSpec, cacheUsed = prepared, used
		}
	}
	body, err := executeProtocolRequest(ctx, input.Config, executeSpec)
	if err == nil || input.Config.InterfaceType != officialGeminiAgentInterface || !cacheUsed || !geminiRequestUsesCachedContent(executeSpec) || !isGeminiCachedContentNotFound(err, stringValue(protocolBodyObject(executeSpec.Body)["cachedContent"])) {
		return body, err
	}

	resourceName := ""
	if body := protocolBodyObject(executeSpec.Body); body != nil {
		resourceName, _ = body["cachedContent"].(string)
	}
	if invalidateErr := invalidateOfficialGeminiAgentCache(ctx, input, baseSpec, resourceName); invalidateErr != nil {
		// The local identity is still removed whenever possible. Do not replace a
		// provider 404 with a cache bookkeeping error or skip the one rebuild.
		log.Printf("gemini agent prompt cache invalidation failed: model=%s reason=%s", safeProviderModel(input.Config.Model), safeProviderLogError(invalidateErr))
	}
	rebuilt, _, prepareErr := prepareOfficialGeminiAgentCacheMode(ctx, input, baseSpec, true)
	if prepareErr != nil {
		log.Printf("gemini agent prompt cache rebuild unavailable: model=%s reason=%s", safeProviderModel(input.Config.Model), safeProviderLogError(prepareErr))
		rebuilt = baseSpec
	}
	// The rebuilt request is intentionally executed only once. If it receives
	// another CachedContent 404, the first stale-cache recovery already happened;
	// preserve the provider error instead of recursively rebuilding forever.
	return executeProtocolRequest(ctx, input.Config, rebuilt)
}

func claudeAgentBody(request map[string]interface{}) map[string]interface{} {
	body := map[string]interface{}{"max_tokens": 4096}
	if messages, ok := request["messages"].([]interface{}); ok {
		claudeMessages := make([]interface{}, 0, len(messages))
		var system []string
		for _, value := range messages {
			message, _ := value.(map[string]interface{})
			role := strings.ToLower(strings.TrimSpace(stringField(message, "role")))
			if role == "system" {
				if content := strings.TrimSpace(fmt.Sprint(message["content"])); content != "" {
					system = append(system, content)
				}
				continue
			}
			if role == "tool" {
				claudeMessages = append(claudeMessages, map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{
					"type": "tool_result", "tool_use_id": stringField(message, "tool_call_id"), "content": fmt.Sprint(message["content"]),
				}}})
				continue
			}
			role = mapClaudeMessageRole(role)
			content := message["content"]
			if content == nil {
				content = ""
			}
			if toolCalls, ok := message["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
				blocks := make([]interface{}, 0, len(toolCalls))
				for _, value := range toolCalls {
					toolCall, _ := value.(map[string]interface{})
					function, _ := toolCall["function"].(map[string]interface{})
					blocks = append(blocks, map[string]interface{}{
						"type": "tool_use", "id": stringField(toolCall, "id"), "name": stringField(function, "name"), "input": claudeToolInput(function["arguments"]),
					})
				}
				content = blocks
			}
			claudeMessages = append(claudeMessages, map[string]interface{}{"role": role, "content": content})
		}
		body["messages"] = claudeMessages
		if len(system) > 0 {
			body["system"] = []interface{}{map[string]interface{}{
				"type": "text", "text": strings.Join(system, "\n\n"),
				"cache_control": map[string]interface{}{"type": "ephemeral"},
			}}
		}
	}
	if tools, ok := request["tools"].([]interface{}); ok && len(tools) > 0 {
		claudeTools := make([]interface{}, 0, len(tools))
		for _, value := range tools {
			tool, _ := value.(map[string]interface{})
			function, _ := tool["function"].(map[string]interface{})
			if len(function) == 0 {
				continue
			}
			claudeTools = append(claudeTools, map[string]interface{}{
				"name": stringField(function, "name"), "description": stringField(function, "description"), "input_schema": function["parameters"],
			})
		}
		if len(claudeTools) > 0 {
			if last, ok := claudeTools[len(claudeTools)-1].(map[string]interface{}); ok {
				last["cache_control"] = map[string]interface{}{"type": "ephemeral"}
			}
			body["tools"] = claudeTools
		}
	}
	if choice, ok := request["tool_choice"]; ok {
		body["tool_choice"] = claudeToolChoice(choice)
	}
	return body
}

func mapClaudeMessageRole(role string) string {
	if role == "assistant" {
		return "assistant"
	}
	return "user"
}

func claudeToolInput(value interface{}) interface{} {
	if raw, ok := value.(string); ok {
		var parsed interface{}
		if json.Unmarshal([]byte(raw), &parsed) == nil && parsed != nil {
			return parsed
		}
	}
	if value != nil {
		return value
	}
	return map[string]interface{}{}
}

func claudeToolChoice(value interface{}) interface{} {
	switch choice := value.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "required":
			return map[string]interface{}{"type": "any"}
		case "none":
			return map[string]interface{}{"type": "auto"}
		default:
			return map[string]interface{}{"type": "auto"}
		}
	case map[string]interface{}:
		if function, ok := choice["function"].(map[string]interface{}); ok && stringField(function, "name") != "" {
			return map[string]interface{}{"type": "tool", "name": stringField(function, "name")}
		}
		if name := stringField(choice, "name"); name != "" {
			return map[string]interface{}{"type": "tool", "name": name}
		}
	}
	return map[string]interface{}{"type": "auto"}
}

func isAgentToolChoiceCompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	var payloadErr providerPayloadError
	if errors.As(err, &payloadErr) {
		message += " " + strings.ToLower(payloadErr.raw)
	}
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		message += " " + strings.ToLower(httpErr.Body)
	}
	return strings.Contains(message, "tool_choice") || strings.Contains(message, "tool choice") || strings.Contains(message, "tool-choice") || strings.Contains(message, "thinking mode")
}

func isAutoAgentToolChoice(value interface{}) bool {
	choice, ok := value.(string)
	return ok && strings.EqualFold(strings.TrimSpace(choice), "auto")
}

func parseAgentToolPayload(payload map[string]interface{}, protocol string) (map[string]interface{}, error) {
	if err := validateTextPayload(payload); err != nil {
		return nil, err
	}
	result := map[string]interface{}{"mode": "text", "text": "", "toolCalls": []interface{}{}}
	if encoded, err := json.Marshal(payload); err == nil {
		attachProviderPiUsage(result, providerPiUsageFromBody(encoded, protocol))
	}
	if protocol == "responses" {
		result["text"] = firstNonEmptyString(stringField(payload, "output_text"), extractResponseText(payload))
		if reasoning := extractResponseReasoning(payload); reasoning != "" {
			result["reasoning"] = reasoning
		}
		calls := make([]interface{}, 0)
		for _, value := range interfaceSlice(payload["output"]) {
			item, _ := value.(map[string]interface{})
			if stringField(item, "type") != "function_call" {
				continue
			}
			calls = append(calls, map[string]interface{}{"id": firstNonEmptyString(stringField(item, "call_id"), stringField(item, "id")), "type": "function", "function": map[string]interface{}{"name": stringField(item, "name"), "arguments": stringField(item, "arguments")}})
		}
		result["toolCalls"] = calls
		// failed/cancelled 是这次调用没成功，不是"内容不完整"：按解析错误处理。
		if cloudAgentResponsesStatusFailed(firstNonEmptyString(stringField(payload, "status"), "")) {
			return nil, fmt.Errorf("上游返回 %s，这一步没有完成", stringField(payload, "status"))
		}
		raw := cloudAgentResponsesStopReason("", payload)
		cloudAgentApplyStopReason(result, raw)
		// 非流式响应本身就是终态：terminalEventSeen/streamDoneSeen 都为真，
		// 只有"原因原文是否非空"需要如实记录。
		cloudAgentApplyStopFacts(result, true, strings.TrimSpace(raw) != "", true)
		return result, nil
	}
	if protocol == "claude-api" {
		content := interfaceSlice(payload["content"])
		calls := make([]interface{}, 0)
		for _, value := range content {
			item, _ := value.(map[string]interface{})
			switch stringField(item, "type") {
			case "text":
				result["text"] = result["text"].(string) + stringField(item, "text")
			case "thinking":
				result["reasoning"] = resultString(result, "reasoning") + firstNonEmptyString(stringField(item, "thinking"), stringField(item, "text"))
			case "tool_use":
				arguments, err := json.Marshal(item["input"])
				if err != nil {
					return nil, err
				}
				calls = append(calls, map[string]interface{}{"id": stringField(item, "id"), "type": "function", "function": map[string]interface{}{"name": stringField(item, "name"), "arguments": string(arguments)}})
			}
		}
		result["toolCalls"] = calls
		if result["text"] == "" && len(calls) == 0 {
			return nil, errors.New("Claude Agent 接口没有返回内容")
		}
		cloudAgentApplyStopReason(result, stringField(payload, "stop_reason"))
		cloudAgentApplyStopFacts(result, true, strings.TrimSpace(stringField(payload, "stop_reason")) != "", true)
		return result, nil
	}
	choices := interfaceSlice(payload["choices"])
	if len(choices) == 0 {
		return nil, errors.New("画布 Agent 接口没有返回 choices")
	}
	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	result["text"] = stringField(message, "content")
	if reasoning := firstNonEmptyString(stringField(message, "reasoning_content"), stringField(message, "reasoning")); reasoning != "" {
		result["reasoning"] = reasoning
	}
	calls := make([]interface{}, 0)
	for _, value := range interfaceSlice(message["tool_calls"]) {
		item, _ := value.(map[string]interface{})
		function, _ := item["function"].(map[string]interface{})
		calls = append(calls, map[string]interface{}{"id": stringField(item, "id"), "type": "function", "function": map[string]interface{}{"name": stringField(function, "name"), "arguments": stringField(function, "arguments")}})
	}
	result["toolCalls"] = calls
	cloudAgentApplyStopReason(result, stringField(choice, "finish_reason"))
	cloudAgentApplyStopFacts(result, true, strings.TrimSpace(stringField(choice, "finish_reason")) != "", true)
	return result, nil
}

func postStreamingAgent(ctx context.Context, config providerConfig, path string, body map[string]interface{}, protocol string, onDelta func(string), onReasoning ...func(string)) (map[string]interface{}, error) {
	body["stream"] = true
	if protocol == "chat-completion" {
		if err := ensureChatCompletionStreamUsage(body); err != nil {
			return nil, err
		}
	}
	parser := newStreamingAgentParser(protocol, onDelta)
	if len(onReasoning) > 0 {
		parser.emitReasoning = onReasoning[0]
	}
	data, mimeType, err := postStreamingBinary(ctx, config, path, body, parser.consume)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(mimeType), "event-stream") {
		var payload map[string]interface{}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("Agent 接口返回格式无效：%w", err)
		}
		return parseAgentToolPayload(payload, protocol)
	}
	parser.flush()
	result, resultErr := parser.result()
	if resultErr == nil {
		attachProviderPiUsage(result, providerPiUsageFromBody(data, protocol))
	}
	return result, resultErr
}

type streamingAgentToolCall struct {
	id        string
	name      string
	arguments string
}

type streamingAgentParser struct {
	protocol     string
	buffer       string
	text         strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*streamingAgentToolCall
	toolCallByID map[string]int
	completed    map[string]interface{}
	// stopReason 是上游的终止原因原文（OpenAI finish_reason / Claude stop_reason / Responses status）。
	// 它决定这一步是"说完了"还是"被截断了"，运行时按它分支，不再靠匹配错误字符串倒推。
	stopReason string
	// terminalEventSeen / streamDoneSeen 是**解析事实**：前者表示收到该协议的终态事件，
	// 后者表示收到该协议的流结束标记（OpenAI `[DONE]`、Claude `message_stop`、Responses 终态事件）。
	// 它们不能由归一化 kind 反推——上游给了新词时 kind 是 unknown，但终态事件确实收到了。
	terminalEventSeen bool
	streamDoneSeen    bool
	err               error
	emit              func(string)
	emitReasoning     func(string)
}

func newStreamingAgentParser(protocol string, emit func(string)) *streamingAgentParser {
	return &streamingAgentParser{protocol: protocol, toolCalls: map[int]*streamingAgentToolCall{}, toolCallByID: map[string]int{}, emit: emit}
}

func (p *streamingAgentParser) consume(mimeType string, chunk []byte) {
	if p == nil || p.err != nil || !strings.Contains(strings.ToLower(mimeType), "event-stream") || len(chunk) == 0 {
		return
	}
	p.buffer += string(chunk)
	p.consumeFrames(false)
}

func (p *streamingAgentParser) flush() {
	if p == nil || p.err != nil {
		return
	}
	p.consumeFrames(true)
}

func (p *streamingAgentParser) consumeFrames(flush bool) {
	for p.err == nil {
		match := sseFrameBoundaryPattern.FindStringIndex(p.buffer)
		if match == nil {
			break
		}
		p.consumeFrame(p.buffer[:match[0]])
		p.buffer = p.buffer[match[1]:]
	}
	if flush && p.err == nil && strings.TrimSpace(p.buffer) != "" {
		p.consumeFrame(p.buffer)
		p.buffer = ""
	}
}

func (p *streamingAgentParser) consumeFrame(frame string) {
	var eventName string
	var dataLines []string
	for _, line := range strings.Split(strings.ReplaceAll(frame, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	raw := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if raw == "[DONE]" {
		// OpenAI 的流结束标记：它本身不是终止原因，但"流被正常收尾"是独立事实。
		p.streamDoneSeen = true
		return
	}
	if raw == "" {
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		p.err = fmt.Errorf("Agent 流式事件解析失败：%w", err)
		return
	}
	if err := validateTextPayload(payload); err != nil {
		p.err = err
		return
	}
	switch p.protocol {
	case "responses":
		p.consumeResponsesEvent(eventName, payload)
	case "claude-api":
		p.consumeClaudeEvent(payload)
	default:
		p.consumeChatCompletionEvent(payload)
	}
}

func (p *streamingAgentParser) consumeResponsesEvent(eventName string, payload map[string]interface{}) {
	eventType := firstNonEmptyString(strings.TrimSpace(eventName), stringField(payload, "type"))
	switch eventType {
	case "response.output_text.delta", "output_text.delta":
		p.appendText(stringField(payload, "delta"))
	case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		p.appendReasoning(stringField(payload, "delta"))
	case "response.completed", "response.incomplete", "response.failed":
		response, _ := payload["response"].(map[string]interface{})
		if eventType == "response.completed" {
			p.completed = response
		}
		// 只有 status=completed 才算"说完了"；incomplete 要再读 incomplete_details.reason
		// 才能分出"输出到顶"与"内容过滤"。failed/cancelled 是这次调用没成功：按解析错误处理，
		// 走既有 model_failure 路径，不能让它在运行时被当成"正常结束但没给原因"。
		status := firstNonEmptyString(stringField(response, "status"), eventType)
		if cloudAgentResponsesStatusFailed(status) {
			p.err = fmt.Errorf("上游返回 %s，这一步没有完成", status)
			return
		}
		p.terminalEventSeen = true
		p.streamDoneSeen = true
		if reason := cloudAgentResponsesStopReason(status, response); reason != "" {
			p.stopReason = reason
		}
	case "response.output_item.added":
		item, _ := payload["item"].(map[string]interface{})
		if stringField(item, "type") == "function_call" {
			index := intField(payload, "output_index", len(p.toolCalls))
			p.setToolCall(index, firstNonEmptyString(stringField(item, "call_id"), stringField(item, "id")), stringField(item, "name"), stringField(item, "arguments"))
		}
	case "response.function_call_arguments.delta":
		index := p.responseToolCallIndex(payload)
		p.toolCall(index).arguments += stringField(payload, "delta")
	case "response.function_call_arguments.done":
		index := p.responseToolCallIndex(payload)
		if arguments := stringField(payload, "arguments"); arguments != "" {
			p.toolCall(index).arguments = arguments
		}
	}
}

func (p *streamingAgentParser) responseToolCallIndex(payload map[string]interface{}) int {
	itemID := firstNonEmptyString(stringField(payload, "item_id"), stringField(payload, "call_id"))
	if index, ok := p.toolCallByID[itemID]; ok {
		return index
	}
	return intField(payload, "output_index", len(p.toolCalls))
}

func (p *streamingAgentParser) consumeChatCompletionEvent(payload map[string]interface{}) {
	choices, _ := payload["choices"].([]interface{})
	for _, value := range choices {
		choice, _ := value.(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})
		p.appendText(streamContentText(delta["content"]))
		p.appendReasoning(firstNonEmptyString(stringField(delta, "reasoning_content"), stringField(delta, "reasoning"), stringField(delta, "reasoning_text")))
		for fallbackIndex, toolValue := range interfaceSlice(delta["tool_calls"]) {
			tool, _ := toolValue.(map[string]interface{})
			index := intField(tool, "index", fallbackIndex)
			function, _ := tool["function"].(map[string]interface{})
			current := p.toolCall(index)
			if id := stringField(tool, "id"); id != "" {
				current.id = id
				p.toolCallByID[id] = index
			}
			if name := stringField(function, "name"); name != "" {
				current.name += name
			}
			current.arguments += stringField(function, "arguments")
		}
		// 终止块才带非空 finish_reason（tool_calls/length/stop/…）；前面的 chunk 一律是 null，
		// 所以每次非空就覆盖，留到流结束时用的就是最后一个有效值。
		if reason := strings.TrimSpace(stringField(choice, "finish_reason")); reason != "" {
			p.stopReason = reason
			p.terminalEventSeen = true
		}
	}
}

func (p *streamingAgentParser) consumeClaudeEvent(payload map[string]interface{}) {
	switch stringField(payload, "type") {
	case "content_block_start":
		block, _ := payload["content_block"].(map[string]interface{})
		index := intField(payload, "index", len(p.toolCalls))
		switch stringField(block, "type") {
		case "text":
			p.appendText(stringField(block, "text"))
		case "thinking":
			p.appendReasoning(firstNonEmptyString(stringField(block, "thinking"), stringField(block, "text")))
		case "tool_use":
			arguments := ""
			if input := block["input"]; input != nil {
				if encoded, err := json.Marshal(input); err == nil && string(encoded) != "{}" {
					arguments = string(encoded)
				}
			}
			p.setToolCall(index, stringField(block, "id"), stringField(block, "name"), arguments)
		}
	case "content_block_delta":
		delta, _ := payload["delta"].(map[string]interface{})
		index := intField(payload, "index", len(p.toolCalls)-1)
		if stringField(delta, "type") == "text_delta" {
			p.appendText(stringField(delta, "text"))
		}
		if stringField(delta, "type") == "thinking_delta" {
			p.appendReasoning(firstNonEmptyString(stringField(delta, "thinking"), stringField(delta, "text")))
		}
		if stringField(delta, "type") == "input_json_delta" {
			p.toolCall(index).arguments += stringField(delta, "partial_json")
		}
	case "message_delta":
		// Claude 的终止原因在 message_delta.stop_reason：
		// max_tokens / tool_use / end_turn / stop_sequence / refusal / pause_turn。
		delta, _ := payload["delta"].(map[string]interface{})
		if reason := strings.TrimSpace(stringField(delta, "stop_reason")); reason != "" {
			p.stopReason = reason
			p.terminalEventSeen = true
		}
	case "message_stop":
		// Claude 的流结束标记。它可能与 stop_reason 同时出现，也可能没有（流被中断），
		// 所以"收到终态事件"与"流正常收尾"必须分开记。
		p.streamDoneSeen = true
	case "error":
		errValue, _ := payload["error"].(map[string]interface{})
		p.err = errors.New(defaultString(stringField(errValue, "message"), "Claude 上游返回失败"))
	}
}

func resultString(value map[string]interface{}, key string) string {
	return stringField(value, key)
}

func (p *streamingAgentParser) appendText(delta string) {
	if delta == "" {
		return
	}
	p.text.WriteString(delta)
	if p.emit != nil {
		p.emit(delta)
	}
}

func (p *streamingAgentParser) appendReasoning(delta string) {
	if delta == "" {
		return
	}
	p.reasoning.WriteString(delta)
	if p.emitReasoning != nil {
		p.emitReasoning(delta)
	}
}

func (p *streamingAgentParser) toolCall(index int) *streamingAgentToolCall {
	if index < 0 {
		index = 0
	}
	if p.toolCalls[index] == nil {
		p.toolCalls[index] = &streamingAgentToolCall{}
	}
	return p.toolCalls[index]
}

func (p *streamingAgentParser) setToolCall(index int, id string, name string, arguments string) {
	call := p.toolCall(index)
	call.id, call.name, call.arguments = id, name, arguments
	if id != "" {
		p.toolCallByID[id] = index
	}
}

func (p *streamingAgentParser) result() (map[string]interface{}, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.completed != nil {
		result, err := parseAgentToolPayload(p.completed, p.protocol)
		if err != nil {
			return nil, err
		}
		if p.text.Len() > 0 {
			result["text"] = p.text.String()
		}
		if p.reasoning.Len() > 0 {
			result["reasoning"] = p.reasoning.String()
		}
		// 流式拿到过终止块时以它为准（parseAgentToolPayload 只能看到 completed 快照）。
		if strings.TrimSpace(p.stopReason) != "" {
			cloudAgentApplyStopReason(result, p.stopReason)
		}
		cloudAgentApplyStopFacts(result, p.terminalEventSeen, strings.TrimSpace(p.stopReason) != "", p.streamDoneSeen)
		return result, nil
	}
	result := map[string]interface{}{"mode": "text", "text": p.text.String(), "toolCalls": []interface{}{}}
	if p.reasoning.Len() > 0 {
		result["reasoning"] = p.reasoning.String()
	}
	indices := make([]int, 0, len(p.toolCalls))
	for index := range p.toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	calls := make([]interface{}, 0, len(indices))
	for _, index := range indices {
		call := p.toolCalls[index]
		if call == nil || strings.TrimSpace(call.id) == "" || strings.TrimSpace(call.name) == "" {
			continue
		}
		arguments := call.arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
			return nil, fmt.Errorf("Agent 工具参数不是完整 JSON：%w", err)
		}
		calls = append(calls, map[string]interface{}{"id": call.id, "type": "function", "function": map[string]interface{}{"name": call.name, "arguments": arguments}})
	}
	result["toolCalls"] = calls
	if p.text.Len() == 0 && len(calls) == 0 {
		return nil, errors.New("画布 Agent 接口没有返回内容")
	}
	cloudAgentApplyStopReason(result, p.stopReason)
	cloudAgentApplyStopFacts(result, p.terminalEventSeen, strings.TrimSpace(p.stopReason) != "", p.streamDoneSeen)
	return result, nil
}

func intField(value map[string]interface{}, key string, fallback int) int {
	number, ok := value[key].(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return fallback
	}
	return int(number)
}

func extractResponseReasoning(payload map[string]interface{}) string {
	var chunks []string
	for _, value := range interfaceSlice(payload["output"]) {
		item, _ := value.(map[string]interface{})
		if stringField(item, "type") != "reasoning" {
			continue
		}
		for _, key := range []string{"summary", "content"} {
			for _, part := range interfaceSlice(item[key]) {
				record, _ := part.(map[string]interface{})
				if text := strings.TrimSpace(stringField(record, "text")); text != "" {
					chunks = append(chunks, text)
				}
			}
		}
	}
	return strings.Join(chunks, "\n")
}

func interfaceSlice(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func cloneStringAnyMap(value map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(value)+1)
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func attachProviderPiUsage(result map[string]interface{}, usage map[string]any) {
	if len(usage) > 0 {
		result["usage"] = usage
	}
}
