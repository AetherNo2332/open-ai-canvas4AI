package commands

import (
	"math"
	"reflect"
	"testing"
)

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

func TestCanvasSettingsAcceptPresetAndThemeMemory(t *testing.T) {
	d := fixture(t, `{"id":"canvas","appearance":{"mode":"dark","extension":"keep"},"nodes":[],"connections":[]}`)
	patch := fixture(t, `{"appearance":{"mode":"preset","preset":{"id":"dark","values":{"baseTheme":"dark","backgroundColor":"#000000","backgroundBrightness":0,"gridColor":"#FFFFFF","gridOpacity":32}},"themeMemory":{"dark":{"mode":"preset","preset":{"id":"dark","values":{"baseTheme":"dark","backgroundColor":"#000000","backgroundBrightness":0,"gridColor":"#FFFFFF","gridOpacity":32}}}}}}`)
	plan, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any(patch)}), ownerContext())
	if err != nil {
		t.Fatalf("current default preset cannot be saved: %v", err)
	}
	want := fixture(t, `{"mode":"preset","extension":"keep","preset":{"id":"dark","values":{"baseTheme":"dark","backgroundColor":"#000000","backgroundBrightness":0,"gridColor":"#FFFFFF","gridOpacity":32}},"themeMemory":{"dark":{"mode":"preset","preset":{"id":"dark","values":{"baseTheme":"dark","backgroundColor":"#000000","backgroundBrightness":0,"gridColor":"#FFFFFF","gridOpacity":32}}}}}`)
	if !reflect.DeepEqual(plan.Document["appearance"], map[string]any(want)) {
		t.Fatalf("preset or theme memory changed: %#v", plan.Document["appearance"])
	}
	undo, err := ApplyChanges(plan.Document, plan.Inverse)
	if err != nil || !reflect.DeepEqual(undo, d) {
		t.Fatalf("preset undo did not restore the prior appearance: %v, %#v", err, undo)
	}
}

func TestCanvasSettingsPartiallyMergePresetAndRememberedFineTuning(t *testing.T) {
	d := fixture(t, `{"id":"canvas","appearance":{"mode":"preset","preset":{"id":"mint","extension":"preset-kept","values":{"baseTheme":"light","backgroundColor":"#DFE9E6","backgroundBrightness":0,"gridColor":"#000000","gridOpacity":23,"extension":"values-kept"}},"themeMemory":{"light":{"mode":"preset","preset":{"id":"mint","values":{"baseTheme":"light","backgroundColor":"#DFE9E6","backgroundBrightness":0,"gridColor":"#000000","gridOpacity":23}}},"dark":{"mode":"custom","custom":{"baseTheme":"dark","backgroundColor":"#101418","backgroundBrightness":0,"gridColor":"#CFD8E3","gridOpacity":30,"extension":"custom-kept"},"extension":"selection-kept"},"extension":"memory-kept"},"extension":"appearance-kept"},"nodes":[],"connections":[]}`)
	patch := fixture(t, `{"appearance":{"preset":{"values":{"gridOpacity":18}},"themeMemory":{"dark":{"custom":{"backgroundBrightness":-4}}}}}`)
	plan, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any(patch)}), ownerContext())
	if err != nil {
		t.Fatalf("fine tuning patch was rejected: %v", err)
	}
	want := fixture(t, `{"mode":"preset","preset":{"id":"mint","extension":"preset-kept","values":{"baseTheme":"light","backgroundColor":"#DFE9E6","backgroundBrightness":0,"gridColor":"#000000","gridOpacity":18,"extension":"values-kept"}},"themeMemory":{"light":{"mode":"preset","preset":{"id":"mint","values":{"baseTheme":"light","backgroundColor":"#DFE9E6","backgroundBrightness":0,"gridColor":"#000000","gridOpacity":23}}},"dark":{"mode":"custom","custom":{"baseTheme":"dark","backgroundColor":"#101418","backgroundBrightness":-4,"gridColor":"#CFD8E3","gridOpacity":30,"extension":"custom-kept"},"extension":"selection-kept"},"extension":"memory-kept"},"extension":"appearance-kept"}`)
	if !reflect.DeepEqual(plan.Document["appearance"], map[string]any(want)) {
		t.Fatalf("partial tuning lost existing values or extensions: %#v", plan.Document["appearance"])
	}
	if reflect.DeepEqual(d["appearance"], plan.Document["appearance"]) {
		t.Fatal("tuning did not change the appearance")
	}
}

