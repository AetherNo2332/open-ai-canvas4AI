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
// 前端弹窗（上下文窗口占用 / 占用分布）与运行诊断直接消费这份结构，不自己再算一遍。
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
	// CompactAtTokens 是上游既有的"可重读正文就地裁剪"触发线（输入预算的 85%，见
	// compactCloudAgentContext，上游 #601 新增到读数里）。它只是给读数一个参照刻度，
	// 不是本读数触发的动作。
	CompactAtTokens int `json:"compactAtTokens,omitempty"`
}

// cloudAgentProjectedInputTokens 合成"下一步输入 token"的权威读数：
// provider 锚点（模型自己的分词器读数）+ 本地估算的有符号增量；没有可用锚点时退回纯估算。
// 压缩触发与压力展示必须共用这一份口径，否则"界面显示 79%、后台却按 82% 压缩"。
// 增量带符号是刻意的（上游 #601）：上下文被裁剪（图片移出、正文卸载）后本地估算会变小，
// 投影也必须跟着变小，否则读数只会单调上涨。
func cloudAgentProjectedInputTokens(pressure cloudAgentContextPressure, state *cloudAgentRuntime) (int, string) {
	if state == nil || state.TokenAnchor == nil || !state.TokenAnchor.Accepted {
		return pressure.EstimatedInputTokens, "estimate"
	}
	anchor := state.TokenAnchor
	delta := pressure.EstimatedInputTokens - anchor.EstimatedTokens
	return max(0, int(anchor.InputTokens)+delta), "provider"
}

// cloudAgentContextPressurePayload 把读数摊成事件载荷。
//
// 两条硬规则（消费方不必猜）：
//  1. 两个量纲各自命名、互不覆盖：本地估算描述**下一次**请求（readingScope=next_request /
//     estimateMethod=local_v1），上游实测量的是**上一次**请求（providerMeasurementScope=
//     previous_request），两者不能画成同一条曲线。
//  2. 没有解析到模型自己声明的窗口时不下发窗口字段与占用率：宁可不给百分比，
//     也不要拿兜底默认窗口编一个看起来很像真的"已占用 80%"（与我方文档"窗口未声明（0）时
//     不下发这几个字段"一致）。
//
// actual 是"实际要发出去的那份信封"（上游 #601 新增的可选参数）：占用分布必须对着真实请求算，
// 准入改写之后的 canonical 与内存里的运行态副本可能不是同一份。
func cloudAgentContextPressurePayload(pressure cloudAgentContextPressure, state *cloudAgentRuntime, actual ...canonicalAgentRequest) map[string]any {
	payload := map[string]any{
		"estimatedInputTokens": pressure.EstimatedInputTokens, "sourceBytes": pressure.SourceBytes,
		"promptChars": pressure.PromptChars, "estimate": pressure.Estimate,
		// 字节/条数兜底判据永远下发（我方 fork 的前端据此画"哪条线在管事"）：上游只报 token 口径，
		// 但窗口未声明时判据会退回这三条，读数必须能说明退回了什么。
		"compactionThresholdBytes": agentcontext.ThresholdBytes, "historyMessageThreshold": agentcontext.ThresholdHistoryMessages,
		"requestHardLimitBytes": cloudAgentRequestHardLimitBytes,
		// provider usage measures the previous request; the estimate describes
		// the next request. Consumers must not draw them as one series.
		"readingScope": "next_request", "estimateMethod": "local_v1",
		// 快照版本与相位：读数属于"下一次请求发出之前"的测量，消费方不必猜。
		"schemaVersion": 2, "phase": "before_request",
	}
	if pressure.PromptLimitChars > 0 {
		payload["promptLimitChars"] = pressure.PromptLimitChars
	}
	payload["modelLimitConfigured"] = pressure.ModelLimitConfigured
	if pressure.ModelLimitConfigured {
		payload["contextWindowTokens"] = pressure.ContextWindowTokens
		payload["reservedOutputTokens"] = pressure.ReservedOutputTokens
		payload["usableInputTokens"] = pressure.UsableInputTokens
		payload["window"] = map[string]any{
			"contextWindowTokens": pressure.ContextWindowTokens, "reservedOutputTokens": pressure.ReservedOutputTokens,
			"usableInputTokens": pressure.UsableInputTokens, "overheadTokens": pressure.OverheadTokens,
			"source": firstNonEmpty(pressure.BudgetSource, "channel-model"),
		}
		if pressure.UsableInputTokens > 0 {
			payload["pressureRatio"] = pressure.PressureRatio
		}
		payload["budgetSource"] = firstNonEmpty(pressure.BudgetSource, "channel-model")
	} else if pressure.BudgetSource != "" {
		payload["budgetSource"] = pressure.BudgetSource
	}
	if pressure.OverheadTokens > 0 {
		payload["overheadTokens"] = pressure.OverheadTokens
	}
	if pressure.InputBudgetTokens > 0 {
		payload["inputBudgetTokens"] = pressure.InputBudgetTokens
	}
	if pressure.CompactAtTokens > 0 {
		payload["compactAtTokens"] = pressure.CompactAtTokens
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
			payload["providerUsage"] = map[string]any{
				"inputTokens": anchor.InputTokens, "cacheReadTokens": anchor.CachedTokens,
				"uncachedInputTokens": max(0, anchor.InputTokens-anchor.CachedTokens),
				"outputTokens":        anchor.OutputTokens,
			}
			// anchorDeltaTokens 的语义是"本地估算相对锚点那一步的增量"，与 projectedTokens 的分母无关，
			// 前端拿它解释"较锚点 +N"，不能改成投影减估算。
			delta := pressure.EstimatedInputTokens - anchor.EstimatedTokens
			payload["anchorDeltaTokens"] = delta
			if anchor.EstimatedTokens > 0 {
				payload["tokenScale"] = math.Round(float64(anchor.InputTokens)/float64(anchor.EstimatedTokens)*10000) / 10000
			}
			tokenSource = "provider"
		} else if anchor.RejectReason != "" {
			payload["anchorRejected"] = anchor.RejectReason
		}
	}
	payload["projectedTokens"] = projectedTokens
	payload["tokenSource"] = tokenSource
	// v2 命名（设计 §4）：两种量纲各自命名，前端不需要靠 tokenSource 猜。
	payload["measurementSource"] = tokenSource
	if tokenSource == "provider" {
		payload["normalizedInputTokens"] = state.TokenAnchor.InputTokens
	} else {
		payload["normalizedInputTokens"] = pressure.EstimatedInputTokens
	}
	payload["projectedNextInputTokens"] = projectedTokens
	if anchor := state.TokenAnchor; anchor != nil {
		payload["anchor"] = map[string]any{
			"id": anchor.TaskID, "valid": anchor.Accepted, "ageSteps": max(0, state.Step-anchor.Step),
		}
	}
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
	payload["breakdown"] = cloudAgentContextBreakdownPayload(state, actual...)
	return payload
}

