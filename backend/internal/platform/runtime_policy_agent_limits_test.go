package platform

import "testing"

func TestAgentStepTimeoutEnvironmentBounds(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"-1", 0}, {"0", 0}, {"1", 30}, {"29", 30}, {"30", 30}, {"90", 90}, {"3601", 3600},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("CANVAS_AGENT_STEP_TIMEOUT_SECONDS", tc.value)
			if got := agentStepTimeoutSecondsDefault(); got != tc.want {
				t.Fatalf("timeout = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAgentCallLimitsPolicyBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*RuntimeTaskPolicy)
		valid bool
	}{
		{"tool-min", func(p *RuntimeTaskPolicy) { p.AgentMaxToolCallsPerStep = 1 }, true},
		{"tool-max", func(p *RuntimeTaskPolicy) { p.AgentMaxToolCallsPerStep = 64 }, true},
		{"tool-zero", func(p *RuntimeTaskPolicy) { p.AgentMaxToolCallsPerStep = 0 }, false},
		{"tool-over", func(p *RuntimeTaskPolicy) { p.AgentMaxToolCallsPerStep = 65 }, false},
		{"read-min", func(p *RuntimeTaskPolicy) { p.AgentMaxReadToolCallsPerRun = 1 }, true},
		{"read-max", func(p *RuntimeTaskPolicy) { p.AgentMaxReadToolCallsPerRun = 9999 }, true},
		{"read-zero", func(p *RuntimeTaskPolicy) { p.AgentMaxReadToolCallsPerRun = 0 }, false},
		{"read-over", func(p *RuntimeTaskPolicy) { p.AgentMaxReadToolCallsPerRun = 10000 }, false},
		{"steps-unlimited", func(p *RuntimeTaskPolicy) { p.AgentMaxStepsPerRun = 0 }, true},
		{"steps-max", func(p *RuntimeTaskPolicy) { p.AgentMaxStepsPerRun = 9999 }, true},
		{"steps-negative", func(p *RuntimeTaskPolicy) { p.AgentMaxStepsPerRun = -1 }, false},
		{"steps-over", func(p *RuntimeTaskPolicy) { p.AgentMaxStepsPerRun = 10000 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultRuntimePolicy()
			tc.set(&policy.Task)
			if err := ValidateRuntimePolicy(policy); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
