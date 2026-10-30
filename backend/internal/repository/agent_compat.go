package repository

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// transactionRepository 让 Agent 仓储在调用方事务里复用当前仓储的配置
// （画布变更通知、编辑凭证等），对应上游 Transaction 内部构造的仓储。
func (r *Repository) transactionRepository(db *gorm.DB) *Repository {
	copy := *r
	copy.db = db
	return &copy
}

// CreateCloudAgentHoldingTask 是"建 run"的唯一写入口：占位任务、报价预留与执行记录
// 必须一起提交。分两次写会留下两种孤儿状态：运行可见但没有预留，或预留已扣但没有运行。
//
// 占位任务的状态是 holding（见 model.TaskStatusHolding）：worker 只领取 queued/running，
// 它唯一的作用是让"这一轮的报价"有一个可退的载体。
func (r *Repository) CreateCloudAgentHoldingTask(task *model.Task, order *model.BillingOrder, run *model.CloudAgentExecution, activeTaskLimit int, skillRows ...[]model.AgentConversationSkill) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if r.agentMessageQueueEnabled {
			if err := r.transactionRepository(tx).LockCanvasWrite(run.UserID, ""); err != nil {
				return err
			}
		}
		return createCloudAgentHoldingTask(tx, task, order, run, activeTaskLimit, skillRows...)
	})
}

func createCloudAgentHoldingTask(tx *gorm.DB, task *model.Task, order *model.BillingOrder, run *model.CloudAgentExecution, activeTaskLimit int, skillRows ...[]model.AgentConversationSkill) error {
	return createCloudAgentHoldingTaskGuarded(tx, task, order, run, activeTaskLimit, false, skillRows...)
}

func createCloudAgentHoldingTaskGuarded(tx *gorm.DB, task *model.Task, order *model.BillingOrder, run *model.CloudAgentExecution, activeTaskLimit int, queueAdmission bool, skillRows ...[]model.AgentConversationSkill) error {
	r := &Repository{db: tx}
	if err := r.requireCurrentCloudAgentParent(run); err != nil {
		return err
	}
	r = New(tx)
	if !queueAdmission {
		if err := r.requireAgentMessageAdmission(run); err != nil {
			return err
		}
	}
	if err := r.requireActiveLogicalModelForTask(tx, task); err != nil {
		return err
	}
	if err := enforceActiveTaskLimit(tx, task.UserID, activeTaskLimit); err != nil {
		return err
	}
	if err := reserveBillingOrder(tx, order); err != nil {
		return err
	}
	if err := tx.Create(task).Error; err != nil {
		return err
	}
	repo := New(tx)
	if err := repo.EnsureCloudAgent(run); err != nil {
		return err
	}
	if err := repo.AttachCloudAgentPiSession(run); err != nil {
		return err
	}
	for _, rows := range skillRows {
		if err := repo.SaveAgentConversationSkills(rows); err != nil {
			return err
		}
	}
	return nil
}

// SwapCloudAgentHoldingForFirstStep 在调用方事务里把占位预留换成真实首步任务：
// 退还占位订单、预留首步报价、落库首步任务行。它刻意不自开事务：首步任务必须与
// 运行检查点从 awaiting_first_step 推进到已派发在同一事务提交，否则会重复计费。
func (r *Repository) SwapCloudAgentHoldingForFirstStep(task *model.Task, order *model.BillingOrder, placeholderOrderID string, activeTaskLimit int) error {
	if err := r.requireActiveLogicalModelForTask(r.db, task); err != nil {
		return err
	}
	if err := enforceActiveTaskLimit(r.db, task.UserID, activeTaskLimit); err != nil {
		return err
	}
	if strings.TrimSpace(placeholderOrderID) != "" {
		if err := New(r.db).RefundBillingOrder(placeholderOrderID, "Agent 首步占位预留退还"); err != nil {
			return err
		}
	}
	if err := reserveBillingOrder(r.db, order); err != nil {
		return err
	}
	return r.db.Create(task).Error
}

