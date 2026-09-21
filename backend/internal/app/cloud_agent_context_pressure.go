package app

import (
	"encoding/json"
	"math"
	"time"
	"unicode"
	"unicode/utf8"

	"infinite-canvas/backend/internal/agentcontext"
)

// cloudAgentContextPressure 是"这次请求还能装多少输入"的读数。
// 前端弹窗（上下文窗口占用 / 占用分布）直接消费这份结构。
type cloudAgentContextPressure struct {
	EstimatedInputTokens     int     `json:"estimatedInputTokens"`
	ContextWindowTokens      int     `json:"contextWindowTokens"`
	ReservedOutputTokens     int     `json:"reservedOutputTokens"`
	UsableInputTokens        int     `json:"usableInputTokens"`
	PressureRatio            float64 `json:"pressureRatio"`
	SourceBytes              int     `json:"sourceBytes"`
	PromptChars              int     `json:"promptChars"`
	PromptLimitChars         int     `json:"promptLimitChars"`
	ModelLimitConfigured     bool    `json:"modelLimitConfigured"`
	Estimate                 bool    `json:"estimate"`
	CompactionSourceBytes    int     `json:"compactionSourceBytes"`
	CompactionThresholdBytes int     `json:"compactionThresholdBytes"`
	HistoryMessages          int     `json:"historyMessages"`
	HistoryMessageThreshold  int     `json:"historyMessageThreshold"`
	CompactionPressureRatio  float64 `json:"compactionPressureRatio"`
	// 三个口径字段说明"这条线是按哪个算式算出来的"：overhead 是工具 schema / 协议包装 /
	// 兜底轮的比例预留，InputBudgetTokens 才是真正可供输入使用的额度。
	OverheadTokens    int    `json:"overheadTokens,omitempty"`
	InputBudgetTokens int    `json:"inputBudgetTokens,omitempty"`
	BudgetSource      string `json:"budgetSource,omitempty"`
}

// cloudAgentProjectedInputTokens 合成"下一步输入 token"的权威读数：
// provider 锚点（模型自己的分词器读数）+ 本地估算的有符号增量；没有可用锚点时退回纯估算。
// 压缩触发与压力展示必须共用这一份口径，否则"界面显示 79%、后台却按 82% 压缩"。
func cloudAgentProjectedInputTokens(pressure cloudAgentContextPressure, state *cloudAgentRuntime) (int, string) {
	if state == nil || state.TokenAnchor == nil || !state.TokenAnchor.Accepted {
		return pressure.EstimatedInputTokens, "estimate"
	}
	anchor := state.TokenAnchor
	delta := pressure.EstimatedInputTokens - anchor.EstimatedTokens
	return max(0, int(anchor.InputTokens)+delta), "provider"
}

