package app

import (
	"context"
	"encoding/json"
	"strings"
	"yingce/backend/internal/model"
	protocolcontract "yingce/backend/internal/protocol"
)

func textThinkingWire(ctx context.Context, interfaceType string, agent bool) string {
	switch interfaceType {
	case string(model.ChannelInterfaceChatCompletion):
		return protocolcontract.TextWireChatCompletion
	case string(model.ChannelInterfaceOpenAIResponse):
		return protocolcontract.TextWireResponses
	case string(model.ChannelInterfaceClaudeAPI):
		return protocolcontract.TextWireClaude
	}
	wire := declaredTextWire(ctx, interfaceType)
	if agent {
		wire = declaredAgentWire(ctx, interfaceType)
	}
	if wire != "" {
		return wire
	}
	if adapter, ok := declarativeProtocolAdapterForContext(ctx, interfaceType); ok {
		summary := adapter.Metadata().Create
		if agent {
			summary = adapter.Metadata().Agent
		}
		if isGeminiGenerateContentPath(protocolcontract.OperationPath(summary)) {
			return thinkingWireGemini
		}
	}
	if interfaceType == "" {
		// 旧任务没有接口类型时走 Responses 兜底，与 runLegacyTextTask 一致。
		return protocolcontract.TextWireResponses
	}
	return ""
}

// thinkingWireForProtocol 在后台保存模型配置时确定协议格式；确定不了返回空，只做通用校验。
func thinkingWireForProtocol(protocol string, registry *protocolcontract.Registry) string {
	switch protocol {
	case string(model.ChannelInterfaceChatCompletion):
		return protocolcontract.TextWireChatCompletion
	case string(model.ChannelInterfaceOpenAIResponse):
		return protocolcontract.TextWireResponses
	case string(model.ChannelInterfaceClaudeAPI):
		return protocolcontract.TextWireClaude
	}
	if registry == nil {
		return ""
	}
	adapter, ok := registry.Resolve(protocol)
	if !ok {
		return ""
	}
	metadata := adapter.Metadata()
	if metadata.TextWire != "" {
		return metadata.TextWire
	}
	if metadata.AgentWire != "" {
		return metadata.AgentWire
	}
	if isGeminiGenerateContentPath(protocolcontract.OperationPath(metadata.Create)) {
		return thinkingWireGemini
	}
	return ""
}

func isGeminiGenerateContentPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	return strings.HasSuffix(path, ":generatecontent") || strings.HasSuffix(path, ":streamgeneratecontent")
}

// textThinkingPlan 是一次请求解析后的思考决定。
type textThinkingPlan struct {
	Config *TextThinkingConfig
	// Level 为空表示不发送思考参数。
	Level string
}

func (p textThinkingPlan) enabled() bool {
	return p.Level != "" && p.Level != ThinkingLevelOff
}

func resolveTextThinkingPlan(input canvasGenerationInput, wire string) (textThinkingPlan, error) {
	config := effectiveTextThinkingConfig(input, wire)
	level, err := resolveThinkingLevel(config, input.TextOptions.ThinkingLevel)
	if err != nil {
		return textThinkingPlan{}, err
	}
	return textThinkingPlan{Config: config, Level: level}, nil
}

// applyTextThinking 按模型声明把思考档位写进上游请求体，返回是否开启了思考。
// 请求档位在任务创建与执行入口已校验；这里解析失败时不写参数。
func applyTextThinking(body map[string]interface{}, input canvasGenerationInput, wire string) bool {
	if wire == protocolcontract.TextWireClaude {
		// Messages API no longer accepts sampling controls; remove them even when thinking is omitted.
		delete(body, "temperature")
		delete(body, "top_p")
		delete(body, "top_k")
	}
	plan, err := resolveTextThinkingPlan(input, wire)
	if err != nil || plan.Level == "" || plan.Config == nil {
		return false
	}
	return writeTextThinking(body, plan, wire, input.MaxOutputTokens)
}

