package app

import (
	"context"
	"sync"
	"testing"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

func budgetCaller() cp.Caller { return cp.Caller{UserID: "user-a"} }

func budgetRoot(t *testing.T, f *platformImageFixture, key string, limit int64) cp.BudgetGrantView {
	t.Helper()
	root, err := f.svc.BudgetGrants().CreateRoot(context.Background(), budgetCaller(), cp.CreateBudgetRequest{LimitMicrocredits: limit, Work: cp.BusinessRef{Application: "test", ObjectType: "work", ObjectID: key}, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func budgetGet(t *testing.T, f *platformImageFixture, id string) cp.BudgetGrantView {
	t.Helper()
	view, err := f.svc.BudgetGrants().Get(context.Background(), budgetCaller(), id)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func budgetSubmit(f *platformImageFixture, grantID, key string) (cp.SubmitReceipt, error) {
	return (&creationPlatform{s: f.svc}).submit(budgetCaller(), f.systemRequest(key), grantID)
}

// A3-A: a root of 1,000 cannot give 600 to two children at once; grants stay in one account and one tree.
func TestAgentBudgetConcurrentAllocation(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		grants := f.svc.BudgetGrants()
		root := budgetRoot(t, f, "alloc-root", 1_000_000)
		const workers = 20
		errs := make([]error, workers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range workers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, errs[i] = grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 600_000, IdempotencyKey: "child-" + string(rune('a'+i))})
			}(i)
		}
		close(start)
		wg.Wait()
		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case !cp.IsCode(err, cp.CodeBudgetExceeded):
				t.Fatalf("allocation %d: %v", i, err)
			}
		}
		if won != 1 {
			t.Fatalf("%d allocations of 600 fit into 1000", won)
		}
		if view := budgetGet(t, f, root.ID); view.AvailableMicrocredits != 400_000 {
			t.Fatalf("root available after one child: %+v", view)
		}
		replayA, err := grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 100_000, IdempotencyKey: "small"})
		if err != nil {
			t.Fatal(err)
		}
		replayB, err := grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 100_000, IdempotencyKey: "small"})
		if err != nil || replayB.ID != replayA.ID || replayB.RootID != root.ID {
			t.Fatalf("replay: %+v %v", replayB, err)
		}
		if _, err := grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 50_000, IdempotencyKey: "small"}); !cp.IsCode(err, cp.CodeIdempotencyConflict) {
			t.Fatalf("same key, other limit: %v", err)
		}
		// Another account can neither read nor allocate from this tree.
		if _, err := grants.Allocate(context.Background(), cp.Caller{UserID: "user-b"}, cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 1, IdempotencyKey: "foreign"}); !cp.IsCode(err, cp.CodeNotFound) {
			t.Fatalf("foreign allocation: %v", err)
		}
		if _, err := grants.Get(context.Background(), cp.Caller{UserID: "user-b"}, root.ID); !cp.IsCode(err, cp.CodeNotFound) {
			t.Fatalf("foreign read: %v", err)
		}
		if _, err := grants.CreateRoot(context.Background(), budgetCaller(), cp.CreateBudgetRequest{LimitMicrocredits: 0, IdempotencyKey: "zero"}); !cp.IsCode(err, cp.CodeInvalidRequest) {
			t.Fatalf("zero root: %v", err)
		}
		var wallet int64
		f.db.Model(&model.CreditLedgerEntry{}).Where("user_id = ?", "user-a").Count(&wallet)
		if available, reserved := platformAvailable(t, f.db, "user-a"); available != 10_000_000 || reserved != 0 || wallet != 0 {
			t.Fatalf("allocating a budget must not touch the wallet: available=%d reserved=%d ledger=%d", available, reserved, wallet)
		}
	})
}

