package model

import "testing"

func TestAgentSchedulerPolicyDefaultsAndValidation(t *testing.T) {
	p := DefaultAgentSchedulerSetting()
	if p.DispatchConcurrency != 4 || p.MaxResidentSessions != 64 || p.MaxResidentPerCanvas != 16 {
		t.Fatalf("defaults: %+v", p)
	}
	if err := ValidateAgentSchedulerSetting(p); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []AgentSchedulerSetting{
		{DispatchConcurrency: 0, MaxResidentSessions: 64, MaxResidentPerCanvas: 16},
		{DispatchConcurrency: 17, MaxResidentSessions: 64, MaxResidentPerCanvas: 16},
		{DispatchConcurrency: 4, MaxResidentSessions: 3, MaxResidentPerCanvas: 3},
		{DispatchConcurrency: 4, MaxResidentSessions: 64, MaxResidentPerCanvas: 65},
	} {
		if ValidateAgentSchedulerSetting(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
