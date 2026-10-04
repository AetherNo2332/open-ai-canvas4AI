package skills

import (
	"errors"
	"testing"

	"infinite-canvas/backend/internal/kernel"
)

func TestResolveSkillSelectionDedupesWithHighestPriorityLayer(t *testing.T) {
	global := []SkillSelectionItem{
		{SkillID: "skill-a", VersionID: "a-v1", ContentHash: "hash-a1", Source: SkillSourceGlobal},
		{SkillID: "skill-b", VersionID: "b-v1", ContentHash: "hash-b1", Source: SkillSourceGlobal},
	}
	middle := []SkillSelectionItem{
		{SkillID: "skill-a", VersionID: "a-v2", ContentHash: "hash-a2", Source: "workspace"},
		{SkillID: "skill-c", VersionID: "c-v1", ContentHash: "hash-c1", Source: "workspace"},
	}
	user := []SkillSelectionItem{
		{SkillID: "skill-b", VersionID: "b-v2", ContentHash: "hash-b2", Source: SkillSourceUser},
		{SkillID: "skill-d", VersionID: "d-v1", ContentHash: "hash-d1", Source: SkillSourceUser},
	}
	resolved, err := ResolveSkillSelection(global, middle, user)
	if err != nil {
		t.Fatal(err)
	}
	want := []SkillSelectionItem{
		{SkillID: "skill-a", VersionID: "a-v2", ContentHash: "hash-a2", Source: "workspace"},
		{SkillID: "skill-b", VersionID: "b-v2", ContentHash: "hash-b2", Source: SkillSourceUser},
		{SkillID: "skill-c", VersionID: "c-v1", ContentHash: "hash-c1", Source: "workspace"},
		{SkillID: "skill-d", VersionID: "d-v1", ContentHash: "hash-d1", Source: SkillSourceUser},
	}
	if len(resolved) != len(want) {
		t.Fatalf("resolved length = %d, want %d: %+v", len(resolved), len(want), resolved)
	}
	for index := range want {
		if resolved[index] != want[index] {
			t.Fatalf("resolved[%d] = %+v, want %+v", index, resolved[index], want[index])
		}
	}
}

func TestResolveSkillSelectionStableOrderForSingleLayer(t *testing.T) {
	layer := []SkillSelectionItem{
		{SkillID: "skill-b", VersionID: "b-v1", Source: SkillSourceGlobal},
		{SkillID: "skill-a", VersionID: "a-v1", Source: SkillSourceGlobal},
	}
	resolved, err := ResolveSkillSelection(layer)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 || resolved[0].SkillID != "skill-b" || resolved[1].SkillID != "skill-a" {
		t.Fatalf("single layer order = %+v", resolved)
	}
}

func TestResolveSkillSelectionRejectsEmptySkillID(t *testing.T) {
	if _, err := ResolveSkillSelection([]SkillSelectionItem{{SkillID: "", VersionID: "v1"}}); err == nil {
		t.Fatal("empty SkillID must fail")
	}
	if _, err := ResolveSkillSelection(
		[]SkillSelectionItem{{SkillID: "skill-a", VersionID: "v1"}},
		[]SkillSelectionItem{{SkillID: "   "}},
	); err == nil {
		t.Fatal("whitespace-only SkillID in a higher layer must fail")
	}
}

func TestResolveSkillSelectionAcceptsEmptyLayers(t *testing.T) {
	resolved, err := ResolveSkillSelection()
	if err != nil {
		t.Fatalf("no layers: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("no layers resolved = %+v", resolved)
	}
	resolved, err = ResolveSkillSelection(nil, []SkillSelectionItem{})
	if err != nil {
		t.Fatalf("empty layers: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("empty layers resolved = %+v", resolved)
	}
}

func TestAdmitSkillCapacityPassesAtExactLimits(t *testing.T) {
	facts := []SkillCapacityFacts{
		{SkillID: "skill-a", FileCount: 2, TotalBytes: 300, ContextBytes: 400},
		{SkillID: "skill-b", FileCount: 1, TotalBytes: 200, ContextBytes: 100},
	}
	budgets := SkillCapacityBudgets{MaxFiles: 3, MaxTotalBytes: 500, MaxContextBytes: 500}
	if err := AdmitSkillCapacity(facts, budgets); err != nil {
		t.Fatalf("exactly-at-limit admission must pass: %v", err)
	}
}

func TestAdmitSkillCapacityRejectsOverBudget(t *testing.T) {
	cases := []struct {
		name    string
		facts   []SkillCapacityFacts
		budgets SkillCapacityBudgets
		budget  string
		limit   any
		actual  any
	}{
		{
			name:    "files",
			facts:   []SkillCapacityFacts{{SkillID: "skill-a", FileCount: 3}},
			budgets: SkillCapacityBudgets{MaxFiles: 2},
			budget:  "files",
			limit:   2,
			actual:  3,
		},
		{
			name:    "total_bytes",
			facts:   []SkillCapacityFacts{{SkillID: "skill-a", TotalBytes: 501}},
			budgets: SkillCapacityBudgets{MaxTotalBytes: 500},
			budget:  "total_bytes",
			limit:   int64(500),
			actual:  int64(501),
		},
		{
			name: "context_bytes",
			facts: []SkillCapacityFacts{
				{SkillID: "skill-a", ContextBytes: 300},
				{SkillID: "skill-b", ContextBytes: 201},
			},
			budgets: SkillCapacityBudgets{MaxContextBytes: 500},
			budget:  "context_bytes",
			limit:   int64(500),
			actual:  int64(501),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := AdmitSkillCapacity(testCase.facts, testCase.budgets)
			if err == nil {
				t.Fatalf("%s over-by-one must be rejected", testCase.name)
			}
			var appErr *kernel.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("error = %T, want *kernel.AppError", err)
			}
			if appErr.Reason != kernel.ReasonAgentSkillBudgetExceeded {
				t.Fatalf("Reason = %q, want %q", appErr.Reason, kernel.ReasonAgentSkillBudgetExceeded)
			}
			if got := appErr.Details["budget"]; got != testCase.budget {
				t.Fatalf("Details[budget] = %#v, want %q", got, testCase.budget)
			}
			if got := appErr.Details["limit"]; got != testCase.limit {
				t.Fatalf("Details[limit] = %#v, want %#v", got, testCase.limit)
			}
			if got := appErr.Details["actual"]; got != testCase.actual {
				t.Fatalf("Details[actual] = %#v, want %#v", got, testCase.actual)
			}
		})
	}
}

func TestAdmitSkillCapacityAcceptsEmptyFacts(t *testing.T) {
	if err := AdmitSkillCapacity(nil, SkillCapacityBudgets{MaxFiles: SkillRunMaxFiles, MaxTotalBytes: SkillRunMaxTotalBytes, MaxContextBytes: SkillRunMaxContextBytes}); err != nil {
		t.Fatalf("empty facts must pass: %v", err)
	}
}

func TestSkillRunBudgetConstants(t *testing.T) {
	if SkillRunMaxFiles != 1024 {
		t.Fatalf("SkillRunMaxFiles = %d, want 1024", SkillRunMaxFiles)
	}
	if SkillRunMaxTotalBytes != 16<<20 {
		t.Fatalf("SkillRunMaxTotalBytes = %d, want %d", SkillRunMaxTotalBytes, 16<<20)
	}
	if SkillRunMaxContextBytes != 512<<10 {
		t.Fatalf("SkillRunMaxContextBytes = %d, want %d", SkillRunMaxContextBytes, 512<<10)
	}
}
