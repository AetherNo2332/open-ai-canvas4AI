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
	return payload
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
