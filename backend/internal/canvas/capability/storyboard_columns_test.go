package capability

import (
	"reflect"
	"testing"
)

func TestStoryboardColumnContract(t *testing.T) {
	names := StoryboardColumnNames()
	if len(names) != 19 {
		t.Fatalf("storyboard column names = %d, want 19: %v", len(names), names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			t.Fatalf("storyboard column names contain empty or duplicate value: %v", names)
		}
		seen[name] = true
	}
	defaults := StoryboardDefaultColumns()
	wantDefaults := []string{"shotNumber", "durationSeconds", "plotDescription", "shotSize", "motion", "assets"}
	if !reflect.DeepEqual(defaults, wantDefaults) {
		t.Fatalf("storyboard default columns = %v, want %v", defaults, wantDefaults)
	}
	for _, column := range defaults {
		if !seen[column] {
			t.Fatalf("default column %q is not part of the column contract", column)
		}
	}
	for _, legacy := range StoryboardLegacyDefaultColumnSets() {
		if len(legacy) == len(defaults) && reflect.DeepEqual(legacy, defaults) {
			t.Fatalf("legacy default set duplicates the current default: %v", legacy)
		}
		for _, column := range legacy {
			if !seen[column] {
				t.Fatalf("legacy default column %q is not part of the column contract", column)
			}
		}
	}

	script, ok := BuiltinRegistry().Resolve("script")
	if !ok {
		t.Fatal("script builtin is unavailable")
	}
	field, ok := script.PatchFields["visibleColumns"]
	if !ok || field.Items == nil {
		t.Fatalf("script visibleColumns editable contract missing: %+v", script.PatchFields)
	}
	if !reflect.DeepEqual(field.Items.Enum, names) {
		t.Fatalf("editable visibleColumns enum drifted from the column contract: %v", field.Items.Enum)
	}
	if field.Items.MaxRunes != 240 || field.MaxItems != 20 {
		t.Fatalf("editable visibleColumns contract changed: maxRunes=%d maxItems=%d", field.Items.MaxRunes, field.MaxItems)
	}
}

func TestBuiltinScriptMetadataUsesDefaultColumns(t *testing.T) {
	script, ok := BuiltinRegistry().Resolve("script")
	if !ok {
		t.Fatal("script builtin is unavailable")
	}
	metadata := script.Metadata("")
	if metadata["workflowKind"] != "script" {
		t.Fatalf("script metadata lost its workflow kind: %+v", metadata)
	}
	storyboard := metadata["storyboard"].(map[string]any)
	want := StoryboardDefaultColumnValues()
	if !reflect.DeepEqual(storyboard["visibleColumns"], want) {
		t.Fatalf("script metadata visibleColumns = %v, want %v", storyboard["visibleColumns"], want)
	}
	if rows, ok := storyboard["rows"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("script metadata rows are not empty: %+v", storyboard["rows"])
	}
}

func TestNormalizeStoryboardColumns(t *testing.T) {
	wantDefault := StoryboardDefaultColumnValues()
	legacySets := StoryboardLegacyDefaultColumnSets()
	legacyInputs := make([][]any, 0, len(legacySets))
	for _, set := range legacySets {
		values := make([]any, len(set))
		for index, column := range set {
			values[index] = column
		}
		legacyInputs = append(legacyInputs, values)
	}

	for name, columns := range map[string][]any{
		"missing":   nil,
		"empty":     {},
		"only-junk": {float64(1), ""},
		"legacy-5":  legacyInputs[0],
		"legacy-4a": legacyInputs[1],
		"legacy-4b": legacyInputs[2],
		"legacy-15": legacyInputs[3],
	} {
		t.Run(name, func(t *testing.T) {
			if got := NormalizeStoryboardColumns(columns); !reflect.DeepEqual(got, wantDefault) {
				t.Fatalf("normalized columns = %v, want current default %v", got, wantDefault)
			}
		})
	}

	manual := []any{"shotNumber", "dialogue", "assets"}
	if got := NormalizeStoryboardColumns(manual); !reflect.DeepEqual(got, manual) {
		t.Fatalf("manual selection was rewritten: %v", got)
	}
	current := StoryboardDefaultColumnValues()
	if got := NormalizeStoryboardColumns(current); !reflect.DeepEqual(got, current) {
		t.Fatalf("current default was rewritten: %v", got)
	}
	repeated := []any{"shotNumber", "shotNumber", "dialogue"}
	if got := NormalizeStoryboardColumns(repeated); !reflect.DeepEqual(got, repeated) {
		t.Fatalf("manual selection with repeats was rewritten: %v", got)
	}
	partial := []any{"shotNumber", float64(2), "dialogue"}
	if got := NormalizeStoryboardColumns(partial); !reflect.DeepEqual(got, []any{"shotNumber", "dialogue"}) {
		t.Fatalf("invalid entries were not dropped: %v", got)
	}

	// Normalization must not hand back the caller's backing array.
	owned := []any{"shotNumber", "dialogue"}
	normalized := NormalizeStoryboardColumns(owned)
	normalized[0] = "assets"
	if owned[0] != "shotNumber" {
		t.Fatal("normalize returned the caller's slice")
	}
	defaults := StoryboardDefaultColumnValues()
	defaults[0] = "assets"
	if StoryboardDefaultColumns()[0] != "shotNumber" {
		t.Fatal("default column values alias package state")
	}
	if StoryboardColumnNames()[0] != "shotNumber" {
		t.Fatal("column names alias package state")
	}
}