// A3-B: concurrent charges under one grant never exceed it; one operation produces one order.
func TestAgentBudgetChargeAdmission(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		root := budgetRoot(t, f, "charge-root", 250_000)
		const workers = 20
		run := func(key func(int) string) []error {
			errs := make([]error, workers)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range workers {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					_, errs[i] = budgetSubmit(f, root.ID, key(i))
				}(i)
			}
			close(start)
			wg.Wait()
			return errs
		}
		for i, err := range run(func(int) string { return "same-op" }) {
			if err != nil {
				t.Fatalf("same-key submit %d: %v", i, err)
			}
		}
		if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != 1 {
			t.Fatalf("one operation produced %d orders", got)
		}
		won := 0
		for i, err := range run(func(i int) string { return "op-" + string(rune('a'+i)) }) {
			switch {
			case err == nil:
				won++
			case !cp.IsCode(err, cp.CodeBudgetExceeded):
				t.Fatalf("submit %d: %v", i, err)
			}
		}
		if won != 1 {
			t.Fatalf("budget of 250k admitted %d more 100k tasks after the first", won)
		}
		view := budgetGet(t, f, root.ID)
		if view.Usage.InFlight != 200_000 || view.AvailableMicrocredits != 50_000 {
			t.Fatalf("usage after admission: %+v", view)
		}
		if _, reserved := platformAvailable(t, f.db, "user-a"); reserved != 200_000 {
			t.Fatalf("wallet reserved %d, want exactly the two admitted tasks", reserved)
		}
	})
}

// A3-C: a failure at any write of a granted submit rolls back the order, task, binding and hold.
func TestAgentBudgetChargeRollback(t *testing.T) {
	for _, table := range []string{"tasks", "billing_orders", "platform_submissions", "credit_ledger_entries"} {
		t.Run(table, func(t *testing.T) {
			platformDialects(t, func(t *testing.T, f *platformImageFixture) {
				root := budgetRoot(t, f, "rollback-root", 1_000_000)
				drop := injectInsertFailure(t, f.db, table)
				if _, err := budgetSubmit(f, root.ID, "rollback"); err == nil {
					t.Fatal("submit should fail")
				}
				for _, check := range []any{&model.Task{}, &model.BillingOrder{}, &model.PlatformSubmission{}, &model.CreditLedgerEntry{}} {
					if got := platformCount(t, f.db, check, "user_id = ?", "user-a"); got != 0 {
						t.Fatalf("%T left %d rows after failure at %s", check, got, table)
					}
				}
				if view := budgetGet(t, f, root.ID); view.Usage != (cp.BudgetUsage{}) || view.AvailableMicrocredits != 1_000_000 {
					t.Fatalf("grant changed after rollback: %+v", view)
				}
				if claimed, err := f.svc.repo.ClaimNextTask("probe", 0); err != nil || claimed != nil {
					t.Fatalf("rolled back submit left a claimable task: %+v %v", claimed, err)
				}
				drop()
				if _, err := budgetSubmit(f, root.ID, "rollback"); err != nil {
					t.Fatalf("same key after rollback: %v", err)
				}
			})
		})
	}
}

// A3-D: closing a child returns only what it can prove unused; pending review stays held.
func TestAgentBudgetUncertainHoldback(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		grants := f.svc.BudgetGrants()
		root := budgetRoot(t, f, "hold-root", 1_000_000)
		child, err := grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 300_000, IdempotencyKey: "hold-child"})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := budgetSubmit(f, child.ID, "hold-task")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&model.BillingOrder{}).Where("task_id = ?", receipt.Snapshot.TaskID).Update("status", model.BillingStatusUncertain).Error; err != nil {
			t.Fatal(err)
		}
		closed, err := grants.Close(context.Background(), budgetCaller(), cp.CloseBudgetRequest{ID: child.ID, ExpectedVersion: child.Version})
		if err != nil || closed.Status != cp.BudgetStatusClosed {
			t.Fatalf("close: %+v %v", closed, err)
		}
		if view := budgetGet(t, f, root.ID); view.AvailableMicrocredits != 900_000 || view.Usage.PendingReview != 100_000 {
			t.Fatalf("root after closing a child with pending review: %+v", view)
		}
		if _, err := grants.Close(context.Background(), budgetCaller(), cp.CloseBudgetRequest{ID: root.ID, ExpectedVersion: root.Version + 7}); !cp.IsCode(err, cp.CodeActionNotAllowed) {
			t.Fatalf("stale close: %v", err)
		}
	})
}

