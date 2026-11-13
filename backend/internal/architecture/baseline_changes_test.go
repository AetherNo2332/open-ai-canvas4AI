package architecture

import "testing"

func TestCompareBaselines(t *testing.T) {
	a := Allowance{ID: "internal/app/old.go:func:Run:complexity", Max: 25, Reason: "existing"}
	b := a
	b.ID = "internal/domain/new.go:func:Run:complexity"
	baseline := func(rows ...Allowance) Baseline { return Baseline{Version: 1, Allowances: rows} }
	for _, tc := range []struct {
		name      string
		old, next Baseline
		moves     []BaselineMove
		fail      bool
	}{
		{"unchanged", baseline(a), baseline(a), nil, false},
		{"deleted", baseline(a), baseline(), nil, false},
		{"lowered", baseline(a), baseline(Allowance{ID: a.ID, Max: 24, Reason: "lower"}), nil, false},
		{"new allowance", baseline(a), baseline(a, b), nil, true},
		{"growth", baseline(a), baseline(Allowance{ID: a.ID, Max: 26, Reason: "growth"}), nil, true},
		{"move", baseline(a), baseline(b), []BaselineMove{{From: a.ID, To: b.ID, Reason: "extract"}}, false},
		{"duplicate", baseline(a), baseline(b), []BaselineMove{{From: a.ID, To: b.ID, Reason: "extract"}, {From: a.ID, To: b.ID, Reason: "again"}}, true},
		{"chain", baseline(a), baseline(b), []BaselineMove{{From: a.ID, To: b.ID, Reason: "extract"}, {From: b.ID, To: "x:complexity", Reason: "chain"}}, true},
		{"missing version", Baseline{}, baseline(), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareBaselines(tc.old, tc.next, tc.moves)
			if (len(got) > 0) != tc.fail {
				t.Fatalf("findings: %+v", got)
			}
		})
	}
}

func TestBaselineMoveEvidence(t *testing.T) {
	from, to := "old.go:func:Run:complexity", "new.go:func:Run:complexity"
	old := Baseline{Version: 1, Allowances: []Allowance{{ID: from, Max: 25, Reason: "debt"}}}
	next := Baseline{Version: 1, Allowances: []Allowance{{ID: to, Max: 25, Reason: "moved"}}}
	metric := func(id, rule string, value int) Finding { return Finding{ID: id, Rule: rule, Value: value} }
	for _, tc := range []struct {
		name          string
		before, after []Finding
		fail          bool
	}{
		{"legal", []Finding{metric(from, "complexity", 25)}, []Finding{metric(to, "complexity", 25)}, false},
		{"source remains", []Finding{metric(from, "complexity", 25)}, []Finding{metric(from, "complexity", 25), metric(to, "complexity", 25)}, true},
		{"target missing", []Finding{metric(from, "complexity", 25)}, nil, true},
		{"rule changed", []Finding{metric(from, "complexity", 25)}, []Finding{metric(to, "function_lines", 25)}, true},
		{"code grows", []Finding{metric(from, "complexity", 24)}, []Finding{metric(to, "complexity", 25)}, true},
		{"old missing", nil, []Finding{metric(to, "complexity", 25)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckBaselineMoves(old, next, []BaselineMove{{From: from, To: to, Reason: "extract"}}, tc.before, tc.after)
			if (len(got) > 0) != tc.fail {
				t.Fatalf("%+v", got)
			}
		})
	}
}
