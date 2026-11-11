package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

var (
	// ErrArkPortraitSessionClosed 表示认证会话已被回调、轮询或过期清理结束，本次调用不再改动。
	ErrArkPortraitSessionClosed = errors.New("认证会话已结束")
	// ErrArkPortraitGroupTaken 表示同一方舟素材组已登记在其他账号名下。
	ErrArkPortraitGroupTaken = errors.New("该素材组已被其他账号登记")
)

const (
	arkPortraitSessionPending = "pending"
	arkPortraitSessionDone    = "verified"
)

// CreateArkPortraitSessionWithReservation 在同一事务中冻结素材组费用并登记认证会话；余额不足时两者都不写入。
func (r *Repository) CreateArkPortraitSessionWithReservation(session *model.ArkPortraitSession, order *model.BillingOrder) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if order != nil {
			if err := reserveBillingOrder(tx, order); err != nil {
				return err
			}
		}
		return tx.Create(session).Error
	})
}

func (r *Repository) SetArkPortraitSessionToken(id string, cipher string) error {
	return r.db.Model(&model.ArkPortraitSession{}).Where("id = ? AND status = ?", id, arkPortraitSessionPending).Updates(map[string]any{"byted_token_cipher": cipher, "updated_at": time.Now()}).Error
}

func (r *Repository) ArkPortraitSessionByState(state string) (*model.ArkPortraitSession, error) {
	var session model.ArkPortraitSession
	if err := r.db.Where("state = ?", state).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *Repository) ArkPortraitSessionForUser(userID, id string) (*model.ArkPortraitSession, error) {
	var session model.ArkPortraitSession
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// PendingArkPortraitSessions 返回用户在指定范围内尚未过期的待认证会话，按创建时间倒序。
func (r *Repository) PendingArkPortraitSessions(userID string, scope string, now time.Time, limit int) ([]model.ArkPortraitSession, error) {
	var sessions []model.ArkPortraitSession
	err := r.db.Where("user_id = ? AND scope = ? AND status = ? AND expires_at > ?", userID, scope, arkPortraitSessionPending, now).Order("created_at desc").Limit(limit).Find(&sessions).Error
	return sessions, err
}

func (r *Repository) CountRecentArkPortraitSessions(userID string, since time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&model.ArkPortraitSession{}).Where("user_id = ? AND created_at > ?", userID, since).Count(&count).Error
	return count, err
}

// CountActiveArkPortraitUserSessions 统计用户自建、仍在有效期内的待认证会话；它们占用真人素材组额度。
func (r *Repository) CountActiveArkPortraitUserSessions(userID string, now time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&model.ArkPortraitSession{}).Where("user_id = ? AND scope = ? AND status = ? AND expires_at > ?", userID, "user", arkPortraitSessionPending, now).Count(&count).Error
	return count, err
}

func (r *Repository) ExpiredPendingArkPortraitSessions(now time.Time, limit int) ([]model.ArkPortraitSession, error) {
	var sessions []model.ArkPortraitSession
	err := r.db.Where("status = ? AND expires_at <= ?", arkPortraitSessionPending, now).Order("expires_at asc").Limit(limit).Find(&sessions).Error
	return sessions, err
}

// FinishArkPortraitSession 只结束仍为 pending 的会话，并在同一事务里退回冻结的素材组费用；
// 回调、轮询、取消与过期清理并发时只有一方生效，返回值表示本次调用是否生效。
func (r *Repository) FinishArkPortraitSession(id, status, resultCode, message, refundNote string) (bool, error) {
	finished := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var session model.ArkPortraitSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", id).Error; err != nil {
			return err
		}
		if session.Status != arkPortraitSessionPending {
			return nil
		}
		result := tx.Model(&model.ArkPortraitSession{}).Where("id = ? AND status = ?", id, arkPortraitSessionPending).Updates(map[string]any{
			"status": status, "result_code": resultCode, "error": message, "updated_at": time.Now(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		finished = true
		if session.BillingOrderID == "" {
			return nil
		}
		return refundArkGroupOrder(tx, session.BillingOrderID, refundNote)
	})
	return finished, err
}