func writeTextThinking(body map[string]interface{}, plan textThinkingPlan, wire string, maxOutputTokens int) bool {
	config := plan.Config
	level := plan.Level
	if level == ThinkingLevelOff {
		writeThinkingDisabled(body, config, wire)
		return false
	}
	switch config.Switch.Kind {
	case ThinkingSwitchEnableFlag:
		body["enable_thinking"] = true
	case ThinkingSwitchThinkingType:
		body["thinking"] = map[string]interface{}{"type": defaultString(config.Switch.OnValue, "enabled")}
	}
	switch config.Intensity.Kind {
	case ThinkingIntensityEffort:
		field, ok := thinkingEffortField(config.Intensity.Field, wire)
		if !ok {
			return false
		}
		setThinkingField(body, field, defaultString(config.Intensity.Values[level], level))
		if wire == protocolcontract.TextWireClaude {
			body["thinking"] = map[string]interface{}{"type": "adaptive"}
		}
	case ThinkingIntensityBudget:
		field, ok := thinkingBudgetField(config.Intensity.Field, wire)
		if !ok {
			return false
		}
		budget := thinkingBudgetFor(config, level, thinkingOutputLimit(body, wire, maxOutputTokens))
		if wire == protocolcontract.TextWireClaude {
			body["thinking"] = map[string]interface{}{"type": "enabled", "budget_tokens": budget}
			ensureClaudeThinkingMaxTokens(body, budget, maxOutputTokens)
		} else {
			setThinkingField(body, field, budget)
		}
	case ThinkingIntensityGeminiLevel:
		setThinkingField(body, "generationConfig.thinkingConfig.thinkingLevel", defaultString(config.Intensity.Values[level], level))
	case ThinkingIntensityModelSuffix:
		if modelName, ok := body["model"].(string); ok && modelName != "" {
			suffix := config.Intensity.Values[level]
			if !strings.HasSuffix(modelName, suffix) {
				body["model"] = modelName + suffix
			}
		}
	}
	if wire == protocolcontract.TextWireResponses {
		if reasoning, ok := body["reasoning"].(map[string]interface{}); ok {
			reasoning["summary"] = "auto"
		} else {
			body["reasoning"] = map[string]interface{}{"summary": "auto"}
		}
	}
	return true
}

// thinkingOutputLimit 取本次请求的输出上限，预算按它的比例分配。
func thinkingOutputLimit(body map[string]interface{}, wire string, maxOutputTokens int) int {
	for _, field := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
		if value := intFromAny(body[field]); value > 0 {
			return value
		}
	}
	if wire == thinkingWireGemini {
		if config, ok := body["generationConfig"].(map[string]interface{}); ok {
			if value := intFromAny(config["maxOutputTokens"]); value > 0 {
				return value
			}
		}
	}
	if maxOutputTokens > 0 {
		return maxOutputTokens
	}
	return thinkingBudgetFallbackLimit
}

func thinkingBudgetFor(config *TextThinkingConfig, level string, limit int) int {
	if budget := config.Intensity.Budgets[level]; budget > 0 {
		return budget
	}
	ratio := thinkingBudgetRatios[level]
	if ratio == 0 {
		ratio = thinkingBudgetRatios[ThinkingLevelMedium]
	}
	budget := int(float64(limit) * ratio)
	if budget < thinkingBudgetFloor {
		budget = thinkingBudgetFloor
	}
	return budget
}

// ensureClaudeThinkingMaxTokens 保证 max_tokens 大于思考预算，并给正文和工具参数留出空间。
func ensureClaudeThinkingMaxTokens(body map[string]interface{}, budget, maxOutputTokens int) {
	current := intFromAny(body["max_tokens"])
	required := budget + thinkingBudgetFloor
	if current > budget {
		return
	}
	if maxOutputTokens > 0 && required > maxOutputTokens {
		required = maxOutputTokens
	}
	if required <= budget {
		required = budget + 1
	}
	body["max_tokens"] = required
}

func setThinkingField(body map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := body
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func intFromAny(value interface{}) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	}
	return 0
}

