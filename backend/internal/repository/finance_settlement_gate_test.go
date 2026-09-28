package repository

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// newSettlementTestRepository builds a file-backed database using the same
// journal mode and busy timeout as production SQLite
// (internal/database/database.go: WAL + _busy_timeout=5000).
//
// Shared-cache in-memory databases are deliberately NOT used: they take
// table-level locks and surface "database table is locked" instead of the
// row/snapshot behaviour a real deployment shows.
func newSettlementTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "settlement.db") + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close settlement test database: %v", err)
		}
	})
	if err := db.AutoMigrate(&model.CreditAccount{}, &model.BillingOrder{}, &model.CreditLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	return &Repository{db: db}, db
}

// settlementFixture returns an order whose 3_000_000 reservation sits next to a
// second, unrelated 3_000_000 reservation on the same account.
//
// The extra reservation is the whole point: the credit-account guard
// (`reserved_microcredits >= reserved`) still matches after one settlement, so it
// cannot be what prevents a double charge. Only the order's own state transition can.
func settlementFixture(t *testing.T, db *gorm.DB) model.BillingOrder {
	t.Helper()
	order := model.BillingOrder{
		ID: "order-1", UserID: "user-1", IdempotencyKey: "task:task-1", TaskID: "task-1",
		Model: "canvas-model", Capability: "text", BillingMode: "fixed",
		AmountMicrocredits: 3_000_000, ReservedAmountMicrocredits: 3_000_000,
		Status: model.BillingStatusRunning,
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CreditAccount{
		UserID: "user-1", AvailableMicrocredits: 9_000_000, ReservedMicrocredits: 6_000_000,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return order
}

func assertSingleSettlement(t *testing.T, db *gorm.DB) {
	t.Helper()
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
		t.Fatal(err)
	}
	if account.ReservedMicrocredits != 3_000_000 {
		t.Fatalf("reserved credits settled %d times (want 3_000_000 left): %+v",
			(6_000_000-account.ReservedMicrocredits)/3_000_000, account)
	}
	if account.AvailableMicrocredits != 9_000_000 {
		t.Fatalf("fixed settlement must not move available credits: %+v", account)
	}
	var consumed int64
	if err := db.Model(&model.CreditLedgerEntry{}).
		Where("billing_order_id = ? AND type = ?", "order-1", model.CreditLedgerConsume).
		Count(&consumed).Error; err != nil {
		t.Fatal(err)
	}
	if consumed != 1 {
		t.Fatalf("consume ledger entries = %d, want exactly 1", consumed)
	}
	var settled model.BillingOrder
	if err := db.First(&settled, "id = ?", "order-1").Error; err != nil {
		t.Fatal(err)
	}
	if settled.Status != model.BillingStatusSettled {
		t.Fatalf("order status = %q, want settled", settled.Status)
	}
	if settled.ActualAmountMicrocredits != 3_000_000 {
		t.Fatalf("actual amount = %d, want 3_000_000", settled.ActualAmountMicrocredits)
	}
}

// TestSettleBillingOrderIsExactlyOnceUnderConcurrentSettlement drives two real
// connections that both start from a non-terminal order.
//
// Both callers read the order before either commits, which is exactly the window
// the pre-fix implementation was unsafe in. SQLite WAL can reject the loser's
// upgrade of its stale read snapshot; callers are expected to retry, so the test
// retries too and then requires BOTH callers to end up successful with a single charge.
func TestSettleBillingOrderIsExactlyOnceUnderConcurrentSettlement(t *testing.T) {
	repo, db := newSettlementTestRepository(t)
	settlementFixture(t, db)
	// The pre-fix implementation read the order before mutating the account; do the
	// same here so both goroutines start from a non-terminal snapshot.
	if err := db.First(&model.BillingOrder{}, "id = ?", "order-1").Error; err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make([]error, 2)
	retries := make([]int, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 20; attempt++ {
				err := repo.SettleBillingOrder("order-1", "provider-1")
				if err == nil || !isRetryableSettlementLock(err) {
					results[index] = err
					return
				}
				retries[index] = attempt + 1
				time.Sleep(time.Duration(5+attempt*5) * time.Millisecond)
			}
			results[index] = errors.New("settlement kept hitting a retryable lock")
		}()
	}
	close(start)
	wg.Wait()

	for index, err := range results {
		if err != nil {
			t.Fatalf("settler %d failed after %d retries: %v", index, retries[index], err)
		}
	}
	t.Logf("loser retries: %v (0 means the gate serialized cleanly)", retries)
	assertSingleSettlement(t, db)
}