// CompleteArkPortraitSession 把认证成功的会话登记为素材组并结算冻结费用，三者在同一事务里完成。
// 会话已由并发调用完成时返回已登记的素材组；已失败、过期或取消时返回 ErrArkPortraitSessionClosed。
func (r *Repository) CompleteArkPortraitSession(sessionID string, group *model.ArkPortraitGroup, settleNote string) (*model.ArkPortraitGroup, error) {
	var registered *model.ArkPortraitGroup
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var session model.ArkPortraitSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", sessionID).Error; err != nil {
			return err
		}
		if session.Status == arkPortraitSessionDone {
			var existing model.ArkPortraitGroup
			if err := tx.Where("project_name = ? AND group_id = ?", session.ProjectName, session.GroupID).First(&existing).Error; err != nil {
				return err
			}
			registered = &existing
			return nil
		}
		if session.Status != arkPortraitSessionPending {
			return ErrArkPortraitSessionClosed
		}
		created, err := createArkPortraitGroup(tx, group)
		if err != nil {
			return err
		}
		registered = created
		if err := tx.Model(&model.ArkPortraitSession{}).Where("id = ?", sessionID).Updates(map[string]any{
			"status": arkPortraitSessionDone, "result_code": "10000", "group_id": group.GroupID, "error": "", "updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}
		if session.BillingOrderID == "" {
			return nil
		}
		return settleArkGroupOrder(tx, session.BillingOrderID, settleNote)
	})
	return registered, err
}

// CreateArkPortraitGroupWithCharge 登记已在方舟创建的素材组，并结算创建前冻结的费用。
func (r *Repository) CreateArkPortraitGroupWithCharge(group *model.ArkPortraitGroup, settleNote string) (*model.ArkPortraitGroup, error) {
	var registered *model.ArkPortraitGroup
	err := r.db.Transaction(func(tx *gorm.DB) error {
		created, err := createArkPortraitGroup(tx, group)
		if err != nil {
			return err
		}
		registered = created
		if group.BillingOrderID == "" {
			return nil
		}
		return settleArkGroupOrder(tx, group.BillingOrderID, settleNote)
	})
	return registered, err
}

// createArkPortraitGroup 以 (ProjectName, GroupID) 去重；同一账号重复登记返回已有记录，其他账号已登记时拒绝。
func createArkPortraitGroup(tx *gorm.DB, group *model.ArkPortraitGroup) (*model.ArkPortraitGroup, error) {
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_name"}, {Name: "group_id"}},
		DoNothing: true,
	}).Create(group)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 1 {
		return group, nil
	}
	var existing model.ArkPortraitGroup
	if err := tx.Where("project_name = ? AND group_id = ?", group.ProjectName, group.GroupID).First(&existing).Error; err != nil {
		return nil, err
	}
	if existing.UserID != group.UserID || existing.Scope != group.Scope {
		return nil, ErrArkPortraitGroupTaken
	}
	return &existing, nil
}

// RefundArkGroupBillingOrder 退回未完成的素材组费用；订单已结算或已退回时不重复处理。
func (r *Repository) RefundArkGroupBillingOrder(orderID string, note string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		return refundArkGroupOrder(tx, orderID, note)
	})
}

// StaleArkGroupBillingOrders 返回长时间仍处于冻结状态、且没有待认证会话引用的素材组订单
// （例如创建虚拟组时进程中断）。真人认证会话由过期清理负责退回，不在这里重复处理。
func (r *Repository) StaleArkGroupBillingOrders(before time.Time, limit int) ([]model.BillingOrder, error) {
	var orders []model.BillingOrder
	pending := r.db.Model(&model.ArkPortraitSession{}).Select("billing_order_id").Where("status = ? AND billing_order_id <> ''", arkPortraitSessionPending)
	err := r.db.Where("scene = ? AND status = ? AND created_at < ? AND id NOT IN (?)", model.BillingSceneArkAssetGroup, model.BillingStatusReserved, before, pending).
		Order("created_at asc").Limit(limit).Find(&orders).Error
	return orders, err
}