// cloudAgentContextBreakdownPayload 报"这次要发出去的请求被谁占着"：三个 canonical 桶
// 加上编译进系统提示的各分段，用的是与占用读数同一把估算尺子。
// 桶合计与 canonical 总量的差是协议外壳（tool choice、缓存键），单独报而不摊进任何一个桶。
func cloudAgentContextBreakdownPayload(state *cloudAgentRuntime, actual ...canonicalAgentRequest) map[string]any {
	canonical := state.Canonical
	if len(actual) > 0 {
		// 上游 #601：优先用"实际发出去的那份信封"，运行态副本可能已被准入改写。
		canonical = actual[0]
	}
	system := []byte(canonical.SystemPrompt)
	tools, _ := json.Marshal(canonical.Tools)
	messages, _ := json.Marshal(canonical.Messages)
	whole, _ := json.Marshal(canonical)
	systemTokens, toolTokens, messageTokens := estimateCloudAgentTokens(system), estimateCloudAgentTokens(tools), estimateCloudAgentTokens(messages)
	// 构成永远由本地估算给出；若已有上游实测锚点，再按锚点比例给出一份"校准读数"，
	// 让展示口径与 provider 的计数同尺度（原值不覆盖）。
	scale := 1.0
	if anchor := state.TokenAnchor; anchor != nil && anchor.Accepted && anchor.EstimatedTokens > 0 {
		scale = float64(anchor.InputTokens) / float64(anchor.EstimatedTokens)
	}
	scaled := func(tokens int) int { return int(math.Round(float64(tokens) * scale)) }
	var systemSegments []cloudAgentContextSegment
	if segments := state.Policy.SystemSegments; len(segments) > 0 {
		// 分段必须把 system 桶填满：编译之外拼接的块（个人记忆）由
		// cloudAgentRecordMemorySegment 登记，剩余零头（标题、分隔与拼接文本）
		// 归入"其它"，否则弹窗里的分段合计会小于 system 桶，看起来像少算。
		systemSegments = append([]cloudAgentContextSegment{}, segments...)
		accountedBytes, accountedTokens := 0, 0
		for _, segment := range systemSegments {
			accountedBytes += segment.Bytes
			accountedTokens += segment.Tokens
		}
		if remainder := len(system) - accountedBytes; remainder > 0 {
			systemSegments = append(systemSegments, cloudAgentContextSegment{
				Key: "other", Label: "其它（标题与拼接）", Bytes: remainder,
				Tokens: max(0, estimateCloudAgentTokens(system)-accountedTokens),
			})
		}
		// 逐段向上取整不满足可加性（Σ⌈ascii/4⌉ ≥ ⌈Σascii/4⌉），所以 system 桶的 token
		// 口径直接取分段合计（上游 #601）：桶与分段是同一份分解，消费方会拿分段算占比，
		// 两边差几 token 就会显示成"分段占了 101%"。
		systemTokens = 0
		for index := range systemSegments {
			systemSegments[index].ScaledTokens = scaled(systemSegments[index].Tokens)
			systemTokens += systemSegments[index].Tokens
		}
	}
	buckets := []map[string]any{
		{"key": "system", "label": "系统提示（含画布摘要）", "bytes": len(system), "tokens": systemTokens, "scaledTokens": scaled(systemTokens)},
		{"key": "tools", "label": "工具 schema", "bytes": len(tools), "tokens": toolTokens, "scaledTokens": scaled(toolTokens)},
		{"key": "messages", "label": "会话消息（含工具结果）", "bytes": len(messages), "tokens": messageTokens, "scaledTokens": scaled(messageTokens)},
	}
	bucketBytes := len(system) + len(tools) + len(messages)
	breakdown := map[string]any{
		"totalBytes": len(whole), "totalTokens": estimateCloudAgentTokens(whole),
		"bucketBytes": bucketBytes, "bucketTokens": systemTokens + toolTokens + messageTokens,
		"envelopeBytes":     max(0, len(whole)-bucketBytes),
		"buckets":           buckets,
		"tokenScale":        math.Round(scale*10000) / 10000,
		"scaledTotalTokens": scaled(estimateCloudAgentTokens(whole)),
	}
	if len(systemSegments) > 0 {
		breakdown["systemSegments"] = systemSegments
	}
	return breakdown
}

