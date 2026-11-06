package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
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

// SaveAgentEcommerceBatch upserts the durable batch record of one space.
func (r *Repository) SaveAgentEcommerceBatch(record *model.AgentEcommerceBatch) error {
	updated := r.db.Model(&model.AgentEcommerceBatch{}).
		Where("owner_id = ? AND batch_id = ?", record.OwnerID, record.BatchID).
		Updates(map[string]any{"product_json": record.ProductJSON, "preset_json": record.PresetJSON, "updated_at": time.Now()})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected == 1 {
		return nil
	}
	return r.db.Create(record).Error
}

// AgentEcommerceBatch returns one durable batch record.
func (r *Repository) AgentEcommerceBatch(ownerID, batchID string) (*model.AgentEcommerceBatch, error) {
	row := &model.AgentEcommerceBatch{}
	err := r.db.First(row, "owner_id = ? AND batch_id = ?", ownerID, batchID).Error
	if err != nil {
		return nil, err
	}
	return row, nil
}

const batchRecordKeySeparator = "\x00"

// AppendAgentEcommerceBatchTask records a submitted task under the batch,
// creating the record when it does not exist yet.
func (r *Repository) AppendAgentEcommerceBatchTask(ownerID, batchID, taskID string) error {
	record, err := r.AgentEcommerceBatch(ownerID, batchID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		sum := sha256.Sum256([]byte(ownerID + batchRecordKeySeparator + batchID))
		record := &model.AgentEcommerceBatch{ID: "aeb" + hex.EncodeToString(sum[:16]), OwnerID: ownerID, BatchID: batchID, TaskRefsJSON: `["` + taskID + `"]`}
		created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(record)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 1 {
			return nil
		}
		return r.AppendAgentEcommerceBatchTask(ownerID, batchID, taskID)
	}
	if err != nil {
		return err
	}
	refs := []string{}
	if record.TaskRefsJSON != "" {
		if jsonErr := json.Unmarshal([]byte(record.TaskRefsJSON), &refs); jsonErr != nil {
			refs = []string{}
		}
	}
	for _, existing := range refs {
		if existing == taskID {
			return nil
		}
	}
	refs = append(refs, taskID)
	encoded, _ := json.Marshal(refs)
	return r.db.Model(record).Update("task_refs_json", string(encoded)).Error
}

// AgentWorksWithSubmittedTasks lists the works of one owner whose tool
// operations reference submitted tasks.
func (r *Repository) AgentWorksWithSubmittedTasks(ownerID string) ([]model.AgentWorkItem, error) {
	rows := []model.AgentWorkItem{}
	err := r.db.Where("owner_id = ? AND id IN (SELECT DISTINCT work_item_id FROM agent_operations WHERE owner_id = ? AND operation_ref LIKE ?)", ownerID, ownerID, "task:%").Find(&rows).Error
	return rows, err
}

// OwnersWithSubmittedTasks lists owners that have any task-carrying operation;
// the runner reconciles their in-flight works after each pass.
func (r *Repository) OwnersWithSubmittedTasks(limit int) ([]string, error) {
	owners := []string{}
	err := r.db.Model(&model.AgentOperation{}).Where("operation_ref LIKE ?", "task:%").Distinct("owner_id").Limit(limit).Pluck("owner_id", &owners).Error
	return owners, err
}
