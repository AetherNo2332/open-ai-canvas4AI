package app

import (
	"slices"
	"testing"

	"yingce/backend/internal/model"
)

func TestAgentDefinitionRegistry(t *testing.T) {
	baseline := AgentDefinition{ID: "canvas.text-only", Version: 1, BusinessType: "canvas", IsDefault: true, BusinessTools: []string{}}
	for _, item := range []struct {
		name   string
		change func(AgentDefinition) []AgentDefinition
	}{
		{"missing default", func(def AgentDefinition) []AgentDefinition {
			def.IsDefault = false
			return []AgentDefinition{def}
		}},
		{"missing tools declaration", func(def AgentDefinition) []AgentDefinition {
			def.BusinessTools = nil
			return []AgentDefinition{def}
		}},
		{"unknown tool", func(def AgentDefinition) []AgentDefinition {
			def.BusinessTools = []string{"invented_tool"}
			return []AgentDefinition{def}
		}},
		{"duplicate default", func(def AgentDefinition) []AgentDefinition {
			second := def
			second.ID = "canvas.second"
			return []AgentDefinition{def, second}
		}},
		{"duplicate tools", func(def AgentDefinition) []AgentDefinition {
			def.BusinessTools = []string{"task_get", "task_get"}
			return []AgentDefinition{def}
		}},
		{"foreign read adapter", func(def AgentDefinition) []AgentDefinition {
			def.ReadAdapters = []string{"drama:episode"}
			return []AgentDefinition{def}
		}},
		{"foreign business tool", func(def AgentDefinition) []AgentDefinition {
			def.BusinessType = "drama"
			def.BusinessTools = []string{"canvas_apply_ops"}
			return []AgentDefinition{def}
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := newAgentDefinitionRegistry(item.change(baseline)); err == nil {
				t.Fatal("invalid Agent registration succeeded")
			}
		})
	}
	s, _ := agentRunFixture(t)
	registry, err := newAgentDefinitionRegistry([]AgentDefinition{baseline})
	if err != nil {
		t.Fatal(err)
	}
	s.agentDefinitions = registry
	req := agentTestRequest()
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ClaimPiAgent("worker-a")
	if err != nil || snapshot == nil || snapshot.RunID != run.ID {
		t.Fatalf("text-only Agent could not be claimed: %+v %v", snapshot, err)
	}
	var names []string
	for _, tool := range snapshot.Tools {
		if tool.Allowed {
			names = append(names, tool.Name)
		}
	}
	if !slices.Equal(names, []string{"plan_update", "finish_run"}) {
		t.Fatalf("text-only Agent inherited business tools: %v", names)
	}
	req.AgentDefinitionID = "drama.analysis"
	req.IdempotencyKey = "invalid-agent-definition"
	if _, err := s.CreateCloudAgentRun("user", req, ""); err == nil {
		t.Fatal("accepted an unregistered or foreign definition")
	}
}

func TestAgentToolAdmission(t *testing.T) {
	s, db := agentRunFixture(t)
	req := agentTestRequest()
	req.ContextScope = []string{"canvas"}
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	execution, state := reloadPiRun(t, s, run.ID)
	if state.Capabilities.DefinitionID != "canvas.default" || state.Capabilities.DefinitionVersion != 1 {
		t.Fatalf("missing frozen definition identity: %+v", state.Capabilities)
	}
	if cloudAgentRunAllowsTool(state, "canvas_apply_ops") || cloudAgentRunAllowsTool(state, "read_foreign_project") {
		t.Fatal("read-only or unregistered call escaped the frozen catalog")
	}
	state.Request.Scope = &AgentBusinessScope{Type: "drama", ID: "agent-canvas"}
	if err := s.authorizeAgentRunTool(execution, state, "task_get"); err == nil {
		t.Fatal("tool accepted a different scope from the admitted run")
	}
	state.Request.Scope = nil
	v2 := AgentDefinition{ID: "canvas.default", Version: 2, BusinessType: "canvas", IsDefault: true, BusinessTools: []string{}}
	registry, err := newAgentDefinitionRegistry([]AgentDefinition{v2})
	if err != nil {
		t.Fatal(err)
	}
	s.agentDefinitions = registry
	if err := s.authorizeAgentRunTool(execution, state, "task_get"); err == nil {
		t.Fatal("the removed version silently executed under the new definition")
	}
	s.agentDefinitions = nil
	if err := s.authorizeAgentRunTool(execution, state, "task_get"); err != nil {
		t.Fatalf("the exact original definition cannot execute: %v", err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", req.CanvasID).Update("user_id", "other-user").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.authorizeAgentRunTool(execution, state, "task_get"); err == nil {
		t.Fatal("tool continued after the user lost access to the object")
	}
}
