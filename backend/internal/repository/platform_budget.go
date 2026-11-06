package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// BudgetNodeRow is a grant with the usage of the orders bound directly to it.
type BudgetNodeRow struct {
	model.PlatformBudgetGrant
	Charged, InFlight, PendingReview, Refunded int64
}

// BudgetAdmission runs inside the task transaction: the tree is locked, its
// usage is read, Check decides, and the order is bound to GrantID before the
// transaction commits. Check carries the policy; the repository only persists.
type BudgetAdmission struct {
	RootID, GrantID string
	Check           func(nodes []BudgetNodeRow) error
}

// ErrBudgetGrantChanged means a conditional grant write lost its race;
// ErrBudgetGrantMissing means the tree root to lock is not the owner's.
var (
	ErrBudgetGrantChanged = errors.New("budget grant changed")
	ErrBudgetGrantMissing = errors.New("budget grant not found")
)

// lockBudgetTree is the first statement of every budget write: a no-op update
// of the root takes PostgreSQL's row lock and SQLite's writer lock, so every
// allocation and charge of one tree is serialized.
func lockBudgetTree(tx *gorm.DB, ownerID, rootID string) error {
	locked := tx.Exec("UPDATE platform_budget_grants SET version = version WHERE id = ? AND owner_id = ?", rootID, ownerID)
	if locked.Error != nil {
		return locked.Error
	}
	if locked.RowsAffected != 1 {
		return ErrBudgetGrantMissing
	}
	return nil
}

func budgetTreeRows(tx *gorm.DB, ownerID, rootID string) ([]BudgetNodeRow, error) {
	grants := []model.PlatformBudgetGrant{}
	if err := tx.Where("owner_id = ? AND root_id = ?", ownerID, rootID).Find(&grants).Error; err != nil {
		return nil, err
	}
	type orderUsage struct {
		GrantID  string
		Status   model.BillingStatus
		Amount   int64
		Reserved int64
		Actual   int64
		Refunded int64
	}
	usage := []orderUsage{}
	err := tx.Table("platform_budget_charges AS c").Joins("JOIN billing_orders AS o ON o.id = c.billing_order_id").
		Select("c.grant_id, o.status, o.amount_microcredits AS amount, o.reserved_amount_microcredits AS reserved, o.actual_amount_microcredits AS actual, o.refunded_amount_microcredits AS refunded").
		Where("c.owner_id = ? AND c.root_id = ?", ownerID, rootID).Scan(&usage).Error
	if err != nil {
		return nil, err
	}
	rows := make(map[string]*BudgetNodeRow, len(grants))
	out := make([]BudgetNodeRow, len(grants))
	for index, grant := range grants {
		out[index] = BudgetNodeRow{PlatformBudgetGrant: grant}
		rows[grant.ID] = &out[index]
	}
	for _, item := range usage {
		row := rows[item.GrantID]
		if row == nil {
			continue
		}
		held := max(item.Reserved, item.Amount)
		switch item.Status {
		case model.BillingStatusSettled:
			row.Charged += item.Actual
			row.Refunded += item.Refunded
		case model.BillingStatusRefunded:
			row.Refunded += item.Refunded
		case model.BillingStatusUncertain:
			row.PendingReview += held
		default:
			row.InFlight += held
		}
	}
	return out, nil
}

// BudgetTree reads one tree without locking it (for views).
func (r *Repository) BudgetTree(ownerID, rootID string) ([]BudgetNodeRow, error) {
	return budgetTreeRows(r.db, ownerID, rootID)
}