func cloudAgentContextPressurePayload(pressure cloudAgentContextPressure, state *cloudAgentRuntime) map[string]any {
	payload := map[string]any{
		"estimatedInputTokens": pressure.EstimatedInputTokens, "contextWindowTokens": pressure.ContextWindowTokens,
		"reservedOutputTokens": pressure.ReservedOutputTokens, "usableInputTokens": pressure.UsableInputTokens,
		"pressureRatio": pressure.PressureRatio, "sourceBytes": pressure.SourceBytes, "promptChars": pressure.PromptChars,
		"promptLimitChars": pressure.PromptLimitChars, "modelLimitConfigured": pressure.ModelLimitConfigured, "estimate": pressure.Estimate,
		"compactionThresholdBytes": agentcontext.ThresholdBytes, "historyMessageThreshold": agentcontext.ThresholdHistoryMessages,
		// 字节判据只在"没声明窗口"时兜底，token 口径才是主判据（前端与文档据此对齐"哪条线在管事"）。
		"requestHardLimitBytes": cloudAgentRequestHardLimitBytes,
		// provider usage measures the previous request; the estimate describes
		// the next request. Consumers must not draw them as one series.
		"readingScope": "next_request", "estimateMethod": "local_v1",
	}
	if pressure.OverheadTokens > 0 {
		payload["overheadTokens"] = pressure.OverheadTokens
	}
	if pressure.InputBudgetTokens > 0 {
		payload["inputBudgetTokens"] = pressure.InputBudgetTokens
	}
	if pressure.BudgetSource != "" {
		payload["budgetSource"] = pressure.BudgetSource
	}
	if state == nil {
		return payload
	}
	// 单步边界（输出上限与墙钟）也要能一眼看出"哪条线在管这一步"：管理员改了策略、
	// 或者把上限配成 0（不限制）时，界面与排查都不该靠猜。
	if state.StepLimits.OutputTokens > 0 {
		payload["stepMaxOutputTokens"] = state.StepLimits.OutputTokens
	}
	if state.StepLimits.Timeout > 0 {
		payload["stepTimeoutSeconds"] = int(state.StepLimits.Timeout / time.Second)
	}
	// 上游实测锚点：provider 用模型自己的分词器报出的 prompt 规模，是权威读数。
	// 投影 = 锚点 + 本地估算的有符号增量（对齐前端 pressureTokens / projectedTokens）。
	projectedTokens, tokenSource := cloudAgentProjectedInputTokens(pressure, state)
	if anchor := state.TokenAnchor; anchor != nil {
		payload["anchorStep"] = anchor.Step
		if anchor.Accepted {
			payload["pressureTokens"] = anchor.InputTokens
			payload["tokenUsage"] = map[string]any{
				"inputTokens": anchor.InputTokens, "cachedInputTokens": anchor.CachedTokens,
				"uncachedInputTokens": max(0, anchor.InputTokens-anchor.CachedTokens), "outputTokens": anchor.OutputTokens,
			}
			payload["providerMeasurementScope"] = "previous_request"
			// anchorDeltaTokens 的语义是"本地估算相对锚点那一步的增量"，与 projectedTokens 的分母无关，
			// 前端拿它解释"较锚点 +N"，不能改成投影减估算。
			delta := pressure.EstimatedInputTokens - anchor.EstimatedTokens
			payload["anchorDeltaTokens"] = delta
			payload["tokenScale"] = math.Round(float64(anchor.InputTokens)/float64(anchor.EstimatedTokens)*10000) / 10000
			tokenSource = "provider"
		} else if anchor.RejectReason != "" {
			payload["anchorRejected"] = anchor.RejectReason
		}
	}
	payload["projectedTokens"] = projectedTokens
	payload["tokenSource"] = tokenSource
	if scale, ok := payload["tokenScale"].(float64); ok && scale > 0 && pressure.UsableInputTokens > 0 {
		payload["projectedPressureRatio"] = math.Min(9.99, float64(projectedTokens)/float64(pressure.UsableInputTokens))
	}
	raw, _ := json.Marshal(state.Canonical.Messages)
	// 条数口径必须数"当前会话"，不是压缩后残留的 TextHistory：后者最多 7 条，
	// 让"≥16 条"这条规则永远是死的（实测 38 条消息的会话也照样不触发）。
	historyMessages := len(state.Canonical.Messages)
	byteRatio := float64(len(raw)) / float64(agentcontext.ThresholdBytes)
	messageRatio := float64(historyMessages) / float64(agentcontext.ThresholdHistoryMessages)
	payload["compactionSourceBytes"] = len(raw)
	payload["historyMessages"] = historyMessages
	payload["compactionThresholdRatio"] = cloudAgentCompactionRatio
	payload["compactionTokenSource"] = tokenSource
	payload["compactionThresholdBytes"] = agentcontext.ThresholdBytes
	if pressure.UsableInputTokens > 0 {
		// 主判据：token 利用率（上游实测投影 ÷ 本次请求真实可用的输入预算）。
		// 字节/条数只在没有配置模型上限时兜底，两者不能各说各话。
		payload["compactionBasis"] = "tokens"
		payload["compactionPressureRatio"] = math.Min(9.99, float64(projectedTokens)/float64(pressure.UsableInputTokens))
	} else {
		payload["compactionBasis"] = "bytes"
		payload["compactionPressureRatio"] = math.Max(byteRatio, messageRatio)
	}
	payload["breakdown"] = cloudAgentContextBreakdownPayload(state)
	return payload
}

