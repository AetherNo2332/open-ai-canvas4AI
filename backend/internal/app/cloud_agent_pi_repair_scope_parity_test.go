package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestAgentParityPiRepairScopeKeepsEligibleCatalogAndRestrictsExecution(t *testing.T) {
	t.Run("eligible catalog survives repeated snapshot conflicts", func(t *testing.T) {
		s, db, run := piAgentTestLeasedFixture(t)
		declareTestChannelWindow(t, db, 64000, 8192)
		_, state := reloadPiRun(t, s, run.ID)
		call := parityCall("canvas_apply_ops", map[string]any{"snapshotHash": "stale", "ops": []any{map[string]any{"type": "add_node", "id": "note", "nodeType": "text"}}})
		err := cloudAgentFieldError("snapshotHash", "stale_snapshot", "canvas changed")
		for range 2 {
			cloudAgentTrackToolRepair(run.ID, state, call, map[string]any{}, err, map[string]any{})
		}
		if len(state.ToolScope) == 0 {
			t.Fatal("repeated snapshot conflicts did not activate repair scope")
		}
		saveCloudAgentCompactionState(t, s, run, state)
		view, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical})
		if err != nil || view.Decision != "model" {
			t.Fatalf("repair scope rejected the run's eligible catalog: view=%+v err=%v", view, err)
		}
		state.DisclosureVersion = cloudAgentToolDisclosureVersion
		state.AdvertisedToolNames = cloudAgentToolNames(state.Canonical.Tools)
		calls := []cloudAgentCall{
			parityCall("canvas_get_state", map[string]any{}),
			parityCall("canvas_arrange_nodes", map[string]any{}),
		}
		admission := cloudAgentPreflightBatch(state, calls)
		if !admission[0].Allowed || admission[1].Allowed || admission[1].Issue != cloudAgentAdmissionInvalidOutput {
			t.Fatalf("execution no longer enforces repair scope: %+v", admission)
		}
		var tasks, orders int64
		db.Model(&model.Task{}).Where("operation = ?", cloudAgentStepOperation).Count(&tasks)
		db.Model(&model.BillingOrder{}).Count(&orders)
		if tasks != 0 || orders != 0 {
			t.Fatalf("preflight created tasks=%d orders=%d", tasks, orders)
		}
	})
	for _, kind := range []string{"readonly", "child role", "unknown tool", "schema drift"} {
		t.Run(kind+" remains rejected", func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			declareTestChannelWindow(t, db, 64000, 8192)
			_, state := reloadPiRun(t, s, run.ID)
			state.ToolScope = []string{"canvas_apply_ops", "canvas_get_state"}
			request := state.Canonical
			switch kind {
			case "readonly":
				state.Request.PermissionMode = "read_only"
			case "child role":
				state.Subagent = &SubagentRuntime{LinkID: "child-link", ParentRunID: "parent", ChildRunID: run.ID, Depth: 1}
			case "unknown tool":
				request.Tools = []map[string]any{{"type": "function", "function": map[string]any{"name": "invented_tool", "parameters": map[string]any{"type": "object"}}}}
			case "schema drift":
				request.Tools = []map[string]any{{"type": "function", "function": map[string]any{"name": "canvas_get_state", "parameters": map[string]any{"type": "string"}}}}
			}
			saveCloudAgentCompactionState(t, s, run, state)
			if _, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: request}); err == nil {
				t.Fatalf("repair scope bypassed %s authorization", kind)
			}
		})
	}
}