func TestCanvasSettingsClearOptionalAppearanceFields(t *testing.T) {
	for _, tt := range []struct {
		name, before, patch, want string
	}{
		{"absent custom", `{"mode":"light"}`, `{"custom":null}`, `{"mode":"light"}`},
		{"custom", `{"mode":"light","custom":{"baseTheme":"dark"}}`, `{"custom":null}`, `{"mode":"light"}`},
		{"preset", `{"mode":"light","preset":{"id":"dark","values":{"baseTheme":"dark"}}}`, `{"preset":null}`, `{"mode":"light"}`},
		{"theme memory", `{"mode":"light","themeMemory":{"dark":{"mode":"custom","custom":{"baseTheme":"dark"}}}}`, `{"themeMemory":null}`, `{"mode":"light"}`},
		{"remembered tone", `{"mode":"light","themeMemory":{"light":{"mode":"custom","custom":{"baseTheme":"light"}},"dark":{"mode":"custom","custom":{"baseTheme":"dark"}}}}`, `{"themeMemory":{"dark":null}}`, `{"mode":"light","themeMemory":{"light":{"mode":"custom","custom":{"baseTheme":"light"}}}}`},
		{"remembered custom", `{"mode":"light","themeMemory":{"dark":{"mode":"preset","preset":{"id":"dark","values":{"baseTheme":"dark"}},"custom":{"baseTheme":"dark"}}}}`, `{"themeMemory":{"dark":{"custom":null}}}`, `{"mode":"light","themeMemory":{"dark":{"mode":"preset","preset":{"id":"dark","values":{"baseTheme":"dark"}}}}}`},
		{"remembered preset", `{"mode":"light","themeMemory":{"dark":{"mode":"custom","preset":{"id":"dark","values":{"baseTheme":"dark"}},"custom":{"baseTheme":"dark"}}}}`, `{"themeMemory":{"dark":{"preset":null}}}`, `{"mode":"light","themeMemory":{"dark":{"mode":"custom","custom":{"baseTheme":"dark"}}}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := fixture(t, `{"id":"canvas","appearance":`+tt.before+`,"nodes":[],"connections":[]}`)
			patch := fixture(t, tt.patch)
			plan, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"appearance": map[string]any(patch)}}), ownerContext())
			if err != nil {
				t.Fatalf("optional appearance field could not be cleared: %v", err)
			}
			if want := fixture(t, tt.want); !reflect.DeepEqual(plan.Document["appearance"], map[string]any(want)) {
				t.Fatalf("optional field was retained or stored as null: %#v", plan.Document["appearance"])
			}
			undo, err := ApplyChanges(plan.Document, plan.Inverse)
			if err != nil || !reflect.DeepEqual(undo, d) {
				t.Fatalf("clear undo did not restore the prior appearance: %v, %#v", err, undo)
			}
		})
	}
}

func TestCanvasSettingsAcceptAppearanceColorAndNumericBoundaries(t *testing.T) {
	d := fixture(t, `{"id":"canvas","appearance":{"mode":"dark"},"nodes":[],"connections":[]}`)
	for _, raw := range []string{
		`{"mode":"custom","custom":{"baseTheme":"light","backgroundColor":"abc","backgroundBrightness":-30,"gridColor":"#123456","gridOpacity":0}}`,
		`{"mode":"preset","preset":{"id":"slate","values":{"baseTheme":"dark","backgroundColor":"#ABCDEF","backgroundBrightness":30,"gridColor":"#fff","gridOpacity":100}}}`,
		`{"themeMemory":{"light":{"mode":"custom","custom":{"baseTheme":"light","backgroundColor":"#A1b2C3","backgroundBrightness":-0.5,"gridColor":"000","gridOpacity":0.5}},"dark":{"mode":"preset","preset":{"id":"dark","values":{"baseTheme":"dark","backgroundColor":"#000000","backgroundBrightness":0,"gridColor":"#FFFFFF","gridOpacity":32}}}}}`,
	} {
		patch := fixture(t, raw)
		if _, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"appearance": map[string]any(patch)}}), ownerContext()); err != nil {
			t.Errorf("valid color or numeric boundary rejected: %s: %v", raw, err)
		}
	}
}

func TestCanvasSettingsValidateAppearancePatchFields(t *testing.T) {
	d := fixture(t, `{"id":"canvas","appearance":{"mode":"dark"},"nodes":[],"connections":[]}`)
	for _, id := range []string{"light", "pink", "sand", "mint", "dark", "slate"} {
		if _, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"appearance": map[string]any{"preset": map[string]any{"id": id}}}}), ownerContext()); err != nil {
			t.Errorf("known preset %s rejected: %v", id, err)
		}
	}
	for _, tt := range []struct {
		name       string
		appearance any
	}{
		{"unknown appearance field", map[string]any{"extension": true}},
		{"unknown mode", map[string]any{"mode": "auto"}},
		{"null mode", map[string]any{"mode": nil}},
		{"null appearance", nil},
		{"invalid preset type", map[string]any{"preset": []any{}}},
		{"unknown preset", map[string]any{"preset": map[string]any{"id": "forest"}}},
		{"null preset id", map[string]any{"preset": map[string]any{"id": nil}}},
		{"unknown preset field", map[string]any{"preset": map[string]any{"extension": true}}},
		{"null preset values", map[string]any{"preset": map[string]any{"values": nil}}},
		{"invalid preset values", map[string]any{"preset": map[string]any{"values": "dark"}}},
		{"unknown preset value", map[string]any{"preset": map[string]any{"values": map[string]any{"extension": true}}}},
		{"invalid remembered theme", map[string]any{"themeMemory": map[string]any{"sepia": nil}}},
		{"invalid memory type", map[string]any{"themeMemory": []any{}}},
		{"invalid selection type", map[string]any{"themeMemory": map[string]any{"dark": "custom"}}},
		{"unknown selection field", map[string]any{"themeMemory": map[string]any{"dark": map[string]any{"extension": true}}}},
		{"nested theme memory", map[string]any{"themeMemory": map[string]any{"dark": map[string]any{"themeMemory": nil}}}},
		{"legacy selection mode", map[string]any{"themeMemory": map[string]any{"dark": map[string]any{"mode": "dark"}}}},
		{"null selection mode", map[string]any{"themeMemory": map[string]any{"dark": map[string]any{"mode": nil}}}},
		{"invalid remembered preset", map[string]any{"themeMemory": map[string]any{"dark": map[string]any{"preset": map[string]any{"id": "forest"}}}}},
		{"invalid remembered custom", map[string]any{"themeMemory": map[string]any{"dark": map[string]any{"custom": map[string]any{"gridOpacity": float64(101)}}}}},
		{"null custom value", map[string]any{"custom": map[string]any{"gridColor": nil}}},
		{"invalid base theme", map[string]any{"custom": map[string]any{"baseTheme": "auto"}}},
		{"unsafe color", map[string]any{"preset": map[string]any{"values": map[string]any{"backgroundColor": "url(http://private)"}}}},
		{"invalid color length", map[string]any{"custom": map[string]any{"gridColor": "#12345"}}},
		{"brightness too low", map[string]any{"custom": map[string]any{"backgroundBrightness": float64(-31)}}},
		{"brightness too high", map[string]any{"custom": map[string]any{"backgroundBrightness": float64(31)}}},
		{"opacity too low", map[string]any{"custom": map[string]any{"gridOpacity": float64(-1)}}},
		{"opacity too high", map[string]any{"custom": map[string]any{"gridOpacity": float64(101)}}},
		{"numeric string", map[string]any{"custom": map[string]any{"gridOpacity": "30"}}},
		{"nonfinite brightness", map[string]any{"custom": map[string]any{"backgroundBrightness": math.NaN()}}},
		{"nonfinite opacity", map[string]any{"custom": map[string]any{"gridOpacity": math.Inf(1)}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Plan(d, command(Operation{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"appearance": tt.appearance}}), ownerContext())
			if err == nil {
				t.Fatalf("invalid appearance accepted: %#v", tt.appearance)
			}
			if !reflect.DeepEqual(plan.Document, d) {
				t.Fatal("rejected settings mutated the document")
			}
		})
	}
}
