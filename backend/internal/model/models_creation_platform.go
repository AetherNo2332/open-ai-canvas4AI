package model

import "time"

// PlatformSubmission 是经创作平台合同提交的任务记录：幂等作用域为 用户 + application + 幂等键，
// 与任务、费用冻结在同一事务写入。Fingerprint 用于判断同键是否同内容；BusinessJSON 只是归属标签。
type PlatformSubmission struct {
	ID                    string    `json:"id" gorm:"primaryKey;size:36"`
	UserID                string    `json:"userId" gorm:"size:36;not null;uniqueIndex:idx_platform_submission_key,priority:1;index:idx_platform_submission_business,priority:1"`
	Application           string    `json:"application" gorm:"size:32;not null;uniqueIndex:idx_platform_submission_key,priority:2;index:idx_platform_submission_business,priority:2"`
	IdempotencyKey        string    `json:"idempotencyKey" gorm:"size:160;not null;uniqueIndex:idx_platform_submission_key,priority:3"`
	Fingerprint           string    `json:"-" gorm:"size:64;not null"`
	TaskID                string    `json:"taskId" gorm:"size:36;not null;uniqueIndex"`
	Capability            string    `json:"capability" gorm:"size:16;not null"`
	BusinessJSON          string    `json:"-" gorm:"type:text"`
	ProjectRef            string    `json:"projectRef,omitempty" gorm:"size:80;index:idx_platform_submission_business,priority:3"`
	ObjectType            string    `json:"objectType,omitempty" gorm:"size:40"`
	ObjectID              string    `json:"objectId,omitempty" gorm:"size:80"`
	OperationID           string    `json:"operationId,omitempty" gorm:"size:160"`
	MaxChargeSet          bool      `json:"-" gorm:"not null;default:false"`
	MaxChargeMicrocredits int64     `json:"-" gorm:"not null;default:0"`
	CreatedAt             time.Time `json:"createdAt"`
}