func settleArkGroupOrder(tx *gorm.DB, orderID string, note string) error {
	var order model.BillingOrder
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", orderID).Error; err != nil {
		return err
	}
	if order.Status == model.BillingStatusSettled {
		return nil
	}
	if order.Scene != model.BillingSceneArkAssetGroup || (order.Status != model.BillingStatusReserved && order.Status != model.BillingStatusRunning) {
		return ErrBillingStateConflict
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
	var account model.CreditAccount
	if err := tx.First(&account, "user_id = ?", order.UserID).Error; err != nil {
		return err
	}
	now := time.Now()
	if err := tx.Model(&model.BillingOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
		"status": model.BillingStatusSettled, "actual_amount_microcredits": order.AmountMicrocredits, "settled_at": &now, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return tx.Create(&model.CreditLedgerEntry{
		ID:                         newRepositoryID(),
		UserID:                     order.UserID,
		Type:                       model.CreditLedgerConsume,
		AmountMicrocredits:         -order.AmountMicrocredits,
		ReservedDeltaMicrocredits:  -order.AmountMicrocredits,
		AvailableAfterMicrocredits: account.AvailableMicrocredits,
		ReservedAfterMicrocredits:  account.ReservedMicrocredits,
		BillingOrderID:             order.ID,
		Model:                      order.Model,
		Scene:                      order.Scene,
		Note:                       truncateArkNote(note),
	}).Error
}

func refundArkGroupOrder(tx *gorm.DB, orderID string, note string) error {
	var order model.BillingOrder
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", orderID).Error; err != nil {
		return err
	}
	if order.Status == model.BillingStatusRefunded || order.Status == model.BillingStatusSettled {
		return nil
	}
	if order.Scene != model.BillingSceneArkAssetGroup {
		return ErrBillingStateConflict
	}
	updated := tx.Model(&model.CreditAccount{}).
		Where("user_id = ? AND reserved_microcredits >= ?", order.UserID, order.AmountMicrocredits).
		Updates(map[string]any{
			"available_microcredits": gorm.Expr("available_microcredits + ?", order.AmountMicrocredits),
			"reserved_microcredits":  gorm.Expr("reserved_microcredits - ?", order.AmountMicrocredits),
			"version":                gorm.Expr("version + 1"),
			"updated_at":             time.Now(),
		})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return errors.New("reserved credit balance is inconsistent")
	}
	var account model.CreditAccount
	if err := tx.First(&account, "user_id = ?", order.UserID).Error; err != nil {
		return err
	}
	now := time.Now()
	note = truncateArkNote(note)
	if err := tx.Model(&model.BillingOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
		"status": model.BillingStatusRefunded, "error": note, "refunded_amount_microcredits": order.AmountMicrocredits, "refunded_at": &now, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return tx.Create(&model.CreditLedgerEntry{
		ID:                         newRepositoryID(),
		UserID:                     order.UserID,
		Type:                       model.CreditLedgerRefund,
		AmountMicrocredits:         order.AmountMicrocredits,
		AvailableDeltaMicrocredits: order.AmountMicrocredits,
		ReservedDeltaMicrocredits:  -order.AmountMicrocredits,
		AvailableAfterMicrocredits: account.AvailableMicrocredits,
		ReservedAfterMicrocredits:  account.ReservedMicrocredits,
		BillingOrderID:             order.ID,
		Model:                      order.Model,
		Scene:                      order.Scene,
		Note:                       note,
	}).Error
}

func truncateArkNote(note string) string {
	runes := []rune(strings.TrimSpace(note))
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return string(runes)
}

// ArkPortraitUserGroups 返回用户自建的素材组。
