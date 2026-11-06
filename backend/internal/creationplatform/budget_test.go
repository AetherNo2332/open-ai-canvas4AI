package creationplatform

import "testing"

func budgetTree(t *testing.T, nodes ...BudgetNode) *BudgetTree {
	t.Helper()
	tree, err := NewBudgetTree(nodes)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestBudgetAllocationCountsOpenChildrenAtTheirLimit(t *testing.T) {
	tree := budgetTree(t,
		BudgetNode{ID: "root", Limit: 1000, Open: true, Direct: BudgetUsage{Charged: 50}},
		BudgetNode{ID: "drama", ParentID: "root", Limit: 600, Open: true, Direct: BudgetUsage{InFlight: 200}},
	)
	if got := tree.Available("root"); got != 350 {
		t.Fatalf("root available = %d, want 350 (1000 - 50 direct - 600 open child)", got)
	}
	if err := tree.AdmitAllocation("root", 400); !IsCode(err, CodeBudgetExceeded) {
		t.Fatalf("over-allocation: %v", err)
	}
	if err := tree.AdmitAllocation("root", 350); err != nil {
		t.Fatalf("exact allocation: %v", err)
	}
	// A charge inside the child is part of the child's 600, not an extra charge on the root.
	if err := tree.AdmitCharge("drama", 400); err != nil {
		t.Fatalf("child charge within its limit: %v", err)
	}
	if err := tree.AdmitCharge("drama", 401); !IsCode(err, CodeBudgetExceeded) {
		t.Fatalf("child charge over its limit: %v", err)
	}
	if got := tree.Available("root"); got != 350 {
		t.Fatalf("child usage must not reduce root availability twice: %d", got)
	}
}

func TestBudgetClosedChildHoldsBackUnsettledSpend(t *testing.T) {
	tree := budgetTree(t,
		BudgetNode{ID: "root", Limit: 1000, Open: true},
		BudgetNode{ID: "drama", ParentID: "root", Limit: 600, Open: false, Direct: BudgetUsage{Charged: 100, PendingReview: 150, Refunded: 80}},
	)
	if got := tree.Available("root"); got != 750 {
		t.Fatalf("closed child returns only unused budget: available=%d, want 750", got)
	}
	if err := tree.AdmitCharge("drama", 1); !IsCode(err, CodeBudgetClosed) {
		t.Fatalf("charge on closed grant: %v", err)
	}
}

func TestBudgetClosedAncestorBlocksDescendants(t *testing.T) {
	tree := budgetTree(t,
		BudgetNode{ID: "root", Limit: 1000, Open: false},
		BudgetNode{ID: "child", ParentID: "root", Limit: 100, Open: true},
	)
	if err := tree.AdmitCharge("child", 1); !IsCode(err, CodeBudgetClosed) {
		t.Fatalf("charge under closed root: %v", err)
	}
	if err := tree.AdmitAllocation("child", 1); !IsCode(err, CodeBudgetClosed) {
		t.Fatalf("allocation under closed root: %v", err)
	}
}

func TestBudgetRollupCountsEachChargeOnce(t *testing.T) {
	tree := budgetTree(t,
		BudgetNode{ID: "root", Limit: 1000, Open: true, Direct: BudgetUsage{Charged: 10}},
		BudgetNode{ID: "a", ParentID: "root", Limit: 300, Open: true, Direct: BudgetUsage{Charged: 20, InFlight: 5}},
		BudgetNode{ID: "a1", ParentID: "a", Limit: 100, Open: true, Direct: BudgetUsage{Charged: 30, PendingReview: 7}},
	)
	got := tree.Subtree("root")
	if got != (BudgetUsage{Charged: 60, InFlight: 5, PendingReview: 7}) {
		t.Fatalf("rollup = %+v", got)
	}
	if got := tree.Descendants("a"); len(got) != 1 || got[0] != "a1" {
		t.Fatalf("descendants = %v", got)
	}
}

func TestBudgetTreeRejectsCyclesAndOrphans(t *testing.T) {
	for name, nodes := range map[string][]BudgetNode{
		"two roots": {{ID: "a", Limit: 1}, {ID: "b", Limit: 1}},
		"orphan":    {{ID: "a", Limit: 1}, {ID: "b", ParentID: "missing", Limit: 1}},
		"cycle":     {{ID: "a", Limit: 1}, {ID: "b", ParentID: "c", Limit: 1}, {ID: "c", ParentID: "b", Limit: 1}},
		"duplicate": {{ID: "a", Limit: 1}, {ID: "a", Limit: 1}},
	} {
		if _, err := NewBudgetTree(nodes); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	tree := budgetTree(t, BudgetNode{ID: "root", Limit: 10, Open: true})
	for _, limit := range []int64{0, -1} {
		if err := tree.AdmitAllocation("root", limit); !IsCode(err, CodeInvalidRequest) {
			t.Fatalf("non-positive allocation %d: %v", limit, err)
		}
	}
	if err := tree.AdmitCharge("missing", 1); !IsCode(err, CodeNotFound) {
		t.Fatalf("unknown grant: %v", err)
	}
}