// HoldingTaskForRun 按运行标识找它的占位任务（找不到返回 nil, nil）。收尾路径必须能在
// 状态无法解码时照样退钱，因此按"行 + 操作名"查找，不解码 StateJSON。
func (r *Repository) HoldingTaskForRun(userID, runID, operation string) (*model.Task, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(operation) == "" {
		return nil, nil
	}
	var task model.Task
	err := r.db.Where("id = ? AND user_id = ? AND operation = ? AND status = ?",
		runID, userID, operation, model.TaskStatusHolding).First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

// RefundCloudAgentHoldingOrder 在调用方事务里退还占位任务承载的报价预留。幂等：
// 已退过的订单由 RefundBillingOrder 直接返回。
func (r *Repository) RefundCloudAgentHoldingOrder(task *model.Task, note string) error {
	if task == nil {
		return errors.New("agent holding task is missing")
	}
	if strings.TrimSpace(task.BillingOrderID) == "" {
		// 占位任务必须承载预留；缺订单号只可能是写坏了，显式失败避免冻结额度无人认领。
		return errors.New("agent holding task has no billing reservation")
	}
	return New(r.db).RefundBillingOrder(task.BillingOrderID, note)
}

// PendingCloudAgentCleanups 返回"已终态但清理未完成"的 Pi 运行，供看门狗补做收尾。
func (r *Repository) PendingCloudAgentCleanups(limit int) ([]model.CloudAgentExecution, error) {
	if limit < 1 || limit > 50 {
		limit = 50
	}
	var runs []model.CloudAgentExecution
	err := r.db.Where("engine = ? AND cleanup_pending = ? AND status IN ?", "pi", true,
		[]string{"completed", "failed", "cancelled", "rejected"}).
		Order("updated_at").Limit(limit).Find(&runs).Error
	return runs, err
}

// CloudAgentRunsWithPendingGenerations 列出仍有待回写生成的 Pi 运行（含已结束的运行），供看门狗回写。
func (r *Repository) CloudAgentRunsWithPendingGenerations(limit int) ([]model.CloudAgentExecution, error) {
	if limit < 1 || limit > 50 {
		limit = 50
	}
	var runs []model.CloudAgentExecution
	err := r.db.Where("engine = ? AND media_task_id <> ?", "pi", "").Order("updated_at").Limit(limit).Find(&runs).Error
	return runs, err
}

// LockCanvasWrite 先锁账号，再锁画布行。空 UPDATE 也会在 SQLite WAL 下提前取得写预留，
// 避免延迟读事务升级失败。Agent 的消息队列与画布命令都依赖这一锁顺序。
func (r *Repository) LockCanvasWrite(ownerID, canvasID string) error {
	if err := r.db.Model(&model.User{}).Where("id = ?", ownerID).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
		return err
	}
	if canvasID != "" {
		return r.db.Model(&model.CanvasProject{}).Where("user_id = ? AND id = ?", ownerID, canvasID).UpdateColumn("revision", gorm.Expr("revision")).Error
	}
	return nil
}

// StructuredWriteQuota 由 app 在构造期注入，Agent 画布命令与媒体版本写入前在同一事务里校验
// 账号结构化数据额度。未注入时不限制（只读 fixture 与离线工具）。
type StructuredWriteQuota func(tx *Repository, ownerID, kind string, countDelta, byteDelta int64) error

// SetStructuredWriteQuota 只能在共享仓储之前调用。
func (r *Repository) SetStructuredWriteQuota(check StructuredWriteQuota) {
	r.structuredWriteQuota = check
}

func (r *Repository) checkCanvasWriteQuota(before, after *model.CanvasProject) error {
	if r.structuredWriteQuota == nil {
		return nil
	}
	delta := int64(len(after.PayloadJSON))
	if before != nil {
		delta -= int64(len(before.PayloadJSON))
	}
	countDelta := int64(0)
	if before == nil {
		countDelta = 1
	}
	return r.structuredWriteQuota(r, after.UserID, "canvas", countDelta, delta)
}

// CanvasWriteQuota 在账号锁之后、文档写入之前执行；传入的仓储使用调用方事务。
type CanvasWriteQuota func(*Repository, string, bool, int64) error

var (
	ErrCanvasTaskRequiresNewVersion = errors.New("画布任务重试需要新版本")
	ErrCanvasNodeLocked             = errors.New("canvas node is locked by another editor")
	ErrCanvasEditProofInvalid       = errors.New("canvas edit proof is invalid or expired")
)

// CanvasEditLeaseError 是 Agent 命令写入撞上浏览器编辑租约时的结构化错误：
// Agent 据 Reason/NodeID 生成可纠正的工具回执，而不是把整轮判死。
type CanvasEditLeaseError struct {
	Reason   string
	CanvasID string
	NodeID   string
}

func (e *CanvasEditLeaseError) Error() string { return e.Reason + ": " + e.CanvasID + "/" + e.NodeID }
func (e *CanvasEditLeaseError) Unwrap() error {
	if e.Reason == "node_locked" {
		return ErrCanvasNodeLocked
	}
	return ErrCanvasEditProofInvalid
}

// Is 让按上游租约错误分类的调用方（HTTP 映射等）也能识别 Agent 的租约冲突。
func (e *CanvasEditLeaseError) Is(target error) bool { return target == ErrCanvasEditLease }

// hasCommandSchema 报告 Agent 画布命令表是否已迁移。只读 fixture 与早于命令表的库
// 视为没有命令层：软删除投影退化为"全部为活动画布"。
func (r *Repository) hasCommandSchema() (bool, error) {
	var count int64
	table := r.db.NamingStrategy.TableName("CanvasDocumentEpoch")
	if r.Dialect() == "postgres" {
		err := r.db.Raw("SELECT CASE WHEN to_regclass(?) IS NULL THEN 0 ELSE 1 END", table).Scan(&count).Error
		return count == 1, err
	}
	err := r.db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error
	return count == 1, err
}
