package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func runAgentToolTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	// 浏览器持久化的是协议中立请求；资源水合和模型路由完成后才展开上游协议，
	// 防止供应商请求体反向污染任务记录，也避免切换模型时复用错误协议。
	if input.AgentRequests != nil && input.AgentRequests.Canonical != nil {
		_, declarative := agentProtocolAdapterForContext(ctx, input.Config.InterfaceType)
		wire := textThinkingWire(ctx, input.Config.InterfaceType, true)
		if wire == protocol.TextWireClaude && claudeToolLoopMissingThinking(input.AgentRequests.Canonical) {
			// 仅旧式预算思考要求工具续轮保留思考块；adaptive 可以合法跳过思考，不能据此降档。
			config := effectiveTextThinkingConfig(input, wire)
			if config != nil && config.Intensity.Kind == ThinkingIntensityBudget && thinkingLevelsContain(config.Levels, ThinkingLevelOff) {
				input.TextOptions.ThinkingLevel = ThinkingLevelOff
			}
		}
		if plan, err := resolveTextThinkingPlan(input, wire); err == nil {
			// Omitted/auto uses the provider default and must keep same-wire reasoning;
			// only a resolved explicit off disables passback.
			input.AgentRequests.Canonical.PassReasoning = plan.Level != ThinkingLevelOff
		}
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
	applyAgentOutputLimit(body, agentStepOutputLimit(input), protocol)
	thinking := applyAgentThinking(body, input, protocol)
	normalizeAgentToolChoice(body, thinking, protocol)
	result, err := postAgentRequest(ctx, input, path, body, protocol)
	return retryAgentToolCompatibility(ctx, input, path, body, protocol, result, err)
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

func agentStepOutputLimit(input canvasGenerationInput) int { return cloudAgentOutputTokens(input) }

func applyAgentThinking(body map[string]interface{}, input canvasGenerationInput, wire string) bool {
	if limit := cloudAgentOutputTokens(input); limit > 0 {
		input.MaxOutputTokens = limit
	}
	return applyTextThinking(body, input, wire)
}

func runDeclarativeAgentTask(ctx context.Context, input canvasGenerationInput, adapter protocol.AgentAdapter) (map[string]interface{}, error) {
	// 线格式取自插件声明的 agent 路径：Chat Completions / Responses / Messages 以外的
	// 接口（如 Gemini generateContent）由插件自己映射，宿主不往里补思考参数。
	wire := declaredAgentWire(ctx, input.Config.InterfaceType)
	knownWire := wire != ""
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
		applyAgentOutputLimit(body, agentStepOutputLimit(input), wire)
		thinking := applyAgentThinking(body, input, wire)
		normalizeAgentToolChoice(body, thinking, wire)
		spec.Body = body
		if input.StreamText {
			body["stream"] = true
			if wire == protocol.TextWireChatCompletion {
				if err := ensureChatCompletionStreamUsage(body); err != nil {
					return nil, err
				}
			}
			data, mime, parser, err := executeTextWireStream(ctx, input.Config, spec, body, wire, func() *streamingAgentParser {
				parser := newStreamingAgentParser(wire, input.OnTextDelta)
				parser.emitReasoning = input.OnReasoningDelta
				return parser
			})
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
	if thinkingWire := textThinkingWire(ctx, input.Config.InterfaceType, true); thinkingWire != "" {
		// Gemini generateContent 等插件自定义线格式：请求体由插件映射，宿主只补思考参数。
		if body := protocolBodyObject(spec.Body); body != nil {
			applyAgentThinking(body, input, thinkingWire)
			spec.Body = body
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
				blocks := make([]interface{}, 0, len(toolCalls)+1)
				// 思考块必须排在本轮内容最前面并原样带回签名，见 attachCanonicalAgentReasoning。
				blocks = append(blocks, interfaceSlice(message["reasoning_blocks"])...)
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
		return parseAgentResponsesPayload(payload, result)
	}
	if protocol == "claude-api" {
		content := interfaceSlice(payload["content"])
		calls := make([]interface{}, 0)
		var blocks []interface{}
		for _, value := range content {
			item, _ := value.(map[string]interface{})
			switch stringField(item, "type") {
			case "text":
				result["text"] = result["text"].(string) + stringField(item, "text")
			case "thinking":
				result["reasoning"] = resultString(result, "reasoning") + firstNonEmptyString(stringField(item, "thinking"), stringField(item, "text"))
				blocks = append(blocks, map[string]interface{}{"type": "thinking", "thinking": stringField(item, "thinking"), "signature": stringField(item, "signature")})
			case "redacted_thinking":
				blocks = append(blocks, map[string]interface{}{"type": "redacted_thinking", "data": stringField(item, "data")})
			case "tool_use":
				arguments, err := json.Marshal(item["input"])
				if err != nil {
					return nil, err
				}
				calls = append(calls, map[string]interface{}{"id": stringField(item, "id"), "type": "function", "function": map[string]interface{}{"name": stringField(item, "name"), "arguments": string(arguments)}})
			}
		}
		result["toolCalls"] = calls
		if len(blocks) > 0 {
			result["reasoningBlocks"] = blocks
		}
		markAgentReasoningWire(result, protocol)
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
	markAgentReasoningWire(result, protocol)
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

func retryAgentToolCompatibility(ctx context.Context, input canvasGenerationInput, path string, body map[string]interface{}, protocol string, result map[string]interface{}, err error) (map[string]interface{}, error) {
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
	if protocol == "claude-api" && isAgentToolChoiceCompatibilityError(err) {
		if choice, _ := body["tool_choice"].(map[string]interface{}); stringField(choice, "type") != "auto" {
			// Claude Opus 5.5、Sonnet 5.5 等模型不支持强制 tool_choice（any/tool），改用 auto 重发一次。
			autoBody := cloneStringAnyMap(body)
			autoBody["tool_choice"] = map[string]interface{}{"type": "auto"}
			result, err = postAgentRequest(ctx, input, path, autoBody, protocol)
		}
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func parseAgentResponsesPayload(payload, result map[string]interface{}) (map[string]interface{}, error) {
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