// A3-B/E: parent totals are the sum of distinct orders; a child's spend is not charged again on the parent.
func TestAgentBudgetRollupNoDoubleCount(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		grants := f.svc.BudgetGrants()
		root := budgetRoot(t, f, "rollup-root", 1_000_000)
		a, err := grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: root.ID, LimitMicrocredits: 300_000, IdempotencyKey: "a"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := grants.Allocate(context.Background(), budgetCaller(), cp.AllocateBudgetRequest{ParentID: a.ID, LimitMicrocredits: 200_000, IdempotencyKey: "b"})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct{ grant, key string }{{root.ID, "on-root"}, {a.ID, "on-a"}, {b.ID, "on-b"}} {
			if _, err := budgetSubmit(f, item.grant, item.key); err != nil {
				t.Fatal(err)
			}
			f.runQueued(t)
		}
		rootView, aView, bView := budgetGet(t, f, root.ID), budgetGet(t, f, a.ID), budgetGet(t, f, b.ID)
		if rootView.Usage.Charged != 300_000 || aView.Usage.Charged != 200_000 || bView.Usage.Charged != 100_000 {
			t.Fatalf("rollup: root=%+v a=%+v b=%+v", rootView.Usage, aView.Usage, bView.Usage)
		}
		if rootView.AvailableMicrocredits != 600_000 || aView.AvailableMicrocredits != 0 || bView.AvailableMicrocredits != 100_000 {
			t.Fatalf("available: root=%d a=%d b=%d", rootView.AvailableMicrocredits, aView.AvailableMicrocredits, bView.AvailableMicrocredits)
		}
		if available, reserved := platformAvailable(t, f.db, "user-a"); available != 10_000_000-300_000 || reserved != 0 {
			t.Fatalf("wallet charged once per order: available=%d reserved=%d", available, reserved)
		}
		// a is full (its limit is used by its own task plus b's open limit).
		if _, err := budgetSubmit(f, a.ID, "a-overflow"); !cp.IsCode(err, cp.CodeBudgetExceeded) {
			t.Fatalf("submit over a's limit: %v", err)
		}
	})
}

// A3-F: once a grant is closed, neither a new submit nor a retry can charge it.
func TestAgentBudgetClosedGrantFencesNewCharges(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		root := budgetRoot(t, f, "fence-root", 1_000_000)
		f.failStatus.Store(400)
		receipt, err := budgetSubmit(f, root.ID, "fence-task")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runQueuedResult(t); err == nil {
			t.Fatal("upstream 400 must fail the task")
		}
		f.failStatus.Store(0)
		view := budgetGet(t, f, root.ID)
		if _, err := f.svc.BudgetGrants().Close(context.Background(), budgetCaller(), cp.CloseBudgetRequest{ID: root.ID, ExpectedVersion: view.Version}); err != nil {
			t.Fatal(err)
		}
		orders := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a")
		if _, err := f.svc.CreationPlatform().Retry(context.Background(), budgetCaller(), receipt.Snapshot.TaskID); !cp.IsCode(err, cp.CodeBudgetClosed) {
			t.Fatalf("retry under closed grant: %v", err)
		}
		if _, err := budgetSubmit(f, root.ID, "after-close"); !cp.IsCode(err, cp.CodeBudgetClosed) {
			t.Fatalf("submit under closed grant: %v", err)
		}
		if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != orders {
			t.Fatalf("closed grant still created orders: %d -> %d", orders, got)
		}
		// Same key and content with a different grant is a different request.
		other := budgetRoot(t, f, "fence-other", 1_000_000)
		if _, err := budgetSubmit(f, other.ID, "fence-task"); !cp.IsCode(err, cp.CodeIdempotencyConflict) {
			t.Fatalf("same key under another grant: %v", err)
		}
	})
}
