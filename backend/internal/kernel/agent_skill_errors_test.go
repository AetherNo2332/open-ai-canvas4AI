package kernel

import "testing"

func TestAgentSkillErrorConstructors(t *testing.T) {
	details := map[string]any{"budget": "context_bytes", "limit": int64(1024), "actual": int64(2048)}

	budget := AgentSkillBudgetExceeded(details)
	if budget.Status != 400 || budget.Code != CodeInvalidArgument {
		t.Fatalf("budget Status/Code = %d/%d, want 400/400", budget.Status, budget.Code)
	}
	if budget.Reason != ReasonAgentSkillBudgetExceeded {
		t.Fatalf("budget Reason = %q, want %q", budget.Reason, ReasonAgentSkillBudgetExceeded)
	}
	if budget.Message == "" {
		t.Fatal("budget Message must be user-safe and non-empty")
	}
	if budget.Details["limit"] != int64(1024) {
		t.Fatalf("budget Details not passthrough: %#v", budget.Details)
	}

	conflict := AgentSkillDefaultsConflict(7)
	if conflict.Status != CodeConflict || conflict.Code != CodeConflict {
		t.Fatalf("conflict Status/Code = %d/%d, want 409/409", conflict.Status, conflict.Code)
	}
	if conflict.Reason != ReasonAgentSkillDefaultsRevisionConflict {
		t.Fatalf("conflict Reason = %q, want %q", conflict.Reason, ReasonAgentSkillDefaultsRevisionConflict)
	}
	if conflict.Details["currentRevision"] != int64(7) {
		t.Fatalf("conflict Details[currentRevision] = %#v, want int64(7)", conflict.Details["currentRevision"])
	}

	invalid := AgentSkillDefaultsInvalid("引用了不存在或已禁用的技能", map[string]any{"skillId": "skill-1"})
	if invalid.Status != 400 || invalid.Code != CodeInvalidArgument {
		t.Fatalf("invalid Status/Code = %d/%d, want 400/400", invalid.Status, invalid.Code)
	}
	if invalid.Reason != ReasonAgentSkillDefaultsInvalid {
		t.Fatalf("invalid Reason = %q, want %q", invalid.Reason, ReasonAgentSkillDefaultsInvalid)
	}
	if invalid.Message != "引用了不存在或已禁用的技能" {
		t.Fatalf("invalid Message = %q", invalid.Message)
	}
	if invalid.Details["skillId"] != "skill-1" {
		t.Fatalf("invalid Details not passthrough: %#v", invalid.Details)
	}
}
