package repository

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

// CommandTransaction makes all document, version, receipt, event, reference and
// history writes composable. Callers must not start a nested write transaction.
func (r *Repository) CommandTransaction(ctx context.Context, fn func(*Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(r.transactionRepository(tx)) })
}

// CanvasAtomicWrite joins an Agent checkpoint transaction when present. Direct
// callers get the same account/canvas lock and atomic document/receipt boundary.
func (r *Repository) CanvasAtomicWrite(ctx context.Context, ownerID, canvasID string, fn func(*Repository) error) error {
	apply := func(tx *Repository) error {
		if err := tx.LockCanvasWrite(ownerID, canvasID); err != nil {
			return err
		}
		return fn(tx)
	}
	if _, active := r.db.Statement.ConnPool.(gorm.TxCommitter); active {
		return apply(r)
	}
	return r.CommandTransaction(ctx, apply)
}
func (r *Repository) CommandEpoch(ownerID, canvasID string) (*model.CanvasDocumentEpoch, error) {
	var row model.CanvasDocumentEpoch
	err := r.db.Where("owner_id = ? AND canvas_id = ?", ownerID, canvasID).First(&row).Error
	return &row, err
}
func (r *Repository) SaveCommandEpoch(row *model.CanvasDocumentEpoch) error {
	return r.db.Save(row).Error
}
func (r *Repository) CommandVersions(canvasID, epoch string) (map[string]model.CanvasEntityVersion, error) {
	rows := []model.CanvasEntityVersion{}
	err := r.db.Where("canvas_id = ? AND epoch = ?", canvasID, epoch).Find(&rows).Error
	values := map[string]model.CanvasEntityVersion{}
	for _, v := range rows {
		values[v.EntityKey] = v
	}
	return values, err
}
func (r *Repository) SaveCommandVersions(rows []model.CanvasEntityVersion, recreated ...map[string]bool) error {
	if len(rows) == 0 {
		return nil
	}
	// Every caller holds the durable account/canvas lock. Centralizing births
	// here covers commands, undo/redo, Host writes, legacy saves and restores.
	type scope struct{ canvasID, epoch string }
	prior := map[scope]map[string]model.CanvasEntityVersion{}
	for i := range rows {
		row := &rows[i]
		key := scope{row.CanvasID, row.Epoch}
		if _, loaded := prior[key]; !loaded {
			values, err := r.CommandVersions(row.CanvasID, row.Epoch)
			if err != nil {
				return err
			}
			prior[key] = values
		}
		before, exists := prior[key][row.EntityKey]
		row.Generation = before.Generation
		forcedBirth := len(recreated) == 1 && recreated[0][row.EntityKey] && !row.Deleted
		if !exists || (before.Deleted && !row.Deleted) || forcedBirth {
			row.Generation = row.Version
		}
	}
	return r.db.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(rows, 200).Error
}
func (r *Repository) CommandReceipt(key model.CanvasCommandReceipt) (*model.CanvasCommandReceipt, error) {
	var row model.CanvasCommandReceipt
	err := r.db.Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND operation_id = ?", key.OwnerID, key.CanvasID, key.Epoch, key.ActorID, key.ClientID, key.OperationID).First(&row).Error
	return &row, err
}

