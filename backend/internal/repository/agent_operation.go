package repository

import (
	"errors"
	"time"

	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// ErrAgentOperationFinished means the receipt already carries a succeeded
// outcome; a later attempt for the same operation must not overwrite it.
var ErrAgentOperationFinished = errors.New("agent tool operation already finished")

// AgentOperation returns one execution receipt of the owner.
func (r *Repository) AgentOperation(ownerID, operationKey string) (*model.AgentOperation, error) {
	row := &model.AgentOperation{}
	err := r.db.First(row, "owner_id = ? AND operation_key = ?", ownerID, operationKey).Error
	if err != nil {
		return nil, err
	}
	return row, nil
}

// InsertAgentOperation writes a pending receipt; the first attempt for an
// operation key wins and later ones find the stored row.
func (r *Repository) InsertAgentOperation(row *model.AgentOperation) (bool, error) {
	created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if created.Error != nil {
		return false, created.Error
	}
	return created.RowsAffected == 1, nil
}

// FinishAgentOperation records the outcome of the owner's pending
// attempt. A succeeded receipt is never overwritten, so a racing loser cannot
// replace the winner's result.
func (r *Repository) FinishAgentOperation(ownerID, operationKey, status, resultJSON, operationRef string, now time.Time) error {
	updated := r.db.Model(&model.AgentOperation{}).
		Where("owner_id = ? AND operation_key = ? AND status <> ?", ownerID, operationKey, "succeeded").
		Updates(map[string]any{"status": status, "result_json": resultJSON, "operation_ref": operationRef, "updated_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected == 0 {
		return ErrAgentOperationFinished
	}
	return nil
}

// AgentTurnContract returns the frozen contract of one turn key.
func (r *Repository) AgentTurnContract(ownerID, turnKey string) (*model.AgentTurnContract, error) {
	row := &model.AgentTurnContract{}
	err := r.db.First(row, "owner_id = ? AND turn_key = ?", ownerID, turnKey).Error
	if err != nil {
		return nil, err
	}
	return row, nil
}

// InsertAgentTurnContract freezes a turn contract once; a concurrent freezer
// loses and reads the stored row.
func (r *Repository) InsertAgentTurnContract(row *model.AgentTurnContract) (bool, error) {
	created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if created.Error != nil {
		return false, created.Error
	}
	return created.RowsAffected == 1, nil
}

// AgentOperationsForWork lists the execution receipts of one work item,
// newest first; the work view and audits read them.
func (r *Repository) AgentOperationsForWork(ownerID, workID string, limit int) ([]model.AgentOperation, error) {
	rows := []model.AgentOperation{}
	err := r.db.Where("owner_id = ? AND work_item_id = ?", ownerID, workID).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
