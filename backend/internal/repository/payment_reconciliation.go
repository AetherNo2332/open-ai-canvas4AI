package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

func (r *Repository) BeginPaymentReconciliation(run *model.PaymentReconciliationRun) (*model.PaymentReconciliationRun, bool, error) {
	if run == nil {
		return nil, false, errors.New("payment reconciliation run is nil")
	}
	var current model.PaymentReconciliationRun
	started := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("provider_id = ? AND bill_date = ?", run.ProviderID, run.BillDate).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			created := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "provider_id"}, {Name: "bill_date"}},
				DoNothing: true,
			}).Create(run)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected == 1 {
				current = *run
				started = true
				return nil
			}
			// Another worker won the unique-key race. Reload and apply the
			// same running/cooldown rules instead of surfacing a conflict.
			if reloadErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("provider_id = ? AND bill_date = ?", run.ProviderID, run.BillDate).
				First(&current).Error; reloadErr != nil {
				return reloadErr
			}
			err = nil
		}
		if err != nil {
			return err
		}
		if current.Status == model.PaymentReconciliationRunning && current.UpdatedAt.After(time.Now().Add(-30*time.Minute)) {
			return nil
		}
		if err := tx.Where("run_id = ?", current.ID).Delete(&model.PaymentReconciliationItem{}).Error; err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&model.PaymentReconciliationRun{}).Where("id = ?", current.ID).Updates(map[string]any{
			"config_id": run.ConfigID, "status": model.PaymentReconciliationRunning,
			"total_items": 0, "match_items": 0, "recovered_items": 0, "error_items": 0,
			"error": "", "started_by": run.StartedBy, "started_at": now, "completed_at": nil,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		started = true
		return tx.First(&current, "id = ?", current.ID).Error
	})
	return &current, started, err
}

func (r *Repository) CompletePaymentReconciliation(id string, items []model.PaymentReconciliationItem, matched, recovered, failed int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if len(items) > 0 {
			if err := tx.CreateInBatches(items, 200).Error; err != nil {
				return err
			}
		}
		now := time.Now()
		updated := tx.Model(&model.PaymentReconciliationRun{}).
			Where("id = ? AND status = ?", strings.TrimSpace(id), model.PaymentReconciliationRunning).
			Updates(map[string]any{
				"status": model.PaymentReconciliationCompleted, "total_items": len(items),
				"match_items": matched, "recovered_items": recovered, "error_items": failed,
				"error": "", "completed_at": &now, "updated_at": now,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("payment reconciliation run is no longer running")
		}
		return nil
	})
}

func (r *Repository) FailPaymentReconciliation(id, message string) error {
	now := time.Now()
	return r.db.Model(&model.PaymentReconciliationRun{}).Where("id = ?", strings.TrimSpace(id)).Updates(map[string]any{
		"status": model.PaymentReconciliationFailed, "error": message,
		"completed_at": &now, "updated_at": now,
	}).Error
}

func (r *Repository) PaymentReconciliationRun(id string) (*model.PaymentReconciliationRun, error) {
	var run model.PaymentReconciliationRun
	return &run, r.db.First(&run, "id = ?", strings.TrimSpace(id)).Error
}

func (r *Repository) PaymentReconciliationRunByDate(providerID, billDate string) (*model.PaymentReconciliationRun, error) {
	var run model.PaymentReconciliationRun
	return &run, r.db.First(&run, "provider_id = ? AND bill_date = ?", strings.TrimSpace(providerID), strings.TrimSpace(billDate)).Error
}

func (r *Repository) AdminPaymentReconciliationRuns(filter PaymentReconciliationFilter, limit, offset int) ([]model.PaymentReconciliationRun, int64, error) {
	var runs []model.PaymentReconciliationRun
	var total int64
	query := r.paymentReconciliationQuery(filter)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("bill_date desc, started_at desc, id desc").Limit(limit).Offset(offset).Find(&runs).Error
	return runs, total, err
}

func (r *Repository) paymentReconciliationQuery(filter PaymentReconciliationFilter) *gorm.DB {
	query := r.db.Model(&model.PaymentReconciliationRun{})
	if normalized := strings.TrimSpace(filter.ProviderID); normalized != "" && normalized != "all" {
		query = query.Where("provider_id = ?", normalized)
	}
	if normalized := strings.TrimSpace(filter.Status); normalized != "" && normalized != "all" {
		query = query.Where("status = ?", normalized)
	}
	if filter.From != "" {
		query = query.Where("bill_date >= ?", filter.From)
	}
	if filter.To != "" {
		query = query.Where("bill_date <= ?", filter.To)
	}
	return query
}

func (r *Repository) PaymentReconciliationItems(runID, result string, limit, offset int) ([]model.PaymentReconciliationItem, int64, error) {
	var items []model.PaymentReconciliationItem
	var total int64
	query := r.paymentReconciliationItemsQuery(runID, result)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("resolved asc, created_at asc, id asc").Limit(limit).Offset(offset).Find(&items).Error
	return items, total, err
}

func (r *Repository) CreditedPaymentOrdersBetween(providerID string, start, end time.Time) ([]model.PaymentOrder, error) {
	var orders []model.PaymentOrder
	err := r.db.Where(
		"provider_id = ? AND status = ? AND provider_paid_at >= ? AND provider_paid_at < ?",
		strings.TrimSpace(providerID), model.PaymentOrderCredited, start, end,
	).Order("provider_paid_at asc").Find(&orders).Error
	return orders, err
}

// UnresolvedPaymentOrderCandidateCountOverlapping reports whether an order
// whose payment outcome is still ambiguous overlaps the bill day. Closed
// orders have already been confirmed unpaid by query/close, while credited
// orders are checked separately against the provider bill.
func (r *Repository) UnresolvedPaymentOrderCandidateCountOverlapping(providerID string, start, end time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&model.PaymentOrder{}).
		Where("provider_id = ? AND status IN ? AND created_at < ? AND expires_at >= ?", strings.TrimSpace(providerID), []model.PaymentOrderStatus{
			model.PaymentOrderCreated, model.PaymentOrderPending, model.PaymentOrderClosing, model.PaymentOrderCreateFailed,
		}, end, start).
		Count(&count).Error
	return count, err
}