func (r *Repository) CommandReceiptAnyEpoch(key model.CanvasCommandReceipt) (*model.CanvasCommandReceipt, error) {
	var row model.CanvasCommandReceipt
	err := r.db.Where("owner_id = ? AND canvas_id = ? AND actor_id = ? AND client_id = ? AND operation_id = ?", key.OwnerID, key.CanvasID, key.ActorID, key.ClientID, key.OperationID).Order("created_at desc").First(&row).Error
	return &row, err
}
func (r *Repository) InsertCommandReceipt(row *model.CanvasCommandReceipt) error {
	return r.db.Create(row).Error
}
func (r *Repository) InsertCommandEvent(row *model.CanvasCommandEvent) error {
	return r.db.Create(row).Error
}
func (r *Repository) CommandEvents(ownerID, canvasID string, after int64, limit int) ([]model.CanvasCommandEvent, error) {
	rows := []model.CanvasCommandEvent{}
	err := r.db.Where("owner_id = ? AND canvas_id = ? AND sequence > ?", ownerID, canvasID, after).Order("sequence ASC").Limit(limit).Find(&rows).Error
	return rows, err
}
func (r *Repository) SaveActorHistory(row *model.CanvasActorHistory) error {
	return r.db.Save(row).Error
}
func (r *Repository) ActorHistoryForCommand(key model.CanvasCommandReceipt, sequence int64) (*model.CanvasActorHistory, error) {
	var row model.CanvasActorHistory
	err := r.db.Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND operation_id = ? AND sequence = ?", key.OwnerID, key.CanvasID, key.Epoch, key.ActorID, key.ClientID, key.OperationID, sequence).First(&row).Error
	return &row, err
}
func (r *Repository) ActorHistory(ownerID, canvasID, epoch, actorID, clientID, state string) (*model.CanvasActorHistory, error) {
	var row model.CanvasActorHistory
	order := "sequence DESC"
	if state == "undone" {
		order = "sequence ASC"
	}
	err := r.db.Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND state = ?", ownerID, canvasID, epoch, actorID, clientID, state).Order(order).First(&row).Error
	return &row, err
}
func (r *Repository) AbandonRedo(ownerID, canvasID, epoch, actorID, clientID string) error {
	return r.db.Model(&model.CanvasActorHistory{}).Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND state = ?", ownerID, canvasID, epoch, actorID, clientID, "undone").Update("state", "abandoned").Error
}
func (r *Repository) SaveCommandResources(ownerID, canvasID, epoch, operationID string, current, history []string) error {
	if err := r.db.Where("canvas_id = ? AND kind = ?", canvasID, "current").Delete(&model.CanvasCommandResource{}).Error; err != nil {
		return err
	}
	rows := []model.CanvasCommandResource{}
	seen := map[string]bool{}
	for _, id := range current {
		if seen[id] {
			continue
		}
		seen[id] = true
		rows = append(rows, model.CanvasCommandResource{OwnerID: ownerID, CanvasID: canvasID, Epoch: epoch, OperationID: "", Kind: "current", ResourceID: id})
	}
	seen = map[string]bool{}
	for _, id := range history {
		if seen[id] {
			continue
		}
		seen[id] = true
		rows = append(rows, model.CanvasCommandResource{OwnerID: ownerID, CanvasID: canvasID, Epoch: epoch, OperationID: operationID, Kind: "history", ResourceID: id})
	}
	if len(rows) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 200).Error
}
func (r *Repository) CommandCreateReceipt(key model.CanvasCreateReceipt) (*model.CanvasCreateReceipt, error) {
	var row model.CanvasCreateReceipt
	err := r.db.Where("owner_id = ? AND actor_id = ? AND client_id = ? AND operation_id = ?", key.OwnerID, key.ActorID, key.ClientID, key.OperationID).First(&row).Error
	return &row, err
}
func (r *Repository) InsertCommandCreateReceipt(row *model.CanvasCreateReceipt) error {
	return r.db.Create(row).Error
}
func (r *Repository) CreateCommandCanvas(row *model.CanvasProject) error {
	if err := r.LockCanvasWrite(row.UserID, row.ID); err != nil {
		return err
	}
	if err := r.checkCanvasWriteQuota(nil, row); err != nil {
		return err
	}
	return r.db.Create(row).Error
}
func (r *Repository) PersistCommandCanvas(row *model.CanvasProject, expected int64) error {
	if err := r.LockCanvasWrite(row.UserID, row.ID); err != nil {
		return err
	}
	before, err := r.CanvasProjectForUser(row.UserID, row.ID)
	if err != nil {
		if r.IsMissing(err) {
			return ErrCanvasRevisionConflict
		}
		return err
	}
	if before.Revision != expected {
		return ErrCanvasRevisionConflict
	}
	if err = r.checkCanvasWriteQuota(before, row); err != nil {
		return err
	}
	result := r.db.Model(&model.CanvasProject{}).Where("id = ? AND user_id = ? AND revision = ?", row.ID, row.UserID, expected).Updates(map[string]any{"payload_json": row.PayloadJSON, "revision": row.Revision, "title": row.Title, "updated_at": row.UpdatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCanvasRevisionConflict
	}
	return nil
}
func (r *Repository) IsMissing(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
func (r *Repository) UpdateCommandReceiptEvent(key model.CanvasCommandReceipt, encoded string, sequence int64) error {
	if err := r.db.Model(&model.CanvasCommandReceipt{}).Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND operation_id = ?", key.OwnerID, key.CanvasID, key.Epoch, key.ActorID, key.ClientID, key.OperationID).Update("receipt_json", encoded).Error; err != nil {
		return err
	}
	return r.db.Model(&model.CanvasCommandEvent{}).Where("canvas_id = ? AND sequence = ?", key.CanvasID, sequence).Update("event_json", encoded).Error
}
