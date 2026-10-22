package app

import "infinite-canvas/backend/internal/platform"

type cloudAgentReadBudget struct {
	ReadCallsUsed      int `json:"readCallsUsed"`
	ReadCallsLimit     int `json:"readCallsLimit"`
	ReadCallsRemaining int `json:"readCallsRemaining"`
}

func (state *cloudAgentRuntime) readBudget() cloudAgentReadBudget {
	limit := state.callLimits().ReadCallsPerRun
	return cloudAgentReadBudget{ReadCallsUsed: state.ReadToolCalls, ReadCallsLimit: limit, ReadCallsRemaining: max(0, limit-state.ReadToolCalls)}
}

func (state *cloudAgentRuntime) withReadBudget(result any) any {
	fields, ok := result.(map[string]any)
	if !ok {
		return result
	}
	receipt := make(map[string]any, len(fields)+3)
	for key, value := range fields {
		receipt[key] = value
	}
	budget := state.readBudget()
	receipt["readCallsUsed"], receipt["readCallsLimit"], receipt["readCallsRemaining"] = budget.ReadCallsUsed, budget.ReadCallsLimit, budget.ReadCallsRemaining
	return receipt
}

type cloudAgentCallLimits struct {
	ToolCallsPerStep int `json:"toolCallsPerStep"`
	ReadCallsPerRun  int `json:"readCallsPerRun"`
	StepsPerRun      int `json:"stepsPerRun"`
}

func cloudAgentCallLimitsForPolicy(policy platform.RuntimeTaskPolicy) cloudAgentCallLimits {
	return cloudAgentCallLimits{
		ToolCallsPerStep: policy.AgentMaxToolCallsPerStep,
		ReadCallsPerRun:  policy.AgentMaxReadToolCallsPerRun,
		StepsPerRun:      policy.AgentMaxStepsPerRun,
	}
}

func (limits cloudAgentCallLimits) withDefaults() cloudAgentCallLimits {
	// Older checkpoints predate these fields; zero is not a configurable tool limit.
	if limits.ToolCallsPerStep == 0 {
		limits.ToolCallsPerStep = platform.DefaultRuntimeAgentToolCallsPerStep
	}
	if limits.ReadCallsPerRun == 0 {
		limits.ReadCallsPerRun = platform.DefaultRuntimeAgentReadCallsPerRun
	}
	return limits
}

func (limits cloudAgentCallLimits) stepLimit(req CloudAgentRequest) int {
	limit := cloudAgentStepLimit(req)
	if limits.StepsPerRun > 0 && (limit == 0 || limits.StepsPerRun < limit) {
		return limits.StepsPerRun
	}
	return limit
}

func (state *cloudAgentRuntime) callLimits() cloudAgentCallLimits {
	if state == nil {
		return (cloudAgentCallLimits{}).withDefaults()
	}
	return state.CallLimits.withDefaults()
}

func (state *cloudAgentRuntime) callLimitFacts() map[string]any {
	limits := state.callLimits()
	limits.StepsPerRun = limits.stepLimit(state.Request)
	return cloudAgentRuntimeMessage(cloudAgentRuntimeContext{
		Kind: cloudAgentRuntimeCallLimits, CallLimits: &limits, ReadToolCalls: state.ReadToolCalls,
	})
}
