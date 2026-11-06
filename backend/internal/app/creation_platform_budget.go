package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// budgetGrantHost implements the hierarchical budget port of the foundation
// billing layer. Grants never debit the wallet; they only bound admission.
type budgetGrantHost struct{ s *Service }

func (s *Service) BudgetGrants() cp.BudgetGrants { return budgetGrantHost{s: s} }

func platformBudgetFingerprint(fingerprint, grantID string) string {
	if grantID == "" {
		return fingerprint
	}
	sum := sha256.Sum256([]byte(fingerprint + "\x00budget:" + grantID))
	return hex.EncodeToString(sum[:])
}

func budgetFingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func budgetKey(req string) (string, error) {
	key := strings.TrimSpace(req)
	if key == "" || len(key) > 200 {
		return "", cp.NewError(cp.CodeInvalidRequest, "幂等键必填，最长 200")
	}
	return key, nil
}

func budgetWork(ref cp.BusinessRef) (string, string, string) {
	return strings.TrimSpace(ref.Application), strings.TrimSpace(ref.ObjectType), strings.TrimSpace(ref.ObjectID)
}

func (h budgetGrantHost) CreateRoot(_ context.Context, caller cp.Caller, req cp.CreateBudgetRequest) (cp.BudgetGrantView, error) {
	if err := requireCaller(caller); err != nil {
		return cp.BudgetGrantView{}, err
	}
	key, err := budgetKey(req.IdempotencyKey)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	if req.LimitMicrocredits <= 0 {
		return cp.BudgetGrantView{}, cp.NewError(cp.CodeInvalidRequest, "预算额度必须大于 0")
	}
	application, objectType, objectID := budgetWork(req.Work)
	id := newID()
	grant := &model.PlatformBudgetGrant{ID: id, OwnerID: caller.UserID, IdempotencyKey: key, RootID: id, LimitMicrocredits: req.LimitMicrocredits, Status: cp.BudgetStatusOpen, Version: 1,
		Fingerprint: budgetFingerprint("root", strconv.FormatInt(req.LimitMicrocredits, 10), application, objectType, objectID), Application: application, ObjectType: objectType, ObjectID: objectID}
	stored, err := h.s.repo.CreateBudgetRoot(grant)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	if stored.Fingerprint != grant.Fingerprint {
		return cp.BudgetGrantView{}, cp.NewError(cp.CodeIdempotencyConflict, "幂等键已用于不同的预算请求")
	}
	return h.view(caller.UserID, stored.ID)
}

func (h budgetGrantHost) Allocate(_ context.Context, caller cp.Caller, req cp.AllocateBudgetRequest) (cp.BudgetGrantView, error) {
	if err := requireCaller(caller); err != nil {
		return cp.BudgetGrantView{}, err
	}
	key, err := budgetKey(req.IdempotencyKey)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	parent, err := h.grant(caller.UserID, req.ParentID)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	application, objectType, objectID := budgetWork(req.Work)
	grant := &model.PlatformBudgetGrant{ID: newID(), OwnerID: caller.UserID, IdempotencyKey: key, RootID: parent.RootID, ParentID: parent.ID, LimitMicrocredits: req.LimitMicrocredits, Status: cp.BudgetStatusOpen, Version: 1,
		Fingerprint: budgetFingerprint("child", parent.ID, strconv.FormatInt(req.LimitMicrocredits, 10), application, objectType, objectID), Application: application, ObjectType: objectType, ObjectID: objectID}
	stored, err := h.s.repo.AllocateBudgetGrant(grant, func(nodes []repository.BudgetNodeRow) error {
		tree, err := budgetTreeOf(nodes)
		if err != nil {
			return err
		}
		return tree.AdmitAllocation(parent.ID, req.LimitMicrocredits)
	})
	if err != nil {
		return cp.BudgetGrantView{}, platformBudgetError(err)
	}
	if stored.Fingerprint != grant.Fingerprint {
		return cp.BudgetGrantView{}, cp.NewError(cp.CodeIdempotencyConflict, "幂等键已用于不同的预算请求")
	}
	return h.view(caller.UserID, stored.ID)
}

