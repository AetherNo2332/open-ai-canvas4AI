package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// ErrAgentWorkChanged means a conditional work write lost its race.
var ErrAgentWorkChanged = errors.New("agent work changed")

// WorkTx is the transactional view the work service uses. Every state change
// and its outbox rows are written through one WorkTx, so they commit or roll
// back together.
type WorkTx struct{ db *gorm.DB }

// InAgentWork runs fn in one transaction. On PostgreSQL the root work row is
// locked first (when rootID is set) so writes to one work tree serialize.
func (r *Repository) InAgentWork(ownerID, rootID string, fn func(tx *WorkTx) error) error {
	return r.db.Transaction(func(db *gorm.DB) error {
		if rootID != "" {
			locked := db.Exec("UPDATE agent_work_items SET version = version WHERE id = ? AND owner_id = ?", rootID, ownerID)
			if locked.Error != nil {
				return locked.Error
			}
			if locked.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		return fn(&WorkTx{db: db})
	})
}

func (t *WorkTx) Work(ownerID, id string) (*model.AgentWorkItem, error) {
	var work model.AgentWorkItem
	if err := t.db.First(&work, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &work, nil
}

func (t *WorkTx) WorkByKey(ownerID, key string) (*model.AgentWorkItem, error) {
	var work model.AgentWorkItem
	if err := t.db.First(&work, "owner_id = ? AND idempotency_key = ?", ownerID, key).Error; err != nil {
		return nil, err
	}
	return &work, nil
}

// Descendants returns every work under rootID, in creation order.
func (t *WorkTx) Descendants(ownerID, rootID string) ([]model.AgentWorkItem, error) {
	rows := []model.AgentWorkItem{}
	err := t.db.Where("owner_id = ? AND root_work_id = ? AND id <> ?", ownerID, rootID, rootID).Order("created_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (t *WorkTx) CreateWork(work *model.AgentWorkItem) error { return t.db.Create(work).Error }

// UpdateWork applies fields at the expected version and bumps the version.
func (t *WorkTx) UpdateWork(ownerID, id string, expectedVersion int64, fields map[string]any) error {
	fields["version"], fields["updated_at"] = gorm.Expr("version + 1"), time.Now()
	updated := t.db.Model(&model.AgentWorkItem{}).Where("id = ? AND owner_id = ? AND version = ?", id, ownerID, expectedVersion).Updates(fields)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrAgentWorkChanged
	}
	return nil
}

func (t *WorkTx) Assignment(ownerID, id string) (*model.AgentAssignment, error) {
	var assignment model.AgentAssignment
	if err := t.db.First(&assignment, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &assignment, nil
}

func (t *WorkTx) LatestAssignment(ownerID, workID string) (*model.AgentAssignment, error) {
	var assignment model.AgentAssignment
	if err := t.db.Where("owner_id = ? AND work_item_id = ?", ownerID, workID).Order("epoch DESC").First(&assignment).Error; err != nil {
		return nil, err
	}
	return &assignment, nil
}

func (t *WorkTx) CreateAssignment(assignment *model.AgentAssignment) error {
	return t.db.Create(assignment).Error
}

// EndAssignment ends a live assignment; ending twice is a no-op.
func (t *WorkTx) EndAssignment(ownerID, id, reason string, now time.Time) error {
	return t.db.Model(&model.AgentAssignment{}).Where("id = ? AND owner_id = ? AND status <> ?", id, ownerID, "ended").
		Updates(map[string]any{"status": "ended", "end_reason": reason, "ended_at": now}).Error
}

// InsertReport stores a report once per (assignment, report ID); it reports
// whether the row is new.
func (t *WorkTx) InsertReport(report *model.AgentWorkReport) (bool, error) {
	created := t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(report)
	return created.RowsAffected == 1, created.Error
}

func (t *WorkTx) InsertInput(input *model.AgentWorkInput) (bool, error) {
	created := t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(input)
	return created.RowsAffected == 1, created.Error
}

func (t *WorkTx) Enqueue(item *model.AgentOutbox) error { return t.db.Create(item).Error }

// CancelPendingOutbox drops pending deliveries of the given work items.
func (t *WorkTx) CancelPendingOutbox(ownerID string, workIDs []string) error {
	if len(workIDs) == 0 {
		return nil
	}
	return t.db.Model(&model.AgentOutbox{}).Where("owner_id = ? AND work_item_id IN ? AND status = ?", ownerID, workIDs, "pending").Update("status", "cancelled").Error
}

// CreateSession inserts an executor Session in the work transaction, or
// returns the one stored under the same owner key.
func (t *WorkTx) CreateSession(session *model.AgentSession) (*model.AgentSession, error) {
	created := t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(session)
	if created.Error != nil {
		return nil, created.Error
	}
	if created.RowsAffected == 1 {
		return session, nil
	}
	var stored model.AgentSession
	err := t.db.First(&stored, "owner_id = ? AND idempotency_key = ?", session.OwnerID, session.IdempotencyKey).Error
	return &stored, err
}

func (r *Repository) AgentWorkReports(ownerID, workID string) ([]model.AgentWorkReport, error) {
	rows := []model.AgentWorkReport{}
	err := r.db.Where("owner_id = ? AND work_item_id = ?", ownerID, workID).Order("created_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

// PendingAgentOutbox lists undelivered items, oldest first.
func (r *Repository) PendingAgentOutbox(ownerID string, limit int) ([]model.AgentOutbox, error) {
	rows := []model.AgentOutbox{}
	err := r.db.Where("owner_id = ? AND status = ?", ownerID, "pending").Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// AllocateBudget allocates a child grant inside the work transaction. The
// work root is locked first and the budget root second, in every caller.
func (t *WorkTx) AllocateBudget(r *Repository, grant *model.PlatformBudgetGrant, check func(nodes []BudgetNodeRow) error) (*model.PlatformBudgetGrant, error) {
	return r.allocateBudgetGrantTx(t.db, grant, check)
}

// CreateBudgetRoot inserts a root grant in the work transaction.
func (t *WorkTx) CreateBudgetRoot(grant *model.PlatformBudgetGrant) error {
	return t.db.Create(grant).Error
}

// CloseBudgets closes the given open grants; closed grants stay closed.
func (t *WorkTx) CloseBudgets(ownerID string, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return t.db.Model(&model.PlatformBudgetGrant{}).Where("owner_id = ? AND id IN ? AND status = ?", ownerID, ids, "open").
		Updates(map[string]any{"status": "closed", "closed_at": now, "version": gorm.Expr("version + 1"), "updated_at": now}).Error
}

// AgentWorkForOwner reads one work item outside a transaction.
func (r *Repository) AgentWorkForOwner(ownerID, id string) (*model.AgentWorkItem, error) {
	return (&WorkTx{db: r.db}).Work(ownerID, id)
}

// AgentAssignmentForOwner reads one assignment outside a transaction.
func (r *Repository) AgentAssignmentForOwner(ownerID, id string) (*model.AgentAssignment, error) {
	return (&WorkTx{db: r.db}).Assignment(ownerID, id)
}

// AgentWorkByKey reads work by its owner idempotency key outside a transaction.
func (r *Repository) AgentWorkByKey(ownerID, key string) (*model.AgentWorkItem, error) {
	return (&WorkTx{db: r.db}).WorkByKey(ownerID, key)
}

// InsertApproval stores an approval once per (owner, Session, call).
func (t *WorkTx) InsertApproval(approval *model.AgentApproval) (*model.AgentApproval, error) {
	created := t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(approval)
	if created.Error != nil {
		return nil, created.Error
	}
	if created.RowsAffected == 1 {
		return approval, nil
	}
	var stored model.AgentApproval
	err := t.db.First(&stored, "owner_id = ? AND session_id = ? AND call_id = ?", approval.OwnerID, approval.SessionID, approval.CallID).Error
	return &stored, err
}

func (t *WorkTx) Approval(ownerID, id string) (*model.AgentApproval, error) {
	var approval model.AgentApproval
	if err := t.db.First(&approval, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &approval, nil
}

// SetApprovalStatus moves an approval from one status to another; a lost
// race returns ErrAgentWorkChanged.
func (t *WorkTx) SetApprovalStatus(ownerID, id, from, to string, now time.Time) error {
	fields := map[string]any{"status": to}
	switch to {
	case "approved", "rejected":
		fields["decided_at"] = now
	case "consumed":
		fields["consumed_at"] = now
	}
	updated := t.db.Model(&model.AgentApproval{}).Where("id = ? AND owner_id = ? AND status = ?", id, ownerID, from).Updates(fields)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrAgentWorkChanged
	}
	return nil
}

// AgentApprovalForOwner reads one approval outside a transaction.
func (r *Repository) AgentApprovalForOwner(ownerID, id string) (*model.AgentApproval, error) {
	return (&WorkTx{db: r.db}).Approval(ownerID, id)
}

// AgentApprovalsForWork lists approvals of a work item or of a whole tree
// when workID is a root.
func (r *Repository) AgentApprovalsForWork(ownerID, workID string, root bool) ([]model.AgentApproval, error) {
	rows := []model.AgentApproval{}
	query := r.db.Where("owner_id = ?", ownerID)
	if root {
		query = query.Where("root_work_id = ?", workID)
	} else {
		query = query.Where("work_item_id = ?", workID)
	}
	err := query.Order("created_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

// DeliverAgentOutbox appends one pending item to its target Session's history
// and marks it delivered, in one transaction. The message ID is the outbox ID,
// so a redelivered item is a no-op. The Session revision advances like any
// other append; no writer lease is needed because the inbox is the queue.
func (r *Repository) DeliverAgentOutbox(item model.AgentOutbox, message model.AgentSessionMessage, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		marked := tx.Model(&model.AgentOutbox{}).Where("id = ? AND owner_id = ? AND status = ?", item.ID, item.OwnerID, "pending").Updates(map[string]any{"status": "delivered", "delivered_at": now})
		if marked.Error != nil || marked.RowsAffected != 1 {
			return marked.Error
		}
		var exists int64
		if err := tx.Model(&model.AgentSessionMessage{}).Where("owner_id = ? AND session_id = ? AND message_id = ?", item.OwnerID, item.TargetSessionID, message.MessageID).Count(&exists).Error; err != nil || exists > 0 {
			return err
		}
		bumped := tx.Model(&model.AgentSession{}).Where("id = ? AND owner_id = ?", item.TargetSessionID, item.OwnerID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now})
		if bumped.Error != nil {
			return bumped.Error
		}
		if bumped.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var last int64
		if err := tx.Model(&model.AgentSessionMessage{}).Where("session_id = ?", item.TargetSessionID).Select("COALESCE(MAX(sequence), 0)").Scan(&last).Error; err != nil {
			return err
		}
		message.OwnerID, message.SessionID, message.Sequence, message.CreatedAt = item.OwnerID, item.TargetSessionID, last+1, now
		return tx.Create(&message).Error
	})
}

// AgentWorkInputByID reads one input for delivery.
func (r *Repository) AgentWorkInputByID(ownerID, id string) (*model.AgentWorkInput, error) {
	var input model.AgentWorkInput
	if err := r.db.First(&input, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &input, nil
}