// markAgentReasoningWire 给带思考内容的 Agent 结果记上产生它的协议。思考签名和
// reasoning_content 只能回传给同一协议，运行中换模型后必须丢弃。
func markAgentReasoningWire(result map[string]interface{}, wire string) {
	if result == nil {
		return
	}
	if stringField(result, "reasoning") != "" || len(interfaceSlice(result["reasoningBlocks"])) > 0 {
		result["reasoningWire"] = wire
	}
}

// agentReasoningPassback 是模型步交给 Pi 运行时保存的思考回传内容，随 Pi 的 thinking 块
// （thinkingSignature 字段）往返，下一步再还原到 canonical assistant 消息上。
type agentReasoningPassback struct {
	Wire   string        `json:"wire"`
	Blocks []interface{} `json:"blocks,omitempty"`
}

func decodeAgentReasoningPassback(raw string) (agentReasoningPassback, bool) {
	var passback agentReasoningPassback
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") || json.Unmarshal([]byte(raw), &passback) != nil || passback.Wire == "" {
		return agentReasoningPassback{}, false
	}
	return passback, true
}

// canonicalAssistantReasoning 把模型步结果里的思考内容整理成 canonical assistant 消息字段。
func canonicalAssistantReasoning(message map[string]any, reasoning, wire string, blocks []interface{}) {
	if wire == "" {
		return
	}
	if wire == protocolcontract.TextWireClaude {
		if len(blocks) > 0 {
			message["reasoning_blocks"] = blocks
			message["reasoning_wire"] = wire
		}
		return
	}
	if wire == protocolcontract.TextWireChatCompletion && strings.TrimSpace(reasoning) != "" {
		message["reasoning_content"] = reasoning
		message["reasoning_wire"] = wire
	}
}

// attachCanonicalAgentReasoning 只回传同协议产生的思考内容；Claude 块必须原样带签名。
// 原始协议内容不能按展示限制截断，整体状态容量仍由已有 checkpoint 边界校验。
func attachCanonicalAgentReasoning(converted, message map[string]interface{}, claude bool) {
	wire := stringField(message, "reasoning_wire")
	if claude {
		if wire == protocolcontract.TextWireClaude {
			if blocks := interfaceSlice(message["reasoning_blocks"]); len(blocks) > 0 {
				converted["reasoning_blocks"] = blocks
			}
		}
		return
	}
	if wire == protocolcontract.TextWireChatCompletion {
		if reasoning := stringField(message, "reasoning_content"); reasoning != "" {
			converted["reasoning_content"] = reasoning
		}
	}
}

// claudeToolLoopMissingThinking 判断会话是否停在一轮没有 Claude 思考块的工具循环里：
// 最后一条 user 消息之后存在工具调用，且最近的工具调用消息没有同协议的思考块。
func claudeToolLoopMissingThinking(source *canonicalAgentRequest) bool {
	if source == nil {
		return false
	}
	for index := len(source.Messages) - 1; index >= 0; index-- {
		message := source.Messages[index]
		switch {
		case stringField(message, "role") == "user":
			return false
		case stringField(message, "type") == "function_call":
			return true
		case stringField(message, "role") == "assistant" && len(canonicalAgentToolCalls(message["tool_calls"])) > 0:
			return stringField(message, "reasoning_wire") != protocolcontract.TextWireClaude || len(interfaceSlice(message["reasoning_blocks"])) == 0
		}
	}
	return false
}

func writeThinkingDisabled(body map[string]interface{}, config *TextThinkingConfig, wire string) {
	switch config.Switch.Kind {
	case ThinkingSwitchEffortNone:
		if field, ok := thinkingEffortField(config.Intensity.Field, wire); ok {
			setThinkingField(body, field, "none")
		}
	case ThinkingSwitchEnableFlag:
		body["enable_thinking"] = false
	case ThinkingSwitchThinkingType:
		body["thinking"] = map[string]interface{}{"type": "disabled"}
	case ThinkingSwitchBudgetZero:
		if field, ok := thinkingBudgetField(config.Intensity.Field, wire); ok {
			setThinkingField(body, field, 0)
		}
	}
}