// estimateCloudAgentTokens 是计量用的本地估算：ASCII 按 4 字节 1 token（向上取整），
// CJK 等非 ASCII 按 1 字符 1 token 计，数值上刻意保守（CJK 创作内容偏多）。
//
// 它与同文件的 cloudAgentEstimatedTokens 不是同一把尺子，也不要合并：后者是
// **请求准入**的判据（非 ASCII 按 1.5 token/字符，宁可高估也不能把装不下的请求放出去），
// 这里是**读数与锚点**的基准——锚点的采信区间（0.5×–2×）与 tokenScale 都是照着这把尺子
// 标定的，换尺子等于把标定作废。它只是压力展示，既不是计费 token 数，
// 也不能替代上游 tokenizer。
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
	pressure.CompactAtTokens = budget.CompactAtTokens
	pressure.BudgetSource = budget.Source
	pressure.UsableInputTokens = budget.InputBudgetTokens
	if pressure.UsableInputTokens > 0 {
		pressure.PressureRatio = math.Min(9.99, float64(pressure.EstimatedInputTokens)/float64(pressure.UsableInputTokens))
	}
	return pressure
}

// canonicalAgentRequestFromInput 从已落库的任务输入里取回"实际发出去的那份 canonical"：
// 压力读数与锚点都必须对着真实信封，而不是内存里另算一份可能与准入改写结果不同的副本。
func canonicalAgentRequestFromInput(input map[string]any) (canonicalAgentRequest, bool) {
	requests, ok := input["agentRequests"].(map[string]any)
	if !ok {
		return canonicalAgentRequest{}, false
	}
	// 上游 #601 的空值守卫：没有 canonical 时 json.Marshal(nil) 会解出一个全零信封并"成功"，
	// 那样读数就会挂在一份并不存在的请求上。
	if requests["canonical"] == nil {
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

// cloudAgentNoteContextWindowResolved 在"窗口从未确认变为已确认"时落一条 context_transition。
//
// 这一步只在读数口径变化时发生（例如早先拿不到模型能力、后来解析到了窗口）：真实上下文没有变小，
// 界面必须能把它标成"模型窗口已识别"，否则一识别就画成骤降（真机实测 94% → 2.08%）。
func cloudAgentNoteContextWindowResolved(runID string, state *cloudAgentRuntime, pressure cloudAgentContextPressure) {
	if state == nil || runID == "" || !pressure.ModelLimitConfigured || state.ContextWindowKnown {
		return
	}
	state.ContextWindowKnown = true
	state.event(runID, "context_transition", map[string]any{
		"kind": "window_resolved", "reason": "window_resolved",
		"after": map[string]any{
			"contextWindowTokens": pressure.ContextWindowTokens, "usableInputTokens": pressure.UsableInputTokens,
			"source": firstNonEmpty(pressure.BudgetSource, "channel-model"),
		},
		"text": "模型窗口已识别：读数改用窗口口径，这不是上下文变少",
	})
}
