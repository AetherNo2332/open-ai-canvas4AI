package references

import (
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/canvas/capability"
	"os"
	"reflect"
	"testing"
)

func TestReconcileIsIdempotent(t *testing.T) {
	doc := fixture(t)
	before, _ := json.Marshal(doc)
	allocated := 0
	nextID := func() string { allocated++; return fmt.Sprintf("edge-%d", allocated) }
	first, err := Reconcile(doc, doc, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Document["connections"].([]any)) < 3 {
		t.Fatal("declared references and output were not materialized")
	}
	firstJSON, _ := json.Marshal(first.Document)
	count := allocated
	second, err := Reconcile(first.Document, first.Document, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, _ := json.Marshal(second.Document)
	after, _ := json.Marshal(doc)
	if string(firstJSON) != string(secondJSON) || allocated != count {
		t.Fatal("repeat reconciliation changed identities or document")
	}
	if string(before) != string(after) {
		t.Fatal("input document mutated")
	}
}
func TestReconcileClearsRemovedRowReferences(t *testing.T) {
	doc := fixture(t)
	n := 0
	nextID := func() string { n++; return fmt.Sprint(n) }
	first, err := Reconcile(doc, doc, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(first.Document)
	var edited map[string]any
	_ = json.Unmarshal(raw, &edited)
	fixtureRow(edited)["assetBindings"] = []any{}
	got, err := Reconcile(first.Document, edited, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range got.Document["connections"].([]any) {
		edge := raw.(map[string]any)
		if edge["managedBy"] == ManagedBy && edge["toNodeId"] == "board" && edge["toHandleId"] == "row:shot" {
			t.Fatal("removed binding retained managed row edge")
		}
	}
	if len(got.Document["nodes"].([]any)) != 4 {
		t.Fatal("clearing relationships deleted assets")
	}
}

// The web editor draws managed lines before saving (canvas-reference-projection.ts reads the same
// fixture). Matching keys, order and allocation keep those ids when this reconciler confirms them.
func TestReconcileMatchesEditorProjection(t *testing.T) {
	raw, err := os.ReadFile("testdata/reference-projection-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string           `json:"name"`
		Owners   []string         `json:"owners"`
		Document map[string]any   `json:"document"`
		Lines    []map[string]any `json:"lines"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		allocated := 0
		got, err := Reconcile(tc.Document, tc.Document, capability.BuiltinRegistry(), func() string { allocated++; return fmt.Sprintf("edge-%d", allocated) })
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		owners := map[string]bool{}
		for _, id := range tc.Owners {
			owners[id] = true
		}
		lines := []map[string]any{}
		for _, edge := range maps(got.Document["connections"]) {
			if str(edge["managedBy"]) == ManagedBy && owners[str(edge["referenceOwnerNodeId"])] {
				lines = append(lines, edge)
			}
		}
		if !reflect.DeepEqual(lines, tc.Lines) {
			gotJSON, _ := json.Marshal(lines)
			wantJSON, _ := json.Marshal(tc.Lines)
			t.Fatalf("%s:\n got %s\nwant %s", tc.Name, gotJSON, wantJSON)
		}
	}
}