func (r *Repository) BudgetGrantForOwner(ownerID, id string) (*model.PlatformBudgetGrant, error) {
	var grant model.PlatformBudgetGrant
	if err := r.db.First(&grant, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &grant, nil
}

func (r *Repository) budgetGrantByKey(tx *gorm.DB, ownerID, key string) (*model.PlatformBudgetGrant, error) {
	var grant model.PlatformBudgetGrant
	if err := tx.First(&grant, "owner_id = ? AND idempotency_key = ?", ownerID, key).Error; err != nil {
		return nil, err
	}
	return &grant, nil
}

// CreateBudgetRoot inserts a root grant, or returns the one stored under the key.
func (r *Repository) CreateBudgetRoot(grant *model.PlatformBudgetGrant) (*model.PlatformBudgetGrant, error) {
	created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(grant)
	if created.Error != nil {
		return nil, created.Error
	}
	if created.RowsAffected == 1 {
		return grant, nil
	}
	return r.budgetGrantByKey(r.db, grant.OwnerID, grant.IdempotencyKey)
}

// AllocateBudgetGrant inserts a child under the locked tree after check
// accepts it. A key already used by the owner returns the stored grant.
func (r *Repository) AllocateBudgetGrant(grant *model.PlatformBudgetGrant, check func(nodes []BudgetNodeRow) error) (*model.PlatformBudgetGrant, error) {
	result := grant
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockBudgetTree(tx, grant.OwnerID, grant.RootID); err != nil {
			return err
		}
		if stored, err := r.budgetGrantByKey(tx, grant.OwnerID, grant.IdempotencyKey); err == nil {
			result = stored
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		nodes, err := budgetTreeRows(tx, grant.OwnerID, grant.RootID)
		if err != nil {
			return err
		}
		if err := check(nodes); err != nil {
			return err
		}
		return tx.Create(grant).Error
	})
	if err != nil {
		// The same key may have been taken in another tree at the same time.
		if stored, lookupErr := r.budgetGrantByKey(r.db, grant.OwnerID, grant.IdempotencyKey); lookupErr == nil {
			return stored, nil
		}
		return nil, err
	}
	return result, nil
}

// CloseBudgetGrant closes an open grant at the expected version.
func (r *Repository) CloseBudgetGrant(ownerID, id string, expectedVersion int64, now time.Time) error {
	updated := r.db.Model(&model.PlatformBudgetGrant{}).
		Where("id = ? AND owner_id = ? AND status = ? AND version = ?", id, ownerID, "open", expectedVersion).
		Updates(map[string]any{"status": "closed", "closed_at": now, "version": gorm.Expr("version + 1"), "updated_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrBudgetGrantChanged
	}
	return nil
}

// admitBudgetCharge locks and checks the tree; it must run before any other
// statement of the task transaction.
func admitBudgetCharge(tx *gorm.DB, ownerID string, budget *BudgetAdmission) error {
	if err := lockBudgetTree(tx, ownerID, budget.RootID); err != nil {
		return err
	}
	nodes, err := budgetTreeRows(tx, ownerID, budget.RootID)
	if err != nil {
		return err
	}
	return budget.Check(nodes)
}

func bindBudgetCharge(tx *gorm.DB, ownerID, taskID string, order *model.BillingOrder, budget *BudgetAdmission) error {
	charge := model.PlatformBudgetCharge{ID: "free:" + taskID + ":" + newRepositoryID(), OwnerID: ownerID, RootID: budget.RootID, GrantID: budget.GrantID, TaskID: taskID}
	if order != nil {
		charge.ID, charge.BillingOrderID = order.ID, order.ID
	}
	return tx.Create(&charge).Error
}

// CreateTaskUnderBudget is CreateTaskWithCreditReservation with the order
// admitted against and bound to a budget grant in the same transaction.
func (r *Repository) CreateTaskUnderBudget(task *model.Task, order *model.BillingOrder, activeTaskLimit int, budget *BudgetAdmission, submission ...*model.PlatformSubmission) error {
	return r.Transaction(func(repo *Repository) error {
		if err := admitBudgetCharge(repo.db, task.UserID, budget); err != nil {
			return err
		}
		if err := r.createTaskTx(repo, task, order, activeTaskLimit, submission); err != nil {
			return err
		}
		return bindBudgetCharge(repo.db, task.UserID, task.ID, order, budget)
	})
}