// cloudAgentContextBreakdownPayload reports what occupies the request that is
// about to be sent: the three canonical buckets plus the compiled system-prompt
// segments, in the same estimate the occupancy figure uses. Buckets and the
// canonical total differ by the envelope (tool choice, cache key), which is
// reported separately rather than folded into a bucket.
func cloudAgentContextBreakdownPayload(state *cloudAgentRuntime) map[string]any {
	canonical := state.Canonical
	system := []byte(canonical.SystemPrompt)
	tools, _ := json.Marshal(canonical.Tools)
	messages, _ := json.Marshal(canonical.Messages)
	whole, _ := json.Marshal(canonical)
	// 构成永远由本地估算给出；若已有上游实测锚点，再按锚点比例给出一份"校准读数"，
	// 让展示口径与 provider 的计数同尺度。
	scale := 1.0
	if anchor := state.TokenAnchor; anchor != nil && anchor.Accepted && anchor.EstimatedTokens > 0 {
		scale = float64(anchor.InputTokens) / float64(anchor.EstimatedTokens)
	}
	scaled := func(tokens int) int { return int(math.Round(float64(tokens) * scale)) }
	buckets := []map[string]any{
		{"key": "system", "label": "系统提示（含画布摘要）", "bytes": len(system), "tokens": estimateCloudAgentTokens(system), "scaledTokens": scaled(estimateCloudAgentTokens(system))},
		{"key": "tools", "label": "工具 schema", "bytes": len(tools), "tokens": estimateCloudAgentTokens(tools), "scaledTokens": scaled(estimateCloudAgentTokens(tools))},
		{"key": "messages", "label": "会话消息（含工具结果）", "bytes": len(messages), "tokens": estimateCloudAgentTokens(messages), "scaledTokens": scaled(estimateCloudAgentTokens(messages))},
	}
	bucketBytes := len(system) + len(tools) + len(messages)
	breakdown := map[string]any{
		"totalBytes": len(whole), "totalTokens": estimateCloudAgentTokens(whole),
		"bucketBytes": bucketBytes, "bucketTokens": estimateCloudAgentTokens(system) + estimateCloudAgentTokens(tools) + estimateCloudAgentTokens(messages),
		"envelopeBytes":     max(0, len(whole)-bucketBytes),
		"buckets":           buckets,
		"tokenScale":        math.Round(scale*10000) / 10000,
		"scaledTotalTokens": scaled(estimateCloudAgentTokens(whole)),
	}
	if segments := state.Policy.SystemSegments; len(segments) > 0 {
		// 分段必须把 system 桶填满：编译之外拼接的块（个人记忆）由
		// cloudAgentRecordMemorySegment 登记，剩余零头（标题、分隔与拼接文本）
		// 归入"其它"，否则弹窗里的分段合计会小于 system 桶，看起来像少算。
		reported := append([]cloudAgentContextSegment{}, segments...)
		accountedBytes, accountedTokens := 0, 0
		for _, segment := range reported {
			accountedBytes += segment.Bytes
			accountedTokens += segment.Tokens
		}
		if remainder := len(system) - accountedBytes; remainder > 0 {
			reported = append(reported, cloudAgentContextSegment{
				Key: "other", Label: "其它（标题与拼接）", Bytes: remainder,
				Tokens: max(0, estimateCloudAgentTokens(system)-accountedTokens),
			})
		}
		for index := range reported {
			reported[index].ScaledTokens = scaled(reported[index].Tokens)
		}
		breakdown["systemSegments"] = reported
	}
	return breakdown
}

// estimateCloudAgentTokens is provider-neutral and deliberately conservative
// for CJK-heavy creative work. It is a UI pressure estimate, never a billing
// token count and never a replacement for provider tokenizers.
func estimateCloudAgentTokens(value []byte) int {
	if len(value) == 0 {
		return 0
	}
	asciiUnits, nonASCII := 0, 0
	for len(value) > 0 {
		r, size := utf8.DecodeRune(value)
		if r == utf8.RuneError && size == 1 {
			asciiUnits++
			value = value[1:]
			continue
		}
		value = value[size:]
		if r <= unicode.MaxASCII {
			asciiUnits++
		} else {
			nonASCII++
		}
	}
	return int(math.Ceil(float64(asciiUnits)/4.0)) + nonASCII
}

// cloudAgentContextPressure 解析"这次请求能用多少输入"。
//
// 展示口径与压缩判据共用同一份预算（含 overhead），否则会出现"界面 79%、后台按 82% 压缩"。
// 没有解析到真实窗口时 UsableInputTokens 保持 0 且 ModelLimitConfigured=false，
// 前端据此显示"未配置模型上限：判据退回字节/条数兜底"而不是编一个占用率。
func (s *Service) cloudAgentContextPressure(canonical canonicalAgentRequest, prompt string, request CloudAgentRequest) cloudAgentContextPressure {
	raw, _ := json.Marshal(canonical)
	pressure := cloudAgentContextPressure{
		EstimatedInputTokens: estimateCloudAgentTokens(raw),
		SourceBytes:          len(raw),
		PromptChars:          utf8.RuneCountInString(prompt),
		Estimate:             true,
	}
	// 提示词字符上限只来自渠道模型能力（逻辑模型没有单一渠道模型，保持 0 = 不限制）。
	if text := s.cloudAgentChannelTextCapability(request); text != nil {
		pressure.PromptLimitChars = text.References.PromptMaxChars
	}
	budget, ok := s.cloudAgentResolvedContextBudget(request)
	if !ok {
		return pressure
	}
	pressure.ModelLimitConfigured = true
	pressure.ContextWindowTokens = budget.ContextWindowTokens
	pressure.ReservedOutputTokens = budget.MaxOutputTokens
	pressure.OverheadTokens = budget.OverheadTokens
	pressure.InputBudgetTokens = budget.InputBudgetTokens
	pressure.BudgetSource = budget.Source
	pressure.UsableInputTokens = budget.InputBudgetTokens
	if pressure.UsableInputTokens > 0 {
		pressure.PressureRatio = math.Min(9.99, float64(pressure.EstimatedInputTokens)/float64(pressure.UsableInputTokens))
	}
	return pressure
}

func canonicalAgentRequestFromInput(input map[string]any) (canonicalAgentRequest, bool) {
	requests, ok := input["agentRequests"].(map[string]any)
	if !ok {
		return canonicalAgentRequest{}, false
	}
	raw, err := json.Marshal(requests["canonical"])
	if err != nil {
		return canonicalAgentRequest{}, false
	}
	var canonical canonicalAgentRequest
	if json.Unmarshal(raw, &canonical) != nil {
		return canonicalAgentRequest{}, false
	}
	return canonical, true
}
