package app

import "infinite-canvas/backend/internal/platform"

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
