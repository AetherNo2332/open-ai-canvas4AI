package connection

import (
	"encoding/json"
	"os"
	"testing"
)

// Golden matrix captured from the manual UI policy before this feature.
func TestManualBaseline(t *testing.T) {
	raw, err := os.ReadFile("manual-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		From, To string
		Allowed  bool
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != len(Builtins)*len(Builtins) {
		t.Fatal("baseline does not cover every built-in pair")
	}
	for _, tc := range cases {
		t.Run(tc.From+"/"+tc.To, func(t *testing.T) {
			err := Validate([]Node{{ID: "from", Type: tc.From}, {ID: "to", Type: tc.To}}, nil, Edge{From: "from", To: "to"})
			if (err == nil) != tc.Allowed {
				t.Fatalf("manual allowed=%v, agent error=%v", tc.Allowed, err)
			}
		})
	}
}

func TestModeAndDistinctInputLimits(t *testing.T) {
	nodes := []Node{{ID: "a", Type: "text"}, {ID: "b", Type: "text"}, {ID: "c1", Type: "text", WorkflowKind: "character"}, {ID: "c2", Type: "text", WorkflowKind: "character"}, {ID: "audio", Type: "audio"}, {ID: "config", Type: "config", Mode: "audio"}, {ID: "i1", Type: "image"}, {ID: "i2", Type: "image"}, {ID: "convert", Type: "media-conversion"}}
	edges := []Edge{{From: "a", To: "config"}}
	if err := Validate(nodes, edges, Edge{From: "b", To: "config"}); err != nil {
		t.Fatalf("multiple text inputs should be legal: %v", err)
	}
	edges = append(edges, Edge{From: "c1", To: "config"})
	if err := Validate(nodes, edges, Edge{From: "c2", To: "config"}); err == nil {
		t.Fatal("multiple character cards accepted for audio")
	}
	if err := Validate(nodes, []Edge{{From: "i1", To: "convert"}}, Edge{From: "i2", To: "convert"}); err == nil {
		t.Fatal("conversion accepted two distinct inputs")
	}
	nodes[5].Mode = "video"
	if err := Validate(nodes, nil, Edge{From: "audio", To: "config"}); err != nil {
		t.Fatalf("config video mode rejected audio: %v", err)
	}
}
