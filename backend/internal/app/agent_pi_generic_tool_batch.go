package app

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/kernel"
)

func piAdmitGenericToolBatchEnvelope(state *cloudAgentRuntime, batch PiToolBatchRequest, text string, expected, legacy []cloudAgentCall, stopReasonKind string) (string, error) {
	stopKind := strings.TrimSpace(stopReasonKind)
	if stopKind == "" {
		stopKind = cloudAgentStopKindUnknown
	}
	if cloudAgentStepStopDisposition(state, stopKind) != cloudAgentStepDispositionAccept {
		return "", kernel.Forbidden("模型终止原因不允许执行工具")
	}
	if len(expected) == 0 {
		expected = legacy
	}
	if !piSameCalls(expected, batch.Calls) {
		return "", kernel.Forbidden("Pi 工具批次与模型结果不一致")
	}
	seen := make(map[string]struct{}, len(batch.Calls))
	for _, call := range batch.Calls {
		if _, duplicate := seen[call.ID]; duplicate || strings.TrimSpace(call.ID) == "" {
			return "", BadAuthRequest("Pi 工具批次包含重复或空的调用 ID")
		}
		seen[call.ID] = struct{}{}
		if !genericPiToolFrozen(state.Execution.Tools, call.Function.Name) {
			return "", kernel.Forbidden("工具不在本次 Pi 执行合同中")
		}
		var arguments map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil || arguments == nil {
			return "", BadAuthRequest("Pi 工具参数必须是 JSON 对象")
		}
		if schema, advertised := cloudAgentAdvertisedTool(state, call.Function.Name); advertised {
			if err := validateCloudAgentToolArguments(schema, call.Function.Arguments); err != nil {
				return "", err
			}
		}
	}
	return text, nil
}
