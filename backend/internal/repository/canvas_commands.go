package repository

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// CommandTransaction makes all document, version, receipt, event, reference and
// history writes composable. Callers must not start a nested write transaction.
func (r *Repository) CommandTransaction(ctx context.Context, fn func(*Repository) error) error {
	return r.transact(r.db.WithContext(ctx), fn)
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

// AgentActor identifies the Agent run behind a canvas write. It is attribution
// only; ownership and permissions are checked by the caller before persisting.
type AgentActor struct{ RunID, UserID string }

// SaveAgentCanvasWrite is the single persistence entry for Agent canvas writes
// (commands Execute/ExecuteHost/undo/restore and the Agent full-document
// writeback). It shares the browser path's revision sequence: lock, revision CAS
// against before.Revision, edit leases, then one UPDATE that bumps revision by 1.
//
// Lock order: account row, then canvas row (LockCanvasWrite). Browser writes lock
// only the canvas row and never the account afterwards, so no path holds the
// canvas while waiting for the account. Agent writes keep the account lock
// because quota and the Agent run/queue state are serialized on it.
//
// events: when the command layer already derived its events (with entity
// versions, receipts and actor history written by itself in the same
// transaction), they are inserted as given. When nil, the before/after diff is
// projected into an Agent-attributed command event, like a browser write.
// On success after.Revision holds the new revision.
func (r *Repository) SaveAgentCanvasWrite(before, after *model.CanvasProject, actor AgentActor, events []model.CanvasCommandEvent) error {
	if before == nil || after == nil || before.ID != after.ID || before.UserID != after.UserID {
		return errors.New("agent canvas write requires the same canvas before and after")
	}
	write := func(tx *Repository) error {
		if err := tx.LockCanvasWrite(after.UserID, after.ID); err != nil {
			return err
		}
		current, err := tx.CanvasProjectForUser(after.UserID, after.ID)
		if err != nil {
			if tx.IsMissing(err) {
				return ErrCanvasRevisionConflict
			}
			return err
		}
		expected := before.Revision
		if current.Revision != expected {
			return ErrCanvasRevisionConflict
		}
		if err = tx.checkCanvasEditLeasesFor(current, after, agentLeaseHolder); err != nil {
			return err
		}
		if err = tx.checkCanvasWriteQuota(current, after); err != nil {
			return err
		}
		result := tx.db.Model(&model.CanvasProject{}).Where("id = ? AND user_id = ? AND revision = ?", after.ID, after.UserID, expected).
			Updates(map[string]any{"project_id": after.ProjectID, "payload_json": after.PayloadJSON, "revision": expected + 1, "title": after.Title, "updated_at": after.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCanvasRevisionConflict
		}
		after.Revision = expected + 1
		// 浏览器的多端同步依赖画布变更通知；Agent 写入同样要触发，否则打开的画布感知不到。
		tx.canvasRevisionChanged(after.UserID, after.ID, after.Revision, CanvasChangeActorAgent)
		if events == nil {
			return tx.projectCanvasWrite(current, after, false, &actor)
		}
		for i := range events {
			if events[i].CanvasID != after.ID || events[i].OwnerID != after.UserID {
				return errors.New("agent canvas event does not belong to the written canvas")
			}
			if err = tx.InsertCommandEvent(&events[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if _, active := r.db.Statement.ConnPool.(gorm.TxCommitter); active {
		return write(r)
	}
	return r.Transaction(write)
}
func (r *Repository) IsMissing(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
func (r *Repository) UpdateCommandReceiptEvent(key model.CanvasCommandReceipt, encoded string, sequence int64) error {
	if err := r.db.Model(&model.CanvasCommandReceipt{}).Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND operation_id = ?", key.OwnerID, key.CanvasID, key.Epoch, key.ActorID, key.ClientID, key.OperationID).Update("receipt_json", encoded).Error; err != nil {
		return err
	}
	return r.db.Model(&model.CanvasCommandEvent{}).Where("canvas_id = ? AND sequence = ?", key.CanvasID, sequence).Update("event_json", encoded).Error
}
