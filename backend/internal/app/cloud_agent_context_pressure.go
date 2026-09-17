package app

import (
	"encoding/json"
	"math"
	"unicode"
	"unicode/utf8"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
)

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
}

func cloudAgentContextPressurePayload(pressure cloudAgentContextPressure, state *cloudAgentRuntime) map[string]any {
	payload := map[string]any{
		"estimatedInputTokens": pressure.EstimatedInputTokens, "contextWindowTokens": pressure.ContextWindowTokens,
		"reservedOutputTokens": pressure.ReservedOutputTokens, "usableInputTokens": pressure.UsableInputTokens,
		"pressureRatio": pressure.PressureRatio, "sourceBytes": pressure.SourceBytes, "promptChars": pressure.PromptChars,
		"promptLimitChars": pressure.PromptLimitChars, "modelLimitConfigured": pressure.ModelLimitConfigured, "estimate": pressure.Estimate,
		"compactionThresholdBytes": agentcontext.ThresholdBytes, "historyMessageThreshold": agentcontext.ThresholdHistoryMessages,
	}
	if state == nil {
		return payload
	}
	raw, _ := json.Marshal(state.Canonical.Messages)
	historyMessages := len(state.TextHistory) + 2
	byteRatio := float64(len(raw)) / float64(agentcontext.ThresholdBytes)
	messageRatio := float64(historyMessages) / float64(agentcontext.ThresholdHistoryMessages)
	payload["compactionSourceBytes"] = len(raw)
	payload["historyMessages"] = historyMessages
	payload["compactionPressureRatio"] = math.Max(byteRatio, messageRatio)
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
	buckets := []map[string]any{
		{"key": "system", "label": "系统提示（含画布摘要）", "bytes": len(system), "tokens": estimateCloudAgentTokens(system)},
		{"key": "tools", "label": "工具 schema", "bytes": len(tools), "tokens": estimateCloudAgentTokens(tools)},
		{"key": "messages", "label": "会话消息（含工具结果）", "bytes": len(messages), "tokens": estimateCloudAgentTokens(messages)},
	}
	bucketBytes := len(system) + len(tools) + len(messages)
	breakdown := map[string]any{
		"totalBytes": len(whole), "totalTokens": estimateCloudAgentTokens(whole),
		"bucketBytes": bucketBytes, "bucketTokens": estimateCloudAgentTokens(system) + estimateCloudAgentTokens(tools) + estimateCloudAgentTokens(messages),
		"envelopeBytes": max(0, len(whole)-bucketBytes),
		"buckets":       buckets,
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

func (s *Service) cloudAgentContextPressure(task *model.Task, canonical canonicalAgentRequest, prompt string) cloudAgentContextPressure {
	raw, _ := json.Marshal(canonical)
	pressure := cloudAgentContextPressure{
		EstimatedInputTokens: estimateCloudAgentTokens(raw),
		SourceBytes:          len(raw),
		PromptChars:          utf8.RuneCountInString(prompt),
		Estimate:             true,
	}
	if task == nil || task.ChannelModelID == "" {
		return pressure
	}
	channelModel, err := s.repo.ChannelModel(task.ChannelModelID)
	if err != nil {
		return pressure
	}
	config, err := normalizedChannelModelCapability(channelModel)
	if err != nil || config == nil || config.Text == nil {
		return pressure
	}
	pressure.PromptLimitChars = config.Text.References.PromptMaxChars
	pressure.ContextWindowTokens = config.Text.ContextWindowTokens
	pressure.ReservedOutputTokens = config.Text.ReservedOutputTokens
	if pressure.ContextWindowTokens <= 0 {
		return pressure
	}
	pressure.ModelLimitConfigured = true
	pressure.UsableInputTokens = pressure.ContextWindowTokens - pressure.ReservedOutputTokens
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
