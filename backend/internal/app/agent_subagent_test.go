package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestAgentSubagentPolicyPersistsUntilDisabled(t *testing.T) {
	s, db := workspaceService(t)
	if err := db.AutoMigrate(&model.AgentSubagentPolicy{}); err != nil {
		t.Fatal(err)
	}
	initial, err := s.AgentSubagentPolicy("user", "workspace-canvas")
	if err != nil || initial.Enabled || initial.Revision != 0 {
		t.Fatalf("default policy: %+v %v", initial, err)
	}
	enabled, err := s.UpdateAgentSubagentPolicy("user", AgentSubagentPolicyRequest{CanvasID: "workspace-canvas", Enabled: true, ExpectedRevision: initial.Revision})
	if err != nil || !enabled.Enabled || enabled.Revision != 1 {
		t.Fatalf("enable policy: %+v %v", enabled, err)
	}
	readBack, err := s.AgentSubagentPolicy("user", "workspace-canvas")
	if err != nil || !readBack.Enabled {
		t.Fatalf("policy did not persist: %+v %v", readBack, err)
	}
	if _, err = s.UpdateAgentSubagentPolicy("user", AgentSubagentPolicyRequest{CanvasID: "workspace-canvas", Enabled: false, ExpectedRevision: 0}); err == nil {
		t.Fatalf("stale revision accepted: %v", err)
	}
	disabled, err := s.UpdateAgentSubagentPolicy("user", AgentSubagentPolicyRequest{CanvasID: "workspace-canvas", Enabled: false, ExpectedRevision: 1})
	if err != nil || disabled.Enabled || disabled.Revision != 2 {
		t.Fatalf("disable policy: %+v %v", disabled, err)
	}
	if _, err = s.AgentSubagentPolicy("other", "workspace-canvas"); err != nil {
		t.Fatalf("policy isolation read should create separate row: %v", err)
	}
}