// TestSettleBillingOrderStaleSnapshotCounterProof is the deterministic
// counter-proof for the gate: it replays the pre-fix ordering (order status written
// last, without a condition) against the same fixture and shows it charges twice.
//
// Without this direction the concurrency test above could pass for the wrong reason.
func TestSettleBillingOrderStaleSnapshotCounterProof(t *testing.T) {
	_, db := newSettlementTestRepository(t)
	stale := settlementFixture(t, db)

	settleWithoutOrderGate := func(order model.BillingOrder) error {
		return db.Transaction(func(tx *gorm.DB) error {
			if order.Status == model.BillingStatusSettled {
				return nil
			}
			updated := tx.Model(&model.CreditAccount{}).
				Where("user_id = ? AND reserved_microcredits >= ?", order.UserID, order.AmountMicrocredits).
				Updates(map[string]any{
					"reserved_microcredits": gorm.Expr("reserved_microcredits - ?", order.AmountMicrocredits),
					"version":               gorm.Expr("version + 1"),
					"updated_at":            time.Now(),
				})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return errors.New("reserved credit balance is inconsistent")
			}
			now := time.Now()
			if err := tx.Model(&model.BillingOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
				"status": model.BillingStatusSettled, "actual_amount_microcredits": order.AmountMicrocredits,
				"settled_at": &now, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			return tx.Create(&model.CreditLedgerEntry{
				ID: newRepositoryID(), UserID: order.UserID, Type: model.CreditLedgerConsume,
				AmountMicrocredits: -order.AmountMicrocredits, ReservedDeltaMicrocredits: -order.AmountMicrocredits,
				BillingOrderID: order.ID,
			}).Error
		})
	}

	// Two callers that both observed `running`: the account guard does not stop either.
	for range 2 {
		if err := settleWithoutOrderGate(stale); err != nil {
			t.Fatalf("pre-fix ordering failed: %v", err)
		}
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
		t.Fatal(err)
	}
	if account.ReservedMicrocredits != 0 {
		t.Fatalf("counter-proof did not reproduce the double charge: %+v", account)
	}
	var ledgerCount int64
	if err := db.Model(&model.CreditLedgerEntry{}).Where("billing_order_id = ?", "order-1").Count(&ledgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 2 {
		t.Fatalf("counter-proof ledger entries = %d, want 2 (one per stale caller)", ledgerCount)
	}

	// Same fixture, real implementation, same stale snapshot: exactly one charge.
	repoFixture, dbFixture := newSettlementTestRepository(t)
	settlementFixture(t, dbFixture)
	for range 2 {
		if err := repoFixture.SettleBillingOrder("order-1", "provider-1"); err != nil {
			t.Fatalf("SettleBillingOrder() error = %v", err)
		}
	}
	assertSingleSettlement(t, dbFixture)
}

// TestSettleBillingOrderKeepsExplicitRefundConflict pins the loser-path semantics:
// a settlement that loses the gate to a refund must stay an explicit error rather
// than being reported as a success.
func TestSettleBillingOrderKeepsExplicitRefundConflict(t *testing.T) {
	repo, db := newSettlementTestRepository(t)
	order := settlementFixture(t, db)
	now := time.Now()
	if err := db.Model(&model.BillingOrder{}).Where("id = ?", order.ID).
		Updates(map[string]any{"status": model.BillingStatusRefunded, "refunded_at": &now}).Error; err != nil {
		t.Fatal(err)
	}
	err := repo.SettleBillingOrder(order.ID, "provider-1")
	if err == nil || !strings.Contains(err.Error(), "already refunded") {
		t.Fatalf("settling a refunded order = %v, want explicit refunded error", err)
	}
}

// isRetryableSettlementLock reports whether a settlement failure is SQLite
// contention rather than a business/billing conflict. Both spellings occur:
// "database is locked" and "database table is locked".
func isRetryableSettlementLock(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "database is locked") ||
		strings.Contains(text, "database table is locked") ||
		strings.Contains(text, "sqlite_busy")
}
