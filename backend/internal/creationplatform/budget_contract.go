package creationplatform

import (
	"context"
	"time"
)

// Budget grant statuses.
const (
	BudgetStatusOpen   = "open"
	BudgetStatusClosed = "closed"
)

// BudgetGrantView is one grant with the usage of its whole subtree. Grants
// never hold credits; the wallet is still debited only by billing orders.
type BudgetGrantView struct {
	ID                    string      `json:"id"`
	RootID                string      `json:"rootId"`
	ParentID              string      `json:"parentId,omitempty"`
	LimitMicrocredits     int64       `json:"limitMicrocredits"`
	AvailableMicrocredits int64       `json:"availableMicrocredits"`
	Usage                 BudgetUsage `json:"usage"`
	Status                string      `json:"status"`
	Version               int64       `json:"version"`
	Work                  BusinessRef `json:"work"`
	CreatedAt             time.Time   `json:"createdAt"`
}

type CreateBudgetRequest struct {
	LimitMicrocredits int64       `json:"limitMicrocredits"`
	Work              BusinessRef `json:"work"`
	IdempotencyKey    string      `json:"idempotencyKey"`
}

type AllocateBudgetRequest struct {
	ParentID          string      `json:"parentId"`
	LimitMicrocredits int64       `json:"limitMicrocredits"`
	Work              BusinessRef `json:"work"`
	IdempotencyKey    string      `json:"idempotencyKey"`
}

type CloseBudgetRequest struct {
	ID              string `json:"id"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

// BudgetGrants is the hierarchical budget port. Only trusted server code calls
// the write methods; a model or browser cannot raise a budget by asking.
type BudgetGrants interface {
	CreateRoot(ctx context.Context, caller Caller, req CreateBudgetRequest) (BudgetGrantView, error)
	Allocate(ctx context.Context, caller Caller, req AllocateBudgetRequest) (BudgetGrantView, error)
	Close(ctx context.Context, caller Caller, req CloseBudgetRequest) (BudgetGrantView, error)
	Get(ctx context.Context, caller Caller, id string) (BudgetGrantView, error)
}
