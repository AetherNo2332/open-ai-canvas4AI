package commands

import "testing"

func TestCanvasSettingsAndConnectionOrderUseSharedCommandHistory(t *testing.T) {
	d := fixture(t, `{"id":"canvas","title":"canvas","appearance":{"mode":"light","extension":"keep"},"nodes":[{"id":"x","type":"text","metadata":{}},{"id":"y","type":"text","metadata":{}},{"id":"z","type":"text","metadata":{}}],"connections":[{"id":"a","fromNodeId":"x","toNodeId":"y"},{"id":"b","fromNodeId":"x","toNodeId":"z"}],"extension":"keep"}`)
	plan, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"appearance": map[string]any{"mode": "dark"}, "backgroundMode": "dots", "showImageInfo": true}}, Operation{Type: "reorder_connections", ID: "canvas", NodeIDs: []string{"b", "a"}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Document["appearance"].(map[string]any)["extension"] != "keep" || plan.Document["extension"] != "keep" || maps(plan.Document["connections"])[0]["id"] != "b" {
		t.Fatal("settings dropped extension or edge ordering")
	}
	if !plan.ReadSet["project:appearance"] || !plan.ReadSet["project:connectionOrder"] || !plan.ReadSet["connection:a"] {
		t.Fatal("missing entity preconditions")
	}
	plan.Document["appearance"].(map[string]any)["custom"] = map[string]any{"baseTheme": "light", "gridColor": "#fff", "extension": "keep"}
	partial, err := Plan(plan.Document, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"appearance": map[string]any{"custom": map[string]any{"baseTheme": "dark"}}}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	custom := partial.Document["appearance"].(map[string]any)["custom"].(map[string]any)
	if custom["gridColor"] != "#fff" || custom["extension"] != "keep" {
		t.Fatal("partial custom appearance lost existing fields")
	}
	original, err := ApplyChanges(plan.Document, plan.Inverse)
	if err != nil || original["appearance"].(map[string]any)["mode"] != "light" {
		t.Fatal("undo failed")
	}
	for _, patch := range []map[string]any{{"viewport": map[string]any{"x": 0}}, {"_deleted": true}, {"showImageInfo": "yes"}, {"backgroundMode": "unsafe"}, {"appearance": map[string]any{"mode": "custom", "custom": map[string]any{"backgroundColor": "url(http://private)"}}}} {
		if _, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: patch}), ownerContext()); err == nil {
			t.Fatalf("invalid settings accepted: %+v", patch)
		}
	}
}