func (h budgetGrantHost) Close(_ context.Context, caller cp.Caller, req cp.CloseBudgetRequest) (cp.BudgetGrantView, error) {
	if err := requireCaller(caller); err != nil {
		return cp.BudgetGrantView{}, err
	}
	grant, err := h.grant(caller.UserID, req.ID)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	if grant.Status == cp.BudgetStatusClosed {
		return h.view(caller.UserID, grant.ID)
	}
	if err := h.s.repo.CloseBudgetGrant(caller.UserID, grant.ID, req.ExpectedVersion, time.Now()); err != nil {
		if errors.Is(err, repository.ErrBudgetGrantChanged) {
			return cp.BudgetGrantView{}, cp.NewError(cp.CodeActionNotAllowed, "预算授权已变化，请刷新后再关闭")
		}
		return cp.BudgetGrantView{}, err
	}
	return h.view(caller.UserID, grant.ID)
}

func (h budgetGrantHost) Get(_ context.Context, caller cp.Caller, id string) (cp.BudgetGrantView, error) {
	if err := requireCaller(caller); err != nil {
		return cp.BudgetGrantView{}, err
	}
	return h.view(caller.UserID, id)
}

func (h budgetGrantHost) grant(ownerID, id string) (*model.PlatformBudgetGrant, error) {
	grant, err := h.s.repo.BudgetGrantForOwner(ownerID, strings.TrimSpace(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, cp.NewError(cp.CodeNotFound, "预算授权不存在")
	}
	return grant, err
}

func (h budgetGrantHost) view(ownerID, id string) (cp.BudgetGrantView, error) {
	grant, err := h.grant(ownerID, id)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	nodes, err := h.s.repo.BudgetTree(ownerID, grant.RootID)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	tree, err := budgetTreeOf(nodes)
	if err != nil {
		return cp.BudgetGrantView{}, err
	}
	return cp.BudgetGrantView{
		ID: grant.ID, RootID: grant.RootID, ParentID: grant.ParentID, LimitMicrocredits: grant.LimitMicrocredits,
		AvailableMicrocredits: tree.Available(grant.ID), Usage: tree.Subtree(grant.ID), Status: grant.Status, Version: grant.Version,
		Work: cp.BusinessRef{Application: grant.Application, ObjectType: grant.ObjectType, ObjectID: grant.ObjectID}, CreatedAt: grant.CreatedAt,
	}, nil
}

func budgetTreeOf(rows []repository.BudgetNodeRow) (*cp.BudgetTree, error) {
	nodes := make([]cp.BudgetNode, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, cp.BudgetNode{ID: row.ID, ParentID: row.ParentID, Limit: row.LimitMicrocredits, Open: row.Status == cp.BudgetStatusOpen,
			Direct: cp.BudgetUsage{Charged: row.Charged, InFlight: row.InFlight, PendingReview: row.PendingReview, Refunded: row.Refunded}})
	}
	return cp.NewBudgetTree(nodes)
}

// budgetAdmission builds the in-transaction admission for an order charged to
// a grant; nil when the task is not under a budget.
func (s *Service) budgetAdmission(ownerID, grantID string, order *model.BillingOrder) (*repository.BudgetAdmission, error) {
	if strings.TrimSpace(grantID) == "" {
		return nil, nil
	}
	grant, err := budgetGrantHost{s: s}.grant(ownerID, grantID)
	if err != nil {
		return nil, err
	}
	amount := int64(0)
	if order != nil {
		amount = max(order.ReservedAmountMicrocredits, order.AmountMicrocredits)
	}
	return &repository.BudgetAdmission{RootID: grant.RootID, GrantID: grant.ID, Check: func(nodes []repository.BudgetNodeRow) error {
		tree, err := budgetTreeOf(nodes)
		if err != nil {
			return err
		}
		return tree.AdmitCharge(grant.ID, amount)
	}}, nil
}

// retryTaskUnderBudget re-queues a task; a platform task submitted under a
// budget grant is admitted against that same grant in the retry transaction.
func (s *Service) retryTaskUnderBudget(ownerID string, task *model.Task, order *model.BillingOrder, activeTaskLimit int) (*model.Task, error) {
	submission, err := s.repo.PlatformSubmissionForTask(ownerID, task.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	budgets := []*repository.BudgetAdmission{}
	if err == nil {
		budget, err := s.budgetAdmission(ownerID, submission.BudgetGrantID, order)
		if err != nil {
			return nil, err
		}
		if budget != nil {
			budgets = append(budgets, budget)
		}
	}
	retried, err := s.repo.RetryTaskWithBilling(ownerID, task, order, activeTaskLimit, budgets...)
	return retried, platformBudgetError(err)
}

// platformBudgetError maps a tree root that is not the owner's to not_found;
// every other error is returned unchanged.
func platformBudgetError(err error) error {
	if errors.Is(err, repository.ErrBudgetGrantMissing) {
		return cp.NewError(cp.CodeNotFound, "预算授权不存在")
	}
	return err
}
