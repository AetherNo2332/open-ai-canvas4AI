package model

import "time"

// PlatformSubmission 是经创作平台合同提交的任务记录：幂等作用域为 用户 + application + 幂等键，
// 与任务、费用冻结在同一事务写入。Fingerprint 用于判断同键是否同内容；BusinessJSON 只是归属标签。
type PlatformSubmission struct {
	ID                    string `json:"id" gorm:"primaryKey;size:36"`
	UserID                string `json:"userId" gorm:"size:36;not null;uniqueIndex:idx_platform_submission_key,priority:1;index:idx_platform_submission_business,priority:1"`
	Application           string `json:"application" gorm:"size:32;not null;uniqueIndex:idx_platform_submission_key,priority:2;index:idx_platform_submission_business,priority:2"`
	IdempotencyKey        string `json:"idempotencyKey" gorm:"size:160;not null;uniqueIndex:idx_platform_submission_key,priority:3"`
	Fingerprint           string `json:"-" gorm:"size:64;not null"`
	TaskID                string `json:"taskId" gorm:"size:36;not null;uniqueIndex"`
	Capability            string `json:"capability" gorm:"size:16;not null"`
	BusinessJSON          string `json:"-" gorm:"type:text"`
	ProjectRef            string `json:"projectRef,omitempty" gorm:"size:80;index:idx_platform_submission_business,priority:3"`
	ObjectType            string `json:"objectType,omitempty" gorm:"size:40"`
	ObjectID              string `json:"objectId,omitempty" gorm:"size:80"`
	OperationID           string `json:"operationId,omitempty" gorm:"size:160"`
	MaxChargeSet          bool   `json:"-" gorm:"not null;default:false"`
	MaxChargeMicrocredits int64  `json:"-" gorm:"not null;default:0"`
	// BudgetGrantID is set when a trusted server caller submitted under a budget
	// grant; retries of the task are admitted against the same grant.
	BudgetGrantID string    `json:"-" gorm:"size:36;not null;default:''"`
	CreatedAt     time.Time `json:"createdAt"`
}

// PlatformBudgetGrant is one node of a hierarchical budget. It holds no
// credits: orders still debit the wallet; a grant only bounds which orders may
// be admitted. Every grant of a tree shares RootID and the owner's account.
type PlatformBudgetGrant struct {
	ID                string     `json:"id" gorm:"primaryKey;size:36"`
	OwnerID           string     `json:"ownerId" gorm:"size:36;not null;uniqueIndex:idx_platform_budget_key,priority:1;index:idx_platform_budget_root,priority:1"`
	IdempotencyKey    string     `json:"-" gorm:"size:240;not null;uniqueIndex:idx_platform_budget_key,priority:2"`
	Fingerprint       string     `json:"-" gorm:"size:64;not null"`
	RootID            string     `json:"rootId" gorm:"size:36;not null;index:idx_platform_budget_root,priority:2"`
	ParentID          string     `json:"parentId" gorm:"size:36;not null;default:'';index"`
	LimitMicrocredits int64      `json:"limitMicrocredits" gorm:"not null"`
	Status            string     `json:"status" gorm:"size:16;not null"`
	Version           int64      `json:"version" gorm:"not null;default:1"`
	Application       string     `json:"application" gorm:"size:32;not null;default:''"`
	ObjectType        string     `json:"objectType" gorm:"size:40;not null;default:''"`
	ObjectID          string     `json:"objectId" gorm:"size:80;not null;default:''"`
	ClosedAt          *time.Time `json:"closedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// PlatformBudgetCharge binds one billing order to the grant it was admitted
// against; the order ID is the key, so an order counts toward exactly one
// grant. Free tasks bind with a synthetic ID and no order.
type PlatformBudgetCharge struct {
	ID             string    `json:"id" gorm:"primaryKey;size:80"`
	OwnerID        string    `json:"ownerId" gorm:"size:36;not null"`
	RootID         string    `json:"rootId" gorm:"size:36;not null;index"`
	GrantID        string    `json:"grantId" gorm:"size:36;not null;index"`
	TaskID         string    `json:"taskId" gorm:"size:36;not null;index"`
	BillingOrderID string    `json:"billingOrderId" gorm:"size:36;not null;default:''"`
	CreatedAt      time.Time `json:"createdAt"`
}
